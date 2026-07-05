// Package backend abstracts the sandbox runtime so callers don't care whether
// a sandbox is a Docker container or a Kubernetes Pod.
package backend

import (
	"context"
	"io"
	"time"

	"github.com/Runewardd/runeward/internal/profile"
)

// Backend provisions and controls sandboxes for a given execution host.
type Backend interface {
	Name() string
	Create(ctx context.Context, spec Spec) (*Sandbox, error)
	Exec(ctx context.Context, id string, req ExecRequest) (*ExecResult, error)
	AttachPTY(ctx context.Context, id string, io PTYStream) error
	CopyFiles(ctx context.Context, id string, files []profile.File) error
	ExportWorkspace(ctx context.Context, id string, w io.Writer) error
	Snapshot(ctx context.Context, id, name string) (*SnapshotRef, error)
	Restore(ctx context.Context, ref SnapshotRef) (*Sandbox, error)
	Kill(ctx context.Context, id string) error
	List(ctx context.Context) ([]Sandbox, error)
}

// Spec is the resolved, backend-agnostic description of a sandbox to create.
type Spec struct {
	Profile      string
	Image        string
	Workdir      string
	User         string
	Env          map[string]string
	Labels       map[string]string
	Files        []profile.File
	SeedDir      string
	Network      profile.Network
	Resources    Resources
	RuntimeClass string
	// ReadOnly mounts the container root filesystem read-only.
	ReadOnly bool
	// Seccomp is a seccomp profile path (Docker: --security-opt seccomp=;
	// k8s: Localhost profile path). Empty means the backend default.
	Seccomp string
	// AppArmor is an AppArmor profile name.
	AppArmor string
}

// Resources are best-effort resource caps applied to the sandbox.
type Resources struct {
	NanoCPUs    int64
	MemoryBytes int64
}

// Sandbox is a handle to a provisioned sandbox.
type Sandbox struct {
	ID        string
	Profile   string
	Backend   string
	Image     string
	Status    string
	CreatedAt time.Time
	Endpoint  string
}

// ExecRequest is a one-shot command execution.
type ExecRequest struct {
	Command []string
	Workdir string
	Env     map[string]string
	Timeout time.Duration
}

// ExecResult captures the outcome of an Exec.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// PTYStream carries the interactive terminal I/O for AttachPTY.
type PTYStream struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	TTY     bool
	Command []string
	Resize  <-chan TermSize
}

// TermSize is a terminal window dimension update.
type TermSize struct {
	Rows uint16
	Cols uint16
}

// SnapshotRef identifies a captured workspace snapshot.
type SnapshotRef struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Profile  string    `json:"profile"`
	Backend  string    `json:"backend"`
	Location string    `json:"location"`
	Sha256   string    `json:"sha256,omitempty"`
	Created  time.Time `json:"created"`
}
