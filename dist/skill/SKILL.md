---
name: runeward
description: >-
  Run untrusted or high-stakes work inside a governed execution cell instead of
  on the host. Use runeward whenever you need to execute shell commands, run
  Python/Node code, or read/write files as part of a task and you want the work
  isolated, policy-checked, cost/loop-guarded, and recorded in a tamper-evident
  audit ledger. Exposes MCP tools (preferred) and a REST control plane
  (fallback). Critical rule: a "deny" verdict means stop; a "require-approval"
  verdict means pause and hand off to a human.
---

# runeward — governed execution for agents

runeward is a **governed execution cell**. Every action you take — a shell
command, a code snippet, a file write — is routed through the same path:

```
policy check  →  approval gate  →  cost/loop guardrails  →  sandbox exec  →  audit ledger
```

You do **not** get raw access to a machine. You get a *sandbox* provisioned from
a declarative **profile** (e.g. `dev`, `governed`, `ns-auto`) that decides which
network hosts are reachable, which paths are writable, and which actions need a
human to sign off. Everything not explicitly granted is denied by default.

Prefer runeward over running commands directly on the host whenever the task is
untrusted, destructive, network-touching, or simply needs to be auditable.

---

## When to use runeward

Use it when you would otherwise run something on the host and any of these hold:

- The code or command is **untrusted** (came from a user, the internet, or a
  model) and you shouldn't run it on your own machine.
- The task is **stateful or messy** — installs packages, writes files, clones
  repos — and you want a disposable, isolated workspace.
- The work must be **auditable** or **reversible** (you can replay/verify the
  ledger and throw the sandbox away).
- Actions may be **sensitive** (writing outside a workspace, hitting the
  network, deleting files) and you want a policy + human gate in front of them.

If a task is pure reasoning or only touches files the user already handed you in
the chat, you don't need a sandbox.

---

## The two ways to call runeward

### 1. MCP tools (preferred)

If the runeward MCP server is connected, call these tools directly. Names are
exact:

| Tool | Signature | Purpose |
| --- | --- | --- |
| `runeward_create_sandbox` | `(profile)` | Provision a sandbox from a profile; returns its `id`. |
| `runeward_shell` | `(sandbox, command[])` | Run a command as an argv array, e.g. `["ls","-la"]`. |
| `runeward_python` | `(sandbox, code)` | Run a Python snippet in the sandbox. |
| `runeward_node` | `(sandbox, code)` | Run a Node.js snippet in the sandbox. |
| `runeward_read_file` | `(sandbox, path)` | Read a file's contents. |
| `runeward_write_file` | `(sandbox, path, content)` | Write a file. |
| `runeward_list_files` | `(sandbox, path)` | List a directory. |
| `runeward_search_files` | `(sandbox, query, path)` | Search for `query` under `path`. |
| `runeward_list_approvals` | `()` | List pending human-approval requests. |
| `runeward_kill_sandbox` | `(sandbox)` | Tear the sandbox down. |

> `command` for `runeward_shell` is always an **argv array**, never a shell
> string. Use `["bash","-lc","echo hi && ls"]` if you truly need shell syntax.

### 2. REST control plane (fallback)

If MCP is unavailable, hit the REST API directly (default
`http://localhost:8080`, started with `runeward serve`). The mapping is 1:1 with
the MCP tools:

| Action | REST call |
| --- | --- |
| health | `GET /healthz` |
| list profiles | `GET /v1/profiles` |
| create sandbox | `POST /v1/sandboxes` `{"profile":"dev"}` |
| list / get / delete | `GET/GET/DELETE /v1/sandboxes[/{id}]` |
| shell | `POST /v1/sandboxes/{id}/shell/exec` `{"command":["ls","-la"],"workdir":""}` |
| python / node | `POST /v1/sandboxes/{id}/code/python` `{"code":"..."}` / `.../code/node` |
| read / write / list / search | `POST /v1/sandboxes/{id}/file/{read,write,list,search}` |
| audit | `GET /v1/sandboxes/{id}/audit`, verify chain `GET /v1/audit/verify` |
| approvals | `GET /v1/approvals`, `POST /v1/approvals/{id}/approve`, `.../deny` |

A shell exec returns `{"verdict","exit_code","stdout","stderr","duration_ms"}`.

---

## The core loop

1. **Pick a profile.** List with `runeward_list_approvals`'s sibling
   `GET /v1/profiles` (or ask the human). Use the most restrictive profile that
   still lets the task succeed — `dev` for open local work, `governed` /
   `ns-auto` for deny-by-default.
