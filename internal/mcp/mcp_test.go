package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/controlplane"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T) *sdk.ClientSession {
	t.Helper()
	t.Setenv("RUNEWARD_STATE_DIR", t.TempDir())
	mgr, err := controlplane.New(t.TempDir())
	if err != nil {
		t.Fatalf("controlplane.New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	srv := NewServer(mgr)
	serverT, clientT := sdk.NewInMemoryTransports()

	ctx := context.Background()
	go func() { _ = srv.Run(ctx, serverT) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0.0.0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestListTools(t *testing.T) {
	cs := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
	}
	for _, want := range []string{
		"runeward_create_sandbox", "runeward_shell", "runeward_browser",
		"runeward_browser_open", "runeward_browser_act", "runeward_browser_close",
		"runeward_python", "runeward_node",
		"runeward_read_file", "runeward_write_file", "runeward_list_files",
		"runeward_search_files", "runeward_list_approvals", "runeward_kill_sandbox",
		"runeward_create_fleet", "runeward_list_fleets", "runeward_list_tasks",
		"runeward_add_task", "runeward_claim_task", "runeward_complete_task",
		"runeward_fail_task", "runeward_kill_fleet", "runeward_heartbeat_task",
	} {
		if !got[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestCallListApprovals(t *testing.T) {
	cs := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "runeward_list_approvals"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "no pending approvals") {
		t.Fatalf("unexpected content: %q", text)
	}
}
