package policy

import (
	"testing"

	"github.com/Runewardd/runeward/internal/profile"
)

func TestMatchArgvCatchesWrappersAndPaths(t *testing.T) {
	rules := []profile.PolicyRule{
		{Tool: "shell", MatchArgv: "rm", Verdict: profile.VerdictDeny, Reason: "no rm"},
	}
	e := New(rules, profile.VerdictAllow)

	deny := []struct {
		name string
		a    Action
	}{
		{"bare rm", Action{Tool: "shell", Arg: "rm -rf /tmp/x", Args: []string{"rm", "-rf", "/tmp/x"}}},
		{"abs path rm", Action{Tool: "shell", Arg: "/bin/rm -rf /tmp/x", Args: []string{"/bin/rm", "-rf", "/tmp/x"}}},
		{"sh -c wrapper", Action{Tool: "shell", Arg: "sh -c 'rm -rf /'", Args: []string{"sh", "-c", "rm -rf /"}}},
		{"bash -c wrapper", Action{Tool: "shell", Args: []string{"/bin/bash", "-c", "rm file"}}},
	}
	for _, tc := range deny {
		if got := e.Evaluate(tc.a); got.Verdict != profile.VerdictDeny {
			t.Errorf("%s: verdict = %s, want deny", tc.name, got.Verdict)
		}
	}

	allow := []struct {
		name string
		a    Action
	}{
		{"different tool exe", Action{Tool: "shell", Args: []string{"remove", "x"}}},
		{"ls", Action{Tool: "shell", Args: []string{"ls", "-la"}}},
		{"echo mentions rm", Action{Tool: "shell", Args: []string{"echo", "rm"}}},
	}
	for _, tc := range allow {
		if got := e.Evaluate(tc.a); got.Verdict != profile.VerdictAllow {
			t.Errorf("%s: verdict = %s, want allow", tc.name, got.Verdict)
		}
	}
}

func TestMatchArgvFallsBackToArgWhenNoArgv(t *testing.T) {
	e := New([]profile.PolicyRule{
		{Tool: "shell", MatchArgv: "curl", Verdict: profile.VerdictDeny},
	}, profile.VerdictAllow)
	// No Args set; the engine should split Arg into tokens.
	if got := e.Evaluate(Action{Tool: "shell", Arg: "curl http://x"}); got.Verdict != profile.VerdictDeny {
		t.Fatalf("verdict = %s, want deny", got.Verdict)
	}
}
