# CLI reference

The `runeward` binary is the primary entrypoint. A bare profile name is shorthand
for `enter <profile>`.

## Global flags

| Flag | Description |
| --- | --- |
| `-c`, `--config-dir` | Pin profile resolution to a directory (or `$RUNEWARD_CONFIG_DIR`). |
| `--help` | Help for any command. |
| `--version` | Print the version. |

## Environment

| Variable | Purpose |
| --- | --- |
| `RUNEWARD_CONFIG_DIR` | Default profile directory. |
| `RUNEWARD_STATE_DIR` | Where the ledger, keys, and fleet state live. **Use a distinct value per running instance.** |

## Commands

| Command | Description |
| --- | --- |
| `runeward enter <profile> [-- cmd...]` | Create a sandbox and open a shell, or run a one-shot command. |
| `runeward serve` | Start the control plane: REST API + web dashboard (default `:8080`). |
| `runeward mcp` | Run the MCP server exposing governed tools to an IDE/agent. |
| `runeward list` | List reachable profiles. |
| `runeward print <profile>` | Print a profile's resolved, secret-redacted policy. |
| `runeward export <sandbox-id> <dest-dir>` | Copy a sandbox workspace back to the host (Docker and k8s). |
| `runeward fleet ...` | Drive multi-agent fleets — see [Fleets](fleets.md). |
| `runeward audit verify <bundle.json>` | Verify an exported, signed audit transcript offline. |
| `runeward bundle ...` | Manage signed OCI policy bundles (`keygen`, `push`, `pull`). |
| `runeward controller` | Run the Kubernetes controller (watches CRDs). |
| `runeward webhook` | Run the admission webhook. |
| `runeward up` | One-command install of the in-cluster control plane. |
| `runeward version` | Print version information. |

## Examples

```bash
# Interactive sandbox
runeward --config-dir examples enter ns-auto

# One-shot command
runeward --config-dir examples enter ns-auto -- python --version

# Control plane with an isolated state dir
RUNEWARD_STATE_DIR=/tmp/rw runeward --config-dir examples serve

# Verify an exported audit bundle
runeward audit verify ./transcript.json

# Sign and publish a policy bundle
runeward bundle keygen --out ./keys
runeward bundle push oci://ghcr.io/acme/runeward-policies:v3 \
  --policy prod.rego --engine rego --key ./keys/bundle.key
```
