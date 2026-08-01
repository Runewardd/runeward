package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/controlplane"
	"github.com/Runewardd/runeward/internal/profile"
	"github.com/spf13/cobra"
)

const starterCharter = `# Runeward starter policy (a "Charter").
# Common terms come first: this file defines one governed AI-agent sandbox.
policy_default = "allow"

[host]
type    = "container"
image   = "ghcr.io/runewardd/runeward-sandbox:latest"
workdir = "/workspace"

# Start with no outbound network access. Add only the hosts the task needs.
[network]
default = "deny"

# Demonstrates deterministic pre-execution blocking.
[[policy]]
tool    = "shell"
match   = "rm -rf *"
verdict = "deny"
reason  = "recursive force delete blocked by the starter policy"

# Keep ordinary local work usable while the starter policy stays understandable.
[[policy]]
tool    = "shell"
match   = "*"
verdict = "allow"

[[policy]]
tool    = "file.write"
match   = "/workspace/**"
verdict = "allow"

[[policy]]
tool    = "file.write"
match   = "*"
verdict = "require-approval"
reason  = "writes outside /workspace require human approval"

[rationing]
wall_clock = "30m"
max_execs  = 200

[chronicle]
redact = true
`

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func newInitCmd() *cobra.Command {
	var force bool
	var name string
	cmd := &cobra.Command{
		Use:   "init [project-dir]",
		Short: "Create a runnable starter policy in .runeward/",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			path, created, err := writeStarterCharter(dir, name, force)
			if err != nil {
				return err
			}
			verb := "already exists"
			if created {
				verb = "created"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", verb, path)
			fmt.Fprintf(cmd.OutOrStdout(), "next: runeward --config-dir %q doctor %s && runeward --config-dir %q enter %s -- echo hello\n", filepath.Dir(path), name, filepath.Dir(path), name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing starter policy")
	cmd.Flags().StringVar(&name, "name", "quickstart", "starter policy name")
	return cmd
}

func writeStarterCharter(projectDir, name string, force bool) (string, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\\`) || name == "." || name == ".." {
		return "", false, fmt.Errorf("invalid starter policy name %q", name)
	}
	configDir := filepath.Join(projectDir, ".runeward")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", false, fmt.Errorf("create %s: %w", configDir, err)
	}
	path := filepath.Join(configDir, name+".toml")
	if _, err := os.Stat(path); err == nil && !force {
		return path, false, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if err := os.WriteFile(path, []byte(starterCharter), 0o644); err != nil {
		return "", false, fmt.Errorf("write %s: %w", path, err)
	}
	return path, true, nil
}

func newDoctorCmd(configDir *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor [charter]",
		Short: "Check policy, runtime, image, and state readiness",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			checks, ready := runDoctor(resolveConfigDir(*configDir), name)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(map[string]any{"ready": ready, "checks": checks}); err != nil {
					return err
				}
			} else {
				for _, check := range checks {
					fmt.Fprintf(cmd.OutOrStdout(), "%-5s %-12s %s\n", doctorMarker(check.Status), check.Name, check.Message)
				}
			}
			if !ready {
				return fmt.Errorf("setup is not ready; fix the failed checks above")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print a machine-readable readiness report")
	return cmd
}

func doctorMarker(status string) string {
	switch status {
	case "ok":
		return "[ok]"
	case "warn":
		return "[warn]"
	default:
		return "[fail]"
	}
}

func runDoctor(configDir, requested string) ([]doctorCheck, bool) {
	checks := []doctorCheck{{Name: "version", Status: "ok", Message: version}}
	ready := true
	names, err := profile.List(profile.Options{ConfigDir: configDir})
	if err != nil || len(names) == 0 {
		msg := "no policies found; run `runeward init`"
		if err != nil {
			msg = err.Error()
		}
		return append(checks, doctorCheck{Name: "policies", Status: "fail", Message: msg}), false
	}
	checks = append(checks, doctorCheck{Name: "policies", Status: "ok", Message: fmt.Sprintf("%d found", len(names))})

	name := requested
	if name == "" {
		name = preferredDoctorProfile(names, configDir)
	}
	p, err := profile.Load(name, profile.Options{ConfigDir: configDir})
	if err != nil {
		return append(checks, doctorCheck{Name: "charter", Status: "fail", Message: err.Error()}), false
	}
	findings := profile.Lint(p)
	errs, warns := 0, 0
	for _, finding := range findings {
		if finding.Severity == profile.SeverityError {
			errs++
		} else {
			warns++
		}
	}
	status := "ok"
	if errs > 0 {
		status, ready = "fail", false
	} else if warns > 0 {
		status = "warn"
	}
	checks = append(checks, doctorCheck{Name: "charter", Status: status, Message: fmt.Sprintf("%s (%d errors, %d warnings)", name, errs, warns)})

	be, err := backend.For(p)
	if err != nil {
		checks = append(checks, doctorCheck{Name: "runtime", Status: "fail", Message: friendlyRuntimeError(err)})
		ready = false
	} else {
		checks = append(checks, doctorCheck{Name: "runtime", Status: "ok", Message: be.Name() + " reachable"})
	}
	imageStatus := "ok"
	imageMessage := p.Host.Image
	if strings.HasSuffix(p.Host.Image, ":dev") || !strings.Contains(p.Host.Image, "/") {
		imageStatus = "warn"
		imageMessage += " (local/development image; prefer a published immutable reference)"
	}
	checks = append(checks, doctorCheck{Name: "image", Status: imageStatus, Message: imageMessage})

	stateDir := os.Getenv("RUNEWARD_STATE_DIR")
	if stateDir == "" {
		if cache, err := os.UserCacheDir(); err == nil {
			stateDir = filepath.Join(cache, "runeward")
		} else {
			stateDir = filepath.Join(os.TempDir(), "runeward")
		}
	}
	if err := checkWritableDir(stateDir); err != nil {
		checks = append(checks, doctorCheck{Name: "state", Status: "fail", Message: err.Error()})
		ready = false
	} else {
		checks = append(checks, doctorCheck{Name: "state", Status: "ok", Message: stateDir})
	}
	return checks, ready
}

func preferredDoctorProfile(names []string, configDir string) string {
	for _, preferred := range []string{"quickstart", "dev", "ns-auto"} {
		for _, name := range names {
			if name == preferred {
				return name
			}
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if p, err := profile.Load(name, profile.Options{ConfigDir: configDir}); err == nil && p.Host.Type != profile.HostK8s {
			return name
		}
	}
	return names[0]
}

func checkWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return fmt.Errorf("state directory %s is not writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

func friendlyRuntimeError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "engine not reachable") {
		if i := strings.Index(msg, " ("); i > 0 {
			msg = msg[:i]
		}
		return msg + "; start Docker/Podman, then run `runeward doctor` again"
	}
	return msg
}

func newQuickstartCmd() *cobra.Command {
	var noRun bool
	var force bool
	cmd := &cobra.Command{
		Use:   "quickstart [project-dir]",
		Short: "Create a starter policy and prove allow, deny, and signed audit",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			path, _, err := writeStarterCharter(dir, "quickstart", force)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "starter policy: %s\n", path)
			if noRun {
				fmt.Fprintln(cmd.OutOrStdout(), "setup complete; run `runeward doctor quickstart` when your container engine is ready")
				return nil
			}
			configDir := filepath.Join(dir, ".runeward")
			checks, ready := runDoctor(configDir, "quickstart")
			if !ready {
				for _, check := range checks {
					fmt.Fprintf(cmd.OutOrStdout(), "%-5s %-12s %s\n", doctorMarker(check.Status), check.Name, check.Message)
				}
				return fmt.Errorf("quickstart is not ready; start the container engine and retry (or use --no-run)")
			}
			return runQuickstartProof(cmd, configDir)
		},
	}
	cmd.Flags().BoolVar(&noRun, "no-run", false, "create the starter policy without launching a sandbox")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing starter policy")
	return cmd
}

func runQuickstartProof(cmd *cobra.Command, configDir string) error {
	mgr, err := controlplane.New(configDir)
	if err != nil {
		return err
	}
	defer mgr.Close()
	ctx := cmd.Context()
	sb, err := mgr.CreateSandbox(ctx, "quickstart", controlplane.CreateOptions{})
	if err != nil {
		return err
	}
	defer mgr.KillSandbox(context.Background(), sb.ID)

	allowed, err := mgr.Shell(ctx, sb.ID, []string{"sh", "-lc", "echo 'allow: sandbox command executed'"}, "/workspace")
	if err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), allowed.Stdout)
	denied, err := mgr.Shell(ctx, sb.ID, []string{"rm", "-rf", "/tmp/runeward-proof"}, "/workspace")
	if err != nil {
		return err
	}
	if denied.Verdict != profile.VerdictDeny {
		return fmt.Errorf("starter policy proof failed: destructive command verdict was %q", denied.Verdict)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deny: %s\n", denied.Reason)
	if err := mgr.VerifyLedger(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "audit: signed Chronicle verified for sandbox %s\n", sb.ID)
	fmt.Fprintln(cmd.OutOrStdout(), "next: runeward --config-dir .runeward serve")
	return nil
}