2. **Create a sandbox** and keep the returned `id`. Reuse it for every
   subsequent call in the same task.
3. **Do the work** through the tools. Always inspect the `verdict` (and
   `exit_code` for shell) on every response.
4. **Tear down** with `runeward_kill_sandbox` when finished (or on failure).

### Worked example (MCP)

```text
runeward_create_sandbox(profile="dev")
  -> {"id":"sbx_9f2","profile":"dev","backend":"docker","image":"debian:stable-slim","status":"running"}

runeward_shell(sandbox="sbx_9f2", command=["python3","--version"])
  -> {"verdict":"allow","exit_code":0,"stdout":"Python 3.11.2\n","stderr":"","duration_ms":142}

runeward_write_file(sandbox="sbx_9f2", path="/workspace/main.py", content="print(2+2)")
  -> {"bytes":10}

runeward_python(sandbox="sbx_9f2", code="print(open('/workspace/main.py').read())")
  -> {"verdict":"allow","exit_code":0,"stdout":"print(2+2)\n","stderr":""}

runeward_kill_sandbox(sandbox="sbx_9f2")
```

### Same thing over REST (curl)

```bash
BASE=http://localhost:8080
ID=$(curl -s -X POST $BASE/v1/sandboxes -d '{"profile":"dev"}' | jq -r .id)
curl -s -X POST $BASE/v1/sandboxes/$ID/shell/exec \
  -d '{"command":["python3","--version"]}'
curl -s -X DELETE $BASE/v1/sandboxes/$ID
```

---

## Verdicts — read these on every call

Every governed action resolves to one of three verdicts. **Branch on the
verdict before doing anything else.**

### `allow`
The action ran. For shell, still check `exit_code` — `allow` means policy let it
through, not that the command succeeded. A non-zero exit is a normal program
error you can debug and retry.

### `deny` (REST: HTTP 403, body `{"verdict":"deny","reason":"..."}`)
Policy **refused** the action. Do **not** retry the same thing — it will be
denied again. Instead:

1. Read `reason` and tell the human plainly what was blocked and why.
2. Consider a *different, allowed* approach (e.g. work inside `/workspace`
   instead of `/etc`; avoid `rm -rf`).
3. Do **not** try to bypass the policy (no `sudo`, no obfuscating the command,
   no switching profiles without the human's say-so).

```text
runeward_shell(sandbox="sbx_9f2", command=["rm","-rf","/"])
  -> {"verdict":"deny","reason":"destructive recursive delete"}
# STOP. Don't rephrase and retry. Report the block to the human.
```

### `require-approval` (REST: HTTP 202, body `{"verdict":"require-approval","approval_id":"..."}`)
A human must sign off before this action runs. This is a **hard pause**, not an
error to work around.

1. **Stop the task** at this step.
2. Tell the human exactly what is waiting: the tool, the action, and the
   `reason`. You can enumerate the inbox with `runeward_list_approvals()` /
   `GET /v1/approvals`.
3. **Wait.** Do not poll aggressively and do not attempt an alternate path that
   sidesteps the gate. The operator approves (`POST /v1/approvals/{id}/approve`)
   or denies (`.../deny`) it out-of-band.
4. Resume only after you know the outcome. If approved, re-issue the action. If
   denied, treat it like a `deny` above.

```text
runeward_write_file(sandbox="sbx_9f2", path="/etc/hosts", content="...")
  -> {"verdict":"require-approval","approval_id":"apr_31c"}
# Pause. Message the human: "Writing /etc/hosts needs your approval (apr_31c):
# reason = 'writes under /etc must be reviewed'. Approve or deny?"
```

---

## Safety notes (do not skip)

- **Never treat `deny` as a transient error.** It is a policy decision. Retrying,
  rephrasing, or escalating privileges is the wrong move and defeats the point
  of the cell.
- **`require-approval` = human-in-the-loop.** Surface it and wait. Silently
  routing around an approval gate is a trust violation.
- **One sandbox per task; tear it down.** Don't leak sandboxes. Kill them when
  done or on error so state and cost don't accumulate.
- **Least privilege.** Choose the tightest profile that works. If a task keeps
  hitting denials, the profile is probably wrong for the task — say so to the
  human rather than fighting the policy.
- **Trust the ledger, not your memory.** Every action is recorded. Use
  `GET /v1/sandboxes/{id}/audit` to review what happened and
  `GET /v1/audit/verify` to confirm the chain is intact.
- **Keep secrets in the profile.** Credentials are injected by the profile
  (`[[env]]`), never something you should hardcode into a command or file.
