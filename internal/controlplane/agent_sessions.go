package controlplane

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/profile"
)

const (
	agentSessionsFileName   = "agent-sessions.json"
	agentSessionDirName     = "agent-sessions"
	maxAgentEventBytes      = 32 << 10
	maxAgentEventsMemory    = 2_048
	maxAgentTranscriptBytes = 64 << 20
)

// AgentSession is one observable, durable agent command. Output events are
// stored separately so metadata lists remain small and reconnecting clients can
// request only the backlog they have not seen.
type AgentSession struct {
	ID                  string       `json:"id"`
	CitadelID           string       `json:"citadel_id"`
	Tenant              string       `json:"tenant,omitempty"`
	Actor               string       `json:"actor,omitempty"`
	Agent               string       `json:"agent,omitempty"`
	Model               string       `json:"model,omitempty"`
	CohortID            string       `json:"cohort_id,omitempty"`
	TaskID              string       `json:"task_id,omitempty"`
	Command             []string     `json:"command,omitempty"`
	Status              string       `json:"status"`
	ExitCode            *int         `json:"exit_code,omitempty"`
	Error               string       `json:"error,omitempty"`
	CreatedAt           time.Time    `json:"created_at"`
	StartedAt           *time.Time   `json:"started_at,omitempty"`
	FinishedAt          *time.Time   `json:"finished_at,omitempty"`
	LastSeq             int64        `json:"last_seq"`
	TranscriptTruncated bool         `json:"transcript_truncated,omitempty"`
	Events              []AgentEvent `json:"-"`
}

// AgentEvent is a reconnectable transcript record. Stream is stdout, stderr,
// or status; Data has already passed through the Citadel's configured scrubber.
type AgentEvent struct {
	Seq    int64     `json:"seq"`
	Time   time.Time `json:"time"`
	Stream string    `json:"stream"`
	Data   string    `json:"data"`
}

type StartAgentSessionOptions struct {
	Command  []string
	Agent    string
	Model    string
	CohortID string
	TaskID   string
}

