<p align="center">
  <img src="docs/assets/runeward-banner-v2.png" alt="runeward — the agent governance harness" width="760" />
</p>

<p align="center">
  <b>The open-source governance harness for AI agents.</b>
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue.svg"></a>
  <a href="https://github.com/Runewardd/runeward/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Runewardd/runeward/actions/workflows/ci.yml/badge.svg"></a>
  <a href="go.mod"><img alt="Go 1.26.5" src="https://img.shields.io/badge/go-1.26.5-00ADD8.svg"></a>
  <a href="https://github.com/Runewardd/runeward/releases"><img alt="Release" src="https://img.shields.io/github/v/release/Runewardd/runeward?sort=semver"></a>
</p>

Put enforceable policy, human approvals, isolated execution, budgets, and signed evidence around
any AI agent. Runeward works with an existing agent or multi-agent framework rather than requiring
a new model or orchestration stack.

<p align="center">
  <img src="docs/assets/runeward-proof.svg" alt="An agent action flows through policy and optional human approval into an isolated sandbox and signed audit trail" width="820" />
</p>

## Prove it in one command

Prerequisites: a running Docker/Podman engine and the `runeward` binary.

```bash
runeward quickstart
```

The command creates `.runeward/quickstart.toml`, checks the policy, runtime, image, and state path,
runs an allowed command, proves a destructive command is denied before execution, and verifies the
signed audit trail. It never overwrites an existing policy unless `--force` is passed.

```bash
runeward doctor quickstart                     # explain setup problems safely
runeward --config-dir .runeward serve          # dashboard + governed REST API
runeward evidence export quickstart -o run.json
runeward evidence verify run.json              # independent policy/audit verification
```

