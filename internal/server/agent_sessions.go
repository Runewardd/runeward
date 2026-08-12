package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Runewardd/runeward/internal/controlplane"
)

func (s *Server) handleStartAgentSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command  []string `json:"command"`
		Agent    string   `json:"agent"`
		Model    string   `json:"model"`
		CohortID string   `json:"cohort_id"`
		TaskID   string   `json:"task_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	as, err := s.mgr.StartAgentSession(r.Context(), r.PathValue("id"), controlplane.StartAgentSessionOptions{
		Command: req.Command, Agent: req.Agent, Model: req.Model,
		CohortID: req.CohortID, TaskID: req.TaskID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, as)
}

func (s *Server) handleListAgentSessions(w http.ResponseWriter, r *http.Request) {
	sessions := s.mgr.AgentSessions(strings.TrimSpace(r.URL.Query().Get("cohort_id")))
	if p := principalFrom(r.Context()); p != nil && !p.Admin {
		filtered := make([]controlplane.AgentSession, 0, len(sessions))
		for _, as := range sessions {
			if as.Tenant == p.TenantID() {
				filtered = append(filtered, as)
			}
		}
		sessions = filtered
	}
	if sessions == nil {
		sessions = []controlplane.AgentSession{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) handleGetAgentSession(w http.ResponseWriter, r *http.Request) {
	as, ok := s.visibleAgentSession(r, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "agent session not found")
		return
	}
	writeJSON(w, http.StatusOK, as)
}

func (s *Server) handleAgentSessionEvents(w http.ResponseWriter, r *http.Request) {
	after, err := parseAgentEventCursor(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	as, events, ok := s.mgr.AgentSessionEvents(r.PathValue("id"), after)
	if !ok || !s.agentSessionVisible(r, as) {
		writeError(w, http.StatusNotFound, "agent session not found")
		return
	}
	if events == nil {
		events = []controlplane.AgentEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": as, "events": events})
}

func (s *Server) handleAgentSessionStream(w http.ResponseWriter, r *http.Request) {
	after, err := parseAgentEventCursor(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if last := strings.TrimSpace(r.Header.Get("Last-Event-ID")); last != "" {
		if n, parseErr := strconv.ParseInt(last, 10, 64); parseErr == nil && n > after {
			after = n
		}
	}
	as, backlog, live, cancel, ok := s.mgr.SubscribeAgentSession(r.PathValue("id"), after)
	if !ok || !s.agentSessionVisible(r, as) {
		if ok && cancel != nil {
			cancel()
		}
		writeError(w, http.StatusNotFound, "agent session not found")
		return
	}
	defer cancel()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	for _, ev := range backlog {
		if err := writeAgentSSE(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-live:
			if !open {
				return
			}
			if err := writeAgentSSE(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) visibleAgentSession(r *http.Request, id string) (controlplane.AgentSession, bool) {
	as, ok := s.mgr.AgentSession(id)
	return as, ok && s.agentSessionVisible(r, as)
}

func (s *Server) agentSessionVisible(r *http.Request, as controlplane.AgentSession) bool {
	p := principalFrom(r.Context())
	return p == nil || p.Admin || as.Tenant == p.TenantID()
}

func parseAgentEventCursor(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("after"))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("after must be a non-negative event sequence")
	}
	return n, nil
}

func writeAgentSSE(w http.ResponseWriter, ev controlplane.AgentEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: agent-output\ndata: %s\n\n", ev.Seq, data)
	return err
}
