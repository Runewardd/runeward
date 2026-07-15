<p align="center">
  <img src="docs/assets/runeward-banner.png" alt="runeward" width="720" />
</p>

<p align="center">
  <b>Governed execution cells for AI agents.</b>
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue.svg"></a>
  <a href="https://github.com/Runewardd/runeward/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Runewardd/runeward/actions/workflows/ci.yml/badge.svg"></a>
  <a href="go.mod"><img alt="Go" src="https://img.shields.io/badge/go-1.25%2B-00ADD8.svg"></a>
  <a href="https://github.com/Runewardd/runeward/releases"><img alt="Release" src="https://img.shields.io/github/v/release/Runewardd/runeward?sort=semver"></a>
</p>

<p align="center">
  Declarative Charters provision isolated Citadels (Docker or Kubernetes) with a deny-by-default
  Perimeter, a tamper-evident Chronicle, human-in-the-loop policy gates, and cost/loop Rationing,
  driven over REST, MCP, a CLI, and a web dashboard.
</p>

> **Product vocabulary.** runeward uses a desert-governance vocabulary across the
> product: a **Citadel** (sandbox/cell), a **Cohort** (fleet), a **Charter**
> (profile), the **Conclave** (approvals), the **Chronicle** (audit ledger), the
> **Perimeter** (egress controls), and **Rationing** (resource guardrails). These
> names are used by the dashboard, CLI commands (`runeward cohort`, `runeward
> chronicle`, `runeward charter`, `runeward archive`), and REST/MCP identifiers
> (`/v1/citadels`, `runeward_create_citadel`) alike. The `runeward` binary name,
> the SDK client method names, and JSON request/response fields (e.g. `profile`)
> keep their original spellings. See
> [Concepts](docs/concepts.md#product-vocabulary) for the complete glossary and
> developer gotchas.

## Why runeward

Letting an AI agent run shell commands, edit files, install packages, and hit the network is useful
right up until it `rm -rf`s the wrong directory, exfiltrates a secret, or burns your API budget in a
retry loop. Raw isolation ("jail the agent in a box") is table stakes. runeward adds the governance
layer *around* the box that most sandboxes lack. Think of it as a seatbelt and flight recorder for
autonomous agents.

The core idea: **don't rely on training or prompting the model to behave — enforce the rules outside
it, in a deny-by-default contract the agent can't talk its way past.** So the agent never has to
*know* it's about to break a rule; the enforcement layer knows and refuses. That also fixes the
scariest failure mode — a control the operator *forgot* to ask for — because anything the Charter
didn't grant is already denied. See [Why governance, not training](https://runewardd.github.io/runeward/why-governance/).

- **Charters are a security contract.** `[host]`, `[network]`, `[[env]]`, `[[file]]`, `[[policy]]`,
  and `[rationing]` declare exactly the access a task needs. Everything you didn't grant is denied by
  default, so the blast radius is explicit.
- **Governed, not just isolated.** Every action flows through one path (policy, the Conclave gate,
  Rationing, backend exec, the Chronicle) whether it arrives via REST, the dashboard, or MCP.
- **Tamper-evident by construction.** An append-only, hash-chained, ed25519-signed ledger records
  every call and its verdict, and exports as an independently verifiable transcript.
- **Human-in-the-loop where it matters.** Per-action `allow` / `deny` / `require-approval` verdicts
  pause risky operations for an operator instead of guessing.
- **Cost and loop Rationing.** Hard caps on wall-clock, exec count, egress requests, and
  token/spend budgets, plus retry-loop detection, stop runaway agents.
- **Authenticated, multi-user control plane.** `serve` binds loopback by default and requires a
  bearer token before it will listen on a public interface; optional multi-principal RBAC scopes each
  token to specific Charters and approval rights, and the dashboard has an interactive login with
  per-user Citadel views.
- **Pluggable backends.** A Docker/Podman backend for zero-setup laptop use, or a Kubernetes backend
  (with strict L3 egress, CRDs, an admission webhook, and PSA + NetworkPolicy multi-tenancy) for
  production and Cohorts. Everything above the backend is identical.
- **Isolated workspaces from your real code.** `copy_from` seeds a Citadel with a copy of a local
  folder (never a mount), and `runeward export` pulls results back out, so the agent works on your
  project without ever touching the original.

### How it compares

|                                    | typical agent sandbox | runeward                                     |
| ---------------------------------- | --------------------- | -------------------------------------------- |
| Isolation (container/VM)           | yes                   | yes (Docker or Kubernetes)                   |
| Deny-by-default network egress     | sometimes             | yes; SNI allowlist, strict L3 on k8s         |
| Per-action policy + approvals      | rare                  | yes; builtin / CEL / OPA-Rego + HITL gates   |
| Tamper-evident, signed audit trail | rare                  | yes; hash-chained + ed25519, verifiable      |
| Cost / loop guardrails             | rare                  | yes; wall-clock, exec, egress, token/spend   |
| Multi-agent Cohorts                | rare                  | yes; N cells + atomic Command Board          |
| Control-plane auth + multi-user    | rare                  | yes; bearer token + RBAC + per-user views    |
| Agent-native surface               | partial               | REST + MCP + CLI + dashboard + SKILL/adapters|

## How runeward fits your agent stack

There are two ways to put an agent behind runeward:

1. **As an MCP server for your IDE/agent (Cursor, Claude Desktop, VS Code).** Point the tool at
   `runeward mcp`; its agent then runs shell/code/file/browser tools inside a governed Citadel
   instead of on your host. Isolation, policy, the Perimeter, and the Chronicle apply to everything it does.
2. **By running an agent CLI inside the Citadel (Codex, Cursor CLI, Claude Code).** A Charter ships
   the agent binary in the image and injects its API key; you launch the agent with a single governed
   exec call (or a whole Cohort of them). See [Running agents and Cohorts](#running-agents-and-cohorts).

## Quick start

Install the latest release (macOS/Linux/Windows CLI, amd64/arm64):

```bash
curl -fsSL https://raw.githubusercontent.com/Runewardd/runeward/main/install.sh | sh
```

Or with Homebrew (once the tap is published):

```bash
brew install Runewardd/tap/runeward
```

<!-- TODO: add a short demo at docs/assets/demo.gif and embed it here -->

Prefer to build from source:

```bash
# Build the single binary
go build -o bin/runeward ./cmd/runeward

# Inspect a Charter's resolved, secret-redacted policy before using it
./bin/runeward --config-dir examples print ns-auto

# List reachable Charters
./bin/runeward --config-dir examples list

# Step into a Citadel interactively (needs Docker/OrbStack running)
./bin/runeward --config-dir examples dev

# Run a single command in a fresh Citadel, then tear it down
./bin/runeward --config-dir examples dev -- uname -a

# Start the governed control plane (REST API + web dashboard) on :8080
./bin/runeward --config-dir examples serve
```

Open [http://localhost:8080](http://localhost:8080) for the dashboard: pick a Charter, click **New**
(optionally point it at a local folder to copy in), and drive the Citadel's terminal, files, shell,
Chronicle timeline, and Conclave inbox.

## Working against your own code

runeward never mounts your host folder into the Citadel. Instead it takes a one-time copy at create,
so the agent works on an isolated `/workspace` and your real files are never modified. There are
three ways to seed it.

**1. In a Charter** with `host.copy_from`:

```toml
[host]
type      = "container"
image     = "runeward-agent:dev"
workdir   = "/workspace"
copy_from = "~/Documents/my-project"   # contents copied into /workspace at create
```

**2. Per-create over REST or the dashboard**, overriding it for a single Citadel:

```bash
curl -sX POST localhost:8080/v1/citadels \
  -d '{"profile":"codex-agent","copy_from":"~/Documents/my-project"}'
```

The dashboard's New dialog has an optional "Copy local folder into workspace" field.

**3. Pull results back out** to a host directory (the Citadel is only read; later host edits never
flow back in):

```bash
./bin/runeward export <citadel-id> ./agent-output      # works for Docker and Kubernetes cells
```

Snapshots (`POST /v1/citadels/{id}/snapshot`) are the other way to preserve or fork a workspace.

## Running agents and Cohorts

Build the agent image once (it ships `codex`, the Cursor CLI (`agent`), Claude Code (`claude`), git,
python, node, and `tar`):

```bash
docker build -f deploy/Dockerfile.agent -t runeward-agent:dev .
```

### Codex (OpenAI)

```bash
printf '%s' "$OPENAI_API_KEY" > ~/.runeward-openai.key   # key read at launch, redacted from ledger
./bin/runeward --config-dir examples serve

# create the governed cell, then run the agent inside it
SB=$(curl -sX POST localhost:8080/v1/citadels -d '{"profile":"codex-agent"}' | jq -r .id)
curl -sX POST localhost:8080/v1/citadels/$SB/shell/exec -d '{"command":["sh","-lc",
  "codex exec --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox '\''write fib.py and run it'\''"]}'
```

`--dangerously-bypass-approvals-and-sandbox` is correct here: runeward is the external Citadel.
`codex exec` authenticates from `CODEX_API_KEY`, so [examples/codex-agent.toml](examples/codex-agent.toml)
injects both `CODEX_API_KEY` and `OPENAI_API_KEY` from the same key file. Only `api.openai.com` is
reachable; everything else is denied.

### Cursor CLI

```bash
printf '%s' "$CURSOR_API_KEY" > ~/.runeward-cursor.key
SB=$(curl -sX POST localhost:8080/v1/citadels -d '{"profile":"cursor-agent"}' | jq -r .id)
curl -sX POST localhost:8080/v1/citadels/$SB/shell/exec -d '{"command":["sh","-lc",
  "agent -p '\''write fib.py and run it'\'' --force --trust --output-format text"]}'
```

See [examples/cursor-agent.toml](examples/cursor-agent.toml). The Cursor CLI is Node-based, so the
Charter sets `NODE_USE_ENV_PROXY=1` to route it through runeward's cooperative Docker egress proxy;
only Cursor's endpoints are allowed.

### Claude / Cursor / VS Code (via MCP)

For IDE agents, expose runeward's governed tools over MCP and let the IDE drive Citadels:

```jsonc
// e.g. Cursor .cursor/mcp.json or Claude Desktop config
{ "mcpServers": { "runeward": { "command": "runeward", "args": ["mcp", "--config-dir", "examples"] } } }
```

The agent then calls `runeward_create_citadel` (which accepts an optional `copy_from`),
`runeward_shell`, `runeward_python`, `runeward_read_file`, `runeward_browser_*`, and so on, all
governed.

### Cohorts (many agents, one Command Board)

A Cohort is N identical governed cells sharing an atomic, concurrency-safe Command Board. Set the replica
count and per-agent model/prompt in the Charter ([examples/codex-fleet.toml](examples/codex-fleet.toml),
[examples/cursor-fleet.toml](examples/cursor-fleet.toml)):

```toml
[cohort]
replicas = 3
```

```bash
FL=$(curl -sX POST localhost:8080/v1/cohorts -d '{"profile":"codex-fleet"}' | jq -r .id)
curl -sX POST localhost:8080/v1/cohorts/$FL/tasks  -d '{"payload":"refactor module A"}'
curl -sX POST localhost:8080/v1/cohorts/$FL/claim  -d '{"owner":"w1"}'   # atomic claim by a worker
```

Dead workers' claims auto-requeue via leases, and the board survives restarts. Pin the model per
agent by baking it into the Charter's launch command (e.g. `codex exec -m o3` or `agent --model ...`).

**Coordination model.** Agents do not talk to each other directly. Each Citadel is isolated (its own
container/pod, workspace, and deny-by-default egress), so one agent cannot reach another unless you
explicitly allowlist it. Instead, workers in a Cohort coordinate through the shared Command Board via the
control plane (claim / complete / fail), which keeps every interaction atomic and audited. Separate
Cohorts each have their own board and are fully isolated from one another.

### Prompt-driven Cohorts: any agent, multiple keys, local LLMs

[examples/build-fleet.toml](examples/build-fleet.toml) (Docker) and
[examples/build-fleet-k8s.toml](examples/build-fleet-k8s.toml) (Kubernetes) are prompt-driven Cohorts:
the board starts empty and you push prompts at runtime. Both wire in keys for all three agents at
once and `serve` simply skips any key file that doesn't exist, so you keep only the key(s) you use.
The `runeward cohort` subcommand drives either one against a running `serve`; pick the agent with
`--agent cursor|codex|claude` and the model with `--model` (both also read `$AGENT` / `$MODEL`):

```bash
printf '%s' "$ANTHROPIC_API_KEY" > ~/.runeward-anthropic.key   # plus openai/cursor keys as needed
./bin/runeward --config-dir examples serve

./bin/runeward cohort --agent claude --model sonnet build "Build a FastAPI todo API with tests"
```

Two ways to work. Each worker has its own `/workspace`, so it matters whether follow-ups land on the
same one.

**A) Iterate on one app** (same workspace accumulates) with `exec`, which pins to a single Citadel.
This is the "pass a prompt, then keep adding prompts/changes" flow:

```bash
runeward cohort --agent claude --model sonnet up
runeward cohort exec "Build a FastAPI todo API in app/ with SQLite and pytest"
runeward cohort exec "Now add a PUT /todos/{id} endpoint and tests for it"   # same code
runeward cohort exec "Add a Dockerfile and a README"
runeward cohort export ./out                                                 # pull results to the host
```

Switch agent/model any time via the flags, e.g.
`runeward cohort --agent codex --model gpt-5-codex exec "refactor the routes"`.

**B) Fan out independent pieces** across the Cohort (parallel) with `add` + `run`; each prompt goes to
whichever worker is free, in its own workspace:

```bash
runeward cohort --agent codex up
runeward cohort add "Build the auth module in auth/ with tests"
runeward cohort add "Build the billing module in billing/ with tests"
runeward cohort add "Build the CLI in cmd/ with tests"
runeward cohort run          # all workers build in parallel
runeward cohort export ./out # out/<citadel-id>/... — assemble the pieces
```

For Kubernetes, the same command drives `build-fleet-k8s` unchanged (`runeward cohort up build-fleet-k8s`);
`serve` reads the key files locally and injects them into every Pod. A dependency-free bash equivalent
lives at [examples/fleet.sh](examples/fleet.sh) if you'd rather script it with `curl`/`jq`.

**Local LLMs.** runeward isn't tied to those cloud CLIs; it governs whatever command you exec, so any
local-model runner works too. Point Codex (or `aider`) at an OpenAI-compatible endpoint via
`OPENAI_BASE_URL` and allow just that host: on Docker that's `host.docker.internal` (Ollama, LM
Studio, vLLM, llama.cpp); on Kubernetes, run the model as an in-cluster Service and allow its DNS
name, or run it *inside* the pod with `network.default = "deny"` and no allow rules for a fully
air-gapped, still-audited Cohort. (Cursor's `agent` is cloud-bound and Claude Code is Anthropic-bound;
Codex and generic CLIs are the local-friendly paths.)

