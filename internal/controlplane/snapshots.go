package controlplane

import (
	"context"
	"fmt"
	"io"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/profile"
)

// Snapshot captures a sandbox's workspace and registers the reference.
func (m *Manager) Snapshot(ctx context.Context, id, name string) (*backend.SnapshotRef, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	ref, err := sess.Backend.Snapshot(ctx, id, name)
	if err != nil {
		return nil, err
	}
	// Carry the originating profile so a restore can re-derive governance.
	ref.Profile = sess.Profile.Name

	m.snapOpMu.Lock()
	defer m.snapOpMu.Unlock()
	m.snapMu.Lock()
	m.snapshots[ref.ID] = *ref
	m.snapshotOwners[ref.ID] = sess.Owner
	m.snapMu.Unlock()
	if err := m.saveSnapshots(); err != nil {
		m.snapMu.Lock()
		delete(m.snapshots, ref.ID)
		delete(m.snapshotOwners, ref.ID)
		m.snapMu.Unlock()
		return nil, fmt.Errorf("persist snapshot reference: %w", err)
	}

	m.record(ctx, sess, "snapshot", name, nil, string(profile.VerdictAllow), 0, 0, "snapshot "+ref.ID)
	return ref, nil
}

// SnapshotOwner returns the principal that created a recovery snapshot.
func (m *Manager) SnapshotOwner(id string) (string, bool) {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	owner, ok := m.snapshotOwners[id]
	return owner, ok
}

// SnapshotRef returns one registered recovery artifact.
func (m *Manager) SnapshotRef(id string) (backend.SnapshotRef, bool) {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	ref, ok := m.snapshots[id]
	return ref, ok
}

// ExportWorkspace streams a point-in-time tar archive from a governed sandbox.
func (m *Manager) ExportWorkspace(ctx context.Context, id string, w io.Writer) error {
	sess, err := m.session(id)
	if err != nil {
		return err
	}
	return sess.Backend.ExportWorkspace(ctx, id, w)
}

// ListSnapshots returns all captured snapshot references.
func (m *Manager) ListSnapshots() []backend.SnapshotRef {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	out := make([]backend.SnapshotRef, 0, len(m.snapshots))
	for _, r := range m.snapshots {
		out = append(out, r)
	}
	return out
}

// RestoreSnapshot recreates a governed sandbox from a snapshot, re-deriving
// policy and guardrails from the snapshot's profile.
func (m *Manager) RestoreSnapshot(ctx context.Context, snapshotID, owner string) (*backend.Sandbox, error) {
	return m.RestoreSnapshotForIdentity(ctx, snapshotID, owner, owner)
}

// RestoreSnapshotForIdentity restores a tenant-owned snapshot while retaining
// the individual human or agent actor in run lineage and Chronicle metadata.
func (m *Manager) RestoreSnapshotForIdentity(ctx context.Context, snapshotID, owner, actor string) (*backend.Sandbox, error) {
	m.snapMu.Lock()
	ref, ok := m.snapshots[snapshotID]
	m.snapMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("snapshot %q not found", snapshotID)
	}

	p, err := profile.Load(ref.Profile, profile.Options{ConfigDir: m.configDir})
	if err != nil {
		return nil, fmt.Errorf("load snapshot profile %q: %w", ref.Profile, err)
	}
	env, secrets, err := resolveEnv(p)
	if err != nil {
		return nil, err
	}
	spec := backend.SpecFromProfile(p, env)

	be, err := backend.For(p)
	if err != nil {
		return nil, err
	}
	sb, err := backend.RestoreSnapshot(ctx, be, ref, spec)
	if err != nil {
		return nil, err
	}

	guard, err := policyGuard(p)
	if err != nil {
		_ = be.Kill(context.Background(), sb.ID)
		return nil, err
	}

	engine, err := newEngine(p)
	if err != nil {
		_ = be.Kill(context.Background(), sb.ID)
		return nil, err
	}
	sess := &Session{
		Sandbox: sb,
		Backend: be,
		Profile: p,
		Engine:  engine,
		Guard:   guard,
		Env:     env,
		Workdir: p.Host.Workdir,
		Owner:   owner,
		Actor:   firstNonEmpty(actor, owner),
		RunID:   newID(),
		secrets: secrets,
	}
	m.mu.Lock()
	m.sessions[sb.ID] = sess
	m.mu.Unlock()
	if err := m.registerRun(sess); err != nil {
		_ = be.Kill(context.Background(), sb.ID)
		m.mu.Lock()
		delete(m.sessions, sb.ID)
		m.mu.Unlock()
		return nil, fmt.Errorf("persist restored run: %w", err)
	}
	m.record(ctx, sess, "snapshot", "restore", nil, string(profile.VerdictAllow), 0, 0, "from "+snapshotID)
	return sb, nil
}
