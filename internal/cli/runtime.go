package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// probeTimeout bounds every shell-out this command makes so a wedged docker
// engine or kube-apiserver can never hang the diagnostic.
const probeTimeout = 5 * time.Second

// runtimeStatus is the roll-up of what a single hardened runtime looks like
// across the backends we can inspect from this host.
type runtimeStatus struct {
	name          string   // human label, e.g. "gVisor"
	dockerHandles []string // matching handler names to look for in docker
	dockerFound   []string // handler names actually registered with docker
	k8sFound      []string // RuntimeClass names whose handler matches
}

func (r runtimeStatus) available() bool {
	return len(r.dockerFound) > 0 || len(r.k8sFound) > 0
}

// newRuntimeCmd inspects the host for VM-grade isolation runtimes (gVisor,
// Kata) and prints turnkey setup guidance when they are missing. It is a
// read-only doctor command: it shells out defensively, treats missing tooling
// as informational, and never panics.
func newRuntimeCmd(configDir *string) *cobra.Command {
	_ = configDir // runtime diagnostics don't resolve profiles; kept for symmetry.

	var strict bool

	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect and set up hardened container runtimes (gVisor/Kata)",
		Long: "Inspect this host for VM-grade isolation runtimes (gVisor's runsc and\n" +
			"Kata Containers) registered with Docker and/or Kubernetes, and print the\n" +
			"steps to wire them into runeward via a Charter's [host] runtime_class.\n\n" +
			"Run with no subcommand for a check; `runtime guide` prints full setup\n" +
			"instructions. See docs/security-model.md.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtimeCheck(cmd.Context(), cmd.OutOrStdout(), strict)
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false,
		"exit non-zero if neither gVisor nor Kata is available (useful in CI)")

	check := &cobra.Command{
		Use:          "check",
		Short:        "Report which hardened runtimes are registered here",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtimeCheck(cmd.Context(), cmd.OutOrStdout(), strict)
		},
	}
	check.Flags().BoolVar(&strict, "strict", false,
		"exit non-zero if neither gVisor nor Kata is available (useful in CI)")

	guide := &cobra.Command{
		Use:          "guide",
		Short:        "Print full gVisor/Kata setup instructions for Docker and Kubernetes",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			printGuide(cmd.OutOrStdout())
			return nil
		},
	}

	cmd.AddCommand(check, guide, newRuntimeInstallCmd())
	return cmd
}

// Bounds and well-known paths for the installer. Downloads get a generous but
// finite window; every privileged path is explicit so nothing is mutated by
// surprise.
const (
	downloadTimeout  = 60 * time.Second
	applyTimeout     = 30 * time.Second
	dockerDaemonJSON = "/etc/docker/daemon.json"
	localBinDir      = "/usr/local/bin"
)

// installOpts is the resolved, validated set of installer inputs.
type installOpts struct {
	runtime      string // "gvisor" | "kata"
	engine       string // "docker" | "k8s"
	apply        bool
	version      string // gVisor release, e.g. "latest"
	runtimeClass string // k8s RuntimeClass name / docker runtime name
	sha256hex    string // optional pin for the runsc binary
}

