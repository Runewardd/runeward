// Package profile defines the declarative profile schema and its loader.
//
// A profile is a named, self-contained security contract for a task: where it
// runs (host), what it can reach (network), what secrets and files it gets,
// which actions require approval (policy), and the resource/loop caps (limits).
// It is resolved fresh on every invocation and never written back to disk.
//
// Profiles may be authored in TOML (default), YAML, or JSON. The struct tags
// carry both `toml` and `json` keys with identical names, so the same field
// spellings work across all three formats (YAML and JSON are decoded via the
// `json` tags). See [Load] for extension resolution.
package profile

// HostType selects the execution backend a profile runs on.
type HostType string

const (
	// HostContainer runs the sandbox as a Docker/Podman container. Default.
	HostContainer HostType = "container"
	// HostK8s runs the sandbox as a Kubernetes Sandbox custom resource.
	HostK8s HostType = "k8s"
)

// Verdict is the decision a policy rule renders for a matched action.
type Verdict string

const (
	VerdictAllow          Verdict = "allow"
	VerdictDeny           Verdict = "deny"
	VerdictRequireApprove Verdict = "require-approval"
)

// Profile is the top-level resolved contract read from a <name>.{toml,yaml,json} file.
type Profile struct {
	// Name is derived from the filename, not the file body.
	Name string `toml:"-" json:"-"`
	// Source is the absolute path the profile was resolved from.
	Source string `toml:"-" json:"-"`

	Host    Host         `toml:"host" json:"host"`
	Prompt  Prompt       `toml:"prompt" json:"prompt"`
	Env     []EnvVar     `toml:"env" json:"env"`
	Files   []File       `toml:"file" json:"file"`
	Network Network      `toml:"network" json:"network"`
	Policy  []PolicyRule `toml:"policy" json:"policy"`
	// PolicyEngine selects the authority engine: "" or "builtin" uses the
	// first-match tool+glob [[policy]] rules; "cel" uses the [[cel]] rules
	// below, each an expression over {tool, arg}; "rego" uses the [rego]
	// OPA/Rego module. The engines are mutually exclusive; when "cel" or
	// "rego" is selected the [[policy]] rules are ignored.
	PolicyEngine string     `toml:"policy_engine" json:"policy_engine"`
	CEL          []CELRule  `toml:"cel" json:"cel"`
	Rego         RegoPolicy `toml:"rego" json:"rego"`
	// PolicyBundle, when set, supersedes the inline policy fields above with a
	// signed policy bundle pulled from an OCI registry.
	PolicyBundle *PolicyBundle `toml:"policy_bundle" json:"policy_bundle"`
	Limits       Limits        `toml:"limits" json:"limits"`
	Fleet        *Fleet        `toml:"fleet" json:"fleet"`
	Audit        Audit         `toml:"audit" json:"audit"`
	// Packages are installed only by `runeward <name> --provision`, never on
	// the run path.
	Packages []string `toml:"packages" json:"packages"`
}

// Host declares where and how a session runs.
type Host struct {
	Type    HostType `toml:"type" json:"type"`
	Name    string   `toml:"name" json:"name"`
	Image   string   `toml:"image" json:"image"`
	User    string   `toml:"user" json:"user"`
	Workdir string   `toml:"workdir" json:"workdir"`
	// CopyFrom, when set, is a local directory whose contents are copied into
	// the workspace at creation. This is a one-time copy: the host directory is
	// never mounted and is never modified, so the agent works against an
	// isolated copy in the sandbox. Supports a leading "~/". The sandbox image
	// must provide `tar`.
	CopyFrom string `toml:"copy_from" json:"copy_from"`
	// Runtime is a backend hint (e.g. "docker", "podman", or "lima" later).
	Runtime string `toml:"runtime" json:"runtime"`
	// RuntimeClass maps to a k8s runtimeClassName (e.g. "gvisor", "kata").
	RuntimeClass string `toml:"runtime_class" json:"runtime_class"`
}

// Prompt is an optional system prompt, inline or sourced from a file.
type Prompt struct {
	Inline string `toml:"inline" json:"inline"`
	File   string `toml:"file" json:"file"`
}

// EnvVar is a single environment value resolved fresh per invocation. Exactly
// one source (Value, File, or Op) should be set.
type EnvVar struct {
	Name string `toml:"name" json:"name"`
	// Value is a literal (least secret; still never persisted under $HOME).
	Value string `toml:"value" json:"value"`
	// File reads the value from a path on the operator's machine.
	File string `toml:"file" json:"file"`
	// Op is a 1Password reference (op://vault/item/field). Resolution deferred.
	Op string `toml:"op" json:"op"`
}

// Secret reports whether this env value carries a sensitive source.
func (e EnvVar) Secret() bool { return e.Op != "" || e.File != "" }

// File is projected into the sandbox, owned root:root at Mode, streamed in
// without touching host disk.
type File struct {
	Path string `toml:"path" json:"path"`
	Mode string `toml:"mode" json:"mode"`
	// File is the source path on the operator's machine.
	File string `toml:"file" json:"file"`
	// Content is an inline literal alternative to File.
	Content string `toml:"content" json:"content"`
}