## Charters

Charters may be authored in TOML, YAML, or JSON; the file extension picks the parser and all three
share the same schema (TOML is shown throughout these docs). `runeward <name>` resolves
`<name>.{toml,yaml,yml,json}` in order:

1. `./.runeward/<name>.*`, project-local and committed with the repo.
2. `~/.config/runeward/<name>.*` (or `$XDG_CONFIG_HOME/runeward/`).

`--config-dir DIR` (or `$RUNEWARD_CONFIG_DIR`) pins the search to a single directory, used for the
sanitized templates in [examples/](examples/). See [examples/ns-auto.toml](examples/ns-auto.toml) for
a fully worked deny-by-default Charter.

Secrets in `[[env]]` are resolved fresh at launch — inline `value`, `file`, or a reference URI:
`op://` (1Password), `env://`, `vault://` (HashiCorp Vault), `aws://` (AWS Secrets Manager), or
`gcp://` (GCP Secret Manager). They are injected into the session, redacted from the Chronicle, and
never written under `$HOME`. Resolution is fail-closed: a reference that can't be resolved aborts
Citadel creation rather than starting without the secret.

## CLI

```
runeward <charter> [-- cmd...]       Provision a Citadel for a Charter and enter it (alias for enter)
runeward enter <charter>             Same, explicit; --keep leaves the Citadel running
runeward cohort <up|add|run|build|exec|status|export|down>   Drive a prompt-driven Cohort (--agent/--model)
runeward export <id> <dir>           Copy a Citadel's /workspace back out to a host directory
runeward print <charter>             Show the resolved, secret-redacted Charter + policy
runeward list                        List reachable Charters
runeward validate <charter>          Statically lint a Charter (missing images, unresolved secrets, dead rules)
runeward policy {test,scaffold}      Simulate a Charter's policy, or print a ready-made policy template
runeward charter {sign,verify}       Produce/verify a detached ed25519 signature over a Charter
runeward runtime {check,guide,install}  Inspect, explain, or install hardened runtimes (gVisor/Kata)
runeward replay <cast>               Replay a recorded terminal session (asciinema v2)
runeward serve [--token ...]         Governed control plane: REST API + web dashboard (127.0.0.1:8080)
runeward mcp [--http]                Model Context Protocol server (stdio, or streamable HTTP)
runeward up [--crds-only]            Install CRDs + namespace + RBAC + controller into k8s
runeward controller                  Reconcile Citadel/Cohort CRDs onto the k8s backend
runeward webhook                     Self-registering admission webhook for ClusterPolicy
runeward chronicle verify                Verify the hash chain + signatures of the Chronicle
runeward archive {keygen,push,pull}   Build/publish/verify signed OCI policy Archives
```

