# Observability

Operators run `runeward serve` and `runeward controller` as long-lived
services, so both expose Prometheus metrics and emit structured logs. Usage
telemetry is available too, but it is strictly opt-in and off by default.

## Metrics

The control plane serves Prometheus metrics at `GET /metrics` (unauthenticated,
same listener as the REST API and dashboard). Point a scraper at it:

```yaml
scrape_configs:
  - job_name: runeward
    static_configs:
      - targets: ["localhost:8080"]
```

Alongside the standard Go process and runtime collectors, runeward exports:

| Metric                             | Type      | Labels           | Meaning                                              |
| ---------------------------------- | --------- | ---------------- | ---------------------------------------------------- |
| `runeward_actions_total`           | counter   | `tool`, `verdict`| Governed actions processed, by tool and verdict.     |
| `runeward_actions_duration_seconds`| histogram | `tool`           | Wall-clock duration of executed governed actions.    |
| `runeward_sandboxes_created_total` | counter   | —                | Sandboxes created since start.                       |
| `runeward_build_info`              | gauge     | `version`        | Always `1`; carries the running version as a label.  |

`verdict` mirrors the ledger: `allow`, `deny`, `require-approval`, or `error`.
A label series appears only after its first event, so a fresh process shows just
`runeward_build_info` and `runeward_sandboxes_created_total` until traffic flows.

Example alerts you can build from these:

- A spike in `runeward_actions_total{verdict="deny"}` — policy is blocking more
  than usual (a misbehaving agent, or a too-tight profile).
- Growth in `runeward_actions_total{verdict="require-approval"}` with no
  approvals — operators are not keeping up with the approval inbox.

## Structured logs

Both services log through Go's `log/slog`. Two environment variables control
output (stderr):

| Variable               | Values                          | Default |
| ---------------------- | ------------------------------- | ------- |
| `RUNEWARD_LOG_FORMAT`  | `text`, `json`                  | `text`  |
| `RUNEWARD_LOG_LEVEL`   | `debug`, `info`, `warn`, `error`| `info`  |

Use `json` when shipping to a log aggregator:

```bash
RUNEWARD_LOG_FORMAT=json RUNEWARD_LOG_LEVEL=info runeward --config-dir examples serve
```

```json
{"time":"2026-07-04T11:28:37Z","level":"INFO","msg":"request","method":"POST","path":"/v1/sandboxes","status":200,"duration_ms":142}
```

`/metrics` and `/healthz` are excluded from the access log so scrapes and health
probes do not drown out the signal.

## Telemetry (opt-in, off by default)

runeward never phones home unless you turn it on **and** point it at a collector
you control. Telemetry is active only when both are set:

```bash
export RUNEWARD_TELEMETRY=1
export RUNEWARD_TELEMETRY_ENDPOINT=https://your-collector.example/ingest
```

The [DO_NOT_TRACK](https://consoledonottrack.com) convention always wins: if
`DO_NOT_TRACK` is truthy, telemetry stays off regardless of the flags above.

What is sent: a small JSON event per service start containing only the runeward
`version`, `os`, `arch`, and non-identifying properties (e.g. whether the
dashboard is enabled). There are no hostnames, paths, profile contents, IDs, or
any other identifying data, and no persistent device identifier. Sends are
best-effort with a 2-second timeout and never block or fail the process.

Every `serve`/`controller` startup logs the current telemetry state so it is
never a surprise:

```
telemetry disabled (opt in with RUNEWARD_TELEMETRY=1 and RUNEWARD_TELEMETRY_ENDPOINT)
```
