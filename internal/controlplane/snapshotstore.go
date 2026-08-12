package controlplane

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Runewardd/runeward/internal/backend"
)

const snapshotsFileName = "snapshots.json"

type persistedSnapshot struct {
	Ref   backend.SnapshotRef `json:"ref"`
	Owner string              `json:"owner,omitempty"`
}

func (m *Manager) snapshotsPath() string {
	if m.stateDir == "" {
		return ""
	}
	return filepath.Join(m.stateDir, snapshotsFileName)
}

func (m *Manager) loadSnapshots() error {
	path := m.snapshotsPath()
	if path == "" {
		return nil
	}
	// #nosec G304 -- path is the fixed snapshotsFileName beneath Manager.stateDir.
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var refs []persistedSnapshot
	if err := json.Unmarshal(b, &refs); err != nil {
		return err
	}
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	for _, item := range refs {
		m.snapshots[item.Ref.ID] = item.Ref
		m.snapshotOwners[item.Ref.ID] = item.Owner
	}
	return nil
}

func (m *Manager) saveSnapshots() error {
	path := m.snapshotsPath()
	if path == "" {
		return nil
	}
	m.snapMu.Lock()
	refs := make([]persistedSnapshot, 0, len(m.snapshots))
	for id, ref := range m.snapshots {
		refs = append(refs, persistedSnapshot{Ref: ref, Owner: m.snapshotOwners[id]})
	}
	m.snapMu.Unlock()
	data, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		return err
	}
	return m.writeStateFile(snapshotsFileName, data)
}