## Control plane (REST)

`runeward serve` routes every tool call through policy, the Conclave gate, Rationing, backend exec, and
the Chronicle, whether it arrives over REST, the dashboard, or MCP. It binds `127.0.0.1` by
default; set a bearer token with `--token` / `$RUNEWARD_API_TOKEN` (or per-principal RBAC via
`$RUNEWARD_AUTHZ_FILE`) before binding a public interface, and pass it as `Authorization: Bearer
<token>` on every request except `/healthz` and the dashboard shell.

```
GET      /healthz
GET      /metrics                           # Prometheus metrics (token-gated when auth is on)
GET      /v1/whoami                         # the caller's identity + capabilities
GET      /v1/charters
POST     /v1/citadels                      {"profile":"dev","copy_from":"~/proj"}   # copy_from optional
GET      /v1/citadels   ·  GET|DELETE /v1/citadels/{id}   # scoped to the caller under RBAC
POST     /v1/citadels/{id}/shell/exec      {"command":["ls","-la"]}
POST     /v1/citadels/{id}/code/python     {"code":"print(2+2)"}
POST     /v1/citadels/{id}/code/node       {"code":"..."}
POST     /v1/citadels/{id}/file/{read,write,list,search}
POST     /v1/citadels/{id}/usage           {"tokens":1200,"cost_usd":0.03}   # accrues toward the budget
POST     /v1/citadels/{id}/snapshot        {"name":"before-refactor"}
GET      /v1/snapshots   ·  POST /v1/snapshots/{id}/restore
POST     /v1/cohorts                         {"profile":"fleet-demo"}   # N cells + shared Command Board
GET      /v1/cohorts   ·  GET|DELETE /v1/cohorts/{id}
GET|POST /v1/cohorts/{id}/tasks             # list / add tasks
POST     /v1/cohorts/{id}/claim             {"owner":"w1"}             # atomic claim
POST     /v1/cohorts/{id}/tasks/{tid}/{complete,fail}
GET      /v1/citadels/{id}/chronicle          # this Citadel's Chronicle events
GET      /v1/citadels/{id}/terminal       # interactive PTY over WebSocket (token via ?token=)
GET      /v1/chronicle/verify                  # verify the hash chain + signatures
GET      /v1/conclave   ·  POST /v1/conclave/{id}/{approve,deny}   # approver identity recorded
POST     /mcp                              # Model Context Protocol (streamable HTTP)
```

