package controlplane

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/fleet"
)

func TestTaskLeaseBindsCohortTaskAndActor(t *testing.T) {
	m := &Manager{leaseKey: []byte("01234567890123456789012345678901")}
	task := fleet.Task{ID: "task-1", Owner: "codex", LeaseExpiry: time.Now().Add(time.Minute), LeaseVersion: 3}
	token, err := m.issueTaskLease("cohort-1", task)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.verifyTaskLease(token, "cohort-1", "task-1", "codex", 3); err != nil {
		t.Fatalf("valid lease rejected: %v", err)
	}
	for _, tc := range []struct{ cohort, task, actor string }{
		{"other", "task-1", "codex"}, {"cohort-1", "other", "codex"}, {"cohort-1", "task-1", "claude"},
	} {
		if err := m.verifyTaskLease(token, tc.cohort, tc.task, tc.actor, 3); !errors.Is(err, ErrInvalidLease) {
			t.Fatalf("wrong binding accepted: %#v, err=%v", tc, err)
		}
	}
	if err := m.verifyTaskLease(token, "cohort-1", "task-1", "codex", 4); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("stale lease version accepted: %v", err)
	}
}

func TestTaskLeaseExpires(t *testing.T) {
	m := &Manager{leaseKey: []byte("01234567890123456789012345678901")}
	token, err := m.issueTaskLease("c", fleet.Task{ID: "t", Owner: "a", LeaseExpiry: time.Now().Add(-time.Second), LeaseVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.verifyTaskLease(token, "c", "t", "a", 1); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("expired lease accepted: %v", err)
	}
}

func TestLeaseKeyIsPersistentAndPrivate(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateLeaseKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateLeaseKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("lease key was not persisted")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir + "/" + leaseKeyFileName)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("lease key permissions = %v", info.Mode().Perm())
		}
	}
}
