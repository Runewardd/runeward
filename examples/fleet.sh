#!/usr/bin/env bash
# Drive a runeward fleet by prompt. Push prompts at runtime and the workers build
# them, using whichever agent you pick. runeward provisions the governed pods and
# the shared task board; this script is the "worker" that runs the agent.
#
# Pick the agent + model with env vars:
#   AGENT=cursor|codex|claude   (default: cursor)
#   MODEL=<slug>                (optional; e.g. sonnet, gpt-5-codex, opus-4.8)
#   RUNEWARD_BASE=http://127.0.0.1:8080
#
# Commands:
#   fleet.sh up [profile]        create a fleet (default profile: build-fleet), remember its id
#   fleet.sh add "<prompt>"      add a prompt to the shared board (fan-out across workers)
#   fleet.sh run                 drain the board: every worker builds pending prompts in parallel
#   fleet.sh build "<prompt>"    up-if-needed + add + run (one-shot)
#   fleet.sh exec "<prompt>"     send a prompt to ONE sandbox (SANDBOX=<id>, default first) so
#                                follow-up changes land in the SAME workspace
#   fleet.sh status              show the board
#   fleet.sh export [dir]        copy every worker's /workspace to ./out (or <dir>)
#   fleet.sh down                kill the fleet and forget it
set -euo pipefail

BASE="${RUNEWARD_BASE:-http://127.0.0.1:8080}"
AGENT="${AGENT:-cursor}"
MODEL="${MODEL:-}"
STATE=".runeward-fleet"

die() { echo "error: $*" >&2; exit 1; }
fid() { [ -f "$STATE" ] || die "no fleet yet - run: $0 up"; cat "$STATE"; }
sandboxes() { curl -sf "$BASE/v1/fleets/$(fid)" | jq -r '.sandboxes[]'; }

# Build the shell/exec request body for a prompt, per selected agent.
cmd_for() {
  jq -n --arg p "$1" --arg m "$MODEL" --arg a "$AGENT" '
    def flags(f): if $m == "" then [] else [f, $m] end;
    if   $a == "cursor" then {command: (["agent","-p",$p,"--force","--output-format","text"] + flags("--model"))}
    elif $a == "codex"  then {command: (["codex","exec","--skip-git-repo-check","--dangerously-bypass-approvals-and-sandbox"] + flags("-m") + [$p])}
    elif $a == "claude" then {command: (["claude","-p",$p,"--dangerously-skip-permissions"] + flags("--model"))}
    else error("unknown AGENT: " + $a) end'
}

run_on() { # sandbox-id, prompt -> runs agent, prints stdout, returns exit status
  curl -sf "$BASE/v1/sandboxes/$1/shell/exec" -d "$(cmd_for "$2")" | jq -r '.stdout // .reason // ""'
}

worker() { # sandbox-id, owner: claim+build until the board is empty
  local sb="$1" owner="$2" claim tid prompt
  while :; do
    claim=$(curl -sf "$BASE/v1/fleets/$(fid)/claim" -d "{\"owner\":\"$owner\"}") || break
    [ "$(echo "$claim" | jq -r '.claimed')" = "true" ] || break
    tid=$(echo "$claim" | jq -r '.task.id')
    prompt=$(echo "$claim" | jq -r '.task.payload')
    echo ">> [$owner/$sb] building: $prompt"
    if run_on "$sb" "$prompt"; then
      curl -sf "$BASE/v1/fleets/$(fid)/tasks/$tid/complete" -d "{\"result\":\"done by $owner\"}" >/dev/null
      echo "<< [$owner] done: $tid"
    else
      curl -sf "$BASE/v1/fleets/$(fid)/tasks/$tid/fail" -d "{\"error\":\"agent exec failed\",\"requeue\":true}" >/dev/null
      echo "!! [$owner] failed (requeued): $tid"
    fi
  done
}

case "${1:-}" in
  up)
    prof="${2:-build-fleet}"
    id=$(curl -sf "$BASE/v1/fleets" -d "{\"profile\":\"$prof\"}" | jq -r '.id')
    [ -n "$id" ] && [ "$id" != "null" ] || die "fleet create failed"
    echo "$id" > "$STATE"
    echo "fleet $id up (profile=$prof); sandboxes:"; sandboxes | sed 's/^/  /'
    ;;
  add)
    [ $# -ge 2 ] || die 'usage: fleet.sh add "<prompt>"'
    curl -sf "$BASE/v1/fleets/$(fid)/tasks" -d "$(jq -n --arg p "$2" '{payload:$p}')" \
      | jq -r '"added task " + .id'
    ;;
  run)
    i=0
    for sb in $(sandboxes); do i=$((i+1)); worker "$sb" "worker-$i" & done
    wait
    echo "--- board ---"; "$0" status
    ;;
  build)
    [ $# -ge 2 ] || die 'usage: fleet.sh build "<prompt>"'
    [ -f "$STATE" ] || "$0" up
    "$0" add "$2" >/dev/null
    "$0" run
    ;;
  exec)
    [ $# -ge 2 ] || die 'usage: fleet.sh exec "<prompt>"  (SANDBOX=<id> to target one)'
    sb="${SANDBOX:-$(sandboxes | head -1)}"
    echo ">> [$sb] $2"; run_on "$sb" "$2"
    ;;
  status)
    curl -sf "$BASE/v1/fleets/$(fid)/tasks" \
      | jq -r '.tasks[] | "\(.state)\t\(.id)\t\(.payload)"'
    ;;
  export)
    dir="${2:-./out}"
    for sb in $(sandboxes); do echo "export $sb -> $dir/$sb"; runeward export "$sb" "$dir/$sb"; done
    ;;
  down)
    curl -sf -X DELETE "$BASE/v1/fleets/$(fid)" >/dev/null && echo "fleet down"
    rm -f "$STATE"
    ;;
  *)
    sed -n '2,/^set -euo/p' "$0" | sed '/^set -/d; s/^# \{0,1\}//'
    ;;
esac
