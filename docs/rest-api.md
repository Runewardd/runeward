# REST API

`runeward serve` exposes the control plane over HTTP (default `:8080`). All
actions flow through the same governed path as every other surface.

!!! warning "Protect the control plane"
    The API has no built-in auth. Bind it to a trusted interface or place it
    behind your own auth/proxy in production. See the [Security model](security-model.md).

## Health

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness probe. |

## Profiles

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/profiles` | List reachable profiles. |

## Sandboxes

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/sandboxes` | Create a sandbox. Body: `{"profile":"...","copy_from":"..."}` (`copy_from` optional). |
| `GET` | `/v1/sandboxes` | List sandboxes. |
| `GET` | `/v1/sandboxes/{id}` | Get one sandbox. |
| `DELETE` | `/v1/sandboxes/{id}` | Kill and remove a sandbox. |

### Actions (governed)

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/sandboxes/{id}/shell/exec` | Run a shell command. |
| `POST` | `/v1/sandboxes/{id}/code/python` | Run Python. |
| `POST` | `/v1/sandboxes/{id}/code/node` | Run Node. |
| `POST` | `/v1/sandboxes/{id}/file/read` | Read a file. |
| `POST` | `/v1/sandboxes/{id}/file/write` | Write a file. |
| `POST` | `/v1/sandboxes/{id}/file/list` | List files. |
| `POST` | `/v1/sandboxes/{id}/file/search` | Search files. |
| `GET` | `/v1/sandboxes/{id}/terminal` | WebSocket terminal (same-origin only). |

### Browser

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/sandboxes/{id}/browser` | One-shot browser action. |
| `POST` | `/v1/sandboxes/{id}/browser/sessions` | Open a stateful session. |
| `POST` | `/v1/sandboxes/{id}/browser/sessions/{sid}/act` | Act in a session. |
| `DELETE` | `/v1/sandboxes/{id}/browser/sessions/{sid}` | Close a session. |

## Fleets

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/fleets` | Create a fleet. |
| `GET` | `/v1/fleets` | List fleets. |
| `GET` | `/v1/fleets/{id}` | Get a fleet. |
| `DELETE` | `/v1/fleets/{id}` | Tear down a fleet. |
| `GET` | `/v1/fleets/{id}/tasks` | List tasks. |
| `POST` | `/v1/fleets/{id}/tasks` | Add a task. |
| `POST` | `/v1/fleets/{id}/claim` | Claim the next task (lease). |
| `POST` | `/v1/fleets/{id}/tasks/{taskID}/complete` | Mark complete. |
| `POST` | `/v1/fleets/{id}/tasks/{taskID}/fail` | Mark failed. |
| `POST` | `/v1/fleets/{id}/tasks/{taskID}/heartbeat` | Renew the lease. |

## Snapshots

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/sandboxes/{id}/snapshot` | Snapshot a workspace. |
| `GET` | `/v1/snapshots` | List snapshots. |
| `POST` | `/v1/snapshots/{id}/restore` | Restore a snapshot. |

## Audit

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/sandboxes/{id}/audit` | Audit events for a sandbox. |
| `GET` | `/v1/audit/verify` | Verify the on-disk hash chain. |
| `GET` | `/v1/audit/pubkey` | The ledger's ed25519 public key. |
| `GET` | `/v1/audit/export` | Export a signed transcript bundle. |

## Approvals

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/approvals` | Pending approvals. |
| `POST` | `/v1/approvals/{id}/approve` | Approve a paused action. |
| `POST` | `/v1/approvals/{id}/deny` | Deny a paused action. |

## Example

```bash
SB=$(curl -sX POST localhost:8080/v1/sandboxes -d '{"profile":"ns-auto"}' | jq -r .id)
curl -sX POST "localhost:8080/v1/sandboxes/$SB/shell/exec" -d '{"cmd":"echo hi"}'
curl -s  "localhost:8080/v1/audit/verify"
curl -sX DELETE "localhost:8080/v1/sandboxes/$SB"
```
