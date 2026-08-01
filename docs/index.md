# runeward

<p align="center">
  <img src="assets/runeward-banner-v2.png" alt="runeward — the agent governance harness" width="680" />
</p>

**The open-source governance harness for AI agents.**

Put enforceable policy, human approvals, isolated execution, budgets, and signed evidence around
any AI agent or subagent without replacing its model or orchestration framework.

## Prove it

With Docker/Podman running:

```bash
runeward quickstart
```

This creates a starter policy, validates the local runtime, proves one allow and one pre-execution
denial, and verifies the signed audit trail. Continue with the [Quickstart](quickstart.md), or open
the dashboard:

```bash
runeward --config-dir .runeward serve
```

## The control loop

```text
agent request → policy → human approval when required → budgets → sandbox → signed audit event
```

- **Policy before execution.** Built-in, CEL, OPA/Rego, and signed OCI policy bundles render
  `allow`, `deny`, or `require-approval` for each action.
- **Governed and isolated.** The same control path wraps Docker/Podman and Kubernetes sandboxes.
- **Portable proof.** Export the resolved policy plus signed audit history and verify it offline.
- **Practical recovery.** Download the workspace or snapshot and restore it from the dashboard.
- **Agent-native.** Use the CLI, REST, MCP, web dashboard, Kubernetes CRDs, or included adapters.

## Naming convention

The docs use familiar terms first: **sandbox**, **policy**, **approvals**, **signed audit trail**,
**network controls**, **budgets**, and **agent group**. The original Runeward names—Citadel,
Charter, Conclave, Chronicle, Perimeter, Rationing, and Cohort—remain in existing API paths and
configuration fields for compatibility. See [Naming](naming.md).

## Where to next

<div class="grid cards" markdown>

- :material-rocket-launch: **[Quickstart](quickstart.md)** — prove allow, deny, and signed audit.
- :material-download: **[Install](install.md)** — verified releases or a source build.
- :material-file-cog: **[Policies](profiles.md)** — write and test a security contract.
- :material-shield-lock: **[Security model](security-model.md)** — guarantees and known limits.
- :material-api: **[REST API](rest-api.md)** — integrate an existing agent or service.
- :material-toy-brick: **[Adapters](adapters.md)** — local Python and TypeScript SDKs.
- :material-shield-account: **[Agent harnessing](agent-harness.md)** — govern agents and delegated subagents.
- :material-account-group: **[Agent groups](fleets.md)** — coordinate governed workers.
- :material-chart-line: **[Observability](observability.md)** — metrics, logs, and telemetry.

</div>

Runeward is open source under the [Apache License 2.0](https://github.com/Runewardd/runeward/blob/main/LICENSE).