// StartAgentSession launches a governed command asynchronously and returns as
// soon as its durable session record exists.
func (m *Manager) StartAgentSession(ctx context.Context, citadelID string, opts StartAgentSessionOptions) (*AgentSession, error) {
	sess, err := m.session(citadelID)
	if err != nil {
		return nil, err
	}
	if len(opts.Command) == 0 {
		return nil, fmt.Errorf("command is required")
	}
	if opts.CohortID != "" {
		f, ok := m.fleet(opts.CohortID)
		if !ok {
			return nil, fmt.Errorf("cohort %q not found", opts.CohortID)
		}
		member := false
		for _, id := range f.Sandboxes {
			if id == citadelID {
				member = true
				break
			}
		}
		if !member {
			return nil, fmt.Errorf("citadel %q is not a member of cohort %q", citadelID, opts.CohortID)
		}
		if opts.TaskID != "" {
			if _, ok := f.Board.Get(opts.TaskID); !ok {
				return nil, fmt.Errorf("task %q not found in cohort %q", opts.TaskID, opts.CohortID)
			}
		}
	}

	actor := sess.Actor
	if ctx != nil {
		if current, ok := ctx.Value(actorContextKey{}).(string); ok && strings.TrimSpace(current) != "" {
			actor = strings.TrimSpace(current)
		}
	}
	command := make([]string, len(opts.Command))
	for i, arg := range opts.Command {
		command[i] = sess.eventScrubber().ScrubString(arg, sess.secrets...)
	}
	now := time.Now().UTC()
	as := &AgentSession{
		ID: newID(), CitadelID: citadelID, Tenant: sess.Owner, Actor: actor,
		Agent: strings.TrimSpace(opts.Agent), Model: strings.TrimSpace(opts.Model),
		CohortID: strings.TrimSpace(opts.CohortID), TaskID: strings.TrimSpace(opts.TaskID),
		Command: command, Status: "queued", CreatedAt: now,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	runCtx = WithActor(runCtx, actor)

	m.agentMu.Lock()
	m.ensureAgentSessionMapsLocked()
	m.agentSessions[as.ID] = as
	m.agentCancels[as.ID] = cancel
	m.agentMu.Unlock()
	if err := m.saveAgentSessions(); err != nil {
		cancel()
		m.agentMu.Lock()
		delete(m.agentSessions, as.ID)
		delete(m.agentCancels, as.ID)
		m.agentMu.Unlock()
		return nil, fmt.Errorf("persist agent session: %w", err)
	}
	copy := cloneAgentSession(as)
	m.agentWG.Add(1)
	go func() {
		defer m.agentWG.Done()
		m.runAgentSession(runCtx, sess, as.ID, opts.Command)
	}()
	return &copy, nil
}

func (m *Manager) runAgentSession(ctx context.Context, sess *Session, id string, command []string) {
	now := time.Now().UTC()
	m.agentMu.Lock()
	if as := m.agentSessions[id]; as != nil {
		as.Status = "running"
		as.StartedAt = &now
	}
	m.agentMu.Unlock()
	_ = m.saveAgentSessions()
	m.appendAgentEvent(id, "status", "running")

	stdout := &agentEventWriter{manager: m, sessionID: id, stream: "stdout", scrub: func(s string) string {
		return sess.eventScrubber().ScrubString(s, sess.secrets...)
	}}
	stderr := &agentEventWriter{manager: m, sessionID: id, stream: "stderr", scrub: func(s string) string {
		return sess.eventScrubber().ScrubString(s, sess.secrets...)
	}}
	arg := strings.Join(command, " ")
	res, runErr := m.govern(ctx, sess, "shell", arg, command, func(execCtx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(execCtx, sess.Sandbox.ID, backend.ExecRequest{
			Command: command, Workdir: sess.Workdir, Env: sess.Env,
			Stdout: stdout, Stderr: stderr, StreamOnly: true,
		})
	})
	stdout.Flush()
	stderr.Flush()

	status, message := "completed", ""
	var exitCode *int
	if runErr != nil {
		status, message = "failed", runErr.Error()
		if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status, message = "canceled", "agent session canceled"
		}
	} else if res == nil {
		status, message = "failed", "agent execution returned no result"
	} else if res.Verdict != profile.VerdictAllow {
		status, message = "denied", res.Reason
	} else {
		code := res.ExitCode
		exitCode = &code
		if code != 0 {
			status, message = "failed", fmt.Sprintf("agent exited with status %d", code)
		}
	}
	m.finishAgentSession(id, status, exitCode, message)
}

func (m *Manager) finishAgentSession(id, status string, exitCode *int, message string) {
	// Commit the final transcript marker before exposing a terminal metadata
	// status. A client that observes completion can therefore immediately fetch
	// a complete backlog without racing this writer.
	m.appendAgentEvent(id, "status", status)
	now := time.Now().UTC()
	m.agentMu.Lock()
	if as := m.agentSessions[id]; as != nil {
		as.Status = status
		as.ExitCode = exitCode
		as.Error = message
		as.FinishedAt = &now
	}
	delete(m.agentCancels, id)
	m.agentMu.Unlock()
	_ = m.saveAgentSessions()

	m.agentMu.Lock()
	for ch := range m.agentSubscribers[id] {
		close(ch)
	}
	delete(m.agentSubscribers, id)
	m.agentMu.Unlock()
}

