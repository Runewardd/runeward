---
name: runeward
description: Run shell, Python, Node, browser, file, snapshot, Cohort, or delegated-agent work inside Runeward Citadels with Charter policy, Conclave approvals, budgets, tenant ownership, and signed Chronicles. Use for untrusted, networked, destructive, delegated, or audit-sensitive agent execution.
---

# Runeward

Start every task with `runeward_whoami`, `runeward_list_charters`, and `runeward_readiness`. Choose the least-privileged Charter, then create one Citadel and reuse it for the task.

Core tools:

- Discovery: `runeward_whoami`, `runeward_list_charters`, `runeward_readiness`.
- Lifecycle: `runeward_create_citadel`, `runeward_list_citadels`, `runeward_kill_citadel`.
- Execution: `runeward_shell`, `runeward_python`, `runeward_node`.
- Browser: `runeward_browser`, `runeward_browser_open`, `runeward_browser_act`, `runeward_browser_close`.
- Files: `runeward_read_file`, `runeward_write_file`, `runeward_list_files`, `runeward_search_files`.
- Governance: `runeward_list_conclave`, `runeward_report_usage`, `runeward_list_runs`, `runeward_export_evidence`, `runeward_verify_chronicle`.
- Recovery: `runeward_snapshot_citadel`, `runeward_list_snapshots`.
- Cohorts: `runeward_create_cohort`, `runeward_list_cohorts`, `runeward_list_tasks`, `runeward_add_task`, `runeward_claim_task`, `runeward_heartbeat_task`, `runeward_complete_task`, `runeward_fail_task`, `runeward_kill_cohort`.

Treat `deny` as final for that action. Treat `require-approval` as a hard pause and surface its approval ID. Never broaden a Charter to work around policy. For child agents, pass `parent_citadel`; Runeward constrains the child to the parent's tenant and Charter. Keep the latest signed `lease_token` private to the worker that claimed a Cohort task and present it on heartbeat, complete, or fail. Export evidence before teardown when the task requires a portable artifact. `copy_from` is administrator-only. Tear down resources when finished.
