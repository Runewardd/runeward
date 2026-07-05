package ledger

import (
	"strings"
	"testing"
)

func TestScrubString(t *testing.T) {
	// Declared secret is masked, not left in cleartext.
	if got := ScrubString("token=hunter2 ok", "hunter2"); strings.Contains(got, "hunter2") {
		t.Fatalf("declared secret not masked: %q", got)
	}
	// Undeclared credential-shaped strings are masked by pattern.
	ghToken := "ghp_" + strings.Repeat("a", 36)
	if got := ScrubString("using "+ghToken, nil...); strings.Contains(got, ghToken) {
		t.Fatalf("github token not masked: %q", got)
	}
	// Clean text is returned unchanged.
	clean := "just some regular output"
	if got := ScrubString(clean); got != clean {
		t.Fatalf("clean text changed: %q", got)
	}
	// Empty stays empty.
	if got := ScrubString(""); got != "" {
		t.Fatalf("empty = %q, want empty", got)
	}
}

func TestScrubDeclaredSecretHashed(t *testing.T) {
	raw := Event{
		Tool:   "shell",
		Action: "deploy",
		Args:   []string{"secret-token", "keep-me"},
		Meta:   map[string]string{"token": "secret-token", "region": "us"},
	}
	payloadBefore := hashPayload(raw)

	got := Scrub(raw, "secret-token")
	if !got.Redacted {
		t.Fatal("Scrub should set Redacted=true when it changes the payload")
	}
	if got.PayloadHash != payloadBefore {
		t.Fatalf("PayloadHash %q != hash of original %q", got.PayloadHash, payloadBefore)
	}
	if !strings.HasPrefix(got.Args[0], "sha256:") {
		t.Fatalf("declared secret arg not hashed: %q", got.Args[0])
	}
	if got.Args[1] != "keep-me" {
		t.Fatalf("non-secret arg altered: %q", got.Args[1])
	}
	if got.Meta["region"] != "us" {
		t.Fatalf("non-secret meta altered: %q", got.Meta["region"])
	}
	if raw.Args[0] != "secret-token" {
		t.Fatal("Scrub mutated the caller's event")
	}
}

func TestScrubUndeclaredSecretPatterns(t *testing.T) {
	cases := map[string]string{
		"aws":    "export AWS_KEY=AKIAIOSFODNN7EXAMPLE",
		"github": "token is ghp_0123456789abcdefghijABCDEFGHIJ0123",
		"bearer": "curl -H 'Authorization: Bearer abcdef123456.token'",
		"kv":     "run with password=hunter2super here",
	}
	for name, action := range cases {
		t.Run(name, func(t *testing.T) {
			got := Scrub(Event{Tool: "shell", Action: action})
			if !got.Redacted {
				t.Fatalf("expected redaction for %q", action)
			}
			if !strings.Contains(got.Action, redactMask) {
				t.Fatalf("expected mask in scrubbed action, got %q", got.Action)
			}
		})
	}
}

func TestScrubLeavesCleanPayloadUntouched(t *testing.T) {
	raw := Event{Tool: "shell", Action: "ls -la", Args: []string{"ls", "-la"}}
	got := Scrub(raw)
	if got.Redacted {
		t.Fatal("clean payload should not be marked redacted")
	}
	if got.Action != "ls -la" || got.PayloadHash != "" {
		t.Fatalf("clean payload altered: %+v", got)
	}
}

func TestScrubKeepsKeyDropsValue(t *testing.T) {
	got := Scrub(Event{Tool: "shell", Action: "DB_PASSWORD=supersecret123"})
	if strings.Contains(got.Action, "supersecret123") {
		t.Fatalf("secret value leaked: %q", got.Action)
	}
	if !strings.Contains(got.Action, "DB_PASSWORD=") {
		t.Fatalf("key should be preserved: %q", got.Action)
	}
}
