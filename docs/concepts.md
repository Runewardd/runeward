# Concepts

runeward is a small control plane in front of a pluggable execution backend.
Everything an agent does passes through one governed path.

## The governed path

Every action — from REST, the dashboard, or MCP — flows through the same
pipeline before it touches a backend:

```
request -> policy engine -> approval gate -> guardrails -> backend exec -> audit ledger
```

If any stage says no, the action is denied or paused, and the outcome is recorded
either way.

## Sandbox (cell)

A **sandbox** (or *cell*) is one isolated execution environment created from a
profile. It has its own filesystem workspace, its own egress policy, and its own
resource limits. Backends:

- **Docker/Podman** — zero-setup, ideal for laptops and CI.
- **Kubernetes** — Pods + PVCs, strict L3 egress via a sidecar, CRDs, an
  admission webhook for org-wide guardrails, and multi-tenancy hardening (Pod
  Security Admission labels + optional default-deny NetworkPolicy).

Everything above the backend is identical, so a profile runs the same either way.

## Profile

A **profile** is the declarative security contract for a sandbox: the base image,
workdir, network egress allowlist, environment/secret injection, per-action
policy, and resource limits. Anything not granted is denied by default. See
[Profiles](profiles.md).

## Policy and approvals

Each action gets a verdict: `allow`, `deny`, or `require-approval`. The engine is
selectable per profile:

- `builtin` — first-match tool + glob rules.
- `cel` — CEL expressions over `{tool, arg}`.
- `rego` — an OPA/Rego module returning a decision.

`require-approval` pauses the action and surfaces it in the dashboard's approvals
inbox (and over REST) for a human to allow or deny.

## Egress control

Network egress is **deny-by-default**. Profiles declare an allowlist; on Docker
this is an in-process SNI-filtering proxy, and on Kubernetes it's enforced at L3
with a sidecar. Guardrails cap the number of egress requests to stop exfiltration
and runaway loops.

## Audit ledger

Every governed action and its verdict is appended to a **tamper-evident ledger**:
append-only, hash-chained, and signed with ed25519. It can be exported as a
transcript bundle and verified offline — the bundle embeds the public key, so a
third party can confirm nothing was altered or dropped.

!!! warning "One writer per ledger"
    The ledger is single-writer (guarded by a file lock). Run each instance with
    its own `RUNEWARD_STATE_DIR`; two processes sharing one ledger will break the
    hash chain and verification will report tampering.

## Fleets

A **fleet** is N sandboxes coordinated by an atomic task board. Workers claim
tasks with a lease (so a dead worker's task is reclaimed), do the work in their
own isolated workspace, and mark completion. See [Fleets](fleets.md).

## Guardrails

Hard limits per sandbox: wall-clock time, exec count, egress request count, and
token/spend budgets (`limits.max_tokens`, `limits.max_cost_usd`, fed by the usage
API), plus retry-loop detection. When a cap is hit, further actions are denied and
logged.

## Surfaces

The same governed core is reachable through:

- **CLI** — `runeward` ([reference](cli.md)).
- **REST API** — the control plane ([reference](rest-api.md)).
- **MCP server** — expose governed tools to an IDE/agent (Cursor, Claude Desktop,
  VS Code).
- **Web dashboard** — served by `runeward serve`.
