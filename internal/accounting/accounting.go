// Package accounting tracks per-sandbox and per-profile token and spend usage,
// exposes Prometheus counters, and supports budget checks.
package accounting

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metrics are registered exactly once at package scope to avoid
// duplicate-registration panics when multiple Trackers are created (e.g. in
// tests). They are labeled by profile so cumulative spend can be tracked
// independently of any single tracker's in-memory maps.
var (
	usageTokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "runeward",
		Subsystem: "usage",
		Name:      "tokens_total",
		Help:      "Total tokens consumed, labeled by profile.",
	}, []string{"profile"})

	usageCostUSDTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "runeward",
		Subsystem: "usage",
		Name:      "cost_usd_total",
		Help:      "Total spend in USD, labeled by profile.",
	}, []string{"profile"})
)

// Usage is a running total of tokens and spend.
type Usage struct {
	Tokens  int64
	CostUSD float64
}

// Tracker accumulates usage per sandbox and per profile. It is safe for
// concurrent use.
type Tracker struct {
	mu      sync.Mutex
	sandbox map[string]Usage
	profile map[string]Usage
}

// New returns a ready-to-use Tracker.
func New() *Tracker {
	return &Tracker{
		sandbox: make(map[string]Usage),
		profile: make(map[string]Usage),
	}
}

// Record adds tokens and cost to both the sandbox and profile totals and
// increments the Prometheus counters for the profile. Negative inputs are
// clamped to zero.
func (t *Tracker) Record(profile, sandbox string, tokens int64, costUSD float64) {
	if tokens < 0 {
		tokens = 0
	}
	if costUSD < 0 {
		costUSD = 0
	}

	t.mu.Lock()
	s := t.sandbox[sandbox]
	s.Tokens += tokens
	s.CostUSD += costUSD
	t.sandbox[sandbox] = s

	p := t.profile[profile]
	p.Tokens += tokens
	p.CostUSD += costUSD
	t.profile[profile] = p
	t.mu.Unlock()

	usageTokensTotal.WithLabelValues(profile).Add(float64(tokens))
	usageCostUSDTotal.WithLabelValues(profile).Add(costUSD)
}

// Usage returns the current totals for a sandbox. Unknown sandboxes return the
// zero Usage.
func (t *Tracker) Usage(sandbox string) Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sandbox[sandbox]
}

// ProfileUsage returns the cumulative totals for a profile. Unknown profiles
// return the zero Usage.
func (t *Tracker) ProfileUsage(profile string) Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.profile[profile]
}

// Over reports whether a sandbox's usage meets or exceeds a positive limit. A
// limit that is <= 0 means "no limit" for that dimension. When over, the second
// return value is a human-readable explanation.
func (t *Tracker) Over(sandbox string, maxTokens int64, maxCostUSD float64) (bool, string) {
	u := t.Usage(sandbox)
	if maxTokens > 0 && u.Tokens >= maxTokens {
		return true, fmt.Sprintf("token budget exhausted: %d/%d tokens used", u.Tokens, maxTokens)
	}
	if maxCostUSD > 0 && u.CostUSD >= maxCostUSD {
		return true, fmt.Sprintf("cost budget exhausted: $%.4f/$%.4f spent", u.CostUSD, maxCostUSD)
	}
	return false, ""
}

// Forget drops a sandbox's per-sandbox entry, typically when the sandbox is
// killed. Profile totals are cumulative and retained.
func (t *Tracker) Forget(sandbox string) {
	t.mu.Lock()
	delete(t.sandbox, sandbox)
	t.mu.Unlock()
}
