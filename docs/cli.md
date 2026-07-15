# CLI reference

The `runeward` binary is the primary entrypoint. A bare Charter name is shorthand
for `enter <charter>`.

## Global flags

| Flag | Description |
| --- | --- |
| `-c`, `--config-dir` | Pin Charter resolution to a directory (or `$RUNEWARD_CONFIG_DIR`). |
| `--help` | Help for any command. |
| `--version` | Print the version. |

## Environment

| Variable | Purpose |
| --- | --- |
| `RUNEWARD_CONFIG_DIR` | Default Charter directory. |
| `RUNEWARD_STATE_DIR` | Where the ledger, keys, terminal recordings, and Cohort state live. **Use a distinct value per running instance.** |
| `RUNEWARD_API_TOKEN` | Bearer token required on every control-plane request (see `--token`). |
| `RUNEWARD_AUTHZ_FILE` | JSON file of named RBAC principals (per-token profile/approval scopes); upgrades the single shared token to multi-principal auth. |
| `RUNEWARD_RATE_LIMIT` | Requests/sec per client IP; unset disables rate limiting. |
| `RUNEWARD_RECORD_TERMINALS` | `1` records governed terminal sessions as asciinema casts under the state dir. |
| `RUNEWARD_AUDIT_WEBHOOK_URL` / `RUNEWARD_AUDIT_FILE` | Stream Chronicle events to a webhook and/or file sink in real time. |
| `RUNEWARD_COPY_FROM_ROOTS` | Colon-separated allowlist restricting `copy_from`/seed source directories. |
| `RUNEWARD_K8S_NAMESPACE` | Namespace for k8s Citadel pods (default `runeward`). |
| `RUNEWARD_K8S_PSA_ENFORCE` | Pod Security Admission enforce level for the managed namespace (`privileged`\|`baseline`\|`restricted`; default `privileged`). |
| `RUNEWARD_K8S_NETWORK_POLICY` | Truthy creates a default-deny (DNS-only egress) NetworkPolicy in the managed namespace. |
| `RUNEWARD_DNS_RESOLVERS` | Comma-separated resolver IPs to confine DNS to under strict egress. |

## Commands

| Command | Description |
| --- | --- |
| `runeward enter <charter> [-- cmd...]` | Create a Citadel and open a shell, or run a one-shot command. |
| `runeward serve [--token ... --tls-cert ... --tls-key ...]` | Start the control plane: REST API + web dashboard (default `127.0.0.1:8080`). |
| `runeward mcp` | Run the MCP server exposing governed tools to an IDE/agent. |
| `runeward list` | List reachable Charters. |
| `runeward print <charter>` | Print a Charter's resolved, secret-redacted policy. |
| `runeward validate <charter> [--strict]` | Statically lint a Charter (missing images, unresolved secrets, unreachable rules, duplicate env). |
| `runeward policy test <charter> --cases <file>` | Simulate a Charter's policy against sample actions, exiting non-zero on mismatch. |
| `runeward policy scaffold [template] [--list]` | Print a ready-made policy template for a common control. |
| `runeward charter sign\|verify <file>` | Produce/verify a detached ed25519 signature over a Charter. |
| `runeward runtime check\|guide\|install <gvisor\|kata>` | Inspect, explain, or install hardened runtimes (gVisor/Kata). |
| `runeward replay <cast>` | Replay a recorded terminal session (asciinema v2 cast). |
| `runeward export <citadel-id> <dest-dir>` | Copy a Citadel workspace back to the host (Docker and k8s). |
| `runeward cohort ...` | Drive multi-agent Cohorts — see [Cohorts](fleets.md). |
| `runeward chronicle verify <bundle.json>` | Verify an exported, signed Chronicle transcript offline. |
| `runeward archive ...` | Manage signed OCI policy Archives (`keygen`, `push`, `pull`). |
| `runeward controller` | Run the Kubernetes controller (watches CRDs). |
| `runeward webhook` | Run the admission webhook. |
| `runeward up` | One-command install of the in-cluster control plane. |
| `runeward version` | Print version information. |

## Examples

```bash
# Interactive Citadel
runeward --config-dir examples enter ns-auto

# One-shot command
runeward --config-dir examples enter ns-auto -- python --version

# Control plane with an isolated state dir
RUNEWARD_STATE_DIR=/tmp/rw runeward --config-dir examples serve

# Verify an exported Chronicle bundle
runeward chronicle verify ./transcript.json

# Sign and publish a policy Archive
runeward archive keygen --out ./keys
runeward archive push oci://ghcr.io/acme/runeward-policies:v3 \
  --policy prod.rego --engine rego --key ./keys/bundle.key

# Authenticated control plane on a non-loopback bind
RUNEWARD_API_TOKEN=$(openssl rand -hex 32) \
  runeward --config-dir examples serve --bind 0.0.0.0 --port 8080

# Lint a Charter and unit-test its policy
runeward --config-dir examples validate ns-auto --strict
runeward --config-dir examples policy test ns-auto \
  --case "tool=shell,action=rm -rf /,expect=deny"

# Scaffold a common policy and append it to a Charter
runeward policy scaffold package-approval >> mycharter.toml

# Install and verify a hardened runtime (dry-run by default; --apply to execute)
runeward runtime install gvisor            # prints the plan
runeward runtime install gvisor --apply    # downloads + registers runsc
runeward runtime check                     # confirm it's registered
```
