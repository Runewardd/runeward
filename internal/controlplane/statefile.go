package controlplane

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeStateFile serializes durable control-plane updates and replaces the
// target atomically. The temporary file is unique, fsynced, and private so
// concurrent Cohort, snapshot, and run updates cannot trample one another.
func (m *Manager) writeStateFile(name string, data []byte) error {
	if m.stateDir == "" {
		return nil
	}
	m.persistMu.Lock()
	defer m.persistMu.Unlock()

	if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	tmp, err := os.CreateTemp(m.stateDir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("protect temporary state file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary state file: %w", err)
	}
	target := filepath.Join(m.stateDir, name)
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	committed = true
	if dir, err := os.Open(m.stateDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
