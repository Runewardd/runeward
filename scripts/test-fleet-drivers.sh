#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/runeward-fleet-driver.XXXXXX")
cleanup() { rm -rf "$work"; }
trap cleanup EXIT

mkdir -p "$work/bin" "$work/run"
cat >"$work/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail

: "${MOCK_CURL_LOG:?}"
: "${MOCK_CLAIM_COUNT:?}"

printf '%q ' "$@" >>"$MOCK_CURL_LOG"
printf '\n' >>"$MOCK_CURL_LOG"

joined="$*"
[[ "$joined" == *"Authorization: Bearer test-token"* ]] || {
  echo "mock: missing bearer token" >&2
  exit 90
}

url=""
body=""
previous=""
for argument in "$@"; do
  if [[ "$previous" == "-d" || "$previous" == "--data" ]]; then body="$argument"; fi
  if [[ "$argument" == http://* || "$argument" == https://* ]]; then url="$argument"; fi
  previous="$argument"
done

case "$url" in
  */v1/cohorts)
    printf '%s\n' '{"id":"fleet-test"}'
    ;;
  */v1/cohorts/fleet-test)
    printf '%s\n' '{"sandboxes":["sandbox-one"],"stats":{"pending":1}}'
    ;;
  */v1/cohorts/fleet-test/tasks)
    if [[ -n "$body" ]]; then
      printf '%s\n' '{"id":"task-one"}'
    else
      printf '%s\n' '{"tasks":[{"id":"task-one","state":"done","payload":"probe"}]}'
    fi
    ;;
  */v1/cohorts/fleet-test/claim)
    count=$(<"$MOCK_CLAIM_COUNT")
    if [[ "$count" == "0" ]]; then
      printf '1' >"$MOCK_CLAIM_COUNT"
      printf '%s\n' '{"claimed":true,"task":{"id":"task-one","payload":"probe","owner":"dev","lease_token":"signed-lease"}}'
    else
      printf '%s\n' '{"claimed":false}'
    fi
    ;;
  */v1/citadels/sandbox-one/shell/exec)
    printf '%s\n' '{"stdout":"AGENT_OK","exit_code":0}'
    ;;
  */v1/cohorts/fleet-test/tasks/task-one/complete)
    jq -e '.owner == "dev" and .lease_token == "signed-lease" and (.result | length > 0)' <<<"$body" >/dev/null
    printf '%s\n' '{"ok":true}'
    ;;
  */v1/cohorts/fleet-test/tasks/task-one/fail)
    jq -e '.owner == "dev" and .lease_token == "signed-lease" and .requeue == true' <<<"$body" >/dev/null
    printf '%s\n' '{"ok":true}'
    ;;
  *)
    echo "mock: unexpected URL: $url" >&2
    exit 91
    ;;
esac
MOCK
chmod +x "$work/bin/curl"

export PATH="$work/bin:$PATH"
export MOCK_CURL_LOG="$work/curl.log"
export MOCK_CLAIM_COUNT="$work/claim-count"
export RUNEWARD_AUTHZ_FILE="$work/authz.json"
export RUNEWARD_TOKEN=test-token
export RUNEWARD_BASE=http://runeward.test

printf '0' >"$MOCK_CLAIM_COUNT"
cd "$work/run"
AGENT=codex "$repo_root/examples/fleet.sh" up build-fleet-k8s >/dev/null
AGENT=codex "$repo_root/examples/fleet.sh" add "probe" >/dev/null
AGENT=codex "$repo_root/examples/fleet.sh" run >/dev/null

printf '0' >"$MOCK_CLAIM_COUNT"
"$repo_root/examples/drive-fleet.sh" fleet-test >/dev/null

if env -u RUNEWARD_TOKEN RUNEWARD_API_TOKEN=wrong-fallback \
  "$repo_root/examples/fleet.sh" status >"$work/no-token.out" 2>&1; then
  echo "fleet driver accepted RUNEWARD_AUTHZ_FILE without RUNEWARD_TOKEN" >&2
  exit 1
fi
grep -q 'RUNEWARD_TOKEN must select a principal' "$work/no-token.out"
grep -q 'Authorization' "$MOCK_CURL_LOG"
grep -q 'test-token' "$MOCK_CURL_LOG"

echo "fleet drivers: RBAC bearer authentication and signed task leases passed"
