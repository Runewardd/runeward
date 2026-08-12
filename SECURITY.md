# Security Policy

runeward is a tool for *containing* untrusted AI-agent activity, so its own
security posture matters. We take vulnerability reports seriously and appreciate
responsible disclosure.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately via one of:

- GitHub's **[Report a vulnerability](https://github.com/Runewardd/runeward/security/advisories/new)**
  (Security Advisories) — preferred.
- Email **adefemi171@gmail.com** with the subject line `runeward security`.

Include, where possible:

- affected version / commit (`runeward version`) and backend (Docker or Kubernetes),
- a description of the issue and its impact,
- steps to reproduce or a proof of concept,
- any suggested remediation.

### What to expect

- **Acknowledgement** within 3 business days.
- An initial assessment and severity rating within 7 business days.
- We aim to ship a fix for confirmed high/critical issues within 30 days and
  will keep you updated on progress.
- With your consent, we will credit you in the release notes and advisory.

Please give us a reasonable window to remediate before any public disclosure.

## Supported versions

runeward is pre-1.0. Security fixes are applied to the latest tagged release and
`main`. Once 1.0 ships, this section will list a support matrix.

## Scope and threat model

runeward's job is to reduce the blast radius of an autonomous agent through
isolation, deny-by-default egress, per-action policy/approvals, guardrails, and a
tamper-evident audit ledger. Understanding what it does — and does not — protect
against helps you use it safely.

**In scope (please report):**

- sandbox escape from a cell to the host or to another cell,
- bypass of the egress allowlist / policy engine / approval gates,
- audit-ledger forgery or silent tampering that verification would miss,
- path traversal / file writes outside the intended workspace (e.g. tar-slip),
- authentication/authorization flaws in the REST API, WebSocket terminal, or
  admission webhook,
- secret leakage in logs, the ledger, or the dashboard.

**Out of scope / operator responsibility:**

- the security of the container runtime, host kernel, and Kubernetes cluster
  runeward is deployed on (keep them patched),
- the trustworthiness of images referenced by profiles and of the agents/CLIs
  you choose to run inside a cell,
- secrets you place in profiles or environment; runeward redacts known secrets
  from the ledger but cannot know about values it was never told are sensitive,
- network exposure of `runeward serve` — the control plane should be bound to a
  trusted interface or placed behind your own auth/proxy in production,
- denial of service from workloads you explicitly grant large resource limits.

runeward is defense-in-depth, not a guarantee. Do not run genuinely hostile code
on infrastructure you cannot afford to lose.
