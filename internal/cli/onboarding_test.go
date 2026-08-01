package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runewardd/runeward/internal/profile"
)

func TestWriteStarterCharter(t *testing.T) {
	dir := t.TempDir()
	path, created, err := writeStarterCharter(dir, "quickstart", false)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("starter should be created")
	}
	if path != filepath.Join(dir, ".runeward", "quickstart.toml") {
		t.Fatalf("path = %q", path)
	}
	p, err := profile.Load("quickstart", profile.Options{WorkingDir: dir})
	if err != nil {
		t.Fatalf("load starter: %v", err)
	}
	if p.Host.Image != "ghcr.io/runewardd/runeward-sandbox:latest" {
		t.Fatalf("image = %q", p.Host.Image)
	}
	if p.Network.Default != "deny" {
		t.Fatalf("network default = %q", p.Network.Default)
	}

	if err := os.WriteFile(path, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, created, err = writeStarterCharter(dir, "quickstart", false)
	if err != nil || created {
		t.Fatalf("existing file: created=%t err=%v", created, err)
	}
	b, _ := os.ReadFile(path)
	if strings.TrimSpace(string(b)) != "sentinel" {
		t.Fatal("existing starter was overwritten without --force")
	}
}

func TestFriendlyRuntimeErrorRedactsSocketDetail(t *testing.T) {
	got := friendlyRuntimeError(assertionError("docker engine not reachable; is docker running? (dial unix /private/path.sock)"))
	if strings.Contains(got, "/private/path.sock") {
		t.Fatalf("raw socket path leaked: %s", got)
	}
	if !strings.Contains(got, "runeward doctor") {
		t.Fatalf("missing recovery instruction: %s", got)
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }
