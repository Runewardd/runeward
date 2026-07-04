# Security model

runeward's job is to reduce the blast radius of an autonomous agent. Knowing what
it does — and does not — protect against is essential to using it safely.

For **reporting vulnerabilities**, see
[SECURITY.md](https://github.com/adefemi171/runeward/blob/main/SECURITY.md).
Please disclose privately; do not open a public issue.

## What runeward provides

- **Isolation.** Each cell runs in a container (Docker/Podman) or Pod (Kubernetes)
  with its own workspace and resource limits.
- **Deny-by-default egress.** Network access is denied unless explicitly
  allowlisted; SNI-filtered on Docker, enforced at L3 on Kubernetes.
- **Per-action policy and approvals.** `allow` / `deny` / `require-approval`
  verdicts, with human-in-the-loop gates for risky operations.
- **Guardrails.** Hard caps on wall-clock, exec count, and egress requests, plus
  retry-loop detection.
- **Tamper-evident audit.** An append-only, hash-chained, ed25519-signed ledger,
  independently verifiable offline.
- **No host mounts.** `copy_from` copies into the sandbox; the host tree is never
  mounted, so the agent can't reach beyond what you seeded.

## In scope (please report)

- Sandbox escape from a cell to the host or another cell.
- Bypass of the egress allowlist, policy engine, or approval gates.
- Audit-ledger forgery or silent tampering that verification would miss.
- Path traversal / writes outside the intended workspace (e.g. tar-slip).
- Auth/authorization flaws in the REST API, WebSocket terminal, or admission
  webhook.
- Secret leakage in logs, the ledger, or the dashboard.

## Operator responsibility (out of scope)

- Security of the container runtime, host kernel, and Kubernetes cluster — keep
  them patched.
- Trustworthiness of images referenced by profiles and of the agents/CLIs you run
  inside a cell.
- Secrets you place in profiles; runeward redacts *known* secret values from the
  ledger but can't know about values it was never told are sensitive.
- Network exposure of `runeward serve` — the control plane has no built-in auth
  and should be bound to a trusted interface or fronted by your own auth/proxy.
- Denial of service from workloads you explicitly grant large resource limits.

## Operational notes

!!! warning "One writer per ledger"
    The audit ledger is single-writer, protected by a file lock. Give each running
    instance its own `RUNEWARD_STATE_DIR`. Two processes sharing one ledger produce
    out-of-order/duplicate records, permanently breaking the hash chain so
    verification reports tampering.

!!! note "Same-origin WebSocket"
    The dashboard terminal WebSocket enforces a same-origin check to prevent
    cross-site hijacking. Front the control plane with TLS in production.

runeward is defense-in-depth, not a hard isolation boundary. Its default
container backend shares the host kernel, so a determined escape via a kernel or
runtime vulnerability is possible. For untrusted or adversarial workloads, add
VM-grade isolation — on Kubernetes set a hardened `runtime_class` (e.g. `gvisor`
or `kata`) in the profile; on Docker, configure your engine's runtime
accordingly or use a disposable host. runeward's sweet spot is governing a
cooperative-but-fallible agent, not caging code whose goal is to break out.
