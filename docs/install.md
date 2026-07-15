# Install

runeward ships as a single static binary (plus helper binaries for the egress
proxy and in-sandbox agent). The `runeward` CLI is built for macOS, Linux, and
Windows on amd64/arm64; the egress proxy and in-sandbox agent are Linux-only
(they run inside Citadels). On Windows use the CLI to drive the Docker or
Kubernetes backend; the `enter` terminal-resize signal is a no-op there.

## One-line installer

```bash
curl -fsSL https://raw.githubusercontent.com/Runewardd/runeward/main/install.sh | sh
```

The installer detects your OS/arch, downloads the latest release, verifies its
checksum, and installs to `/usr/local/bin` (falling back to `~/.local/bin`).

Pin a version or install location with environment variables:

```bash
RUNEWARD_VERSION=v0.1.0 RUNEWARD_BIN_DIR="$HOME/.local/bin" \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/Runewardd/runeward/main/install.sh)"
```

## Homebrew

```bash
brew install Runewardd/tap/runeward
```

## From source

Requires **Go 1.25+**:

```bash
git clone https://github.com/Runewardd/runeward
cd runeward
go build -o bin/runeward ./cmd/runeward
./bin/runeward version
```

## Container images

Multi-arch (amd64/arm64) images are published to GitHub Container Registry and
cosign-signed by digest:

```bash
docker pull ghcr.io/runewardd/runeward:latest          # control plane / CLI
docker pull ghcr.io/runewardd/runeward-egress:latest   # strict-egress sidecar
docker pull ghcr.io/runewardd/runeward-agent:latest    # in-sandbox agent
docker pull ghcr.io/runewardd/runeward-sandbox:latest  # default sandbox base
```

## Verifying release artifacts

Every release is signed with [cosign](https://docs.sigstore.dev/) using keyless
(Fulcio/Rekor) signing — no long-lived keys, and the signing identity is the
GitHub Actions workflow itself.

Verify the checksums file (which covers every archive and SBOM):

```bash
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/Runewardd/runeward' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt
```

Verify a container image by tag:

```bash
cosign verify ghcr.io/runewardd/runeward:latest \
  --certificate-identity-regexp 'https://github.com/Runewardd/runeward' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
```

## Prerequisites

- **Docker / OrbStack / Podman** (any docker-compatible CLI) for the container
  backend. runeward runs a fast preflight check and gives a clear error if the
  engine isn't reachable.
- Optional: a **Kubernetes** cluster and `helm` for the Kubernetes backend and
  the [Helm chart](https://github.com/Runewardd/runeward/tree/main/deploy/helm/runeward).

## Verify

```bash
runeward version
runeward --help
```

Next: the [Quickstart](quickstart.md).
