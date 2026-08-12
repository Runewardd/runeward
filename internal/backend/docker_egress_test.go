package backend

import (
	"os"
	"strings"
	"testing"

	"github.com/Runewardd/runeward/internal/profile"
)

func TestDockerStrictEgressStartsSidecarAsRoot(t *testing.T) {
	t.Setenv("RUNEWARD_EGRESS_IMAGE", "example.invalid/runeward-egress:test")

	dockerBin := writeFakeDocker(t)
	d := &Docker{
		bin:       dockerBin,
		proxies:   make(map[string]*hostProxy),
		egressCtr: make(map[string]string),
	}

	if _, err := d.startEgressContainer(t.Context(), "test-id", profile.Network{}); err != nil {
		t.Fatalf("startEgressContainer: %v", err)
	}

	args, err := os.ReadFile(dockerBin + ".args")
	if err != nil {
		t.Fatalf("read captured docker arguments: %v", err)
	}
	if got := string(args); !containsArgPair(got, "--user", "0:0") {
		t.Fatalf("strict-egress sidecar must start as root before dropping privileges; args: %s", got)
	}
}

func writeFakeDocker(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/docker"
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$0.args"
case "$1" in
  run) printf 'fake-container-id\n' ;;
  logs) printf 'runeward-egress transparent proxy listening on :15001\n' >&2 ;;
  inspect) printf 'true\n' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	return bin
}

func containsArgPair(args, key, value string) bool {
	return strings.Contains(args, key+"\n"+value+"\n")
}