// AgentSessions lists durable sessions in creation order, optionally scoped to
// one Cohort. Callers apply tenant filtering at the transport boundary.
func (m *Manager) AgentSessions(cohortID string) []AgentSession {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	m.ensureAgentSessionMapsLocked()
	out := make([]AgentSession, 0, len(m.agentSessions))
	for _, as := range m.agentSessions {
		if cohortID == "" || as.CohortID == cohortID {
			out = append(out, cloneAgentSession(as))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (m *Manager) AgentSession(id string) (AgentSession, bool) {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	as, ok := m.agentSessions[id]
	if !ok {
		return AgentSession{}, false
	}
	return cloneAgentSession(as), true
}

func (m *Manager) AgentSessionEvents(id string, after int64) (AgentSession, []AgentEvent, bool) {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	as, ok := m.agentSessions[id]
	if !ok {
		return AgentSession{}, nil, false
	}
	events := m.agentEventsAfterLocked(as, after)
	return cloneAgentSession(as), events, true
}

// SubscribeAgentSession atomically returns backlog and registers a live event
// channel, avoiding a gap between replay and follow mode.
func (m *Manager) SubscribeAgentSession(id string, after int64) (AgentSession, []AgentEvent, <-chan AgentEvent, func(), bool) {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	as, ok := m.agentSessions[id]
	if !ok {
		return AgentSession{}, nil, nil, nil, false
	}
	ch := make(chan AgentEvent, 256)
	terminal := agentSessionTerminal(as.Status)
	if !terminal {
		m.ensureAgentSessionMapsLocked()
		if m.agentSubscribers[id] == nil {
			m.agentSubscribers[id] = make(map[chan AgentEvent]struct{})
		}
		m.agentSubscribers[id][ch] = struct{}{}
	} else {
		close(ch)
	}
	cancel := func() {
		m.agentMu.Lock()
		if subs := m.agentSubscribers[id]; subs != nil {
			if _, exists := subs[ch]; exists {
				delete(subs, ch)
				close(ch)
			}
		}
		m.agentMu.Unlock()
	}
	return cloneAgentSession(as), m.agentEventsAfterLocked(as, after), ch, cancel, true
}

func (m *Manager) appendAgentEvent(id, stream, data string) {
	if data == "" {
		return
	}
	for len(data) > maxAgentEventBytes {
		m.appendAgentEvent(id, stream, data[:maxAgentEventBytes])
		data = data[maxAgentEventBytes:]
	}
	m.agentMu.Lock()
	as := m.agentSessions[id]
	if as == nil {
		m.agentMu.Unlock()
		return
	}
	as.LastSeq++
	ev := AgentEvent{Seq: as.LastSeq, Time: time.Now().UTC(), Stream: stream, Data: data}
	as.Events = append(as.Events, ev)
	if len(as.Events) > maxAgentEventsMemory {
		as.Events = append([]AgentEvent(nil), as.Events[len(as.Events)-maxAgentEventsMemory:]...)
	}
	if !as.TranscriptTruncated {
		if err := m.persistAgentEventLocked(id, ev); err != nil {
			as.TranscriptTruncated = true
		}
	}
	for ch := range m.agentSubscribers[id] {
		select {
		case ch <- ev:
		default:
		}
	}
	m.agentMu.Unlock()
}

func (m *Manager) cancelAgentSessions(citadelID string) {
	m.agentMu.Lock()
	var cancels []context.CancelFunc
	for id, cancel := range m.agentCancels {
		as := m.agentSessions[id]
		if citadelID == "" || (as != nil && as.CitadelID == citadelID) {
			cancels = append(cancels, cancel)
		}
	}
	m.agentMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *Manager) ensureAgentSessionMapsLocked() {
	if m.agentSessions == nil {
		m.agentSessions = make(map[string]*AgentSession)
	}
	if m.agentSubscribers == nil {
		m.agentSubscribers = make(map[string]map[chan AgentEvent]struct{})
	}
	if m.agentCancels == nil {
		m.agentCancels = make(map[string]context.CancelFunc)
	}
}

func cloneAgentSession(as *AgentSession) AgentSession {
	copy := *as
	copy.Command = append([]string(nil), as.Command...)
	copy.Events = nil
	return copy
}

func eventsAfter(events []AgentEvent, after int64) []AgentEvent {
	out := make([]AgentEvent, 0)
	for _, ev := range events {
		if ev.Seq > after {
			out = append(out, ev)
		}
	}
	return out
}

func (m *Manager) agentEventsAfterLocked(as *AgentSession, after int64) []AgentEvent {
	if as == nil {
		return nil
	}
	if len(as.Events) == 0 || after >= as.Events[0].Seq-1 || m.stateDir == "" {
		return eventsAfter(as.Events, after)
	}
	persisted, err := m.loadAllAgentEventsLocked(as.ID)
	if err != nil || len(persisted) == 0 {
		return eventsAfter(as.Events, after)
	}
	last := persisted[len(persisted)-1].Seq
	for _, ev := range as.Events {
		if ev.Seq > last {
			persisted = append(persisted, ev)
		}
	}
	return eventsAfter(persisted, after)
}

func agentSessionTerminal(status string) bool {
	switch status {
	case "completed", "failed", "denied", "canceled", "interrupted":
		return true
	default:
		return false
	}
}

func (m *Manager) saveAgentSessions() error {
	m.agentMu.Lock()
	sessions := make([]AgentSession, 0, len(m.agentSessions))
	for _, as := range m.agentSessions {
		sessions = append(sessions, cloneAgentSession(as))
	}
	m.agentMu.Unlock()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].CreatedAt.Before(sessions[j].CreatedAt) })
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	return m.writeStateFile(agentSessionsFileName, data)
}

