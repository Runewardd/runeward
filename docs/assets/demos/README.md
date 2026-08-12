# Demo artifacts

Recorded, reviewable demo artifacts live here. Generate the live agent-session
and sandbox-isolation recording from the repository root with:

```bash
./scripts/demo-agent-session-escape.sh record
```

That produces `agent-session-escape.cast` plus a small, private-data-scrubbed
artifact bundle under `agent-session-escape/`. The checked-in MP4 is a curated
rendering for the documentation site. Review generated evidence before
committing it; transcript redaction is defense-in-depth and custom secret
formats may need additional Charter scrub patterns.