A denied tool call returns `403`; a require-approval call blocks until an operator resolves it via
the Conclave inbox (returning `202` with an `approval_id` if it waits too long). Once a Citadel's
reported usage exceeds its Charter's `rationing.max_tokens`/`max_cost_usd`, further calls are denied
fail-closed. Pin the Chronicle and signing-key location with `$RUNEWARD_STATE_DIR`.

## Observability

`serve` and `controller` are built to run as services:

- **Metrics.** Prometheus exposition at `GET /metrics` — `runeward_actions_total{tool,verdict}`,
  `runeward_actions_duration_seconds{tool}`, `runeward_sandboxes_created_total`,
  `runeward_usage_tokens_total{profile}`, `runeward_usage_cost_usd_total{profile}`, and
  `runeward_build_info{version}`, plus the standard Go/process collectors.
- **Structured logs.** Both services log via `log/slog`. Set `RUNEWARD_LOG_FORMAT=json` for
  aggregators and `RUNEWARD_LOG_LEVEL=debug|info|warn|error` to tune verbosity.
- **Telemetry is opt-in and off by default.** It only activates when you set both
  `RUNEWARD_TELEMETRY=1` and `RUNEWARD_TELEMETRY_ENDPOINT` (and never when `DO_NOT_TRACK` is set).
  Events carry only version/os/arch — no hostnames, paths, IDs, or Charter contents.

