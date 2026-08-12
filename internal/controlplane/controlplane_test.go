package controlplane

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/ledger"
	"github.com/Runewardd/runeward/internal/policy"
	"github.com/Runewardd/runeward/internal/profile"
)

// fakeBackend echoes commands so tests can run without a container runtime.
type fakeBackend struct {
	execs  int
	result *backend.ExecResult
}

func (f *fakeBackend) Name() string { return "fake" }

func (f *fakeBackend) Create(ctx context.Context, spec backend.Spec) (*backend.Sandbox, error) {
	return &backend.Sandbox{ID: "fake-1", Profile: spec.Profile, Backend: "fake", Status: "running"}, nil
}

func (f *fakeBackend) Exec(ctx context.Context, id string, req backend.ExecRequest) (*backend.ExecResult, error) {
	f.execs++
	if f.result != nil {
		return f.result, nil
	}
	if len(req.Command) > 0 && req.Command[0] == "false" {
		return &backend.ExecResult{ExitCode: 1, Stderr: "failed", Duration: time.Millisecond}, nil
	}
	return &backend.ExecResult{ExitCode: 0, Stdout: strings.Join(req.Command, " "), Duration: time.Millisecond}, nil
}

func (f *fakeBackend) AttachPTY(ctx context.Context, id string, io backend.PTYStream) error {
	return nil
}
func (f *fakeBackend) CopyFiles(ctx context.Context, id string, files []profile.File) error {
	return nil
}
func (f *fakeBackend) ExportWorkspace(ctx context.Context, id string, w io.Writer) error {
	return nil
}
func (f *fakeBackend) Snapshot(ctx context.Context, id, name string) (*backend.SnapshotRef, error) {
	return &backend.SnapshotRef{}, nil
}
func (f *fakeBackend) Restore(ctx context.Context, ref backend.SnapshotRef) (*backend.Sandbox, error) {
	return &backend.Sandbox{}, nil
}
func (f *fakeBackend) Kill(ctx context.Context, id string) error           { return nil }
func (f *fakeBackend) List(ctx context.Context) ([]backend.Sandbox, error) { return nil, nil }

func newTestManager(t *testing.T, rules []profile.PolicyRule, wait time.Duration) (*Manager, *fakeBackend) {
	t.Helper()
	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	fb := &fakeBackend{}
	p := &profile.Profile{Name: "test"}
	guard, _ := policy.NewGuard(p.Limits)
	guard.Start()

	m := &Manager{
		ledger:       l,
		approvals:    NewApprovalStore(),
		approvalWait: wait,
		sessions: map[string]*Session{
			"fake-1": {
				Sandbox: &backend.Sandbox{ID: "fake-1", Profile: "test", Backend: "fake", Status: "running"},
				Backend: fb,
				Profile: p,
				Engine:  policy.New(rules, profile.VerdictAllow),
				Guard:   guard,
			},
		},
	}
	return m, fb
}

func TestGovernAllow(t *testing.T) {
	m, _ := newTestManager(t, nil, time.Second)
	res, err := m.Shell(context.Background(), "fake-1", []string{"echo", "hi"}, "")
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	if res.Verdict != profile.VerdictAllow {
		t.Fatalf("verdict = %q, want allow", res.Verdict)
	}
	if res.Stdout != "echo hi" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
	if err := m.Ledger().Verify(); err != nil {
		t.Fatalf("ledger verify: %v", err)
	}
}

func TestCodeCapabilityIsEnforcedBeforeExecution(t *testing.T) {
	m, fb := newTestManager(t, nil, time.Second)
	if _, err := m.Python(context.Background(), "fake-1", "print(1)"); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("Python without capability error = %v", err)
	}
	if fb.execs != 0 {
		t.Fatalf("unsupported runtime executed %d times", fb.execs)
	}
	m.sessions["fake-1"].Profile.Capabilities = []string{"python"}
	res, err := m.Python(context.Background(), "fake-1", "print(1)")
	if err != nil || res.ExitCode != 0 || fb.execs != 1 {
		t.Fatalf("Python with capability: result=%+v execs=%d err=%v", res, fb.execs, err)
	}
}

func TestProfileCapabilitiesInferOfficialImages(t *testing.T) {
	p := &profile.Profile{Host: profile.Host{Image: "ghcr.io/runewardd/runeward-sandbox:latest"}}
	got := profileCapabilities(p)
	for _, want := range []string{"python", "node", "browser"} {
		if !hasCapability(p, want) {
			t.Fatalf("capabilities %v missing %q", got, want)
		}
	}
	if hasCapability(&profile.Profile{Host: profile.Host{Image: "debian:stable-slim"}}, "python") {
		t.Fatal("plain Debian must not claim Python")
	}
	if hasCapability(&profile.Profile{Host: profile.Host{Image: "runeward-ide:latest"}}, "python") {
		t.Fatal("lean IDE image must not claim optional language runtimes")
	}
}

