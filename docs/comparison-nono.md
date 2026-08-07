# Runeward and nono

[nono](https://github.com/nolabs-ai/nono) and Runeward both reduce the blast
radius of coding agents, but they solve different layers of the problem.

## Short version

- Choose **nono** when the primary need is a fast, local, OS-native sandbox for
  one process and its tools without a daemon, container, or VM.
- Choose **Runeward** when the primary need is a shared governance control plane:
  multi-tenant identity, policy decisions, human approvals, budgets, distributed
  agent groups, durable run lineage, and portable signed evidence across local or
  Kubernetes-backed execution.
- They can be complementary. A future native Runeward backend could use nono as
  the local Citadel enforcement mechanism while Runeward retains governance,
  approvals, orchestration, and evidence.

## Capability comparison

| Area | nono | Runeward |
| --- | --- | --- |
| Primary boundary | Native child-process sandbox | Governed Citadel (Docker, Podman, or Kubernetes) |
| Runtime model | No daemon, container, or VM | Long-lived control plane and isolated runtime resources |
| OS enforcement | Landlock on Linux/WSL2; Seatbelt on macOS | Container hardening, seccomp/AppArmor, strict L3 egress, optional gVisor/Kata |
| Policy unit | Filesystem/network capabilities and per-tool child profiles | Per-action `allow`/`deny`/`require-approval` Charter rules plus CEL/Rego/bundles |
| Credentials | Proxy-based credential injection keeps real keys outside the sandbox | Secret sources, injection into Citadels, redaction, tenant-scoped authentication |
| Human approval | Not its primary control-plane abstraction | Conclave inbox with attributed, scoped decisions |
| Multi-agent work | Profiles and controlled tool subprocesses | Cohorts, signed task leases, parent/child run lineage, provider-neutral APIs |
| Budgets | Resource/process sandbox focus | Wall-clock, exec, egress, retry, token, and cost limits |
| Audit/evidence | Tamper-evident audit and instruction attestation | Hash-chained Ed25519 Chronicle plus portable evidence documents and UI/API export |
| Remote/team operation | Local-first | REST, MCP, dashboard, SDKs, RBAC/OIDC, Kubernetes controller |
| Startup/overhead | Very low native-process overhead | Higher container/pod overhead in exchange for a broader isolation and governance plane |

## Where nono is ahead

nono has a mature native-sandbox story, including inherited filesystem
constraints, tool-specific child sandboxes, a profile registry, instruction
attestation, and phantom-token credential proxying. Those are valuable for a
developer who wants to wrap `codex`, `claude`, or another local command with
minimal startup cost. See nono's [OS sandbox](https://www.nono.sh/os-sandbox),
[CLI](https://nono.sh/cli), and
[credential injection](https://www.nono.sh/credential-injection) documentation.

Runeward should not imitate that positioning or rename its existing concepts.
Its clearest differentiation is governance across people, agents, and runtime
backends: one tenant can give Codex, Claude, Copilot, CI, and reviewers distinct
identities while sharing Citadels or Cohorts under a common Charter. Signed task
leases prevent a worker from completing another worker's claim, and durable Run
records retain actor/provider/model/parent attribution after a Citadel exits.

## Where Runeward is ahead

Runeward provides controls that are deliberately outside a local sandbox's
scope: centrally visible approvals, token and spend budgets, a dashboard,
multi-principal resource ownership, OIDC, Kubernetes orchestration, recovery
snapshots, SDKs, and portable signed evidence. It also supports VM-grade runtime
classes such as gVisor or Kata where a shared-kernel container is insufficient.

## Honest limitations

Runeward's container backend is heavier and its interactive terminal/IDE is not
a per-keystroke policy gate. A command receives a policy verdict only when it is
routed through the governed REST, MCP, dashboard, or SDK tool surface. nono's
native approach is more convenient for transparently wrapping an arbitrary local
agent process. Runeward's production value depends on integrations actually
using the governed surface and on operators configuring suitable Charters,
identity claims, runtime isolation, and network enforcement.
