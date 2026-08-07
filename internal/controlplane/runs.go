package controlplane

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const runsFileName = "runs.json"

// Run is the durable, provider-neutral record for one agent execution lineage
// node. Citadels are runtime resources; Runs remain queryable after teardown.
type Run struct {
	ID          string     `json:"id"`
	ParentRunID string     `json:"parent_run_id,omitempty"`
	CitadelID   string     `json:"citadel_id"`
	Tenant      string     `json:"tenant,omitempty"`
	Actor       string     `json:"actor,omitempty"`
	Charter     string     `json:"charter"`
	Agent       string     `json:"agent,omitempty"`
	Provider    string     `json:"provider,omitempty"`
	Model       string     `json:"model,omitempty"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (m *Manager) runsPath() string {
	if m.stateDir == "" {
		return ""
	}
	return filepath.Join(m.stateDir, runsFileName)
}

func (m *Manager) loadRuns() error {
	path := m.runsPath()
	if path == "" {
		return nil
	}
	// #nosec G304 -- path is the fixed runsFileName beneath Manager.stateDir.
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var runs []Run
	if err := json.Unmarshal(data, &runs); err != nil {
		return err
	}
	m.runMu.Lock()
	interrupted := false
	now := time.Now().UTC()
	for _, run := range runs {
		// Runtime sessions cannot survive a control-plane restart. Preserve the
		// lineage, but make the terminal state truthful instead of leaving a
		// permanently "running" record behind.
		if run.Status == "running" {
			run.Status = "interrupted"
			run.Error = "control plane restarted before the run completed"
			run.FinishedAt = &now
			interrupted = true
		}
		m.runs[run.ID] = run
	}
	m.runMu.Unlock()
	if interrupted {
		return m.saveRuns()
	}
	return nil
}

func (m *Manager) saveRuns() error {
	m.runMu.Lock()
	runs := make([]Run, 0, len(m.runs))
	for _, run := range m.runs {
		runs = append(runs, run)
	}
	m.runMu.Unlock()
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	return m.writeStateFile(runsFileName, data)
}

func (m *Manager) registerRun(sess *Session) error {
	m.runOpMu.Lock()
	defer m.runOpMu.Unlock()
	run := Run{
		ID: sess.RunID, ParentRunID: sess.ParentRunID, CitadelID: sess.Sandbox.ID,
		Tenant: sess.Owner, Actor: sess.Actor, Charter: sess.Profile.Name,
		Agent: sess.Agent, Provider: sess.Provider, Model: sess.Model,
		Status: "running", CreatedAt: time.Now().UTC(),
	}
	m.runMu.Lock()
	if existing, exists := m.runs[run.ID]; exists && existing.CitadelID != run.CitadelID {
		m.runMu.Unlock()
		return &runConflictError{id: run.ID}
	}
	m.runs[run.ID] = run
	m.runMu.Unlock()
	if err := m.saveRuns(); err != nil {
		m.runMu.Lock()
		delete(m.runs, run.ID)
		m.runMu.Unlock()
		return err
	}
	return nil
}

type runConflictError struct{ id string }

func (e *runConflictError) Error() string { return "run " + e.id + " already exists" }

func (m *Manager) finishRun(id string, runErr error) error {
	if id == "" {
		return nil
	}
	m.runOpMu.Lock()
	defer m.runOpMu.Unlock()
	m.runMu.Lock()
	run, ok := m.runs[id]
	if !ok {
		m.runMu.Unlock()
		return nil
	}
	now := time.Now().UTC()
	run.FinishedAt = &now
	run.Status = "completed"
	if runErr != nil {
		run.Status = "failed"
		run.Error = runErr.Error()
	}
	m.runs[id] = run
	m.runMu.Unlock()
	return m.saveRuns()
}

// ListRuns returns durable run records in creation order.
func (m *Manager) ListRuns() []Run {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	out := make([]Run, 0, len(m.runs))
	for _, run := range m.runs {
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Run returns one durable run record.
func (m *Manager) Run(id string) (Run, bool) {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	run, ok := m.runs[id]
	return run, ok
}