func TestRuntimeCapabilityVerificationAndDiscovery(t *testing.T) {
	missing := &fakeBackend{result: &backend.ExecResult{ExitCode: 127}}
	p := &profile.Profile{Host: profile.Host{Image: "custom:1"}, Capabilities: []string{"python"}}
	if err := verifyRuntimeCapabilities(context.Background(), missing, "fake-1", p); err == nil || !strings.Contains(err.Error(), "does not provide") {
		t.Fatalf("missing declared runtime error = %v", err)
	}

	discovered := &fakeBackend{result: &backend.ExecResult{ExitCode: 0, Stdout: "python\nnode\n"}}
	p = &profile.Profile{Host: profile.Host{Image: "custom:1"}}
	if err := verifyRuntimeCapabilities(context.Background(), discovered, "fake-1", p); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Capabilities, ",") != "python,node" {
		t.Fatalf("discovered capabilities = %v", p.Capabilities)
	}
}

func TestCheckProfilePrerequisitesRejectsMissingEnv(t *testing.T) {
	name := "RUNEWARD_TEST_MISSING_PREREQUISITE"
	t.Setenv(name, "")
	p := &profile.Profile{Env: []profile.EnvVar{{Name: "TOKEN", Op: "env://" + name}}}
	if err := CheckProfilePrerequisites(p); err == nil || !strings.Contains(err.Error(), name) {
		t.Fatalf("missing prerequisite error = %v", err)
	}
}

func TestGovernDeny(t *testing.T) {
	rules := []profile.PolicyRule{{Tool: "shell", Match: "rm *", Verdict: profile.VerdictDeny, Reason: "no deletes"}}
	m, fb := newTestManager(t, rules, time.Second)
	res, err := m.Shell(context.Background(), "fake-1", []string{"rm", "-rf", "/"}, "")
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	if res.Verdict != profile.VerdictDeny {
		t.Fatalf("verdict = %q, want deny", res.Verdict)
	}
	if fb.execs != 0 {
		t.Fatalf("denied action should not have executed, execs=%d", fb.execs)
	}
	if res.Reason != "no deletes" {
		t.Fatalf("reason = %q", res.Reason)
	}
}

func TestGovernApprovalTimeoutThenPending(t *testing.T) {
	rules := []profile.PolicyRule{{Tool: "file.write", Match: "/etc/*", Verdict: profile.VerdictRequireApprove, Reason: "sensitive path"}}
	m, _ := newTestManager(t, rules, 20*time.Millisecond)
	res, err := m.FileWrite(context.Background(), "fake-1", "/etc/passwd", "x")
	if err != nil {
		t.Fatalf("filewrite: %v", err)
	}
	if res.Verdict != profile.VerdictRequireApprove || !res.Pending {
		t.Fatalf("expected pending require-approval, got %+v", res)
	}
	if res.ApprovalID == "" {
		t.Fatalf("expected approval id")
	}
}

func TestGovernApprovalApproved(t *testing.T) {
	rules := []profile.PolicyRule{{Tool: "file.write", Match: "/etc/*", Verdict: profile.VerdictRequireApprove}}
	m, fb := newTestManager(t, rules, 5*time.Second)

	type outcome struct {
		res *ToolResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := m.FileWrite(context.Background(), "fake-1", "/etc/hosts", "127.0.0.1 x")
		done <- outcome{res, err}
	}()

	// Poll for the approval to appear, then approve it.
	var id string
	for i := 0; i < 100; i++ {
		list := m.Approvals().List()
		if len(list) == 1 {
			id = list[0].ID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("approval never appeared")
	}
	if !m.Approvals().Resolve(id, true) {
		t.Fatal("resolve failed")
	}

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("filewrite: %v", o.err)
		}
		if o.res.Verdict != profile.VerdictAllow {
			t.Fatalf("verdict = %q, want allow", o.res.Verdict)
		}
		if fb.execs != 1 {
			t.Fatalf("approved action should have executed once, execs=%d", fb.execs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("filewrite did not complete after approval")
	}
}

func TestGuardMaxExecs(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	p := &profile.Profile{Name: "capped", Limits: profile.Limits{MaxExecs: 1}}
	guard, _ := policy.NewGuard(p.Limits)
	guard.Start()
	m := &Manager{
		ledger:    l,
		approvals: NewApprovalStore(),
		sessions: map[string]*Session{
			"fake-1": {
				Sandbox: &backend.Sandbox{ID: "fake-1", Profile: "capped", Status: "running"},
				Backend: &fakeBackend{},
				Profile: p,
				Engine:  policy.New(nil, profile.VerdictAllow),
				Guard:   guard,
			},
		},
	}

	if res, _ := m.Shell(context.Background(), "fake-1", []string{"echo", "1"}, ""); res.Verdict != profile.VerdictAllow {
		t.Fatalf("first exec verdict = %q", res.Verdict)
	}
	res, _ := m.Shell(context.Background(), "fake-1", []string{"echo", "2"}, "")
	if res.Verdict != profile.VerdictDeny {
		t.Fatalf("second exec should be blocked by max_execs, got %q", res.Verdict)
	}
}
