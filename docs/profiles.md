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
# runtime_class = "gvisor"       # k8s only: hardened runtime for untrusted workloads

[network]
default = "deny"                 # deny-by-default egress

[[network.rule]]                 # one rule per allow/deny entry
verdict  = "allow"
hostname = "api.openai.com, *.githubusercontent.com"   # comma-separated; supports *.wildcards

[[env]]
name  = "OPENAI_API_KEY"
value = "sk-..."                 # or: file = "~/.secrets/openai"; or op = "op://vault/item/field"

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
```

## Sections

| Section | Purpose |
| --- | --- |
| `[host]` | Backend (`container` or `k8s`), image, workdir, optional `copy_from` to seed the workspace, and optional `runtime_class` (Kubernetes only) to select a hardened runtime like `gvisor`/`kata` for VM-grade isolation. |
| `[network]` + `[[network.rule]]` | Egress policy. `default = "deny"` plus one `[[network.rule]]` per `verdict`/`hostname` (or `cidr`) entry; hostnames support `*.wildcard` and comma-separated lists. |
| `[[env]]` | Environment/secret injection: literal `value`, from a `file`, or a 1Password `op://` reference. Known secrets are redacted from the ledger. |
| `[[file]]` | Files written into the sandbox at create. |
| `[[policy]]` / `[[cel]]` / `[rego]` | Per-action verdicts. Choose the engine with top-level `policy_engine`. |
| `[policy_bundle]` | Pull signed, versioned policy from an OCI artifact instead of inline rules. |
| `[limits]` | Guardrails: `wall_clock` (duration string), `max_execs`, `egress_requests`, and loop detection via `loop_window`/`loop_threshold`. |

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
op   = "op://Private/GitHub/token"   # 1Password reference
```

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

See the [`examples/`](https://github.com/adefemi171/runeward/tree/main/examples)
directory for complete, runnable profiles.
