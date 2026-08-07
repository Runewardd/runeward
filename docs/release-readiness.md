# v0.3 production-readiness checklist

Do not tag `v0.3.0` until every item below passes on the release commit. This is
a pre-1.0 release: passing this checklist means the release is suitable for
careful evaluation, not that Runeward makes stable or absolute security guarantees.

## Identity and tenancy

- Store the authz file as `0600`; duplicate principal names and tokens must fail startup.
- Use unique tokens of at least 32 random characters for network listeners.
- Declare `allowed_profiles` and, for reviewers, `approval_profiles`.
- Give every person and agent a distinct actor identity; assign a shared `tenant` only when they
  should collaborate on the same resources. Test OIDC claim mapping when OIDC is enabled.
- Require TLS on network listeners; use `--allow-insecure-http` only behind a documented trusted
  TLS-terminating proxy.
- Run the role-by-transport suite for REST, MCP stdio, and MCP HTTP, including cross-owner negative cases.
- Verify non-admin `copy_from` overrides fail and set `RUNEWARD_COPY_FROM_ROOTS` for administrator imports.

## Runtime and recovery

- Validate every shipped Charter with zero errors. Treat warnings in production Charters as release blockers.
- Exercise claim, heartbeat, completion, failure, requeue, restart, and owner restoration for Cohorts.
- Verify Cohort lease tokens cannot be replayed for another task, actor, or tenant and that expired
  leases fail closed.
- Exercise snapshot creation, server restart, owner-filtered listing, and restore.
- Verify run lineage persists, active runs become `interrupted` on restart, and tenant filters hold.
- Run Docker and Kubernetes end-to-end suites plus dashboard browser automation.
- Require the authenticated six-role E2E job: identity, launch scope, shared-tenant access,
  cross-tenant denial, secret readiness, governed shell/code capability errors, and Cohort lifecycle.

## Agent compatibility

- Test the Codex plugin, Claude project MCP config, Copilot CLI/VS Code MCP config, and the remote `/mcp` endpoint.
- Confirm MCP returns structured discovery results and authorization failures cannot be confused with policy denials.
- Confirm delegated children cannot widen the parent Charter.
- Run both production examples under `examples/safe` through `validate --strict`.

## Supply chain

- Scan every runtime and IDE image for high/critical vulnerabilities.
- Generate SBOMs, sign images and release checksums, and verify signatures before promotion.
- Confirm prerelease tags use npm's `next` channel and never move a container `latest` tag.
- Keep every base image digest-pinned. The IDE images use the verified
  multi-platform `code-server:4.130.0-noble` index digest recorded in the
  release audit; both architectures must still rebuild successfully on the RC.

## Release order

1. Prepare the RC metadata with
   `python3 scripts/set-release-version.py 0.3.0-rc.2`, commit it, and require
   `python3 scripts/check-sdk-versions.py v0.3.0-rc.2` to pass.
2. Merge and run the full protected-branch suite, then tag `v0.3.0-rc.2`.
3. Install the RC through Homebrew, PyPI, npm (`next`), OCI, and the Codex
   plugin package. Perform clean-machine install and rollback tests.
4. Prepare the stable metadata with
   `python3 scripts/set-release-version.py 0.3.0`, repeat the protected-branch
   suite, and tag `v0.3.0` only after the RC evidence bundle verifies.
