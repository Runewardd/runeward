package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/fleet"
)

func TestLoadFleetsRequeuesOnlyLegacyUnsignedClaims(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	state := []persistedFleet{{
		ID:      "cohort-1",
		Profile: "agent",
		Owner:   "tenant-a",
		Tasks: []fleet.Task{
			{ID: "legacy", State: fleet.StateClaimed, Owner: "old-worker", UpdatedAt: now},
			{ID: "signed", State: fleet.StateClaimed, Owner: "live-worker", LeaseVersion: 2, LeaseExpiry: now.Add(time.Minute), UpdatedAt: now},
			{ID: "done", State: fleet.StateDone, Result: "ok", UpdatedAt: now},
		},
	}}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{stateDir: dir, fleets: make(map[string]*Fleet)}
	if err := m.writeStateFile(fleetsFileName, b); err != nil {
		t.Fatal(err)
	}
	if err := m.loadFleets(); err != nil {
		t.Fatal(err)
	}

	tasks, err := m.ListTasks("cohort-1")
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].State != fleet.StatePending || tasks[0].Owner != "" ||
		!strings.Contains(tasks[0].Error, "legacy unsigned lease") {
		t.Fatalf("legacy claim was not safely requeued: %#v", tasks[0])
	}
	if tasks[1].State != fleet.StateClaimed || tasks[1].Owner != "live-worker" || tasks[1].LeaseVersion != 2 {
		t.Fatalf("signed claim was unexpectedly changed: %#v", tasks[1])
	}
	if tasks[2].State != fleet.StateDone || tasks[2].Result != "ok" {
		t.Fatalf("completed task was unexpectedly changed: %#v", tasks[2])
	}

	// The migration is durable, not only an in-memory repair.
	restarted := &Manager{stateDir: dir, fleets: make(map[string]*Fleet)}
	if err := restarted.loadFleets(); err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.ListTasks("cohort-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].State != fleet.StatePending || loaded[0].Owner != "" {
		t.Fatalf("migrated task was not persisted: %#v", loaded[0])
	}
}
