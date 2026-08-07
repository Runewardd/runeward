---
name: runeward-governed-execution
description: Run shell commands, Python or Node code, browser actions, file operations, or delegated agent work inside Runeward Citadels with Charter policy, human approvals, budgets, tenant ownership, and a signed Chronicle. Use whenever Codex should execute untrusted, networked, destructive, delegated, or audit-sensitive work through the Runeward MCP tools.
---

# Runeward governed execution

1. Call `runeward_whoami` and `runeward_list_charters` before creating anything.
2. Select the least-privileged Charter that can complete the task. Do not switch to a broader Charter to bypass a denial.
3. Call `runeward_readiness` for that Charter, then `runeward_create_citadel`. Set `agent="codex"`; include provider/model metadata when known.
4. Reuse that Citadel for the task and perform all execution through Runeward tools.
5. Treat a policy `deny` as final for that action. Explain the reason and choose only a genuinely different allowed approach.
6. Treat `require-approval` as a hard pause. Surface the approval ID and wait for a human decision.
7. For delegation, pass `parent_citadel`, `run_id`, `agent`, `provider`, and `model` when creating a child. Runeward requires the child to inherit the parent's tenant and Charter; inspect `runeward_list_runs` for lineage.
8. Use `runeward_export_evidence` before teardown when a portable signed result is required.
9. Tear down Citadels and Cohorts when finished unless the user explicitly asks to preserve them.

Never pass host paths through `copy_from`; it is an administrator-only operation. Never expose bearer tokens in prompts, tool arguments, files, or output.
