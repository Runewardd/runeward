package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func (s *Server) handleConversationPublish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		RunID   string `json:"run_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	msg, err := s.mgr.PublishConversation(r.Context(), r.PathValue("id"), req.Role, req.Content, req.RunID)
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

func (s *Server) handleConversationHistory(w http.ResponseWriter, r *http.Request) {
	afterID, err := parseUintQuery(r, "after_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := 500
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
	}
	messages, err := s.mgr.ConversationHistory(r.PathValue("id"), afterID, limit)
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func parseUintQuery(r *http.Request, name string) (uint64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, &queryError{name: name}
	}
	return v, nil
}

type queryError struct{ name string }

func (e *queryError) Error() string { return e.name + " must be an unsigned integer" }

// handleConversationStream provides a read-only WebSocket. It subscribes
// before reading history so a message published during connection setup is not
// lost; message IDs de-duplicate the overlap.
func (s *Server) handleConversationStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	live, cancel, err := s.mgr.SubscribeConversation(id)
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}
	defer cancel()

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("conversation stream upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	history, err := s.mgr.ConversationHistory(id, 0, 500)
	if err != nil {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "history unavailable"))
		return
	}
	var lastID uint64
	for _, msg := range history {
		if err := conn.WriteJSON(msg); err != nil {
			return
		}
		lastID = msg.ID
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			// This endpoint is deliberately observer-only. Any application data
			// from the client terminates the stream rather than reaching an agent.
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "read-only stream"), time.Now().Add(time.Second))
			return
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-readDone:
			return
		case msg, ok := <-live:
			if !ok {
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "citadel ended"))
				return
			}
			if msg.ID <= lastID {
				continue
			}
			payload, err := json.Marshal(msg)
			if err != nil || conn.WriteMessage(websocket.TextMessage, payload) != nil {
				return
			}
			lastID = msg.ID
		}
	}
}
