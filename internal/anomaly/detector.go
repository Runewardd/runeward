// Package anomaly provides a lightweight, in-process behavioural anomaly
// detector that plugs into the audit-sink chain. It watches the stream of
// ledger events and emits structured slog warnings when a session's activity
// crosses configurable thresholds: contacting too many distinct network
// destinations, bursting shell executions, or accumulating denials. It keeps
// only small per-session summaries in memory and never blocks the caller, so
// it is safe to drop into auditsink.NewMulti alongside the real sinks.
package anomaly

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Runewardd/runeward/internal/auditsink"
	"github.com/Runewardd/runeward/internal/ledger"
)

// Environment variables recognised by New, with their defaults.
const (
	// EnvMaxHosts caps distinct network destinations per session before a
	// novel-host anomaly fires. Default 20.
	EnvMaxHosts = "RUNEWARD_ANOMALY_MAX_HOSTS"
	// EnvExecBurst is the number of shell execs within Window that trips an
	// exec-burst anomaly. Default 30.
	EnvExecBurst = "RUNEWARD_ANOMALY_EXEC_BURST"
	// EnvWindow is the sliding window for exec-burst detection. Default 1m.
	EnvWindow = "RUNEWARD_ANOMALY_WINDOW"
	// EnvMaxDenies caps deny verdicts per session before a denial-spike
	// anomaly fires. Default 10.
	EnvMaxDenies = "RUNEWARD_ANOMALY_MAX_DENIES"
)

// Default thresholds used when the corresponding env var is unset or invalid.
const (
	defaultMaxHosts  = 20
	defaultExecBurst = 30
	defaultWindow    = time.Minute
	defaultMaxDenies = 10
)

// warnCooldown is the minimum interval between warnings for the same
// (session, kind) pair, so a sustained anomaly logs at most once per minute.
const warnCooldown = time.Minute

// Anomaly kind identifiers, used both as the slog "anomaly" attribute value
// and as the per-session rate-limit key.
const (
	kindNovelHost   = "novel_host"
	kindExecBurst   = "exec_burst"
	kindDenialSpike = "denial_spike"
)

// Counts is a snapshot of how many times each anomaly kind has fired (after
// rate limiting) since the detector was created.
type Counts struct {
	NovelHost   int
	ExecBurst   int
	DenialSpike int
}

// sessionState holds the small rolling summary tracked for a single session.
type sessionState struct {
	// hosts is the set of distinct destinations contacted with an allowed
	// verdict.
	hosts map[string]struct{}
	// execTimes holds shell-exec timestamps within the current window,
	// oldest first.
	execTimes []time.Time
	// denies counts deny verdicts seen for the session.
	denies int
	// lastWarn records the last warning time per anomaly kind for rate
	// limiting.
	lastWarn map[string]time.Time
}

func newSessionState() *sessionState {
	return &sessionState{
		hosts:    make(map[string]struct{}),
		lastWarn: make(map[string]time.Time),
	}
}

// Detector inspects audit events and warns on anomalous per-session
// behaviour. It implements auditsink.Sink so it can be composed into
// auditsink.NewMulti. It is safe for concurrent use.
type Detector struct {
	logger *slog.Logger

	// now supplies the current time for sliding windows and rate limiting;
	// it defaults to time.Now and is overridable in-package for tests.
	now func() time.Time

	maxHosts  int
	execBurst int
	window    time.Duration
	maxDenies int

	mu       sync.Mutex
	sessions map[string]*sessionState
	counts   Counts
}

// Compile-time check that Detector satisfies the audit sink contract.
var _ auditsink.Sink = (*Detector)(nil)

// New returns a Detector with thresholds read from the environment, falling
// back to the documented defaults. A nil logger uses slog.Default.
func New(logger *slog.Logger) *Detector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Detector{
		logger:    logger,
		now:       time.Now,
		maxHosts:  envInt(EnvMaxHosts, defaultMaxHosts),
		execBurst: envInt(EnvExecBurst, defaultExecBurst),
		window:    envDuration(EnvWindow, defaultWindow),
		maxDenies: envInt(EnvMaxDenies, defaultMaxDenies),
		sessions:  make(map[string]*sessionState),
	}
}

