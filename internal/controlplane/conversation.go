package controlplane

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	conversationHistoryLimit  = 500
	conversationContentLimit  = 64 << 10
	conversationSubscriberBuf = 64
)

// ConversationMessage is one redacted, display-safe turn in a Citadel's live
// conversation feed. The feed is operational visibility, not a replacement
// for the signed Chronicle.
type ConversationMessage struct {
	ID        uint64    `json:"id"`
	Sandbox   string    `json:"sandbox"`
	Role      string    `json:"role"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	RunID     string    `json:"run_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Redacted  bool      `json:"redacted,omitempty"`
}

type conversationStream struct {
	nextID  uint64
	history []ConversationMessage
	nextSub uint64
	subs    map[uint64]chan ConversationMessage
}

type conversationHub struct {
	mu      sync.Mutex
	streams map[string]*conversationStream
}

func newConversationHub() *conversationHub {
	return &conversationHub{streams: make(map[string]*conversationStream)}
}

func (m *Manager) conversationHub() *conversationHub {
	if m == nil {
		return nil
	}
	m.conversationMu.Lock()
	defer m.conversationMu.Unlock()
	if m.conversation == nil {
		m.conversation = newConversationHub()
	}
	return m.conversation
}

// PublishConversation adds a turn and broadcasts it to read-only observers.
// The authenticated actor is derived from ctx; callers cannot spoof authors.
// Declared and pattern-detected secrets are scrubbed before storage or fan-out.
func (m *Manager) PublishConversation(ctx context.Context, sandbox, role, content, runID string) (ConversationMessage, error) {
	sess, err := m.session(sandbox)
	if err != nil {
		return ConversationMessage{}, err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "user", "assistant", "tool", "system":
	default:
		return ConversationMessage{}, fmt.Errorf("conversation role must be user, assistant, tool, or system")
	}
	if content == "" {
		return ConversationMessage{}, fmt.Errorf("conversation content must not be empty")
	}
	if !utf8.ValidString(content) {
		return ConversationMessage{}, fmt.Errorf("conversation content must be valid UTF-8")
	}
	if len(content) > conversationContentLimit {
		return ConversationMessage{}, fmt.Errorf("conversation content exceeds %d bytes", conversationContentLimit)
	}
	author := sess.Actor
	if ctx != nil {
		if current, ok := ctx.Value(actorContextKey{}).(string); ok && strings.TrimSpace(current) != "" {
			author = strings.TrimSpace(current)
		}
	}
	if author == "" {
		author = role
	}
	if strings.TrimSpace(runID) == "" {
		runID = sess.RunID
	}
	scrubbed := sess.eventScrubber().ScrubString(content, sess.secrets...)
	msg := ConversationMessage{
		Sandbox:   sandbox,
		Role:      role,
		Author:    safeTTYText(author),
		Content:   safeTTYText(scrubbed),
		RunID:     safeTTYText(strings.TrimSpace(runID)),
		CreatedAt: time.Now().UTC(),
		Redacted:  scrubbed != content,
	}
	return m.conversationHub().publish(msg), nil
}

// ConversationHistory returns at most limit messages newer than afterID.
func (m *Manager) ConversationHistory(sandbox string, afterID uint64, limit int) ([]ConversationMessage, error) {
	if _, err := m.session(sandbox); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > conversationHistoryLimit {
		limit = conversationHistoryLimit
	}
	return m.conversationHub().history(sandbox, afterID, limit), nil
}

// SubscribeConversation streams future messages until cancel is called or the
// Citadel is removed. Slow observers lose old live updates but can recover via
// ConversationHistory without blocking an agent.
func (m *Manager) SubscribeConversation(sandbox string) (<-chan ConversationMessage, func(), error) {
	if _, err := m.session(sandbox); err != nil {
		return nil, nil, err
	}
	ch, cancel := m.conversationHub().subscribe(sandbox)
	return ch, cancel, nil
}

func (h *conversationHub) stream(sandbox string) *conversationStream {
	s := h.streams[sandbox]
	if s == nil {
		s = &conversationStream{subs: make(map[uint64]chan ConversationMessage)}
		h.streams[sandbox] = s
	}
	return s
}

func (h *conversationHub) publish(msg ConversationMessage) ConversationMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stream(msg.Sandbox)
	s.nextID++
	msg.ID = s.nextID
	s.history = append(s.history, msg)
	if len(s.history) > conversationHistoryLimit {
		s.history = append([]ConversationMessage(nil), s.history[len(s.history)-conversationHistoryLimit:]...)
	}
	for _, ch := range s.subs {
		select {
		case ch <- msg:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- msg:
			default:
			}
		}
	}
	return msg
}

func (h *conversationHub) history(sandbox string, afterID uint64, limit int) []ConversationMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[sandbox]
	if s == nil {
		return []ConversationMessage{}
	}
	out := make([]ConversationMessage, 0, len(s.history))
	for _, msg := range s.history {
		if msg.ID > afterID {
			out = append(out, msg)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return append([]ConversationMessage(nil), out...)
}

func (h *conversationHub) subscribe(sandbox string) (<-chan ConversationMessage, func()) {
	h.mu.Lock()
	s := h.stream(sandbox)
	s.nextSub++
	id := s.nextSub
	ch := make(chan ConversationMessage, conversationSubscriberBuf)
	s.subs[id] = ch
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if current := h.streams[sandbox]; current != nil {
				delete(current.subs, id)
			}
			h.mu.Unlock()
		})
	}
}

func (h *conversationHub) forget(sandbox string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[sandbox]
	delete(h.streams, sandbox)
	if s == nil {
		return
	}
	for _, ch := range s.subs {
		close(ch)
	}
}

// safeTTYText removes terminal control sequences while preserving ordinary
// Unicode, tabs, and line breaks. Conversation content is displayed inside
// xterm.js and must never be able to set titles, links, or clipboard contents.
func safeTTYText(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return r
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, value)
}