// newRuntimeInstallCmd is a turnkey installer for the hardened runtimes. It is
// safe by default: with no --apply it only prints the exact commands and
// manifests it *would* run. Every download is checksum-verified and fails
// closed; every shell-out is bounded by a context timeout and never panics;
// missing tooling is reported as actionable guidance rather than a crash.
func newRuntimeInstallCmd() *cobra.Command {
	var (
		engine       string
		apply        bool
		version      string
		runtimeClass string
		sha256hex    string
	)

	cmd := &cobra.Command{
		Use:   "install <gvisor|kata>",
		Short: "Install and register a hardened runtime (gVisor/Kata) with Docker or Kubernetes",
		Long: "Install a VM-grade isolation runtime and wire it into a backend.\n\n" +
			"Dry-run is the DEFAULT: without --apply this prints the exact commands\n" +
			"and manifests it would run and mutates nothing. All downloads are\n" +
			"checksum-verified (fail closed); all shell-outs are timeout-bounded.\n\n" +
			"Examples:\n" +
			"  runeward runtime install gvisor                 # dry-run docker plan\n" +
			"  runeward runtime install gvisor --apply         # download+verify+register\n" +
			"  runeward runtime install gvisor --engine k8s    # print a RuntimeClass\n" +
			"  runeward runtime install kata --engine k8s --apply",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := strings.ToLower(strings.TrimSpace(args[0]))
			switch rt {
			case "gvisor", "kata":
			default:
				return fmt.Errorf("unknown runtime %q: expected \"gvisor\" or \"kata\"", args[0])
			}

			eng := strings.ToLower(strings.TrimSpace(engine))
			switch eng {
			case "docker", "k8s":
			default:
				return fmt.Errorf("unknown --engine %q: expected \"docker\" or \"k8s\"", engine)
			}

			cls := strings.TrimSpace(runtimeClass)
			if cls == "" {
				cls = rt // defaults: "gvisor" / "kata"
			}
			ver := strings.TrimSpace(version)
			if ver == "" {
				ver = "latest"
			}

			opts := installOpts{
				runtime:      rt,
				engine:       eng,
				apply:        apply,
				version:      ver,
				runtimeClass: cls,
				sha256hex:    strings.ToLower(strings.TrimSpace(sha256hex)),
			}

			out := cmd.OutOrStdout()
			switch {
			case opts.engine == "k8s":
				return installK8s(cmd.Context(), out, opts)
			case opts.runtime == "gvisor":
				return installGvisorDocker(cmd.Context(), out, opts)
			default:
				return installKataDocker(out, opts)
			}
		},
	}

	cmd.Flags().StringVar(&engine, "engine", "docker", "target backend: \"docker\" or \"k8s\"")
	cmd.Flags().BoolVar(&apply, "apply", false, "actually perform the install (default is a safe dry-run)")
	cmd.Flags().StringVar(&version, "version", "latest", "gVisor release to install (e.g. \"latest\" or \"20240101\")")
	cmd.Flags().StringVar(&runtimeClass, "runtime-class", "", "k8s RuntimeClass name (default \"gvisor\"/\"kata\")")
	cmd.Flags().StringVar(&sha256hex, "sha256", "", "optional sha256 pin for the runsc binary (hex); otherwise the bucket's .sha512 sidecar is required")
	return cmd
}

