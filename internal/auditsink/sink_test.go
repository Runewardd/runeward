package auditsink

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/ledger"
)

func sampleEvent() ledger.Event {
	return ledger.Event{
		Seq:       7,
		Time:      time.Date(2026, 7, 4, 17, 0, 0, 0, time.UTC),
		SessionID: "sess-abc",
		Sandbox:   "sbx-1",
		Profile:   "default",
		Tool:      "shell",
		Action:    "rm -rf /tmp/x",
		Args:      []string{"-rf", "/tmp/x"},
		Verdict:   "allow",
		Hash:      "deadbeef",
	}
}

func TestWebhookSinkDeliversEvent(t *testing.T) {
	type result struct {
		ev          ledger.Event
		contentType string
		auth        string
	}
	got := make(chan result, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev ledger.Event
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Errorf("server: bad JSON body: %v", err)
		}
		got <- result{
			ev:          ev,
			contentType: r.Header.Get("Content-Type"),
			auth:        r.Header.Get("Authorization"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewWebhookSink(WebhookConfig{
		URL:         srv.URL,
		HeaderKey:   "Authorization",
		HeaderValue: "Bearer secret-token",
	})
	defer s.Close()

	want := sampleEvent()
	s.Emit(want)

	select {
	case r := <-got:
		if r.ev.Seq != want.Seq {
			t.Errorf("Seq = %d, want %d", r.ev.Seq, want.Seq)
		}
		if r.ev.SessionID != want.SessionID {
			t.Errorf("SessionID = %q, want %q", r.ev.SessionID, want.SessionID)
		}
		if r.ev.Tool != want.Tool {
			t.Errorf("Tool = %q, want %q", r.ev.Tool, want.Tool)
		}
		if r.ev.Action != want.Action {
			t.Errorf("Action = %q, want %q", r.ev.Action, want.Action)
		}
		if r.ev.Verdict != want.Verdict {
			t.Errorf("Verdict = %q, want %q", r.ev.Verdict, want.Verdict)
		}
		if r.contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.contentType)
		}
		if r.auth != "Bearer secret-token" {
			t.Errorf("Authorization = %q, want %q", r.auth, "Bearer secret-token")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook server did not receive event within timeout")
	}
}

func TestWebhookSinkRetries(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	done := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	s := NewWebhookSink(WebhookConfig{URL: srv.URL})
	defer s.Close()
	s.Emit(sampleEvent())

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("webhook did not succeed after retry")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts < 2 {
		t.Errorf("attempts = %d, want >= 2 (retry expected)", attempts)
	}
}

func TestFileSinkAppendsJSONLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	s, err := NewFileSink(path, nil)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	ev1 := sampleEvent()
	ev2 := sampleEvent()
	ev2.Seq = 8
	ev2.SessionID = "sess-def"
	s.Emit(ev1)
	s.Emit(ev2)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// File mode should be 0o600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var events []ledger.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev ledger.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal line: %v", err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Seq != 7 || events[1].Seq != 8 {
		t.Errorf("seqs = %d,%d, want 7,8", events[0].Seq, events[1].Seq)
	}
	if events[1].SessionID != "sess-def" {
		t.Errorf("events[1].SessionID = %q, want sess-def", events[1].SessionID)
	}
}

// TestEmitDoesNotBlockOnSlowEndpoint asserts Emit returns quickly even when
// the webhook endpoint hangs and the queue is saturated with events.
func TestEmitDoesNotBlockOnSlowEndpoint(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // hang until the test releases it
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(block)

	s := NewWebhookSink(WebhookConfig{URL: srv.URL, QueueSize: 8})
	defer s.Close()

	// Fire far more events than the queue can hold; the worker is stuck on
	// the hanging request, so the queue saturates and Emit must drop rather
	// than block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100000; i++ {
			ev := sampleEvent()
			ev.Seq = i
			s.Emit(ev)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked: 100k emits did not complete within 2s")
	}
}

func TestFromEnvNopWhenUnset(t *testing.T) {
	t.Setenv(EnvWebhookURL, "")
	t.Setenv(EnvFile, "")

	s, err := FromEnv(nil)
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if _, ok := s.(nopSink); !ok {
		t.Errorf("FromEnv returned %T, want nopSink", s)
	}
	s.Emit(sampleEvent()) // must not panic
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestFromEnvInvalidURL(t *testing.T) {
	t.Setenv(EnvWebhookURL, "not-a-valid-scheme://%zz")
	if _, err := FromEnv(nil); err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestFromEnvRejectsNonHTTPURL(t *testing.T) {
	t.Setenv(EnvWebhookURL, "ftp://example.com/hook")
	if _, err := FromEnv(nil); err == nil {
		t.Fatal("expected error for non-http URL, got nil")
	}
}

func TestFromEnvBadHeader(t *testing.T) {
	t.Setenv(EnvWebhookURL, "https://example.com/hook")
	t.Setenv(EnvWebhookHeader, "no-colon-here")
	if _, err := FromEnv(nil); err == nil {
		t.Fatal("expected error for malformed header, got nil")
	}
}

func TestFromEnvBuildsMulti(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvWebhookURL, "https://example.com/hook")
	t.Setenv(EnvFile, filepath.Join(dir, "audit.jsonl"))

	s, err := FromEnv(nil)
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	defer s.Close()
	m, ok := s.(*Multi)
	if !ok {
		t.Fatalf("FromEnv returned %T, want *Multi", s)
	}
	if len(m.sinks) != 2 {
		t.Errorf("Multi has %d sinks, want 2", len(m.sinks))
	}
}
