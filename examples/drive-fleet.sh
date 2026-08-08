#!/usr/bin/env bash
# Drive a runeward fleet: give each sandbox one task, run the Cursor agent on it,
# then mark the task done (or failed). runeward provisions the governed pods and
# the shared task board; this loop is the "worker" that actually runs the agent.
#
#   RUNEWARD_TOKEN=<principal token> ./examples/drive-fleet.sh <fleet-id> [model]
set -euo pipefail

BASE="${RUNEWARD_BASE:-http://127.0.0.1:8080}"
FID="${1:?usage: drive-fleet.sh <fleet-id> [model]}"
MODEL="${2:-opus-4.8}"   # use the exact slug your `agent` CLI accepts
if [ -n "${RUNEWARD_AUTHZ_FILE:-}" ] && [ -z "${RUNEWARD_TOKEN:-}" ]; then
  echo "error: RUNEWARD_TOKEN must select a principal when RUNEWARD_AUTHZ_FILE is set" >&2
  exit 1
fi
TOKEN="${RUNEWARD_TOKEN:-${RUNEWARD_API_TOKEN:-}}"
AUTH=()
if [ -n "$TOKEN" ]; then AUTH=(-H "Authorization: Bearer $TOKEN"); fi

api() { curl "${AUTH[@]}" "$@"; }

# One worker per sandbox in the fleet (portable read loop; works on bash 3.2).
SANDBOXES=()
while IFS= read -r line; do SANDBOXES+=("$line"); done \
  < <(api -sf "$BASE/v1/cohorts/$FID" | jq -r '.sandboxes[]')
echo "fleet $FID has ${#SANDBOXES[@]} sandboxes; model=$MODEL"

i=0
for SB in "${SANDBOXES[@]}"; do
  i=$((i+1))
  OWNER="worker-$i"

  CLAIM=$(api -sf "$BASE/v1/cohorts/$FID/claim" -d "{\"owner\":\"$OWNER\"}")
  if [ "$(jq -r '.claimed' <<<"$CLAIM")" != "true" ]; then
    echo "[$OWNER] no task to claim; skipping $SB"; continue
  fi
  TID=$(jq -r '.task.id'      <<<"$CLAIM")
  PROMPT=$(jq -r '.task.payload' <<<"$CLAIM")
  LEASE=$(jq -r '.task.lease_token' <<<"$CLAIM")
  TASK_OWNER=$(jq -r '.task.owner' <<<"$CLAIM")
  echo "[$OWNER] $SB claimed $TID: $PROMPT"

  # Build the exec request safely (payload can contain quotes/newlines).
  CMD=$(jq -n --arg p "$PROMPT" --arg m "$MODEL" \
    '{command:["agent","-p",$p,"--model",$m,"--force","--trust","--output-format","text"]}')

  if OUT=$(api -sf "$BASE/v1/citadels/$SB/shell/exec" -d "$CMD"); then
    echo "$OUT" | jq -r '.stdout // ""'
    COMPLETE=$(jq -n --arg owner "$TASK_OWNER" --arg lease "$LEASE" --arg result "done by $OWNER" \
      '{owner:$owner, lease_token:$lease, result:$result}')
    api -sf "$BASE/v1/cohorts/$FID/tasks/$TID/complete" -d "$COMPLETE" >/dev/null
    echo "[$OWNER] completed $TID"
  else
    FAILED=$(jq -n --arg owner "$TASK_OWNER" --arg lease "$LEASE" \
      '{owner:$owner, lease_token:$lease, error:"agent exec failed", requeue:true}')
    api -sf "$BASE/v1/cohorts/$FID/tasks/$TID/fail" -d "$FAILED" >/dev/null
    echo "[$OWNER] FAILED $TID (requeued)"
  fi
done

echo "--- final board ---"
api -sf "$BASE/v1/cohorts/$FID/tasks" | jq -r '.tasks[] | "\(.state)\t\(.id)\t\(.payload)"'
