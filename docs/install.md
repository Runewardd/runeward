# Install

runeward ships as a single static binary (plus helper binaries for the egress
proxy and in-sandbox agent). macOS and Linux on amd64/arm64 are supported.

## One-line installer

```bash
curl -fsSL https://raw.githubusercontent.com/adefemi171/runeward/main/install.sh | sh
```

The installer detects your OS/arch, downloads the latest release, verifies its
checksum, and installs to `/usr/local/bin` (falling back to `~/.local/bin`).

Pin a version or install location with environment variables:

```bash
RUNEWARD_VERSION=v0.1.0 RUNEWARD_BIN_DIR="$HOME/.local/bin" \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/adefemi171/runeward/main/install.sh)"
```

## Homebrew

```bash
brew install adefemi171/tap/runeward
```

## From source

Requires **Go 1.25+**:

```bash
git clone https://github.com/adefemi171/runeward
cd runeward
go build -o bin/runeward ./cmd/runeward
./bin/runeward version
```

## Container image

Images are published to GitHub Container Registry:

```bash
docker pull ghcr.io/adefemi171/runeward:latest
```

## Prerequisites

- **Docker / OrbStack / Podman** (any docker-compatible CLI) for the container
  backend. runeward runs a fast preflight check and gives a clear error if the
  engine isn't reachable.
- Optional: a **Kubernetes** cluster and `helm` for the Kubernetes backend and
  the [Helm chart](https://github.com/adefemi171/runeward/tree/main/deploy/helm/runeward).

## Verify

```bash
runeward version
runeward --help
```

Next: the [Quickstart](quickstart.md).
