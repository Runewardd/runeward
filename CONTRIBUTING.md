# Contributing to runeward

Thanks for your interest in improving runeward. This guide covers how to build,
test, and submit changes.

By participating you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Prerequisites

- **Go 1.25+** (the toolchain version pinned in `go.mod`).
- **Docker or OrbStack** (or any docker-compatible CLI) for the container backend.
- Optional: a **Kubernetes** cluster (OrbStack, kind, k3d, etc.) and `helm` for
  backend and chart work.

## Build and run

```bash
# Build the main binary
go build -o bin/runeward ./cmd/runeward

# Explore example profiles without touching a backend
./bin/runeward --config-dir examples list
./bin/runeward --config-dir examples print ns-auto

# Run the control plane (REST API + dashboard) with an isolated state dir
RUNEWARD_STATE_DIR=/tmp/rw-dev ./bin/runeward --config-dir examples serve
```

> Tip: always give a dev instance its own `RUNEWARD_STATE_DIR`. Multiple writers
> sharing the default ledger will trip the tamper-evident audit chain.

## Before you open a PR

Run the same checks CI runs, locally:

```bash
gofmt -l internal cmd     # must print nothing
go vet ./...
go build ./...
GOOS=linux GOARCH=amd64 go build ./...   # egress/strict build tags
go test ./... -count=1
```

If you touch the Helm chart:

```bash
helm lint deploy/helm/runeward
helm template runeward deploy/helm/runeward --set server.enabled=true >/dev/null
```

## Coding conventions

- Keep the code `gofmt`-clean and `go vet`-clean; CI enforces both.
- Match the surrounding style. Comments should explain intent or non-obvious
  trade-offs, not narrate what the code plainly does.
- Add or update tests for behavior changes. Security-sensitive paths (egress,
  policy, ledger, tar extraction, auth) should always come with tests.
- Never commit secrets, API keys, ledgers, or built binaries. Check `.gitignore`
  if you add new generated artifacts.

## Commit and PR guidelines

- Write focused commits with clear messages (imperative mood, e.g. "add k8s
  egress preflight"). Explain the *why* in the body when it isn't obvious.
- Keep PRs scoped to one logical change; smaller PRs get reviewed faster.
- In the PR description, note what you changed, why, and how you tested it.
- Link any related issue.

## Reporting bugs and requesting features

Use the GitHub issue templates. For anything security-related, **do not** open a
public issue — follow [SECURITY.md](SECURITY.md) instead.

## License

By contributing, you agree that your contributions are licensed under the
Apache License 2.0, the same license that covers the project (see
[LICENSE](LICENSE)).
