# Fleets

A fleet is N governed sandboxes coordinated by an atomic task board. It's how you
run many agents in parallel (or iterate on one app) with the same isolation,
policy, egress, and audit guarantees as a single cell.

The `runeward fleet` command is a client to a running control plane
(`runeward serve`), so start that first.

## Subcommands

| Command | What it does |
| --- | --- |
| `runeward fleet up [profile]` | Create a fleet of workers from a profile. |
| `runeward fleet add <prompt>` | Queue a task (its own workspace when a worker picks it up). |
| `runeward fleet run` | Let idle workers claim and execute queued tasks in parallel. |
| `runeward fleet build <prompt>` | Shorthand: add a task and run it. |
| `runeward fleet exec <prompt>` | Run a prompt pinned to a single sandbox (accumulates in one workspace). |
| `runeward fleet status` | Show workers and task board state. |
| `runeward fleet export [dir]` | Copy the finished workspace(s) back to the host. |
| `runeward fleet down` | Tear the fleet down. |

Select the agent and model with environment variables, e.g. `AGENT=claude`,
`MODEL=sonnet`, `AGENT=codex MODEL=gpt-5-codex`, or `AGENT=cursor`.

## Two ways to work

Each worker has its own `/workspace` (fleet isolation), so it matters whether
follow-ups land on the same one.

### A) Iterate on one app

Use `exec`, which pins to a single sandbox — the "pass a prompt, then add more
prompts/changes" flow where the same code accumulates:

```bash
AGENT=claude MODEL=sonnet runeward fleet up
runeward fleet exec "Build a FastAPI todo API in app/ with SQLite and pytest"
runeward fleet exec "Now add a PUT /todos/{id} endpoint and tests"   # same code
runeward fleet exec "Add a Dockerfile and a README"
runeward fleet export ./out
```

Switch agent/model any time by changing the env vars.

### B) Fan-out independent pieces

Use `add` + `run`; each prompt goes to whichever worker is free, in its own
workspace, and they build in parallel:

```bash
AGENT=codex runeward fleet up
runeward fleet add "Build the auth module in auth/ with tests"
runeward fleet add "Build the billing module in billing/ with tests"
runeward fleet add "Build the CLI in cmd/ with tests"
runeward fleet run          # all workers build in parallel
runeward fleet export ./out
```

## Inter-agent coordination

Agents don't talk peer-to-peer. Coordination happens through the control plane's
**atomic task board**: workers claim tasks under a lease, heartbeat while working,
and mark completion. If a worker dies, its lease expires and the task is
reclaimed by another. This is the same on Docker and Kubernetes.

## Multiple keys and local LLMs

A fleet can mix providers (Cursor, Codex, Claude) and keys, and can target a
**local LLM** exposed over an OpenAI-compatible endpoint (e.g. Ollama, LM Studio)
by adding the endpoint to the profile's egress allowlist and pointing the agent's
base-URL env var at it. See
[`examples/build-fleet.toml`](https://github.com/Runewardd/runeward/blob/main/examples/build-fleet.toml)
and
[`examples/build-fleet-k8s.toml`](https://github.com/Runewardd/runeward/blob/main/examples/build-fleet-k8s.toml).
