// Package fleet implements a concurrency-safe, atomic task board for multi-agent
// fleets. A fleet is a set of N sandboxes ("workers") that pull work from a
// single shared [Board]. The board guarantees that each task is claimed by at
// most one worker at a time (no double-claim under concurrency) and supports
// requeue-on-failure so that transient errors can be retried.
//
// The board is a purely in-memory primitive. It holds tasks in a map keyed by
// ID and an insertion-ordered slice, so claims are served FIFO over the pending
// tasks while snapshots preserve insertion order. All operations are guarded by
// a single [sync.Mutex], making every state transition atomic with respect to
// concurrent callers.
//
// It depends only on the Go standard library.
package fleet

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TaskState is the lifecycle state of a [Task].
type TaskState string

// The set of task states. A task starts [StatePending], becomes [StateClaimed]
// when a worker takes it, and finishes as either [StateDone] or [StateFailed].
// A failed task may be requeued back to [StatePending] for a retry.
const (
	// StatePending marks a task waiting to be claimed by a worker.
	StatePending TaskState = "pending"
	// StateClaimed marks a task currently owned by a worker.
	StateClaimed TaskState = "claimed"
	// StateDone marks a task that completed successfully.
	StateDone TaskState = "done"
	// StateFailed marks a task that failed and was not requeued.
	StateFailed TaskState = "failed"
)

// Sentinel errors returned by [Board] methods. Callers should compare against
// them with [errors.Is] rather than by string matching.
var (
	// ErrNotFound is returned when no task exists for the given ID.
	ErrNotFound = errors.New("fleet: task not found")
	// ErrIllegalTransition is returned when a state change is not valid for a
	// task's current state (e.g. completing a task that is not claimed).
	ErrIllegalTransition = errors.New("fleet: illegal state transition")
)

// Task is a unit of work on the board. It is safe to copy: [Board] methods hand
// out copies so callers can never mutate board state through a returned value.
type Task struct {
	// ID is the unique, board-assigned identifier.
	ID string `json:"id"`
	// Payload is the opaque work description supplied by the producer.
	Payload string `json:"payload"`
	// State is the task's current lifecycle state.
	State TaskState `json:"state"`
	// Owner is the id of the worker that last claimed the task ("" if never
	// claimed or after a requeue clears it).
	Owner string `json:"owner"`
	// Attempts counts how many times the task has been claimed.
	Attempts int `json:"attempts"`
	// CreatedAt is when the task was added, in UTC.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the task last changed state, in UTC.
	UpdatedAt time.Time `json:"updated_at"`
	// Result holds the success output set by [Board.Complete].
	Result string `json:"result"`
	// Error holds the failure message set by [Board.Fail].
	Error string `json:"error"`
	// LeaseExpiry is when the current claim's lease expires. A claimed task
	// whose lease passes without a heartbeat is automatically requeued by
	// [Board.Sweep]. Zero means no lease (claims never expire).
	LeaseExpiry time.Time `json:"lease_expiry,omitempty"`
}

