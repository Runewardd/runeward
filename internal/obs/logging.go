// Package obs holds runeward's observability wiring: structured logging and
// Prometheus metrics. It has no dependency on the control plane, so any package
// can record metrics without creating an import cycle.
package obs

import (
	"log"
	"log/slog"
	"os"
	"strings"
)

// NewLogger builds the structured logger for the operator-facing services.
// $RUNEWARD_LOG_FORMAT selects "json" or "text" (default text); $RUNEWARD_LOG_LEVEL
// selects debug/info/warn/error (default info). Logs go to stderr.
func NewLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(os.Getenv("RUNEWARD_LOG_LEVEL"))}
	var h slog.Handler
	if strings.EqualFold(strings.TrimSpace(os.Getenv("RUNEWARD_LOG_FORMAT")), "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// StdLogger adapts an slog.Logger to a *log.Logger, so packages that still take
// the stdlib logger emit through the same structured pipeline.
func StdLogger(l *slog.Logger) *log.Logger {
	return slog.NewLogLogger(l.Handler(), slog.LevelInfo)
}