// installGvisorDocker downloads runsc + its containerd shim from the official
// gVisor release bucket, verifies each with the bucket's .sha512 sidecar (or
// the runsc binary against --sha256), installs them into /usr/local/bin, and
// merges a "runsc" runtimes entry into /etc/docker/daemon.json. Nothing is
// mutated without --apply; privileged writes as non-root print sudo commands.
func installGvisorDocker(ctx context.Context, out io.Writer, opts installOpts) error {
	arch, err := mapGvisorArch(runtime.GOARCH)
	if err != nil {
		return err
	}
	base := fmt.Sprintf("https://storage.googleapis.com/gvisor/releases/release/%s/%s", opts.version, arch)
	runscURL := base + "/runsc"
	shimURL := base + "/containerd-shim-runsc-v1"

	printHeader(out, "gVisor (runsc) -> Docker")
	fmt.Fprintln(out, "Plan:")
	fmt.Fprintf(out, "  1. Download + checksum-verify:\n")
	fmt.Fprintf(out, "       %s (+ .sha512)\n", runscURL)
	fmt.Fprintf(out, "       %s (+ .sha512)\n", shimURL)
	if opts.sha256hex != "" {
		fmt.Fprintf(out, "     runsc pinned to --sha256 %s\n", opts.sha256hex)
	}
	fmt.Fprintf(out, "  2. chmod 0755 and install runsc + containerd-shim-runsc-v1 into %s\n", localBinDir)
	fmt.Fprintf(out, "  3. Merge a \"runsc\" entry under \"runtimes\" in %s (existing keys preserved)\n", dockerDaemonJSON)
	fmt.Fprintln(out, "  4. Restart the engine: systemctl restart docker")
	fmt.Fprintln(out)

	if !opts.apply {
		printDryRunNote(out)
		printNextSteps(out, "runsc")
		return nil
	}

	tmp, err := os.MkdirTemp("", "runeward-gvisor-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	root := os.Geteuid() == 0
	if root {
		defer os.RemoveAll(tmp)
	}

	runscPath := filepath.Join(tmp, "runsc")
	shimPath := filepath.Join(tmp, "containerd-shim-runsc-v1")

	fmt.Fprintln(out, "Downloading and verifying...")
	if err := downloadVerified(ctx, out, runscURL, runscPath, opts.sha256hex); err != nil {
		return err
	}
	if err := downloadVerified(ctx, out, shimURL, shimPath, ""); err != nil {
		return err
	}
	for _, p := range []string{runscPath, shimPath} {
		if err := os.Chmod(p, 0o755); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}

	if !root {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Not running as root: binaries verified in a temp dir. Finish as root:")
		for _, name := range []string{"runsc", "containerd-shim-runsc-v1"} {
			fmt.Fprintf(out, "  sudo install -m 0755 %s %s\n", filepath.Join(tmp, name), filepath.Join(localBinDir, name))
		}
		merged, err := mergeDockerRuntime("runsc", filepath.Join(localBinDir, "runsc"), nil)
		if err != nil {
			return err
		}
		if err := applyDaemonJSON(out, merged, false); err != nil {
			return err
		}
		fmt.Fprintf(out, "  (remove the temp dir when done: rm -rf %s)\n", tmp)
		printNextSteps(out, "runsc")
		return nil
	}

	for _, name := range []string{"runsc", "containerd-shim-runsc-v1"} {
		src := filepath.Join(tmp, name)
		dst := filepath.Join(localBinDir, name)
		if err := moveFile(src, dst); err != nil {
			return fmt.Errorf("install %s: %w", dst, err)
		}
		fmt.Fprintf(out, "  installed %s\n", dst)
	}
	merged, err := mergeDockerRuntime("runsc", filepath.Join(localBinDir, "runsc"), nil)
	if err != nil {
		return err
	}
	if err := applyDaemonJSON(out, merged, true); err != nil {
		return err
	}
	printNextSteps(out, "runsc")
	return nil
}

// installKataDocker does not attempt Kata's distro-specific package install. It
// prints clear guidance plus the daemon.json runtimes entry, and with --apply
// only merges that entry when a Kata binary is already present on the host.
func installKataDocker(out io.Writer, opts installOpts) error {
	printHeader(out, "Kata Containers -> Docker")
	fmt.Fprintln(out, "Kata's runtime install is distro-specific and is NOT automated here.")
	fmt.Fprintln(out, "Install it via your distro or the official installer, e.g.:")
	fmt.Fprintln(out, "  https://github.com/kata-containers/kata-containers/tree/main/docs/install")
	fmt.Fprintln(out)

	kataPath := detectKataBinary()
	entryPath := kataPath
	if entryPath == "" {
		entryPath = "/usr/bin/containerd-shim-kata-v2"
	}

	fmt.Fprintf(out, "Docker runtimes entry to add to %s (merge, don't clobber):\n", dockerDaemonJSON)
	fmt.Fprintf(out, "  \"runtimes\": { \"kata\": { \"path\": %q } }\n", entryPath)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Then restart the engine: systemctl restart docker")
	fmt.Fprintln(out)

	if !opts.apply {
		printDryRunNote(out)
		printNextSteps(out, "kata")
		return nil
	}

	if kataPath == "" {
		fmt.Fprintln(out, "Kata binary not found (looked for containerd-shim-kata-v2 / kata-runtime).")
		fmt.Fprintln(out, "Install Kata first, then re-run with --apply to register it with docker.")
		printNextSteps(out, "kata")
		return nil
	}
	fmt.Fprintf(out, "Found Kata binary: %s\n", kataPath)

	merged, err := mergeDockerRuntime("kata", entryPath, nil)
	if err != nil {
		return err
	}
	if err := applyDaemonJSON(out, merged, os.Geteuid() == 0); err != nil {
		return err
	}
	printNextSteps(out, "kata")
	return nil
}

// installK8s renders a RuntimeClass manifest for the chosen runtime. Dry-run
// prints the YAML; --apply pipes it to `kubectl apply -f -` (30s timeout). A
// missing kubectl is reported with the manifest, not treated as fatal.
func installK8s(ctx context.Context, out io.Writer, opts installOpts) error {
	handler := "runsc"
	label := "gVisor (runsc)"
	if opts.runtime == "kata" {
		handler = "kata"
		label = "Kata Containers"
	}

	printHeader(out, fmt.Sprintf("%s -> Kubernetes RuntimeClass", label))
	manifest := runtimeClassManifest(opts.runtimeClass, handler)
	fmt.Fprintln(out, "RuntimeClass manifest:")
	fmt.Fprintln(out)
	fmt.Fprint(out, manifest)
	fmt.Fprintln(out)

	if !opts.apply {
		fmt.Fprintln(out, "Dry-run (default): nothing was applied.")
		fmt.Fprintln(out, "Re-run with --apply to `kubectl apply -f -` the manifest above.")
		printNextSteps(out, opts.runtimeClass)
		return nil
	}

	bin, err := exec.LookPath("kubectl")
	if err != nil {
		fmt.Fprintln(out, "kubectl not found in PATH — apply the manifest above manually, e.g.:")
		fmt.Fprintln(out, "  kubectl apply -f - <<'EOF'")
		fmt.Fprint(out, manifest)
		fmt.Fprintln(out, "EOF")
		printNextSteps(out, opts.runtimeClass)
		return nil
	}

	cctx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()
	c := exec.CommandContext(cctx, bin, "apply", "-f", "-")
	c.Stdin = strings.NewReader(manifest)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("kubectl apply failed: %s", firstLine(detail))
	}
	if s := strings.TrimSpace(stdout.String()); s != "" {
		fmt.Fprintln(out, s)
	}
	fmt.Fprintf(out, "applied RuntimeClass %q (handler %s)\n", opts.runtimeClass, handler)
	printNextSteps(out, opts.runtimeClass)
	return nil
}

