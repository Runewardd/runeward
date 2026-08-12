package controlplane

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/backend"
)

type streamingFakeBackend struct {
	*fakeBackend
}

func (f *streamingFakeBackend) Exec(_ context.Context, _ string, req backend.ExecRequest) (*backend.ExecResult, error) {
	if req.Stdout != nil {
		_, _ = io.WriteString(req.Stdout, "hello\nsecret=sk-ant-abcdefghijklmnopqrstuvwxyz\n")
	}
	if req.Stderr != nil {
		_, _ = io.WriteString(req.Stderr, "warning\n")
	}
	return &backend.ExecResult{ExitCode: 0, Duration: time.Millisecond}, nil
}

func TestAgentSessionStreamsRedactedEventsAndPersists(t *testing.T) {
	m, fb := newTestManager(t, nil, 0)
	m.stateDir = t.TempDir()
	streaming := &streamingFakeBackend{fakeBackend: fb}
	m.sessions["fake-1"].Backend = streaming
	m.sessions["fake-1"].secrets = []string{"declared-secret"}

	as, err := m.StartAgentSession(context.Background(), "fake-1", StartAgentSessionOptions{
		Command: []string{"agent", "-p", "use declared-secret"}, Agent: "cursor", Model: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, ok := m.AgentSession(as.ID)
		if !ok {
			t.Fatal("agent session disappeared")
		}
		if agentSessionTerminal(current.Status) {
			if current.Status != "completed" || current.ExitCode == nil || *current.ExitCode != 0 {
				t.Fatalf("unexpected terminal session: %#v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for agent session")
		}
		time.Sleep(10 * time.Millisecond)
	}

	current, events, ok := m.AgentSessionEvents(as.ID, 0)
	if !ok || len(events) < 4 {
		t.Fatalf("session/events missing: session=%#v events=%#v", current, events)
	}
	encoded := ""
	for _, ev := range events {
		encoded += ev.Data
	}
	if !strings.Contains(encoded, "hello") || !strings.Contains(encoded, "warning") {
		t.Fatalf("stream output missing: %q", encoded)
	}
	if strings.Contains(encoded, "sk-ant-") || strings.Contains(strings.Join(current.Command, " "), "declared-secret") {
		t.Fatalf("session leaked a credential: session=%#v events=%q", current, encoded)
	}
	if !strings.Contains(encoded, "[REDACTED]") {
		t.Fatalf("expected redaction marker in %q", encoded)
	}

	meta, err := os.Stat(filepath.Join(m.stateDir, agentSessionsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Mode().Perm() != 0o600 {
		t.Fatalf("metadata mode = %v", meta.Mode().Perm())
	}
	eventInfo, err := os.Stat(filepath.Join(m.stateDir, agentSessionDirName, as.ID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if eventInfo.Mode().Perm() != 0o600 {
		t.Fatalf("event mode = %v", eventInfo.Mode().Perm())
	}
	restarted := &Manager{
		stateDir:         m.stateDir,
		agentSessions:    make(map[string]*AgentSession),
		agentSubscribers: make(map[string]map[chan AgentEvent]struct{}),
		agentCancels:     make(map[string]context.CancelFunc),
	}
	if err := restarted.loadAgentSessions(); err != nil {
		t.Fatal(err)
	}
	restored, restoredEvents, ok := restarted.AgentSessionEvents(as.ID, 0)
	if !ok || restored.Status != "completed" || len(restoredEvents) != len(events) {
		t.Fatalf("persisted transcript did not reload: session=%#v events=%d", restored, len(restoredEvents))
	}
}

func TestAgentSessionSubscriptionReturnsBacklogAndLiveEvents(t *testing.T) {
	m := &Manager{
		stateDir:         t.TempDir(),
		agentSessions:    make(map[string]*AgentSession),
		agentSubscribers: make(map[string]map[chan AgentEvent]struct{}),
		agentCancels:     make(map[string]context.CancelFunc),
	}
	id := newID()
	m.agentSessions[id] = &AgentSession{ID: id, Status: "running", CreatedAt: time.Now().UTC()}
	m.appendAgentEvent(id, "stdout", "first\n")
	_, backlog, live, cancel, ok := m.SubscribeAgentSession(id, 0)
	if !ok || len(backlog) != 1 || backlog[0].Data != "first\n" {
		t.Fatalf("unexpected backlog: %#v", backlog)
	}
	defer cancel()
	m.appendAgentEvent(id, "stderr", "second\n")
	select {
	case ev := <-live:
		if ev.Data != "second\n" || ev.Seq != 2 {
			t.Fatalf("unexpected live event: %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}
