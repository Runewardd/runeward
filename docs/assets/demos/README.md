# Demo artifacts

Recorded, reviewable demo artifacts live here. Generate the live agent-session
and sandbox-isolation recording from the repository root with:

```bash
./scripts/demo-agent-session-escape.sh record
```

That produces `agent-session-escape.cast`, an H.264
`agent-session-escape.mp4` when Swift and FFmpeg are available, plus a
private-data-scrubbed artifact bundle under `agent-session-escape/`. Review
generated evidence before committing it; transcript redaction is
defense-in-depth and custom secret formats may need additional Charter scrub
patterns.