// mapGvisorArch maps Go's GOARCH to the arch token used in the gVisor release
// bucket path. gVisor publishes builds for x86_64 and arm64.
func mapGvisorArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported architecture %q: gVisor publishes amd64 (x86_64) and arm64 builds", goarch)
	}
}

// runtimeClassManifest renders a minimal, valid RuntimeClass for the handler.
func runtimeClassManifest(name, handler string) string {
	return fmt.Sprintf("apiVersion: node.k8s.io/v1\nkind: RuntimeClass\nmetadata:\n  name: %s\nhandler: %s\n", name, handler)
}

// downloadVerified fetches url into dst and verifies it, failing closed. If
// sha256hex is set the file is checked against it; otherwise the bucket's
// "<url>.sha512" sidecar is REQUIRED and verified. If neither verifies, the
// binary is refused.
func downloadVerified(ctx context.Context, out io.Writer, url, dst, sha256hex string) error {
	if err := httpGetToFile(ctx, url, dst); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}

	if sha256hex != "" {
		if err := verifyChecksumFile(dst, sha256hex, sha256.New()); err != nil {
			return err
		}
		fmt.Fprintf(out, "  verified %s (sha256 %s)\n", filepath.Base(dst), shortHex(sha256hex))
		return nil
	}

	sidecar := dst + ".sha512"
	if err := httpGetToFile(ctx, url+".sha512", sidecar); err != nil {
		return fmt.Errorf("download checksum %s.sha512: %w (refusing to install an unverified binary)", url, err)
	}
	data, err := os.ReadFile(sidecar)
	if err != nil {
		return fmt.Errorf("read checksum sidecar: %w", err)
	}
	want, err := parseChecksumSidecar(data)
	if err != nil {
		return err
	}
	if err := verifyChecksumFile(dst, want, sha512.New()); err != nil {
		return err
	}
	fmt.Fprintf(out, "  verified %s (sha512 %s)\n", filepath.Base(dst), shortHex(want))
	return nil
}

