// Package telemetry sends optional, anonymous usage events. It is OFF by
// default and only active when the operator explicitly opts in AND configures
// an endpoint, so runeward never phones home without consent.
//
// Enable with:
//
//	RUNEWARD_TELEMETRY=1
//	RUNEWARD_TELEMETRY_ENDPOINT=https://your-collector.example/ingest
//
// The DO_NOT_TRACK convention (https://consoledonottrack.com) always wins.
// Events carry only the runeward version, OS, and architecture plus any
// explicit, non-identifying properties the caller passes — no hostnames, no
// paths, no profile contents, no IDs.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	enableEnv   = "RUNEWARD_TELEMETRY"
	endpointEnv = "RUNEWARD_TELEMETRY_ENDPOINT"
)

// Enabled reports whether telemetry should be sent: opt-in flag set, an
// endpoint configured, and DO_NOT_TRACK not set.
func Enabled() bool {
	if isTrue(os.Getenv("DO_NOT_TRACK")) {
		return false
	}
	return isTrue(os.Getenv(enableEnv)) && strings.TrimSpace(os.Getenv(endpointEnv)) != ""
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type event struct {
	Event   string            `json:"event"`
	Version string            `json:"version"`
	OS      string            `json:"os"`
	Arch    string            `json:"arch"`
	Props   map[string]string `json:"props,omitempty"`
	Time    string            `json:"time"`
}

// Report sends an anonymous event best-effort. It returns immediately (the send
// happens in the background with a short timeout) and never errors; when
// telemetry is disabled it is a no-op.
func Report(version, name string, props map[string]string) {
	if !Enabled() {
		return
	}
	endpoint := strings.TrimSpace(os.Getenv(endpointEnv))
	ev := event{
		Event:   name,
		Version: version,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Props:   props,
		Time:    time.Now().UTC().Format(time.RFC3339),
	}
	go func() {
		body, err := json.Marshal(ev)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
}

// Notice is a one-line, human-readable disclosure of the current telemetry
// state, suitable for logging at startup.
func Notice() string {
	if Enabled() {
		return "telemetry enabled: anonymous usage events (version, os, arch) sent to " +
			strings.TrimSpace(os.Getenv(endpointEnv)) + "; unset " + enableEnv + " to disable"
	}
	return "telemetry disabled (opt in with " + enableEnv + "=1 and " + endpointEnv + ")"
}