// Network is the declarative egress/ingress policy. An empty [network] block
// means fully open; setting Default = "deny" enables the allowlist.
type Network struct {
	Default string        `toml:"default" json:"default"`
	Rules   []NetworkRule `toml:"rule" json:"rule"`
	// Enforce selects how the allowlist is enforced. "" (default) uses the
	// cooperative HTTP(S)_PROXY mechanism (an app can bypass it by ignoring the
	// proxy env). "strict" (alias "l3") adds kernel-level enforcement on the
	// Kubernetes backend: an iptables init container transparently redirects all
	// egress through the proxy so it cannot be bypassed. Ignored by the docker
	// backend (which always uses the cooperative host proxy).
	Enforce string `toml:"enforce" json:"enforce"`
}

// StrictEgress reports whether L3 (kernel-level) egress enforcement is requested.
func (n Network) StrictEgress() bool {
	return n.Default == "deny" && (n.Enforce == "strict" || n.Enforce == "l3")
}

// DenyByDefault reports whether unmatched egress should be blocked.
func (n Network) DenyByDefault() bool { return n.Default == "deny" }

// NetworkRule allows or denies traffic to a hostname (supports *.wildcards).
type NetworkRule struct {
	Verdict  string `toml:"verdict" json:"verdict"`
	Hostname string `toml:"hostname" json:"hostname"`
	CIDR     string `toml:"cidr" json:"cidr"`
}

// PolicyRule maps an action (a tool plus optional argument/path/host globs) to
// a verdict evaluated before the action executes.
type PolicyRule struct {
	// Tool matches the action surface: shell|python|node|file.read|file.write|
	// file.edit|net (supports "*").
	Tool string `toml:"tool" json:"tool"`
	// Match is a glob applied to the action's primary argument (command,
	// path, or hostname depending on Tool).
	Match   string  `toml:"match" json:"match"`
	Verdict Verdict `toml:"verdict" json:"verdict"`
	// Reason is surfaced to the approver / recorded in the ledger.
	Reason string `toml:"reason" json:"reason"`
}

// CELRule is one rule of a CEL-based policy bundle. Expr is a boolean CEL
// expression evaluated against the variables `tool` (string) and `arg`
// (string); the first rule whose Expr is true renders its Verdict. This is the
// alternative to the built-in [PolicyRule] engine for teams that standardize on
// CEL. Example: expr = 'tool == "shell" && arg.startsWith("rm ")'.
type CELRule struct {
	Expr    string  `toml:"expr" json:"expr"`
	Verdict Verdict `toml:"verdict" json:"verdict"`
	Reason  string  `toml:"reason" json:"reason"`
}

// UsesCEL reports whether the profile selects the CEL authority engine.
func (p *Profile) UsesCEL() bool { return p.PolicyEngine == "cel" }

// RegoPolicy configures the Rego (OPA) authority engine. Provide the policy
// as an inline Module or a File path (exactly one). Query is the decision
// entrypoint, defaulting to "data.runeward.decision".
type RegoPolicy struct {
	Module string `toml:"module" json:"module"`
	File   string `toml:"file" json:"file"`
	Query  string `toml:"query" json:"query"`
}

// UsesRego reports whether the profile selects the Rego authority engine.
func (p *Profile) UsesRego() bool { return p.PolicyEngine == "rego" }

// PolicyBundle references a signed policy bundle stored as an OCI artifact.
// When set, it supersedes the inline policy fields: the bundle is pulled and
// (when VerifyKey is set) its ed25519 signature is verified before its policy
// is loaded into the authority engine.
type PolicyBundle struct {
	Ref       string `toml:"ref" json:"ref"`               // oci://registry/repo:tag or repo@sha256:...
	VerifyKey string `toml:"verify_key" json:"verify_key"` // base64 ed25519 public key; when set, signature is required
	PlainHTTP bool   `toml:"plain_http" json:"plain_http"` // allow http registries (local testing)
}

// UsesPolicyBundle reports whether the profile sources its authority policy
// from a signed OCI policy bundle.
func (p *Profile) UsesPolicyBundle() bool { return p.PolicyBundle != nil && p.PolicyBundle.Ref != "" }

// Limits declares the cost and loop guardrails for a session.
type Limits struct {
	// WallClock is a duration string (e.g. "30m"). Empty = unlimited.
	WallClock string `toml:"wall_clock" json:"wall_clock"`
	// MaxExecs caps the number of tool invocations. 0 = unlimited.
	MaxExecs int `toml:"max_execs" json:"max_execs"`
	// EgressRequests caps outbound requests through the proxy. 0 = unlimited.
	EgressRequests int `toml:"egress_requests" json:"egress_requests"`
	// LoopWindow / LoopThreshold configure non-converging loop detection:
	// killing a session that repeats >= LoopThreshold near-identical failing
	// actions within LoopWindow.
	LoopWindow    string `toml:"loop_window" json:"loop_window"`
	LoopThreshold int    `toml:"loop_threshold" json:"loop_threshold"`
}

// Fleet spawns N sandboxes from the same contract sharing a coordinated task
// board and artifact volume.
type Fleet struct {
	Replicas int `toml:"replicas" json:"replicas"`
	// TaskBoard is an optional seed list of task identifiers to distribute.
	TaskBoard []string `toml:"task_board" json:"task_board"`
}

// Audit configures the tamper-evident ledger sink and redaction policy.
type Audit struct {
	// Sink is a path (SQLite/BoltDB file) or URI for the append-only ledger.
	Sink string `toml:"sink" json:"sink"`
	// Redact, when true (default), stores hashes + correlation IDs instead of
	// full sensitive payloads.
	Redact *bool `toml:"redact" json:"redact"`
}

// RedactEnabled reports the effective redaction setting (defaults to true).
func (a Audit) RedactEnabled() bool { return a.Redact == nil || *a.Redact }