// httpGetToFile streams a GET response body to dst with a bounded context.
// Any non-200 status is an error; the body is never buffered wholesale.
func httpGetToFile(ctx context.Context, url, dst string) error {
	cctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// verifyChecksumFile hashes path with h and compares (case-insensitively)
// against expectedHex, returning an error — failing closed — on any mismatch.
func verifyChecksumFile(path, expectedHex string, h hash.Hash) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(strings.TrimSpace(got), strings.TrimSpace(expectedHex)) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s (failing closed)", filepath.Base(path), got, expectedHex)
	}
	return nil
}

// parseChecksumSidecar extracts the hex digest from a sha512sum-style sidecar
// ("<hex>  <filename>").
func parseChecksumSidecar(data []byte) (string, error) {
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) == 0 || fields[0] == "" {
		return "", fmt.Errorf("empty or malformed checksum sidecar")
	}
	return fields[0], nil
}

// mergeDockerRuntime read-merge-writes daemon.json in memory: it loads the
// existing file (if any), adds/updates one entry under "runtimes" without
// clobbering other keys or other runtimes, and returns pretty-printed JSON.
func mergeDockerRuntime(name, path string, extra map[string]any) ([]byte, error) {
	top := map[string]json.RawMessage{}
	data, err := os.ReadFile(dockerDaemonJSON)
	switch {
	case err == nil:
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &top); err != nil {
				return nil, fmt.Errorf("parse %s: %w", dockerDaemonJSON, err)
			}
		}
	case os.IsNotExist(err):
		// fresh file
	default:
		return nil, fmt.Errorf("read %s: %w", dockerDaemonJSON, err)
	}

	runtimes := map[string]json.RawMessage{}
	if raw, ok := top["runtimes"]; ok {
		if err := json.Unmarshal(raw, &runtimes); err != nil {
			return nil, fmt.Errorf("parse runtimes in %s: %w", dockerDaemonJSON, err)
		}
	}

	entry := map[string]any{"path": path}
	for k, v := range extra {
		entry[k] = v
	}
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	runtimes[name] = entryJSON

	runtimesJSON, err := json.Marshal(runtimes)
	if err != nil {
		return nil, err
	}
	top["runtimes"] = runtimesJSON

	return json.MarshalIndent(top, "", "  ")
}

// applyDaemonJSON writes the merged daemon.json when running as root, otherwise
// prints the exact sudo commands to write it. It never mutates the file as a
// non-root user.
func applyDaemonJSON(out io.Writer, merged []byte, root bool) error {
	if root {
		if err := os.MkdirAll(filepath.Dir(dockerDaemonJSON), 0o755); err != nil {
			return fmt.Errorf("ensure %s dir: %w", dockerDaemonJSON, err)
		}
		if err := os.WriteFile(dockerDaemonJSON, append(merged, '\n'), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dockerDaemonJSON, err)
		}
		fmt.Fprintf(out, "  wrote %s\n", dockerDaemonJSON)
		fmt.Fprintln(out, "  restart the engine: systemctl restart docker")
		return nil
	}
	fmt.Fprintf(out, "  # not root: write the merged %s yourself:\n", dockerDaemonJSON)
	fmt.Fprintf(out, "  sudo tee %s >/dev/null <<'EOF'\n", dockerDaemonJSON)
	fmt.Fprintln(out, string(merged))
	fmt.Fprintln(out, "EOF")
	fmt.Fprintln(out, "  sudo systemctl restart docker")
	return nil
}

