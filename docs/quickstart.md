# Quickstart

This walks from zero to a governed sandbox and the control-plane dashboard in
about a minute. It assumes Docker/OrbStack is running.

!!! tip "Always give an instance its own state dir"
    The audit ledger is single-writer. Set a dedicated `RUNEWARD_STATE_DIR` per
    running instance so two processes never share (and corrupt) one ledger.

## 1. Create a profile

A profile is the security contract for a sandbox. runeward loads profiles from a
config directory (`--config-dir` or `$RUNEWARD_CONFIG_DIR`). If you installed via
Homebrew or the binary, you don't have any yet — author one in its own folder:

```bash
mkdir -p ~/runeward-profiles
cat > ~/runeward-profiles/dev.toml <<'EOF'
[host]
type    = "container"
image   = "debian:stable-slim"
workdir = "/workspace"

# Deny-by-default egress; allow only what you list (supports *.wildcards,
# comma-separated). Omit the whole [network] block to leave egress open.
[network]
default = "deny"

[[network.rule]]
verdict  = "allow"
hostname = "*.debian.org"

# Per-action policy: block destructive shell commands.
[[policy]]
tool    = "shell"
match   = "rm -rf *"
verdict = "deny"
reason  = "no recursive deletes"

[limits]
wall_clock = "30m"
max_execs  = 500
EOF
```

!!! warning "Point `--config-dir` at a folder of only profiles"
    runeward tries to parse **every** `.toml`/`.yaml`/`.yml`/`.json` file in the
    config dir as a profile. Pointing it at, say, a repo root will throw parse
    errors for unrelated files like `mkdocs.yml`. Use a dedicated directory.

If you cloned the repo instead, skip this step and use the ready-made profiles in
`examples/` (substitute `--config-dir examples` and a name like `dev` or `ns-auto`
below).

## 2. Inspect the profile

Print the resolved, secret-redacted policy before you use it:

```bash
runeward --config-dir ~/runeward-profiles list
runeward --config-dir ~/runeward-profiles print dev
```

## 3. Run a command in a fresh sandbox

A bare profile name is shorthand for `enter <profile>`:

```bash
# Interactive shell in a sandbox
runeward --config-dir ~/runeward-profiles enter dev

# Or run a single command, then tear down
runeward --config-dir ~/runeward-profiles enter dev -- uname -a
```

## 4. Start the control plane

```bash
RUNEWARD_STATE_DIR=/tmp/rw runeward --config-dir ~/runeward-profiles serve
```

This serves the REST API and web dashboard on `:8080`. Open
[http://localhost:8080](http://localhost:8080), pick a profile, click **New**
(optionally point it at a local folder to copy in), and drive the sandbox's
terminal, files, audit timeline, and approvals inbox.

## 5. Drive it over REST

```bash
# Create a sandbox
SB=$(curl -sX POST localhost:8080/v1/sandboxes -d '{"profile":"dev"}' | jq -r .id)

# Run a shell command through the governance path
curl -sX POST "localhost:8080/v1/sandboxes/$SB/shell/exec" \
  -d '{"cmd":"echo hello from a governed cell"}'

# See the signed audit trail, then verify the chain
curl -s "localhost:8080/v1/sandboxes/$SB/audit"
curl -s "localhost:8080/v1/audit/verify"

# Tear it down
curl -sX DELETE "localhost:8080/v1/sandboxes/$SB"
```

## 6. Work against your own code

runeward never mounts your host folder — it takes a one-time copy at create, so
the agent works on an isolated `/workspace` and your real files are untouched.

```bash
# Seed from a local folder at create time
curl -sX POST localhost:8080/v1/sandboxes \
  -d '{"profile":"dev","copy_from":"~/Documents/my-project"}'

# Pull the agent's results back out to the host
runeward export <sandbox-id> ./agent-output
```

## Next steps

- [Concepts](concepts.md) — how the pieces fit together.
- [Profiles](profiles.md) — write your own security contract.
- [Fleets](fleets.md) — run many governed agents in parallel.