See [docs/observability](https://runewardd.github.io/runeward/observability/) for details.

Releases are signed with keyless [cosign](https://docs.sigstore.dev/) and ship SBOMs; see
[Verifying release artifacts](https://runewardd.github.io/runeward/install/#verifying-release-artifacts).

## MCP

`runeward mcp` exposes the same governed tools over the Model Context Protocol: stdio by default
(Claude Desktop / Cursor / VS Code), or `--http` for the streamable transport (also mounted at `/mcp`
by `runeward serve`).

- **Citadel tools:** `runeward_create_citadel` (accepts `copy_from`), `runeward_shell`,
  `runeward_browser`, `runeward_browser_open`, `runeward_browser_act`, `runeward_browser_close`,
  `runeward_python`, `runeward_node`, `runeward_read_file`, `runeward_write_file`,
  `runeward_list_files`, `runeward_search_files`, `runeward_list_conclave`, `runeward_kill_citadel`.
- **Cohort tools:** `runeward_create_cohort`, `runeward_list_cohorts`, `runeward_list_tasks`,
  `runeward_add_task`, `runeward_claim_task`, `runeward_complete_task`, `runeward_fail_task`,
  `runeward_kill_cohort`.

A policy deny surfaces as a tool error; a require-approval verdict tells the agent to pause for a human.

## Adapters

Adapters make runeward's governed tools a first-class citizen in the agent frameworks you already
use: import a small client (or a ready-made set of framework "tools") and hand them to your agent.
The agent calls `runeward_shell`, `runeward_python`, `runeward_read_file`, … like any other tool, but
every call flows through the same governed path (policy → Conclave → Rationing → Citadel → Chronicle).
Each returns one of three verdicts — `allow`, `deny` ("don't retry"), or `require-approval` ("pause
for a human") — surfaced as typed errors on the raw client and as model-readable strings on the
framework tools. See [docs/adapters](https://runewardd.github.io/runeward/adapters/) and
[`adapters/`](adapters/).

**Python (`runeward`)** — pure-stdlib client; framework tools are lazy-loaded optional extras for
LangChain, CrewAI, LlamaIndex, the OpenAI Agents SDK, and Strands:

```bash
pip install runeward                    # core client only (no third-party deps)
pip install "runeward[strands]"         # a framework extra (also: langchain, crewai, llamaindex, openai-agents)
```

```python
from runeward import RunewardClient
from runeward.strands_tools import make_runeward_tools   # or langchain_/crewai_/llamaindex_/openai_agents_tools

tools = make_runeward_tools(RunewardClient("http://localhost:8080"))
# hand `tools` to your Strands / LangChain / CrewAI / LlamaIndex / OpenAI-Agents agent
```

**TypeScript (`@runeward/sdk`)** — `fetch`-based client with no runtime deps; tools use optional peers
for the Vercel AI SDK, LangChain.js, and Strands:

```bash
npm install @runeward/sdk ai zod                    # Vercel AI SDK tools
npm install @runeward/sdk @langchain/core zod       # LangChain.js tools
npm install @runeward/sdk @strands-agents/sdk zod   # Strands Agents SDK tools
```

```ts
import { RunewardClient } from "@runeward/sdk";
import { makeRunewardTools } from "@runeward/sdk/strands-tools";   // or "/ai-tools", "/langchain-tools"

const tools = await makeRunewardTools(new RunewardClient());
```

Already on an **MCP client** (Claude Desktop, Cursor, VS Code)? You don't need an adapter — point it
at `runeward mcp` (see [MCP](#mcp) above).

## Kubernetes

One command installs the CRDs and the controller into the current cluster (idempotent, server-side
apply, using your kubeconfig):

```bash
go build -o bin/runeward ./cmd/runeward
docker build -f deploy/Dockerfile -t runeward:latest .   # shared with OrbStack/Docker Desktop k8s
./bin/runeward up                                         # CRDs + namespace + RBAC + controller
# or just the CRDs:  ./bin/runeward up --crds-only

# Provide Charters the controller can resolve
kubectl -n runeward create configmap runeward-profiles --from-file=examples/
```

Or via Helm:

```bash
helm install runeward deploy/helm/runeward -n runeward --create-namespace \
  --set image.tag=latest --set server.enabled=true
```

Then drive it declaratively:

```yaml
apiVersion: runeward.dev/v1alpha1
kind: Citadel
metadata: { name: demo, namespace: runeward }
spec: { profile: k8s }
---
apiVersion: runeward.dev/v1alpha1
kind: Cohort
metadata: { name: crew, namespace: runeward }
spec: { profile: fleet-demo }
```

```bash
kubectl -n runeward get citadels,cohorts
```

The controller provisions the backing Pods/PVCs, populates `.status` (`citadelId`/`cohortId`, phase,
task stats), and tears everything down via a finalizer on delete. On k8s, egress can be enforced at
L3 (`enforce = "strict"`: an iptables init container plus a transparent SNI proxy) so it cannot be
bypassed by an uncooperative process.

For multi-tenancy, the managed namespace carries Pod Security Admission labels
(`RUNEWARD_K8S_PSA_ENFORCE`, or the chart's `podSecurityStandard`), Citadel containers always drop
`ALL` capabilities and disable privilege escalation, and an optional default-deny NetworkPolicy
(DNS-only egress) isolates Citadel pods (`RUNEWARD_K8S_NETWORK_POLICY`, or the chart's
`networkPolicy.enabled`). Pair this with a hardened `runtime_class` (gVisor/Kata) — install one with
`runeward runtime install` — for VM-grade isolation of untrusted workloads.

For org-shared cells that shouldn't live in a single team's namespace, use cluster-scoped
`ClusterCitadel` / `ClusterCohort` (same spec, no `namespace`); the same controller reconciles them
cluster-wide.

### Org-wide policy defaults

A cluster-scoped `ClusterPolicy` sets org-wide guardrails on `Citadel`/`Cohort` resources, enforced by
`runeward webhook` (a self-registering validating and mutating admission webhook that mints its own
serving cert):

```yaml
apiVersion: runeward.dev/v1alpha1
kind: ClusterPolicy
metadata: { name: org-defaults }
spec:
  allowedProfiles: ["k8s", "fleet-*"]   # globs; empty = any
  deniedProfiles: ["*-admin"]
  defaultProfile: "k8s"                  # mutating: fills empty spec.profile
  allowedNamespaces: ["team-*"]
  requiredLabels: ["owner"]
```

Enable it with the chart (`--set webhook.enabled=true`); the validating webhook is fail-closed
(`failurePolicy: Fail`) and denies resources that violate policy. The mutating defaulting path is
best-effort (`failurePolicy: Ignore`) for missing `spec.profile`.

## Policy engines

Authority is `allow` / `deny` / `require-approval` per action, chosen with `policy_engine`:

- `builtin` (default): first-match tool + glob rules (`[[policy]]`).
- `cel`: CEL expressions over `{tool, arg}` (`[[cel]]`); see `examples/`.
- `rego`: OPA/Rego module returning `data.runeward.decision` (`[rego]`); see
  [examples/rego.toml](examples/rego.toml).

Instead of embedding policy inline, a Charter can pull it from a signed, versioned OCI policy Archive,
so a security team ships one artifact many Charters consume:

```toml
[policy_bundle]
ref        = "oci://ghcr.io/acme/runeward-policies:v3"
verify_key = "<base64 ed25519 public key>"   # when set, a valid signature is REQUIRED (fail-closed)
```

```bash
runeward archive keygen --out ./keys
runeward archive push oci://ghcr.io/acme/runeward-policies:v3 \
    --policy prod.rego --engine rego --key ./keys/bundle.key
runeward archive pull oci://ghcr.io/acme/runeward-policies:v3 --verify-key ./keys/bundle.pub
```

The signature covers the content-addressed config and layer digests and rides in the OCI manifest
annotations; see [examples/policy-bundle.toml](examples/policy-bundle.toml).

## Testing

See [docs/E2E-TESTING.md](docs/E2E-TESTING.md) for an end-to-end local walkthrough covering the Docker
and Kubernetes backends, deny-by-default and strict egress, snapshots, multi-agent Cohorts, and wiring
the MCP server into Claude Desktop, Cursor, and VS Code.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for how to build,
test, and submit changes, and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community
expectations. Found a security issue? Please follow [SECURITY.md](SECURITY.md) and
report it privately.

## License

Licensed under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