// moveFile relocates src to dst, falling back to copy+remove when the two live
// on different filesystems (temp dir vs /usr/local/bin).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// detectKataBinary returns the path to an installed Kata shim/runtime, or "".
func detectKataBinary() string {
	for _, c := range []string{
		"/usr/bin/containerd-shim-kata-v2",
		"/usr/local/bin/containerd-shim-kata-v2",
		"/usr/bin/kata-runtime",
	} {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	for _, name := range []string{"containerd-shim-kata-v2", "kata-runtime"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// printHeader prints a title underlined to its own width.
func printHeader(out io.Writer, title string) {
	fmt.Fprintln(out, title)
	fmt.Fprintln(out, strings.Repeat("=", len(title)))
	fmt.Fprintln(out)
}

// printDryRunNote states plainly that nothing ran and how to actually apply.
func printDryRunNote(out io.Writer) {
	fmt.Fprintln(out, "Dry-run (default): nothing above was executed.")
	fmt.Fprintln(out, "Re-run with --apply to perform these steps.")
}

// printNextSteps always closes the installer with how to wire the runtime into
// a profile and how to verify it.
func printNextSteps(out io.Writer, className string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps")
	fmt.Fprintln(out, "----------")
	fmt.Fprintf(out, "  1. Set runtime_class = %q under [host] in your Charter.\n", className)
	fmt.Fprintln(out, "  2. Verify with: runeward runtime check")
	fmt.Fprintln(out, "  3. Details: docs/security-model.md")
}

// shortHex trims a digest to a readable prefix for progress output.
func shortHex(h string) string {
	if len(h) > 12 {
		return h[:12] + "..."
	}
	return h
}

// runtimeCheck probes Docker and Kubernetes for hardened runtimes, prints a
// status section plus targeted guidance for anything missing, and (only under
// --strict) returns an error when nothing VM-grade is available. It always
// exits 0 otherwise: it's a diagnostic, not a gate.
func runtimeCheck(ctx context.Context, out io.Writer, strict bool) error {
	statuses := []*runtimeStatus{
		{name: "gVisor", dockerHandles: []string{"runsc"}},
		{name: "Kata", dockerHandles: []string{"kata", "kata-runtime", "kata-qemu", "kata-clh"}},
	}

	fmt.Fprintln(out, "runeward runtime check")
	fmt.Fprintln(out, "======================")
	fmt.Fprintln(out)

	inspectDocker(ctx, out, statuses)
	fmt.Fprintln(out)
	inspectKube(ctx, out, statuses)
	fmt.Fprintln(out)

	// Summary table.
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RUNTIME\tDOCKER\tKUBERNETES\tSTATUS")
	anyAvailable := false
	for _, s := range statuses {
		if s.available() {
			anyAvailable = true
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			s.name, joinOrDash(s.dockerFound), joinOrDash(s.k8sFound), availLabel(s.available()))
	}
	_ = tw.Flush()
	fmt.Fprintln(out)

	// Targeted guidance for whatever is missing.
	for _, s := range statuses {
		if !s.available() {
			printMissingGuidance(out, s.name)
		}
	}

	if anyAvailable {
		fmt.Fprintln(out, "At least one VM-grade runtime is available. Set `runtime_class` in a")
		fmt.Fprintln(out, "Charter's [host] block to use it (see `runeward runtime guide`).")
	} else {
		fmt.Fprintln(out, "No VM-grade isolation runtime detected. Run `runeward runtime guide`")
		fmt.Fprintln(out, "for full setup instructions, or see docs/security-model.md.")
	}

	if strict && !anyAvailable {
		return fmt.Errorf("no hardened runtime (gVisor/Kata) available")
	}
	return nil
}

// inspectDocker asks the docker engine for its registered runtimes and records
// which hardened handlers are present. A missing or unreachable docker is
// reported as a note, not a failure.
func inspectDocker(ctx context.Context, out io.Writer, statuses []*runtimeStatus) {
	fmt.Fprintln(out, "Docker")
	fmt.Fprintln(out, "------")

	bin, err := exec.LookPath("docker")
	if err != nil {
		fmt.Fprintln(out, "  docker not found in PATH — skipping (install Docker to inspect its runtimes).")
		return
	}

	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "info", "--format", "{{json .Runtimes}}")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		fmt.Fprintf(out, "  docker engine not reachable — skipping (%s).\n", firstLine(detail))
		return
	}

	// `.Runtimes` is a JSON object keyed by runtime name.
	var runtimes map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &runtimes); err != nil {
		fmt.Fprintf(out, "  could not parse docker runtimes — skipping (%v).\n", err)
		return
	}

	names := make([]string, 0, len(runtimes))
	for name := range runtimes {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(out, "  registered runtimes: %s\n", joinOrDash(names))

	registered := make(map[string]bool, len(names))
	for _, name := range names {
		registered[name] = true
	}
	for _, s := range statuses {
		for _, h := range s.dockerHandles {
			if registered[h] {
				s.dockerFound = append(s.dockerFound, h)
			}
		}
	}
}

// inspectKube lists RuntimeClasses via kubectl (if present) and matches them
// against the hardened handlers we care about. Absence of kubectl or a cluster
// is informational only.
func inspectKube(ctx context.Context, out io.Writer, statuses []*runtimeStatus) {
	fmt.Fprintln(out, "Kubernetes")
	fmt.Fprintln(out, "----------")

	bin, err := exec.LookPath("kubectl")
	if err != nil {
		fmt.Fprintln(out, "  kubectl not found in PATH — skipping (install kubectl to inspect RuntimeClasses).")
		return
	}

	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "get", "runtimeclass", "-o", "name")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		fmt.Fprintf(out, "  cluster not reachable — skipping (%s).\n", firstLine(detail))
		return
	}

	var classes []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// `-o name` prints "runtimeclass.node.k8s.io/<name>".
		if _, name, ok := strings.Cut(line, "/"); ok {
			line = name
		}
		classes = append(classes, line)
	}
	sort.Strings(classes)
	fmt.Fprintf(out, "  RuntimeClasses: %s\n", joinOrDash(classes))

	// Match RuntimeClass names against the hardened handlers. We can't read
	// each class's handler without extra calls, so match on the class name
	// containing a known handler token (the conventional naming).
	for _, s := range statuses {
		for _, c := range classes {
			lc := strings.ToLower(c)
			for _, h := range s.dockerHandles {
				if lc == h || strings.Contains(lc, strings.TrimSuffix(h, "-runtime")) {
					s.k8sFound = append(s.k8sFound, c)
					break
				}
			}
		}
		s.k8sFound = dedupe(s.k8sFound)
	}
}

