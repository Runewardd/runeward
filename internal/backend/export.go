package backend

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Detect returns the backend hosting the given sandbox id, probing Docker first
// and then Kubernetes. It lets id-only commands (like `runeward export`) work
// without knowing the originating profile. A backend that cannot be constructed
// (e.g. no kubeconfig) is skipped rather than treated as an error.
func Detect(ctx context.Context, id string) (Backend, error) {
	if d, err := NewDocker(); err == nil && d.has(ctx, id) {
		return d, nil
	}
	if k, err := NewK8s(); err == nil && k.has(ctx, id) {
		return k, nil
	}
	return nil, fmt.Errorf("sandbox %q not found (looked in docker and kubernetes)", id)
}

// Export copies the workspace of sandbox id out to destDir on the host,
// auto-detecting the backend. The sandbox is only read; destDir is created if
// needed and populated with a point-in-time copy of the workspace, so later
// host edits never flow back into the sandbox.
func Export(ctx context.Context, id, destDir string) error {
	be, err := Detect(ctx, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("export: create destination %q: %w", destDir, err)
	}

	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(be.ExportWorkspace(ctx, id, pw))
	}()
	if err := extractTar(pr, destDir); err != nil {
		_ = pr.CloseWithError(err)
		return err
	}
	return nil
}