These docs describe `main`. Until the next tagged release includes the commands above, use the
[from-source install](#install) for this quickstart.

## What Runeward adds

| Concern | Container alone | Runeward |
| --- | --- | --- |
| Tool calls | Executes what the process requests | Checks every shell, code, file, network, and browser action first |
| Risky actions | Application-specific | `allow`, `deny`, or `require-approval` with an attributed decision |
| Network | Usually open unless separately configured | Deny-by-default hostname policy; strict L3 enforcement on Kubernetes |
| Limits | CPU/memory | Wall-clock, exec, egress, token, cost, and retry-loop budgets |
| Audit | Runtime logs | Append-only, hash-chained, Ed25519-signed events |
| Handoff | Ad-hoc logs and folders | Workspace tar, recovery snapshots, and portable signed evidence JSON |
| Interfaces | Runtime-specific | CLI, REST, MCP, web dashboard, Kubernetes CRDs, and local SDK adapters |

Every governed action follows one path:

```text
agent request → policy → human approval when required → limits → sandbox → signed audit event
```

## Naming

Documentation and UI use familiar terms first. Existing API paths and file fields retain the
original themed names for compatibility.

| Plain-language term | Runeward name | Existing surface |
| --- | --- | --- |
| Sandbox | Citadel | `/v1/citadels`, Kubernetes `Citadel` |
| Policy file/profile | Charter | `/v1/charters`, `*.toml` profile |
| Approvals | Conclave | `/v1/conclave` |
| Signed audit trail | Chronicle | `/v1/chronicle`, `[chronicle]` |
| Network controls | Perimeter | `/perimeter`, `[network]` |
| Budgets and limits | Rationing | `[rationing]` |
| Agent group/fleet | Cohort | `/v1/cohorts`, `[cohort]` |

See the full [naming and writing convention](docs/naming.md).

## Install

### From source (recommended for `main`)

Requires Go **1.26.5** and a running Docker/Podman engine for local sandboxes.

```bash
git clone https://github.com/Runewardd/runeward
cd runeward
go build -o bin/runeward ./cmd/runeward
./bin/runeward quickstart
```

### Signed release installer

The macOS/Linux installer requires [`cosign`](https://docs.sigstore.dev/cosign/system_config/installation/)
so it can fail closed while verifying the signed checksum manifest. Windows release binaries are
downloaded manually from [Releases](https://github.com/Runewardd/runeward/releases).

```bash
curl -fsSL https://raw.githubusercontent.com/Runewardd/runeward/main/install.sh | sh
```

Install either dependency-light SDK from its language registry:

```bash
python -m pip install runeward
npm install @runeward/sdk
```

The Homebrew tap is not published yet. SDK source and framework adapters remain in
[`adapters/python`](adapters/python) and [`adapters/typescript`](adapters/typescript).

## Use it with an agent

Expose governed tools to an MCP-capable IDE or agent:

```json
{
  "mcpServers": {
    "runeward": {
      "command": "runeward",
      "args": ["mcp", "--config-dir", ".runeward"]
    }
  }
}
```

Or place an agent CLI inside a sandbox and run one or many governed workers:

```bash
runeward cohort --agent claude --model sonnet build "Build a tested API"
```

Adapters are included for LangChain, CrewAI, LlamaIndex, OpenAI Agents, Strands, Vercel AI SDK,
and LangChain.js. See [Adapters](docs/adapters.md) and [agent groups](docs/fleets.md).

## Harness agents and subagents

Runeward is the enforcement boundary around an agent, not the component that decides how the agent
reasons. Route the tool calls of a parent agent and each delegated subagent through Runeward to give
them explicit policy, approval, isolation, budget, and evidence boundaries.

Existing concepts keep their meaning: a **Cohort** is a group of peer workers sharing a task board;
it is not being renamed to “subagents.” Parent/child delegation remains the orchestrator's concern
today, while every participating agent can receive the same or a stricter Charter and its own
Citadel and Chronicle. See [Agent harnessing](docs/agent-harness.md).

## Policy workflow

Policies support built-in glob rules, CEL, OPA/Rego, and signed OCI bundles. Test them in CI, start
from a reviewed scaffold, or derive exact proposals from verified production evidence:

```bash
runeward policy scaffold package-approval
runeward policy test quickstart --case 'tool=shell,action=rm -rf /,expect=deny'
runeward policy learn run.json > proposed-policy.toml
```

`policy learn` never edits a policy automatically. It verifies the evidence first, skips redacted
actions, produces exact matches, and requires a human to review and broaden them.

## Security posture

- The server binds to loopback by default and requires authentication before a non-loopback bind.
- Multi-principal RBAC scopes sandboxes, agent groups, recovery snapshots, and dashboard views to
  their owner. Embedded HTTP MCP is disabled when RBAC is enabled because its authorization context
  is not yet unified; run a separately scoped MCP service if needed.
- Browser automation is experimental and disabled by default. Enable it only in a trusted deployment
  with `RUNEWARD_ENABLE_EXPERIMENTAL_BROWSER=1` after reviewing the [security model](docs/security-model.md).
- Per-action policy applies to tool calls routed through the control plane (REST, MCP, dashboard
  file/shell/code actions, and SDKs). An interactive terminal or a process already running inside a
  sandbox is a direct sandbox session: it receives isolation/network/resource controls and terminal
  recording, but its individual commands are not intercepted for approval. Use governed tool calls
  when command-level policy and signed verdicts are required.
- Report vulnerabilities privately using [SECURITY.md](SECURITY.md). Known pre-1.0 limitations and
  remediation work remain visible in [ROADMAP.md](ROADMAP.md).

## Documentation

- [Quickstart](docs/quickstart.md)
- [Policies / Charters](docs/profiles.md)
- [REST API](docs/rest-api.md)
- [Security model](docs/security-model.md)
- [End-to-end testing](docs/E2E-TESTING.md)
- Published site: [runewardd.github.io/runeward](https://runewardd.github.io/runeward/)

Contributions are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under
[Apache 2.0](LICENSE).