// printMissingGuidance prints concise, runtime-specific setup steps.
func printMissingGuidance(out io.Writer, name string) {
	switch name {
	case "gVisor":
		fmt.Fprintln(out, "gVisor is not registered. To enable it:")
		fmt.Fprintln(out, "  1. Install runsc: https://gvisor.dev/docs/user_guide/install/")
		fmt.Fprintln(out, "  2. Docker: add a runtimes entry to /etc/docker/daemon.json, e.g.")
		fmt.Fprintln(out, "       { \"runtimes\": { \"runsc\": { \"path\": \"/usr/local/bin/runsc\" } } }")
		fmt.Fprintln(out, "     then `sudo systemctl restart docker`.")
		fmt.Fprintln(out, "  3. Kubernetes: create a RuntimeClass named `gvisor` (handler: runsc).")
		fmt.Fprintln(out, "  4. Point runeward at it: set `runtime_class = \"gvisor\"` in the Charter [host].")
		fmt.Fprintln(out)
	case "Kata":
		fmt.Fprintln(out, "Kata Containers is not registered. To enable it:")
		fmt.Fprintln(out, "  1. Install Kata: https://github.com/kata-containers/kata-containers")
		fmt.Fprintln(out, "  2. Docker: add a runtimes entry to /etc/docker/daemon.json, e.g.")
		fmt.Fprintln(out, "       { \"runtimes\": { \"kata\": { \"path\": \"/usr/bin/kata-runtime\" } } }")
		fmt.Fprintln(out, "     then `sudo systemctl restart docker`.")
		fmt.Fprintln(out, "  3. Kubernetes: create a RuntimeClass named `kata` (handler: kata).")
		fmt.Fprintln(out, "  4. Point runeward at it: set `runtime_class = \"kata\"` in the Charter [host].")
		fmt.Fprintln(out)
	}
}

