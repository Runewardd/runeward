package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConversationPublishesRedactedDisplaySafeTurns(t *testing.T) {
	m, _ := newTestManager(t, nil, time.Second)
	m.sessions["fake-1"].Actor = "agent-a"
	m.sessions["fake-1"].RunID = "run-1"
	m.sessions["fake-1"].secrets = []string{"declared-secret"}

	stream, cancel, err := m.SubscribeConversation("fake-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	ctx := WithActor(context.Background(), "codex")
	msg, err := m.PublishConversation(ctx, "fake-1", "assistant", "done declared-secret\x1b]52;c;bad\a", "")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Author != "codex" || msg.RunID != "run-1" || !msg.Redacted {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if strings.Contains(msg.Content, "declared-secret") || strings.ContainsRune(msg.Content, '\x1b') || strings.ContainsRune(msg.Content, '\a') {
		t.Fatalf("unsafe or unredacted content: %q", msg.Content)
	}
	select {
	case got := <-stream:
		if got.ID != msg.ID {
			t.Fatalf("stream id=%d want=%d", got.ID, msg.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for conversation turn")
	}
	history, err := m.ConversationHistory("fake-1", 0, 10)
	if err != nil || len(history) != 1 || history[0].ID != msg.ID {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestConversationValidation(t *testing.T) {
	m, _ := newTestManager(t, nil, time.Second)
	if _, err := m.PublishConversation(context.Background(), "fake-1", "owner", "hello", ""); err == nil {
		t.Fatal("expected invalid role error")
	}
	if _, err := m.PublishConversation(context.Background(), "fake-1", "user", "", ""); err == nil {
		t.Fatal("expected empty content error")
	}
	if _, err := m.PublishConversation(context.Background(), "missing", "user", "hello", ""); err == nil {
		t.Fatal("expected missing sandbox error")
	}
}
