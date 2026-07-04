# Quickstart

This walks from zero to a governed sandbox and the control-plane dashboard in
about a minute. It assumes Docker/OrbStack is running.

!!! tip "Always give an instance its own state dir"
    The audit ledger is single-writer. Set a dedicated `RUNEWARD_STATE_DIR` per
    running instance so two processes never share (and corrupt) one ledger.

## 1. Inspect a profile

Profiles are the security contract. Print the resolved, secret-redacted policy
before you use one:

```bash
runeward --config-dir examples list
runeward --config-dir examples print ns-auto
```

## 2. Run a one-shot command in a fresh sandbox

A bare profile name is shorthand for `enter <profile>`:

```bash
# Interactive shell in a sandbox
runeward --config-dir examples enter ns-auto

# Or run a single command, then tear down
runeward --config-dir examples enter ns-auto -- uname -a
```

## 3. Start the control plane

```bash
RUNEWARD_STATE_DIR=/tmp/rw ./bin/runeward --config-dir examples serve
```

This serves the REST API and web dashboard on `:8080`. Open
[http://localhost:8080](http://localhost:8080), pick a profile, click **New**
(optionally point it at a local folder to copy in), and drive the sandbox's
terminal, files, audit timeline, and approvals inbox.

## 4. Drive it over REST

```bash
# Create a sandbox
SB=$(curl -sX POST localhost:8080/v1/sandboxes -d '{"profile":"ns-auto"}' | jq -r .id)

# Run a shell command through the governance path
curl -sX POST "localhost:8080/v1/sandboxes/$SB/shell/exec" \
  -d '{"cmd":"echo hello from a governed cell"}'

# See the signed audit trail, then verify the chain
curl -s "localhost:8080/v1/sandboxes/$SB/audit"
curl -s "localhost:8080/v1/audit/verify"

# Tear it down
curl -sX DELETE "localhost:8080/v1/sandboxes/$SB"
```

## 5. Work against your own code

runeward never mounts your host folder — it takes a one-time copy at create, so
the agent works on an isolated `/workspace` and your real files are untouched.

```bash
# Seed from a local folder at create time
curl -sX POST localhost:8080/v1/sandboxes \
  -d '{"profile":"codex-agent","copy_from":"~/Documents/my-project"}'

# Pull the agent's results back out to the host
runeward export <sandbox-id> ./agent-output
```

## Next steps

- [Concepts](concepts.md) — how the pieces fit together.
- [Profiles](profiles.md) — write your own security contract.
- [Fleets](fleets.md) — run many governed agents in parallel.