// printGuide prints the full setup walkthrough for both runtimes and explains
// how runeward consumes runtime_class.
func printGuide(out io.Writer) {
	const guide = `runeward VM-grade isolation setup guide
=======================================

runeward can run each Citadel under a hardened, VM-grade runtime instead of the
host kernel's default runc. Two options are supported today:

  - gVisor (runsc): an application kernel in userspace; strong syscall isolation.
  - Kata Containers: lightweight VMs; hardware-virtualization isolation.

How runeward consumes it
------------------------
A Charter's [host] block may set:

    [host]
    type          = "docker"   # or "k8s"
    runtime_class = "gvisor"    # maps to the runtime/RuntimeClass name

  - Docker: runtime_class is passed as "docker run --runtime <value>". The value
    must be a runtime registered with the docker engine (see below). If it isn't
    registered, container creation fails closed with a clear error.
  - Kubernetes: runtime_class is set as the Pod's "runtimeClassName". The named
    RuntimeClass must exist in the cluster, or the Pod is rejected.

In both cases an unregistered runtime fails closed — runeward never silently
falls back to the default runtime. See docs/security-model.md.

gVisor (runsc)
--------------
1. Install runsc: https://gvisor.dev/docs/user_guide/install/
2. Docker: register runsc as a runtime in /etc/docker/daemon.json:

       {
         "runtimes": {
           "runsc": { "path": "/usr/local/bin/runsc" }
         }
       }

   Restart the engine: sudo systemctl restart docker
   Verify: docker info --format '{{json .Runtimes}}'  (should list "runsc")
3. Kubernetes: create a RuntimeClass whose handler is runsc:

       apiVersion: node.k8s.io/v1
       kind: RuntimeClass
       metadata:
         name: gvisor
       handler: runsc

4. Use it: set runtime_class = "gvisor" in the Charter [host] block.

Kata Containers
---------------
1. Install Kata: https://github.com/kata-containers/kata-containers
2. Docker: register the kata runtime in /etc/docker/daemon.json:

       {
         "runtimes": {
           "kata": { "path": "/usr/bin/kata-runtime" }
         }
       }

   Restart the engine: sudo systemctl restart docker
   Verify: docker info --format '{{json .Runtimes}}'  (should list "kata")
3. Kubernetes: create a RuntimeClass whose handler is kata:

       apiVersion: node.k8s.io/v1
       kind: RuntimeClass
       metadata:
         name: kata
       handler: kata

4. Use it: set runtime_class = "kata" in the Charter [host] block.

Verify your setup any time with: runeward runtime check
`
	fmt.Fprint(out, guide)
}

func availLabel(ok bool) string {
	if ok {
		return "available"
	}
	return "missing"
}

func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func dedupe(items []string) []string {
	if len(items) < 2 {
		return items
	}
	seen := make(map[string]bool, len(items))
	out := items[:0]
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}
