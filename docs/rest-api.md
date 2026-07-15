# REST API

`runeward serve` exposes the control plane over HTTP (default `127.0.0.1:8080`).
All actions flow through the same governed path as every other surface.

## Authentication

`serve` binds `127.0.0.1` by default and refuses a non-loopback `--bind` unless
authentication is configured. Set a bearer token with `--token` /
`RUNEWARD_API_TOKEN`, or point `RUNEWARD_AUTHZ_FILE` at a JSON store of named
principals (each with its own token, an allowed-profile glob list, and
approval/admin flags) for multi-principal RBAC. When set, the token is required
on every request except `/healthz` and the static dashboard shell — pass it as
`Authorization: Bearer <token>`, an `X-Runeward-Token` header, or a `?token=`
query param (the last is required for the terminal WebSocket). Optional TLS via
`--tls-cert`/`--tls-key`; request bodies are capped at 16 MiB. See the
[Security model](security-model.md).

Under RBAC, a non-admin principal sees and can act on only the Citadels it
created; admins see all.

## Health & identity

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness probe (unauthenticated). |
| `GET` | `/v1/whoami` | The authenticated caller's identity and capabilities (name, admin, can_approve, allowed_profiles). |
| `GET` | `/metrics` | Prometheus metrics. |

## Charters

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/charters` | List reachable Charters. |

## Citadels

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/citadels` | Create a Citadel. Body: `{"profile":"...","copy_from":"..."}` (`copy_from` optional). |
| `GET` | `/v1/citadels` | List Citadels (scoped to the caller under RBAC). |
| `GET` | `/v1/citadels/{id}` | Get one Citadel (includes `owner` and cumulative `usage`). |
| `DELETE` | `/v1/citadels/{id}` | Kill and remove a Citadel. |

### Actions (governed)

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/citadels/{id}/shell/exec` | Run a shell command. |
| `POST` | `/v1/citadels/{id}/code/python` | Run Python. |
| `POST` | `/v1/citadels/{id}/code/node` | Run Node. |
| `POST` | `/v1/citadels/{id}/file/read` | Read a file. |
| `POST` | `/v1/citadels/{id}/file/write` | Write a file. |
| `POST` | `/v1/citadels/{id}/file/list` | List files. |
| `POST` | `/v1/citadels/{id}/file/search` | Search files. |
| `POST` | `/v1/citadels/{id}/usage` | Report model usage. Body: `{"tokens":123,"cost_usd":0.04}`; accrues toward the Charter's `rationing.max_tokens`/`max_cost_usd` budget. |
| `GET` | `/v1/citadels/{id}/perimeter` | Egress (Perimeter) decision log for the Citadel. |
| `GET` | `/v1/citadels/{id}/terminal` | WebSocket terminal (same-origin only). |

### Browser

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
| `POST` | `/v1/cohorts/{id}/claim` | Claim the next task (lease). |
| `POST` | `/v1/cohorts/{id}/tasks/{taskID}/complete` | Mark complete. |
| `POST` | `/v1/cohorts/{id}/tasks/{taskID}/fail` | Mark failed. |
| `POST` | `/v1/cohorts/{id}/tasks/{taskID}/heartbeat` | Renew the lease. |

## Snapshots

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/citadels/{id}/snapshot` | Snapshot a workspace. |
| `GET` | `/v1/snapshots` | List snapshots. |
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
