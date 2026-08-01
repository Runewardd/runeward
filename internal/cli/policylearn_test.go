package cli

import (
	"strings"
	"testing"

	"github.com/Runewardd/runeward/internal/ledger"
)

func TestRenderLearnedPolicyIsExactAndReviewable(t *testing.T) {
	events := []ledger.Event{
		{Tool: "shell", Action: "terraform apply", Verdict: "deny"},
		{Tool: "net", Action: "api.openai.com", Verdict: "allow"},
		{Tool: "file.write", Action: "/workspace/secret", Verdict: "require-approval", Redacted: true},
	}
	got := renderLearnedPolicy(events, true)
	for _, want := range []string{
		`tool    = "shell"`, `match   = "terraform apply"`, `verdict = "deny"`,
		`hostname = "api.openai.com"`, "Skipped 1 redacted", "POLICY_OWNER",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderLearnedPolicyOmitsAllowedByDefault(t *testing.T) {
	got := renderLearnedPolicy([]ledger.Event{{Tool: "shell", Action: "echo ok", Verdict: "allow"}}, false)
	if strings.Contains(got, "[[policy]]") {
		t.Fatalf("unexpected allow suggestion:\n%s", got)
	}
}
