# Codex, Claude Code, and GitHub Copilot

Runeward exposes the same governed execution surface to every MCP-capable agent. The checked-in `.mcp.json`, `.vscode/mcp.json`, Cursor configuration, Copilot custom agent, and `dist/codex-plugin/runeward` package all invoke `runeward mcp` without embedding credentials.

## Shared setup

Install Runeward, put `runeward` on `PATH`, and export configuration in the process that launches the agent:

```bash
export RUNEWARD_CONFIG_DIR="$PWD/examples"
export RUNEWARD_STATE_DIR="$HOME/.cache/runeward/agent"
export RUNEWARD_AUTHZ_FILE="$HOME/.config/runeward/authz.json"
export RUNEWARD_MCP_DEFAULT_TOKEN="$(security find-generic-password -w -s runeward-agent)"
```

Alternatively, save a short-lived OIDC credential once; Cohort CLI and stdio
MCP use it automatically when an explicit environment token is absent:

```bash
runeward auth login --issuer https://id.example.com \
  --client-id runeward-cli --audience runeward
runeward auth status
```

Never commit the token or put it in MCP arguments. Give each human, agent, CI
job, and delegated worker a distinct principal. Principals that are meant to
collaborate share a `tenant`; their individual names remain the Chronicle actor.
Use at least 32 random characters for any static token accepted on a
non-loopback listener.

## Codex

Install the plugin under `dist/codex-plugin/runeward`, or use the repository `.mcp.json`. The bundled skill requires identity and Charter discovery, least privilege, hard pauses for Conclave approvals, delegated-run lineage, and teardown.

## Claude Code

Claude Code discovers the repository `.mcp.json`. Start Claude from an environment containing the variables above. Keep project configuration credential-free so forks and logs cannot disclose a bearer token.

## GitHub Copilot

VS Code uses `.vscode/mcp.json`. The custom agent at `.github/agents/runeward.agent.md` allowlists the normal Citadel execution tools and intentionally excludes approval resolution and Cohort administration.

Copilot cloud agents cannot reach a developer's local Docker socket. Deploy `runeward serve` behind HTTPS and connect to its `/mcp` endpoint with a dedicated workload principal. Remote MCP requests are authorized per request; do not place a second shared proxy token in front of RBAC.

## Delegated agents

Pass `parent_citadel`, `run_id`, `agent`, `provider`, and `model` when creating a
child. Runeward records durable provider-neutral Run lineage and requires a child
to inherit the parent's tenant and exact Charter. Use a separate principal per
concurrently trusted agent where practical.
