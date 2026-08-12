# REST API

`runeward serve` exposes the control plane over HTTP (default `127.0.0.1:8080`).
All actions flow through the same governed path as every other surface.

## Authentication

`serve` binds `127.0.0.1` by default and refuses a non-loopback `--bind` unless
authentication is configured. Set a bearer token with `--token` /
`RUNEWARD_API_TOKEN`, or point `RUNEWARD_AUTHZ_FILE` at a JSON store of named
principals (each with its own token, launch scope, optional approval-profile
scope, and admin flag) for multi-principal RBAC. When set, the token is required
on every request except `/healthz` and the static dashboard shell — pass it as
`Authorization: Bearer <token>` or an `X-Runeward-Token` header. The browser
obtains a short-lived, single-use scoped ticket for the terminal WebSocket, so
long-lived credentials never appear in URLs. Non-loopback listeners require TLS
via `--tls-cert`/`--tls-key` unless `--allow-insecure-http` explicitly
acknowledges a trusted TLS-terminating proxy; request bodies are capped at 16
MiB. See the
[Security model](security-model.md).

Under RBAC, a non-admin principal sees and can act on only its tenant's
resources; admins see all. Different agent principals can share a tenant while
remaining independently attributable. OIDC is enabled with
`RUNEWARD_OIDC_ISSUER` and `RUNEWARD_OIDC_AUDIENCE`; signed Runeward claims map
to the same authorization model as local tokens.

## Health & identity

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness probe (unauthenticated). |
| `GET` | `/v1/whoami` | Identity and effective capabilities, including launch and approval scopes. |
| `GET` | `/v1/readiness?profile=NAME` | Check policy, runtime, and image readiness without exposing host paths. |
| `GET` | `/metrics` | Prometheus metrics. |

## Charters

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/charters` | List reachable Charters (under RBAC, scoped to the caller's `allowed_profiles`). |

## Citadels

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/citadels` | Create a Citadel. Supports `profile`, admin-only `copy_from`, and optional `parent_citadel`, `run_id`, `agent`, `provider`, and `model` lineage. |
| `GET` | `/v1/citadels` | List Citadels (scoped to the caller under RBAC). |
| `GET` | `/v1/citadels/{id}` | Get one Citadel (includes `owner` and cumulative `usage`). |
| `DELETE` | `/v1/citadels/{id}` | Kill and remove a Citadel. |
| `GET` | `/v1/citadels/{id}/workspace` | Download the isolated workspace as a tar archive. |
| `GET` | `/v1/citadels/{id}/evidence` | Download resolved policy + signed audit as portable evidence JSON. |

## Runs

Runs are durable, provider-neutral lineage records. They survive Citadel
teardown and include tenant, actor, Charter, agent/provider/model, optional
parent run, timestamps, and terminal status.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/runs` | List accessible Runs (tenant-scoped under RBAC). |
| `GET` | `/v1/runs/{id}` | Get one accessible Run. |

## Agent sessions

Agent sessions are governed asynchronous commands with durable, redacted
stdout/stderr events. Cohort CLI commands use them for live worker output. A
client can reconnect with an event sequence cursor without losing the backlog.

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/citadels/{id}/agent-sessions` | Start a session. Body: `command`, optional `agent`, `model`, `cohort_id`, and `task_id`. |
| `GET` | `/v1/agent-sessions?cohort_id=...` | List accessible sessions, optionally scoped to a Cohort. |
| `GET` | `/v1/agent-sessions/{id}` | Read durable metadata and terminal status. |
| `GET` | `/v1/agent-sessions/{id}/events?after=N` | Read reconnectable transcript events after sequence `N`. |
| `GET` | `/v1/agent-sessions/{id}/stream?after=N` | Follow backlog and live output as Server-Sent Events; `Last-Event-ID` is also accepted. |

Events have `seq`, `time`, `stream` (`stdout`, `stderr`, or `status`), and
`data`. Transcript data and stored command arguments pass through the Citadel's
secret scrubber before they are written or delivered. Under RBAC, session
metadata and transcripts are tenant-scoped. Persistence is capped at 64 MiB per
session; metadata sets `transcript_truncated` if that limit or a write failure is
reached while live delivery continues.

