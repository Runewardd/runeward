# v0.3.0 release-gate audit

Audit date: 2026-08-07

This record distinguishes checks that passed from checks that require a
network-capable CI runner or release candidate. It is not a release approval.

## Passed locally

| Gate | Result |
| --- | --- |
| Version consistency | Python, npm, npm lockfile, MCP implementation/manifest/OCI reference, Codex plugin, and Helm chart/app versions are checked from one release version. |
| Native CLI artifact | The CLI built with release ldflags and reported `runeward v0.3.0`. |
| Host compilation | Every Go package compiled. |
| Static analysis | `go vet ./...` passed. |
| Core tests | Authz/OIDC, credentials, Cohorts, control plane, MCP, and CLI passed. |
| Python SDK | Four client tests passed from source. |
| TypeScript SDK | TypeScript build and two client tests passed. |
| npm package | `npm pack` produced `@runeward/sdk@0.3.0-rc.1`; an offline clean-prefix install imported `RunewardClient`. |
| Codex plugin | Manifest version, component paths, MCP command, skill frontmatter, and the tagged plugin archive validate together. |
| Production Charters | Every Charter in `examples/safe` passed `validate --strict`. |
| Helm | Chart lint and controller/server rendering passed with app and image tag `0.3.0-rc.1`. |
| Source/config hygiene | Go formatting, workflow YAML, JSON manifests, dashboard JavaScript syntax, and `git diff --check` passed. |

The local Go checks used the cached dependency versions available in the
managed workspace because it cannot write to the host module cache or reach the
module proxy. GitHub Actions must repeat them against the committed `go.mod` and
`go.sum` before release.

## Requires CI or an RC environment

| Gate | Why it remains open | Required evidence |
| --- | --- | --- |
| Complete Go suite | The managed workspace rejects loopback listeners. Seven existing HTTP/proxy/browser packages stop with `bind: operation not permitted`. | A green unmodified `go test ./... -count=1` in GitHub Actions. |
| Docker E2E | Access to the OrbStack Docker socket is denied here. | Quickstart, Docker Citadel lifecycle, policy, Conclave, Cohort, snapshot, browser, and evidence scenarios from `E2E-TESTING.md`. |
| Kubernetes E2E | The configured kind cluster depends on the inaccessible Docker runtime. | CRD/controller/webhook, strict egress, namespace, restore, and Cohort results from a disposable cluster. |
| Dashboard browser automation | A live local control plane cannot bind a port in this workspace. | Authenticated role-by-role browser run with console/network errors captured. |
| Image vulnerability gate | Docker and Trivy are unavailable. | Successful builds and zero HIGH/CRITICAL findings for all six images in the release workflow. |
| IDE base digest | Both IDE Dockerfiles now pin `codercom/code-server:4.130.0-noble` to multi-platform index digest `sha256:d39c88f837f78465b4cf99f54b76eadf52440dc9ffb589e2ddc5d2fd9d0592d2`. | Rebuild both architectures in CI and retain the successful image-scan evidence. |
| Python distribution install | The local environment has no cached `hatchling`, and external package access is unavailable. | Build wheel/sdist, `twine check`, clean-venv installation, and import/version check from the artifacts. |
| SBOM and signatures | Syft and Cosign are not installed locally and keyless signing requires the release workflow's OIDC identity. | Verify archive/image SBOMs, checksum signature, and all six image signatures. |
| Channel installs | Homebrew, PyPI, npm, OCI, and rollback tests require published RC artifacts. | Install `v0.3.0-rc.1` on clean macOS/Linux environments and verify rollback before the stable tag. |

## Decision

Do not create the stable `v0.3.0` tag yet. Push the release candidate branch,
let the protected CI/security workflows complete, pin and scan the IDE base,
then publish `v0.3.0-rc.1` for clean-machine channel testing. Promote to
`v0.3.0` only when every open row above has attached evidence.