// Stats is a point-in-time summary of the tasks on a board, broken down by
// state.
type Stats struct {
	Total   int `json:"total"`
	Pending int `json:"pending"`
	Claimed int `json:"claimed"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
}

// Board is a concurrency-safe, in-memory task board. The zero value is not
// usable; construct one with [NewBoard] or [Seed]. All methods are safe for
// concurrent use.
type Board struct {
	mu sync.Mutex
	// tasks indexes every task by ID.
	tasks map[string]*Task
	// order records IDs in insertion order and defines FIFO claim order.
	order []string
	// lease is how long a claim is valid before [Board.Sweep] requeues it
	// absent a heartbeat. Zero disables lease expiry.
	lease time.Duration
}

// NewBoard returns an empty board ready for use.
func NewBoard() *Board {
	return &Board{tasks: make(map[string]*Task)}
}

// SetLease sets the claim lease duration. When d > 0, [Board.Claim] stamps each
// claimed task with a lease deadline of now+d, [Board.Heartbeat] extends it, and
// [Board.Sweep] requeues tasks whose lease has passed (the "dead worker"
// recovery path). A non-positive d disables lease expiry.
func (b *Board) SetLease(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lease = d
}

// Load reconstructs a board from a previously exported task snapshot (see
// [Board.Export]), preserving IDs, states, and insertion order. It is used to
// restore a persisted board across control-plane restarts.
func Load(tasks []Task, lease time.Duration) *Board {
	b := &Board{tasks: make(map[string]*Task, len(tasks)), lease: lease}
	for i := range tasks {
		t := tasks[i]
		cp := t
		b.tasks[cp.ID] = &cp
		b.order = append(b.order, cp.ID)
	}
	return b
}

// Export returns a deep copy of every task in insertion order, suitable for
// serialization. Unlike [Board.List] it is explicitly the persistence view.
func (b *Board) Export() []Task {
	return b.List()
}

// Seed returns a new board with one pending task per payload, added in order.
// Task IDs are auto-generated. It is a convenience wrapper over [NewBoard] and
// [Board.Add].
func Seed(payloads []string) *Board {
	b := NewBoard()
	for _, p := range payloads {
		b.Add(p)
	}
	return b
}

// Add enqueues a new pending task with the given payload and returns a copy of
// it. The task is appended to the end of the FIFO order.
func (b *Board) Add(payload string) *Task {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now().UTC()
	t := &Task{
		ID:        newID(),
		Payload:   payload,
		State:     StatePending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	b.tasks[t.ID] = t
	b.order = append(b.order, t.ID)

	cp := *t
	return &cp
}

// Claim atomically takes the oldest pending task, marks it claimed by owner,
// increments its attempt count, and returns a copy of it with ok true. If no
// task is pending it returns the zero Task and false.
//
// Because the whole scan-and-mutate is performed under the board mutex,
// concurrent callers can never be handed the same task.
func (b *Board) Claim(owner string) (Task, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, id := range b.order {
		t := b.tasks[id]
		if t.State != StatePending {
			continue
		}
		now := time.Now().UTC()
		t.State = StateClaimed
		t.Owner = owner
		t.Attempts++
		t.UpdatedAt = now
		if b.lease > 0 {
			t.LeaseExpiry = now.Add(b.lease)
		}
		return *t, true
	}
	return Task{}, false
}

// Heartbeat extends the lease on a claimed task held by owner, returning a copy
// of the refreshed task. It returns [ErrNotFound] if no such task exists,
// [ErrIllegalTransition] if the task is not currently claimed, or an error if
// the task is claimed by a different owner. Heartbeat is a no-op on the lease
// deadline when the board has no lease configured.
func (b *Board) Heartbeat(id, owner string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if t.State != StateClaimed {
		return Task{}, fmt.Errorf("%w: heartbeat requires claimed, task %q is %s", ErrIllegalTransition, id, t.State)
	}
	if t.Owner != owner {
		return Task{}, fmt.Errorf("%w: task %q is owned by %q, not %q", ErrIllegalTransition, id, t.Owner, owner)
	}
	now := time.Now().UTC()
	t.UpdatedAt = now
	if b.lease > 0 {
		t.LeaseExpiry = now.Add(b.lease)
	}
	return *t, nil
}

// Sweep requeues every claimed task whose lease has expired as of now (owner
// cleared, lease cleared, attempts retained) so a stalled or dead worker's task
// returns to the pending pool. It returns copies of the tasks it requeued.
// Tasks with a zero LeaseExpiry (no lease) are never swept.
func (b *Board) Sweep(now time.Time) []Task {
	b.mu.Lock()
	defer b.mu.Unlock()

	var requeued []Task
	for _, id := range b.order {
		t := b.tasks[id]
		if t.State != StateClaimed || t.LeaseExpiry.IsZero() {
			continue
		}
		if now.After(t.LeaseExpiry) {
			t.State = StatePending
			t.Owner = ""
			t.LeaseExpiry = time.Time{}
			t.Error = "lease expired; requeued"
			t.UpdatedAt = now.UTC()
			requeued = append(requeued, *t)
		}
	}
	return requeued
}

// Complete marks the claimed task id as done with the given result. It returns
// [ErrNotFound] if no such task exists, or [ErrIllegalTransition] if the task is
// not currently claimed.
func (b *Board) Complete(id, result string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.tasks[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if t.State != StateClaimed {
		return fmt.Errorf("%w: complete requires claimed, task %q is %s", ErrIllegalTransition, id, t.State)
	}
	t.State = StateDone
	t.Result = result
	t.Error = ""
	t.LeaseExpiry = time.Time{}
	t.UpdatedAt = time.Now().UTC()
	return nil
}

// Fail marks the claimed task id as failed with errMsg. When requeue is true the
// task is instead returned to the pending pool (Owner cleared, Attempts kept) so
// another worker can retry it. It returns [ErrNotFound] if no such task exists,
// or [ErrIllegalTransition] if the task is not currently claimed.
func (b *Board) Fail(id, errMsg string, requeue bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.tasks[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if t.State != StateClaimed {
		return fmt.Errorf("%w: fail requires claimed, task %q is %s", ErrIllegalTransition, id, t.State)
	}
	t.Error = errMsg
	t.UpdatedAt = time.Now().UTC()
	t.LeaseExpiry = time.Time{}
	if requeue {
		t.State = StatePending
		t.Owner = ""
		return nil
	}
	t.State = StateFailed
	return nil
}

// Get returns a copy of the task with the given ID and true, or the zero Task
// and false if it does not exist.
func (b *Board) Get(id string) (Task, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// List returns a snapshot copy of every task in insertion order.
func (b *Board) List() []Task {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]Task, 0, len(b.order))
	for _, id := range b.order {
		out = append(out, *b.tasks[id])
	}
	return out
}

// Stats returns a point-in-time count of tasks by state.
func (b *Board) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := Stats{Total: len(b.order)}
	for _, id := range b.order {
		switch b.tasks[id].State {
		case StatePending:
			s.Pending++
		case StateClaimed:
			s.Claimed++
		case StateDone:
			s.Done++
		case StateFailed:
			s.Failed++
		}
	}
	return s
}

// Remaining returns the number of tasks still in flight: pending plus claimed.
func (b *Board) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := 0
	for _, id := range b.order {
		switch b.tasks[id].State {
		case StatePending, StateClaimed:
			n++
		}
	}
	return n
}

// newID returns a short random hex identifier. crypto/rand.Read never returns a
// short read or error on supported platforms, but we fall back to a
// timestamp-derived id defensively so ID generation cannot panic.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
