#!/usr/bin/env bash
# Drive a runeward fleet: give each sandbox one task, run the Cursor agent on it,
# then mark the task done (or failed). runeward provisions the governed pods and
# the shared task board; this loop is the "worker" that actually runs the agent.
#
#   ./examples/drive-fleet.sh <fleet-id> [model]
set -euo pipefail

BASE="${RUNEWARD_BASE:-http://127.0.0.1:8080}"
FID="${1:?usage: drive-fleet.sh <fleet-id> [model]}"
MODEL="${2:-opus-4.8}"   # use the exact slug your `agent` CLI accepts

# One worker per sandbox in the fleet (portable read loop; works on bash 3.2).
SANDBOXES=()
while IFS= read -r line; do SANDBOXES+=("$line"); done \
  < <(curl -sf "$BASE/v1/fleets/$FID" | jq -r '.sandboxes[]')
echo "fleet $FID has ${#SANDBOXES[@]} sandboxes; model=$MODEL"

i=0
for SB in "${SANDBOXES[@]}"; do
  i=$((i+1))
  OWNER="worker-$i"

  CLAIM=$(curl -sf "$BASE/v1/fleets/$FID/claim" -d "{\"owner\":\"$OWNER\"}")
  if [ "$(jq -r '.claimed' <<<"$CLAIM")" != "true" ]; then
    echo "[$OWNER] no task to claim; skipping $SB"; continue
  fi
  TID=$(jq -r '.task.id'      <<<"$CLAIM")
  PROMPT=$(jq -r '.task.payload' <<<"$CLAIM")
  echo "[$OWNER] $SB claimed $TID: $PROMPT"

  # Build the exec request safely (payload can contain quotes/newlines).
  CMD=$(jq -n --arg p "$PROMPT" --arg m "$MODEL" \
    '{command:["agent","-p",$p,"--model",$m,"--force","--trust","--output-format","text"]}')

  if OUT=$(curl -sf "$BASE/v1/sandboxes/$SB/shell/exec" -d "$CMD"); then
    echo "$OUT" | jq -r '.stdout // ""'
    curl -sf "$BASE/v1/fleets/$FID/tasks/$TID/complete" \
      -d "{\"result\":\"done by $OWNER\"}" >/dev/null
    echo "[$OWNER] completed $TID"
  else
    curl -sf "$BASE/v1/fleets/$FID/tasks/$TID/fail" \
      -d "{\"error\":\"agent exec failed\",\"requeue\":true}" >/dev/null
    echo "[$OWNER] FAILED $TID (requeued)"
  fi
done

echo "--- final board ---"
curl -sf "$BASE/v1/fleets/$FID/tasks" | jq -r '.tasks[] | "\(.state)\t\(.id)\t\(.payload)"'