// Emit inspects a single event and raises anomaly warnings as needed. It
// never blocks and never fails, matching the auditsink.Sink contract.
func (d *Detector) Emit(ev ledger.Event) {
	t := ev.Time
	if t.IsZero() {
		t = d.now()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	st := d.sessions[ev.SessionID]
	if st == nil {
		st = newSessionState()
		d.sessions[ev.SessionID] = st
	}

	switch ev.Tool {
	case "net", "browser":
		if isAllowed(ev.Verdict) {
			if host := parseHost(ev.Action); host != "" {
				if _, seen := st.hosts[host]; !seen {
					st.hosts[host] = struct{}{}
					if len(st.hosts) > d.maxHosts {
						d.warn(st, kindNovelHost, t, ev,
							"session contacted an unusual number of distinct destinations",
							slog.String("host", host),
							slog.Int("distinct_hosts", len(st.hosts)),
							slog.Int("threshold", d.maxHosts),
						)
					}
				}
			}
		}
	case "shell":
		st.execTimes = appendWithinWindow(st.execTimes, t, d.window)
		if len(st.execTimes) > d.execBurst {
			d.warn(st, kindExecBurst, t, ev,
				"session exceeded the shell execution burst threshold",
				slog.Int("execs_in_window", len(st.execTimes)),
				slog.Int("threshold", d.execBurst),
				slog.Duration("window", d.window),
			)
		}
	}

	if ev.Verdict == "deny" {
		st.denies++
		if st.denies > d.maxDenies {
			d.warn(st, kindDenialSpike, t, ev,
				"session exceeded the denial spike threshold",
				slog.Int("denies", st.denies),
				slog.Int("threshold", d.maxDenies),
			)
		}
	}
}

// Close is a no-op flush; the detector holds no external resources.
func (d *Detector) Close() error { return nil }

// Counts returns a snapshot of the anomaly counters.
func (d *Detector) Counts() Counts {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counts
}

// warn emits a rate-limited warning for the given anomaly kind and, when not
// suppressed, increments the corresponding counter. The caller holds d.mu.
func (d *Detector) warn(st *sessionState, kind string, t time.Time, ev ledger.Event, msg string, attrs ...any) {
	if last, ok := st.lastWarn[kind]; ok && t.Sub(last) < warnCooldown {
		return
	}
	st.lastWarn[kind] = t

	switch kind {
	case kindNovelHost:
		d.counts.NovelHost++
	case kindExecBurst:
		d.counts.ExecBurst++
	case kindDenialSpike:
		d.counts.DenialSpike++
	}

	base := []any{
		slog.String("anomaly", kind),
		slog.String("session_id", ev.SessionID),
		slog.String("sandbox", ev.Sandbox),
		slog.String("profile", ev.Profile),
	}
	d.logger.Warn(msg, append(base, attrs...)...)
}

// appendWithinWindow appends t to times and drops any entries older than
// window relative to t, keeping the slice bounded by the sliding window.
func appendWithinWindow(times []time.Time, t time.Time, window time.Duration) []time.Time {
	times = append(times, t)
	cutoff := t.Add(-window)
	i := 0
	for i < len(times) && times[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		times = times[i:]
	}
	return times
}

// isAllowed reports whether a verdict permitted the action.
func isAllowed(verdict string) bool {
	return verdict == "allow"
}

// parseHost extracts a best-effort hostname from an Action that may be a bare
// host, a "host:port", or a full URL. It returns "" when nothing host-like
// can be found.
func parseHost(action string) string {
	s := strings.TrimSpace(action)
	if s == "" {
		return ""
	}
	// Drop a scheme like "https://".
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Drop path, query, or fragment.
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// Drop any userinfo ("user:pass@host").
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	// Handle bracketed IPv6 ("[::1]:8080") before generic port stripping.
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i >= 0 {
			return strings.ToLower(strings.TrimSpace(s[1:i]))
		}
	}
	// Drop a trailing ":port".
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// envInt reads a positive integer from the named env var, or returns def.
func envInt(name string, def int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// envDuration reads a positive time.Duration from the named env var, or
// returns def.
func envDuration(name string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	dur, err := time.ParseDuration(raw)
	if err != nil || dur <= 0 {
		return def
	}
	return dur
}
