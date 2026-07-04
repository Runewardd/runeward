package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	actionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "runeward",
		Subsystem: "actions",
		Name:      "total",
		Help:      "Governed actions processed, labeled by tool and verdict.",
	}, []string{"tool", "verdict"})

	actionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "runeward",
		Subsystem: "actions",
		Name:      "duration_seconds",
		Help:      "Wall-clock duration of executed governed actions.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"tool"})

	sandboxesCreated = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "runeward",
		Subsystem: "sandboxes",
		Name:      "created_total",
		Help:      "Total number of sandboxes created.",
	})

	buildInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "runeward",
		Name:      "build_info",
		Help:      "Build metadata; always 1, labeled by version.",
	}, []string{"version"})
)

// RecordAction records one governed action outcome. durationMS <= 0 (denied or
// non-executed actions) skips the duration observation.
func RecordAction(tool, verdict string, durationMS int64) {
	actionsTotal.WithLabelValues(tool, verdict).Inc()
	if durationMS > 0 {
		actionDuration.WithLabelValues(tool).Observe(float64(durationMS) / 1000)
	}
}

// IncSandboxCreated bumps the sandbox-creation counter.
func IncSandboxCreated() { sandboxesCreated.Inc() }

// SetBuildInfo publishes the running version as a build_info gauge.
func SetBuildInfo(version string) { buildInfo.WithLabelValues(version).Set(1) }

// MetricsHandler serves the Prometheus exposition endpoint.
func MetricsHandler() http.Handler { return promhttp.Handler() }
