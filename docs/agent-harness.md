# Agent harnessing

Runeward is an **agent governance harness**: an enforcement and evidence boundary around agents
that already exist. It does not choose a model, plan tasks, or replace an orchestration framework.
It governs the actions that an agent attempts to take.

```text
agent or subagent
        ↓
Runeward policy → human approval when required → budgets → Citadel → signed Chronicle
```

The existing Runeward concepts and programmatic names do not change:

- A **Citadel** is an isolated sandbox.
- A **Charter** is its policy and limits profile.
- A **Chronicle** is its signed audit trail.
- A **Cohort** is a group of peer workers sharing a task board.

## One agent

Connect the agent through MCP or one of the Python and TypeScript adapters. Give it a Charter with
the smallest tool, network, filesystem, and budget surface needed for the job. A denied action is a
final policy decision; an approval-required action pauses until an operator decides it.

## Peer workers

Use a Cohort when several equivalent workers should pull independent tasks from one command board.
Every worker runs in a Citadel created from the Cohort's Charter. The Cohort name and API remain
unchanged.

## Parent agents and delegated subagents

A subagent is a child delegated by a parent agent; it is not another name for a Cohort. To harness
an orchestrator that already supports subagents:

1. Route the parent and every child agent's tool calls through Runeward.
2. Give each child its own Citadel when workspaces or evidence must be isolated.
3. Assign the same Charter as the parent or a separately reviewed, stricter Charter.
4. Preserve the orchestrator's parent/child identifiers alongside the exported Runeward evidence.
5. Never automatically retry a denial or work around an approval gate in a child agent.

This provides governed child execution today, but Runeward does not yet expose a native
parent/child run tree or automatically prove that a child's Charter is narrower than its parent's.
Those are explicit boundaries rather than implied guarantees.

## Integration choices

| Agent surface | Recommended connection |
| --- | --- |
| MCP-capable agent or IDE | `runeward mcp` |
| Python agent framework | `runeward` package and the relevant optional adapter |
| TypeScript agent framework | `@runeward/sdk` and its framework subpath |
| Custom orchestrator | REST API or the dependency-light SDK client |
| Parallel peer workers | Existing `runeward cohort` commands and `/v1/cohorts` API |

See [Adapters](adapters.md), [Cohorts](fleets.md), and the [Security model](security-model.md).
