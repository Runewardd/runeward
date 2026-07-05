# Profiles

A profile is the declarative security contract for a sandbox. runeward resolves
profiles from a config directory (`--config-dir` or `$RUNEWARD_CONFIG_DIR`) and
supports **TOML, YAML, and JSON** — pick per file by extension (`.toml`,
`.yaml`/`.yml`, `.json`). TOML is the default in the examples.

Inspect the resolved, secret-redacted result before use:

```bash
runeward --config-dir examples print <profile>
```

## Anatomy

```toml
[host]
type      = "container"          # or "k8s"
image     = "runeward-agent:dev"
workdir   = "/workspace"
copy_from = "~/Documents/my-project"   # optional: seed /workspace at create
# runtime_class = "gvisor"       # hardened runtime (Docker --runtime / k8s runtimeClassName)
# read_only  = true              # read-only rootfs (writable /tmp + workspace)
# seccomp    = "/etc/seccomp/strict.json"   # Docker --security-opt seccomp / k8s Localhost profile
# apparmor   = "runtime/default"            # AppArmor profile

[network]
default = "deny"                 # deny-by-default egress

[[network.rule]]                 # one rule per allow/deny entry
verdict  = "allow"
hostname = "api.openai.com, *.githubusercontent.com"   # comma-separated; supports *.wildcards

[[env]]
name  = "OPENAI_API_KEY"
value = "sk-..."                 # or file = "~/.secrets/openai"; or op = "env://OPENAI_API_KEY" / "vault://kv/openai#key"

[[file]]
path    = "/workspace/README.md"
content = "seeded at create"

[[policy]]
tool    = "shell"
match   = "rm -rf *"
verdict = "require-approval"

[limits]
wall_clock      = "15m"          # duration string; empty/zero means unlimited
max_execs       = 200
egress_requests = 100            # cap outbound requests through the proxy
max_tokens      = 2000000        # cap reported model tokens (0 = unlimited)
max_cost_usd    = 25.0           # cap reported spend in USD (0 = unlimited)
```

## Sections

| Section | Purpose |
| --- | --- |
| `[host]` | Backend (`container` or `k8s`), image, workdir, optional `copy_from` to seed the workspace, optional `runtime_class` to select a hardened runtime like `gvisor`/`kata` for VM-grade isolation (maps to `--runtime` on Docker and `runtimeClassName` on Kubernetes), optional `read_only = true` to mount the root filesystem read-only (writable `/tmp` + workspace), and optional `seccomp` / `apparmor` to pin a seccomp/AppArmor profile (Docker `--security-opt`; k8s Localhost profiles — k8s pods default to the runtime's seccomp profile). |
| `[network]` + `[[network.rule]]` | Egress policy. `default = "deny"` plus one `[[network.rule]]` per `verdict`/`hostname` (or `cidr`) entry; hostnames support `*.wildcard` and comma-separated lists. |
| `[[env]]` | Environment/secret injection: literal `value`, from a `file`, or an `op` scheme reference — `env://NAME` (host env var), `vault://<mount>/<path>#<field>` (Vault KV v2 via `VAULT_ADDR`/`VAULT_TOKEN`), `aws://<secret-id>[#json-key]` (AWS Secrets Manager), `gcp://<name>[#version]` (GCP Secret Manager), or `op://…` (1Password, not built in). Resolution is fail-closed; known secrets are redacted from the ledger. |
| `[[file]]` | Files written into the sandbox at create. |
| `[[policy]]` / `[[cel]]` / `[rego]` | Per-action verdicts. Choose the engine with top-level `policy_engine`. |
| `[policy_bundle]` | Pull signed, versioned policy from an OCI artifact instead of inline rules. |
| `[limits]` | Guardrails: `wall_clock` (duration string), `max_execs`, `egress_requests`, loop detection via `loop_window`/`loop_threshold`, and budget caps `max_tokens`/`max_cost_usd` (enforced once usage is reported to `POST /v1/sandboxes/{id}/usage`). |

## Secret injection

```toml
[[env]]
name  = "OPENAI_API_KEY"
value = "sk-..."            # literal (redacted in the ledger)

[[env]]
name = "ANTHROPIC_API_KEY"
file = "~/.secrets/anthropic"   # read from a host file at create

[[env]]
name = "GITHUB_TOKEN"
op   = "env://GITHUB_TOKEN"          # host env var

[[env]]
name = "DB_PASSWORD"
op   = "vault://kv/database/prod#password"   # HashiCorp Vault KV v2 (VAULT_ADDR/VAULT_TOKEN)

[[env]]
name = "STRIPE_KEY"
op   = "aws://prod/stripe#secret_key"        # AWS Secrets Manager (AWS_REGION + creds); #key extracts a JSON field

[[env]]
name = "SIGNING_KEY"
op   = "gcp://signing-key"                   # GCP Secret Manager (GOOGLE_CLOUD_PROJECT + access token / metadata); version defaults to latest
```

The `op` key takes a scheme reference resolved fresh at sandbox creation:
`env://NAME`, `vault://<mount>/<path>#<field>`, `aws://<secret-id>[#json-key]`
(AWS Secrets Manager — `AWS_REGION`/`AWS_DEFAULT_REGION` plus standard
`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_SESSION_TOKEN`),
`gcp://<name>[#version]` or `gcp://projects/<p>/secrets/<n>/versions/<v>` (GCP
Secret Manager — `GOOGLE_CLOUD_PROJECT` plus `GOOGLE_OAUTH_ACCESS_TOKEN` or the
GCE metadata server), or `op://…` (1Password, not built in — always fails
closed). Resolution is fail-closed: an unresolvable reference aborts sandbox
creation rather than starting without the secret.

## Seeding and exporting workspaces

runeward never mounts your host directory. `copy_from` takes a one-time copy into
`/workspace` at create; later host edits do not sync in, and the agent's changes
stay in the sandbox. Pull results back out with:

```bash
runeward export <sandbox-id> ./out
```

## Policy engines

Set `policy_engine` at the top level:

- `builtin` (default) — first-match tool + glob rules via `[[policy]]`.
- `cel` — CEL expressions over `{tool, arg}` via `[[cel]]`.
- `rego` — an OPA/Rego module returning `data.runeward.decision` via `[rego]`.

Instead of inline rules, a profile can consume a signed OCI policy bundle so a
security team ships one artifact many profiles reuse:

```toml
[policy_bundle]
ref        = "oci://ghcr.io/acme/runeward-policies:v3"
verify_key = "<base64 ed25519 public key>"   # when set, a valid signature is REQUIRED
```

See the [`examples/`](https://github.com/Runewardd/runeward/tree/main/examples)
directory for complete, runnable profiles.
