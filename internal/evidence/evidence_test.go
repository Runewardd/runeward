package evidence

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Runewardd/runeward/internal/ledger"
)

func TestDocumentVerifyDetectsPolicyAndEventTampering(t *testing.T) {
	dir := t.TempDir()
	l, err := ledger.Open(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ledger.LoadOrCreateSigner(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.SetSigner(signer)
	if _, err := l.Append(ledger.Event{SessionID: "s1", Tool: "shell", Action: "echo ok", Verdict: "allow"}); err != nil {
		t.Fatal(err)
	}
	var exported bytes.Buffer
	if err := l.ExportBundle(&exported, "s1", signer.Public()); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	var bundle ledger.Bundle
	if err := json.Unmarshal(exported.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}

	doc := New("test", "quickstart", "test.toml", "image", "resolved policy", nil, bundle)
	if count, err := Verify(doc); err != nil || count != 1 {
		t.Fatalf("verify count=%d err=%v", count, err)
	}
	doc.Policy.Resolved = "tampered"
	if _, err := Verify(doc); err == nil {
		t.Fatal("policy tampering was not detected")
	}
}
