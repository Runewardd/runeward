package cli

import (
	"testing"

	"github.com/Runewardd/runeward/internal/profile"
)

func TestParseInlineCase(t *testing.T) {
	c, err := parseInlineCase("tool=shell,action=rm -rf /,expect=deny")
	if err != nil {
		t.Fatalf("parseInlineCase: %v", err)
	}
	if c.Tool != "shell" {
		t.Errorf("tool = %q, want shell", c.Tool)
	}
	if c.Action != "rm -rf /" {
		t.Errorf("action = %q, want %q", c.Action, "rm -rf /")
	}
	if c.Expect != "deny" {
		t.Errorf("expect = %q, want deny", c.Expect)
	}

	if _, err := parseInlineCase("tool=shell,bogus=1"); err == nil {
		t.Error("expected error for unknown key")
	}
	if _, err := parseInlineCase("noequals"); err == nil {
		t.Error("expected error for missing '='")
	}
}

func TestNormalizeVerdict(t *testing.T) {
	cases := map[string]profile.Verdict{
		"allow":            profile.VerdictAllow,
		"DENY":             profile.VerdictDeny,
		"require-approval": profile.VerdictRequireApprove,
		"approval":         profile.VerdictRequireApprove,
	}
	for in, want := range cases {
		got, err := normalizeVerdict(in)
		if err != nil {
			t.Errorf("normalizeVerdict(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeVerdict(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := normalizeVerdict("maybe"); err == nil {
		t.Error("expected error for unknown verdict")
	}
	if _, err := normalizeVerdict(""); err == nil {
		t.Error("expected error for empty verdict")
	}
}
