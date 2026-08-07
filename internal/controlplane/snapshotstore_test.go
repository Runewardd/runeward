package controlplane

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/backend"
)

type snapshotTestBackend struct {
	fakeBackend
}

func (b *snapshotTestBackend) Snapshot(_ context.Context, id, name string) (*backend.SnapshotRef, error) {
	return &backend.SnapshotRef{
		ID:       "snapshot-1",
		Name:     name,
		Backend:  b.Name(),
		Location: filepath.Join("snapshots", id),
		Created:  time.Unix(1_700_000_000, 0).UTC(),
	}, nil
}

func TestSnapshotPersistsProfileAndTenantOwner(t *testing.T) {
	stateDir := t.TempDir()
	mgr, _ := newTestManager(t, nil, time.Second)
	mgr.stateDir = stateDir
	mgr.snapshots = make(map[string]backend.SnapshotRef)
	mgr.snapshotOwners = make(map[string]string)
	mgr.sessions["fake-1"].Backend = &snapshotTestBackend{}
	mgr.sessions["fake-1"].Owner = "tenant-alpha"

	ref, err := mgr.Snapshot(context.Background(), "fake-1", "before-upgrade")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if ref.Profile != "test" {
		t.Fatalf("snapshot profile = %q, want test", ref.Profile)
	}

	reloaded := &Manager{
		stateDir:       stateDir,
		snapshots:      make(map[string]backend.SnapshotRef),
		snapshotOwners: make(map[string]string),
	}
	if err := reloaded.loadSnapshots(); err != nil {
		t.Fatalf("loadSnapshots: %v", err)
	}
	got, ok := reloaded.SnapshotRef(ref.ID)
	if !ok {
		t.Fatalf("snapshot %q missing after restart", ref.ID)
	}
	if got.Profile != "test" || got.Name != "before-upgrade" {
		t.Fatalf("reloaded snapshot = %#v", got)
	}
	if owner, ok := reloaded.SnapshotOwner(ref.ID); !ok || owner != "tenant-alpha" {
		t.Fatalf("reloaded owner = %q, %v; want tenant-alpha, true", owner, ok)
	}
	if snapshots := reloaded.ListSnapshots(); len(snapshots) != 1 || snapshots[0].ID != ref.ID {
		t.Fatalf("ListSnapshots = %#v", snapshots)
	}
}
