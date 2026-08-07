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

## Agent compatibility

- Test the Codex plugin, Claude project MCP config, Copilot CLI/VS Code MCP config, and the remote `/mcp` endpoint.
- Confirm MCP returns structured discovery results and authorization failures cannot be confused with policy denials.
- Confirm delegated children cannot widen the parent Charter.
- Run both production examples under `examples/safe` through `validate --strict`.

## Supply chain

- Scan every runtime and IDE image for high/critical vulnerabilities.
- Generate SBOMs, sign images and release checksums, and verify signatures before promotion.
- Confirm prerelease tags use npm's `next` channel and never move a container `latest` tag.
- Pin every base image by digest before the final release tag. The IDE images remain release-blocked while their `code-server` base uses a mutable tag.

## Release order

1. Merge and run the full protected-branch suite.
2. Publish an RC and install it through Homebrew, PyPI, npm, OCI, and the Codex plugin package.
3. Perform clean-machine install and rollback tests.
4. Tag `v0.3.0` only after the RC evidence bundle verifies successfully.
