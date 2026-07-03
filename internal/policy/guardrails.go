package policy

import (
	"errors"
	"sync"
	"time"

	"github.com/adefemi171/runeward/internal/profile"
)

// Sentinel errors returned by [Guard]. Callers should compare with
// [errors.Is].
var (
	// ErrWallClock is returned once the session wall-clock deadline passes.
	ErrWallClock = errors.New("policy: wall-clock deadline exceeded")
	// ErrMaxExecs is returned once the exec budget is exhausted.
	ErrMaxExecs = errors.New("policy: max execs exceeded")
	// ErrEgressBudget is returned once the egress request budget is
	// exhausted.
	ErrEgressBudget = errors.New("policy: egress budget exceeded")
	// ErrLoopDetected is returned once a non-converging failure loop trips
	// the loop detector.
	ErrLoopDetected = errors.New("policy: runaway loop detected")
)

// Guard enforces per-session cost and loop guardrails derived from
// [profile.Limits]. It is safe for concurrent use.
//
// All limits are opt-in: a zero/empty value in the source [profile.Limits]
// disables that particular guard (unlimited execs, no wall-clock, and so on).
//
// Typical use:
//
//	g, err := NewGuard(limits)
//	// ...
//	g.Start()
//	for _, step := range plan {
//	    if err := g.CheckExec(); err != nil { return err }
//	    err := run(step)
//	    g.RecordOutcome(step.Key(), err != nil)
//	}
type Guard struct {
	wallClock   time.Duration // 0 = disabled
	maxExecs    int           // 0 = unlimited
	egressLimit int           // 0 = unlimited
	loopWindow  time.Duration // 0 = loop detection disabled
	loopThresh  int           // 0 = loop detection disabled

	now func() time.Time // injectable clock for testing

	mu        sync.Mutex
	started   bool
	startTime time.Time
	execs     int
	egress    int
	loopKey   string                 // key that tripped the loop, if any
	tripped   bool                   // whether a loop has tripped
	failures  map[string][]time.Time // sliding window of failure timestamps per key
}

// NewGuard builds a [Guard] from limits. WallClock and LoopWindow are parsed
// with [time.ParseDuration]; an empty string leaves that guard disabled. An
// error is returned if either duration string is present but unparseable.
func NewGuard(limits profile.Limits) (*Guard, error) {
	var (
		wall time.Duration
		loop time.Duration
		err  error
	)
	if limits.WallClock != "" {
		if wall, err = time.ParseDuration(limits.WallClock); err != nil {
			return nil, err
		}
	}
	if limits.LoopWindow != "" {
		if loop, err = time.ParseDuration(limits.LoopWindow); err != nil {
			return nil, err
		}
	}
	return &Guard{
		wallClock:   wall,
		maxExecs:    limits.MaxExecs,
		egressLimit: limits.EgressRequests,
		loopWindow:  loop,
		loopThresh:  limits.LoopThreshold,
		now:         time.Now,
		failures:    make(map[string][]time.Time),
	}, nil
}

// Start records the session start time used for wall-clock enforcement. It is
// safe to call more than once; only the first call takes effect.
func (g *Guard) Start() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.started {
		g.started = true
		g.startTime = g.now()
	}
}

// CheckExec must be called before each tool invocation. It enforces, in order:
// the wall-clock deadline, a previously tripped loop, and the exec budget,
// incrementing the exec counter only when the call is permitted.
//
// It returns [ErrWallClock], [ErrLoopDetected], or [ErrMaxExecs] respectively,
// or nil when the exec may proceed.
func (g *Guard) CheckExec() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.wallClock > 0 && g.started {
		if g.now().Sub(g.startTime) >= g.wallClock {
			return ErrWallClock
		}
	}
	if g.tripped {
		return ErrLoopDetected
	}
	if g.maxExecs > 0 && g.execs >= g.maxExecs {
		return ErrMaxExecs
	}
	g.execs++
	return nil
}

// CheckEgress must be called before each outbound request. It enforces the
// egress budget, incrementing the egress counter only when permitted, and
// returns [ErrEgressBudget] once the budget is exhausted.
func (g *Guard) CheckEgress() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.egressLimit > 0 && g.egress >= g.egressLimit {
		return ErrEgressBudget
	}
	g.egress++
	return nil
}

// RecordOutcome records the result of an action identified by key. Successful
// outcomes (failed == false) clear the key's failure window, treating any
// success as convergence. Failures append to a sliding window; once a key
// accumulates loopThreshold failures within loopWindow the loop detector trips
// and stays tripped until [Guard.Reset].
//
// Loop detection is disabled (this method only bookkeeps) when either
// loopWindow or loopThreshold is zero.
func (g *Guard) RecordOutcome(key string, failed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !failed {
		delete(g.failures, key)
		return
	}
	if g.loopWindow <= 0 || g.loopThresh <= 0 {
		return
	}

	now := g.now()
	cutoff := now.Add(-g.loopWindow)

	// Prune timestamps that have aged out of the sliding window.
	times := g.failures[key]
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	g.failures[key] = kept

	if len(kept) >= g.loopThresh {
		g.tripped = true
		g.loopKey = key
	}
}

// LoopTripped reports whether a runaway failure loop has been detected and, if
// so, the key responsible.
func (g *Guard) LoopTripped() (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tripped, g.loopKey
}

// Reset clears counters, the failure windows, and any tripped loop state so the
// same Guard can be reused for a fresh session. The parsed limits are retained.
func (g *Guard) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.started = false
	g.startTime = time.Time{}
	g.execs = 0
	g.egress = 0
	g.tripped = false
	g.loopKey = ""
	g.failures = make(map[string][]time.Time)
}
