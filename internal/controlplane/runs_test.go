package controlplane

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/profile"
)

func TestRunsPersistAndRunningBecomesInterruptedOnRestart(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{stateDir: dir, runs: map[string]Run{
		"run-1": {ID: "run-1", CitadelID: "citadel-1", Tenant: "team", Actor: "codex", Charter: "dev", Status: "running", CreatedAt: time.Now().Add(-time.Minute)},
	}}
	if err := m.saveRuns(); err != nil {
		t.Fatal(err)
	}
	restarted := &Manager{stateDir: dir, runs: make(map[string]Run)}
	if err := restarted.loadRuns(); err != nil {
		t.Fatal(err)
	}
	run, ok := restarted.Run("run-1")
	if !ok || run.Status != "interrupted" || run.FinishedAt == nil || run.Error == "" {
		t.Fatalf("unexpected restored run: %#v", run)
	}
	info, err := os.Stat(dir + "/" + runsFileName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("runs file permissions = %v", info.Mode().Perm())
	}
}

func TestChronicleUsesCurrentActorWithinTenant(t *testing.T) {
	m, _ := newTestManager(t, nil, 0)
	sess := &Session{
		Sandbox: &backend.Sandbox{ID: "citadel-1"},
		Profile: &profile.Profile{Name: "dev"},
		Owner:   "team-alpha",
		Actor:   "creator",
		RunID:   "run-1",
	}
	m.record(WithActor(context.Background(), "codex-agent"), sess, "shell", "echo hi", nil, "allow", 0, 1, "")
	records, err := m.Ledger().Records()
	if err != nil {
		t.Fatal(err)
	}
	last := records[len(records)-1]
	if last.Meta["actor"] != "codex-agent" || last.Meta["tenant"] != "team-alpha" || last.Meta["run_id"] != "run-1" {
		t.Fatalf("unexpected attribution: %#v", last.Meta)
	}
}