func (m *Manager) loadAgentSessions() error {
	if m.stateDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(m.stateDir, agentSessionsFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var sessions []AgentSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		return err
	}
	interrupted := false
	now := time.Now().UTC()
	m.agentMu.Lock()
	m.ensureAgentSessionMapsLocked()
	for i := range sessions {
		as := sessions[i]
		if !validAgentSessionID(as.ID) {
			continue
		}
		as.Events, _ = m.loadAgentEventsLocked(as.ID)
		if as.Status == "queued" || as.Status == "running" {
			lastStatus := ""
			if n := len(as.Events); n > 0 && as.Events[n-1].Stream == "status" {
				lastStatus = as.Events[n-1].Data
			}
			if agentSessionTerminal(lastStatus) {
				as.Status = lastStatus
			} else {
				as.Status = "interrupted"
				as.Error = "control plane restarted before the agent session completed"
			}
			as.FinishedAt = &now
			interrupted = true
		}
		if n := len(as.Events); n > 0 && as.Events[n-1].Seq > as.LastSeq {
			as.LastSeq = as.Events[n-1].Seq
		}
		m.agentSessions[as.ID] = &as
	}
	m.agentMu.Unlock()
	if interrupted {
		return m.saveAgentSessions()
	}
	return nil
}

func (m *Manager) persistAgentEventLocked(id string, ev AgentEvent) error {
	if m.stateDir == "" || !validAgentSessionID(id) {
		return nil
	}
	dir := filepath.Join(m.stateDir, agentSessionDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if info, statErr := f.Stat(); statErr != nil {
		return statErr
	} else if info.Size()+int64(len(line)) > maxAgentTranscriptBytes {
		return fmt.Errorf("agent transcript exceeds %d bytes", maxAgentTranscriptBytes)
	}
	if _, err := f.Write(line); err != nil {
		return err
	}
	return nil
}

func (m *Manager) loadAgentEventsLocked(id string) ([]AgentEvent, error) {
	events, err := m.loadAllAgentEventsLocked(id)
	if len(events) > maxAgentEventsMemory {
		events = append([]AgentEvent(nil), events[len(events)-maxAgentEventsMemory:]...)
	}
	return events, err
}

func (m *Manager) loadAllAgentEventsLocked(id string) ([]AgentEvent, error) {
	path := filepath.Join(m.stateDir, agentSessionDirName, id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var events []AgentEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 256<<10)
	for scanner.Scan() {
		var ev AgentEvent
		if json.Unmarshal(scanner.Bytes(), &ev) == nil {
			events = append(events, ev)
		}
	}
	return events, scanner.Err()
}

func validAgentSessionID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16
}

type agentEventWriter struct {
	mu        sync.Mutex
	manager   *Manager
	sessionID string
	stream    string
	pending   []byte
	scrub     func(string) string
}

func (w *agentEventWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		idx := bytes.IndexByte(w.pending, '\n')
		if idx < 0 && len(w.pending) <= maxAgentEventBytes {
			break
		}
		end := idx + 1
		if idx < 0 || end > maxAgentEventBytes {
			end = maxAgentEventBytes
		}
		w.emit(string(w.pending[:end]))
		w.pending = w.pending[end:]
	}
	return len(p), nil
}

func (w *agentEventWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.emit(string(w.pending))
		w.pending = nil
	}
}

func (w *agentEventWriter) emit(data string) {
	if w.scrub != nil {
		data = w.scrub(data)
	}
	w.manager.appendAgentEvent(w.sessionID, w.stream, data)
}

var _ io.Writer = (*agentEventWriter)(nil)