### Actions (governed)

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/citadels/{id}/shell/exec` | Run a shell command. |
| `POST` | `/v1/citadels/{id}/agent-sessions` | Start a streaming, persisted agent command. |
| `POST` | `/v1/citadels/{id}/code/python` | Run Python. |
| `POST` | `/v1/citadels/{id}/code/node` | Run Node. |
| `POST` | `/v1/citadels/{id}/file/read` | Read a file. |
| `POST` | `/v1/citadels/{id}/file/write` | Write a file. |
| `POST` | `/v1/citadels/{id}/file/list` | List files. |
| `POST` | `/v1/citadels/{id}/file/search` | Search files. |
| `POST` | `/v1/citadels/{id}/usage` | Report model usage. Body: `{"tokens":123,"cost_usd":0.04}`; accrues toward the Charter's `rationing.max_tokens`/`max_cost_usd` budget. |
| `GET` | `/v1/citadels/{id}/perimeter` | Egress (Perimeter) decision log for the Citadel. |
| `GET` | `/v1/citadels/{id}/terminal` | WebSocket terminal (same-origin only). |
| `POST` | `/v1/citadels/{id}/terminal-ticket` | Mint a short-lived terminal ticket. |
| `GET` | `/v1/citadels/{id}/ide` / `/ide/{path...}` | Experimental browser IDE reverse proxy (HTTP + WebSocket). |
| `POST` | `/v1/citadels/{id}/ide-ticket` | Mint a short-lived IDE ticket. |

### Browser IDE (experimental)

Opt-in GUI against `/workspace` via in-cell code-server. Requires
`RUNEWARD_ENABLE_EXPERIMENTAL_IDE=1`, Charter `[ide] enabled = true`, and an
IDE-capable target (`ide` or `ide-agents`) from `deploy/Dockerfile.ide`. Mint
`kind=ide` via `POST /v1/tickets` (or `/ide-ticket`), then open
`/v1/citadels/{id}/ide?ticket=…`. The ticket is single-use; a session cookie
covers subsequent asset loads. List/get may include `ide` / `ide_agents`.

**Limits (summary):** not per-keystroke policy; no Cursor/Claude Desktop/Codex
GUIs in-cell; no first-class GitHub Copilot (Open VSX). Full write-up:
[Browser IDE](browser-ide.md).

## Error contract

Non-governance failures include a stable `code`: `authentication_required`,
`authz_denied`, `not_found`, `conflict`, `rate_limited`, `invalid_request`, or
`internal_error`. Policy decisions remain `{"verdict":"deny",...}` and approval
pauses remain `{"verdict":"require-approval","approval_id":"..."}`. SDKs expose
authorization and policy denials as different exception types.

### Browser

Browser automation is experimental and disabled by default. Set
`RUNEWARD_ENABLE_EXPERIMENTAL_BROWSER=1` only in a trusted deployment after
reviewing the [security model](security-model.md).

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/citadels/{id}/browser` | One-shot browser action. |
| `POST` | `/v1/citadels/{id}/browser/sessions` | Open a stateful session. |
| `POST` | `/v1/citadels/{id}/browser/sessions/{sid}/act` | Act in a session. |
| `DELETE` | `/v1/citadels/{id}/browser/sessions/{sid}` | Close a session. |

## Cohorts

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/cohorts` | Create a Cohort. |
| `GET` | `/v1/cohorts` | List Cohorts. |
| `GET` | `/v1/cohorts/{id}` | Get a Cohort. |
| `DELETE` | `/v1/cohorts/{id}` | Tear down a Cohort. |
| `GET` | `/v1/cohorts/{id}/tasks` | List tasks. |
| `POST` | `/v1/cohorts/{id}/tasks` | Add a task. |
| `POST` | `/v1/cohorts/{id}/claim` | Claim the next task; returns a signed `lease_token`. |
| `POST` | `/v1/cohorts/{id}/tasks/{taskID}/complete` | Mark complete; requires `lease_token`. |
| `POST` | `/v1/cohorts/{id}/tasks/{taskID}/fail` | Mark failed; requires `lease_token`. |
| `POST` | `/v1/cohorts/{id}/tasks/{taskID}/heartbeat` | Renew the lease; requires the current token and returns a refreshed token. |

## Snapshots

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/citadels/{id}/snapshot` | Snapshot a workspace. |
| `GET` | `/v1/snapshots` | List snapshots (tenant-scoped under RBAC). |
| `POST` | `/v1/snapshots/{id}/restore` | Restore a snapshot. |

## Chronicle

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/citadels/{id}/chronicle` | Chronicle (audit) events for a Citadel. |
| `GET` | `/v1/chronicle/verify` | Verify the on-disk hash chain. |
| `GET` | `/v1/chronicle/pubkey` | The ledger's ed25519 public key. |
| `GET` | `/v1/chronicle/export` | Export a signed transcript bundle. |

## Conclave

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/conclave` | Pending approvals. |
| `POST` | `/v1/conclave/{id}/approve` | Approve a paused action. |
| `POST` | `/v1/conclave/{id}/deny` | Deny a paused action. |

## Example

```bash
AUTH=(-H "Authorization: Bearer $RUNEWARD_API_TOKEN")   # omit when serving without a token: AUTH=()
SB=$(curl -s "${AUTH[@]}" -X POST localhost:8080/v1/citadels -d '{"profile":"ns-auto"}' | jq -r .id)
curl -s "${AUTH[@]}" -X POST "localhost:8080/v1/citadels/$SB/shell/exec" -d '{"command":["echo","hi"]}'
curl -s "${AUTH[@]}" -X POST "localhost:8080/v1/citadels/$SB/usage" -d '{"tokens":1200,"cost_usd":0.03}'
curl -s "${AUTH[@]}" "localhost:8080/v1/chronicle/verify"
curl -s "${AUTH[@]}" -X DELETE "localhost:8080/v1/citadels/$SB"
```
