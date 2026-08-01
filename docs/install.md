# Install

## From source

The documentation follows `main`. Build from source to use the newest `quickstart`, `doctor`,
portable evidence, policy-learning, and dashboard recovery features.

Requires **Go 1.26.5**:

```bash
git clone https://github.com/Runewardd/runeward
cd runeward
go build -o bin/runeward ./cmd/runeward
./bin/runeward version
```

## Signed release installer (macOS/Linux)

The installer requires `curl` or `wget`, `tar`, a SHA-256 utility, and
[`cosign`](https://docs.sigstore.dev/cosign/system_config/installation/). It fails closed: the
signed checksum manifest is verified before the archive checksum and install.

```bash
curl -fsSL https://raw.githubusercontent.com/Runewardd/runeward/main/install.sh | sh
```

Pin a release or location:

```bash
RUNEWARD_VERSION=v0.2.0 RUNEWARD_BIN_DIR="$HOME/.local/bin" \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/Runewardd/runeward/main/install.sh)"
```

The shell installer supports macOS and Linux. Windows CLI archives are available from
[GitHub Releases](https://github.com/Runewardd/runeward/releases); the in-sandbox helpers are Linux
binaries because they run inside Docker or Kubernetes.

!!! note "Registry status"
    The Python and npm SDKs are published independently from the Go CLI. The Homebrew tap is still
    pending; use the verified installer or a release archive for the CLI.

## SDK adapters

```bash
python -m pip install runeward
npm install @runeward/sdk
```

See [Adapters](adapters.md) for optional framework dependencies and repository-development installs.

## Container images

Published multi-architecture images:

```bash
docker pull ghcr.io/runewardd/runeward:latest
docker pull ghcr.io/runewardd/runeward-egress:latest
docker pull ghcr.io/runewardd/runeward-agent:latest
docker pull ghcr.io/runewardd/runeward-sandbox:latest
```

Images and release checksums are signed by the release workflow. Verification examples are in
[SECURITY.md](../SECURITY.md).

## Runtime prerequisites

- Docker, OrbStack, or Podman for local sandboxes.
- Optionally Kubernetes and Helm for cluster deployments.
- Run `runeward doctor <policy>` before launch to check the selected runtime and image.

Next: [Quickstart](quickstart.md).
