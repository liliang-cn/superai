#!/usr/bin/env bash
# End-to-end check against a running SuperAI, exercising the paths a person and
# a program each take: password -> session cookie -> RPC, and bearer -> MCP ->
# the schedule tools.
#
# Written after a release where all of this was verified by hand, one curl at a
# time, and none of it was repeatable the next day.
#
#   SUPERAI_URL=https://ai.superleo.cn \
#   SUPERAI_PASSWORD=... SUPERAI_TOKEN=... ./scripts/e2e.sh
#
# Everything it creates, it deletes. It runs against a live instance and writes
# real data on the way through, so it deliberately touches only schedules it
# named itself.
set -uo pipefail

URL="${SUPERAI_URL:-http://127.0.0.1:43117}"
PASSWORD="${SUPERAI_PASSWORD:-}"
TOKEN="${SUPERAI_TOKEN:-}"
COOKIE_JAR="$(mktemp)"
MARKER="e2e-$$-$(date +%s)"
FAILED=0

cleanup() { rm -f "$COOKIE_JAR"; }
trap cleanup EXIT

pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; FAILED=$((FAILED + 1)); }
step() { printf '\n%s\n' "$1"; }

# expect <description> <actual> <wanted>
expect() {
  if [ "$2" = "$3" ]; then pass "$1"; else fail "$1 (got '$2', want '$3')"; fi
}

# contains <description> <haystack> <needle>
contains() {
  case "$2" in
  *"$3"*) pass "$1" ;;
  *) fail "$1 (missing '$3')" ;;
  esac
}

step "auth"
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 "$URL/api/rpc/ping" -X POST)
expect "an unauthenticated RPC is refused" "$code" "401"

if [ -n "$PASSWORD" ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -c "$COOKIE_JAR" \
    -X POST "$URL/api/login" -H 'Content-Type: application/json' \
    -d "{\"password\":\"$PASSWORD\"}")
  expect "the login form accepts the password" "$code" "200"

  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -b "$COOKIE_JAR" \
    -X POST "$URL/api/rpc/ping")
  if [ "$code" = "401" ]; then
    fail "the session cookie is accepted by RPC"
  else
    pass "the session cookie is accepted by RPC (HTTP $code)"
  fi

  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -c "$COOKIE_JAR" \
    -X POST "$URL/api/login" -H 'Content-Type: application/json' \
    -d '{"password":"definitely-not-the-password"}')
  if [ "$code" = "200" ]; then
    fail "a wrong password is rejected"
  else
    pass "a wrong password is rejected (HTTP $code)"
  fi
else
  echo "  skip  password paths (set SUPERAI_PASSWORD)"
fi

step "static app"
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 "$URL/")
expect "the page loads without credentials, so a password can be typed" "$code" "200"

if [ -z "$TOKEN" ]; then
  echo
  echo "  skip  MCP paths (set SUPERAI_TOKEN)"
  [ "$FAILED" -eq 0 ] && { echo; echo "all checks passed"; exit 0; }
  echo
  echo "$FAILED check(s) failed"
  exit 1
fi

MCP="$URL/mcp"
HEADERS=(-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json'
  -H 'Accept: application/json, text/event-stream')

step "mcp handshake"
init_headers="$(mktemp)"
curl -s -D "$init_headers" --max-time 20 -X POST "$MCP" "${HEADERS[@]}" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}' >/dev/null
SID=$(grep -i '^mcp-session-id' "$init_headers" | tr -d '\r' | awk '{print $2}')
rm -f "$init_headers"
if [ -n "$SID" ]; then pass "initialize returns a session id"; else fail "initialize returns a session id"; exit 1; fi

mcp() {
  curl -s --max-time 60 -X POST "$MCP" "${HEADERS[@]}" -H "Mcp-Session-Id: $SID" -d "$1"
}
mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null

tools=$(mcp '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
for tool in create list enable delete run_now; do
  contains "tools/list offers superai_schedule_$tool" "$tools" "superai_schedule_$tool"
done

step "schedule lifecycle"
created=$(mcp "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"superai_schedule_create\",\"arguments\":{\"name\":\"$MARKER\",\"cron\":\"7 4 * * *\",\"prompt\":\"end-to-end probe for $MARKER, reply with the single word ok\"}}}")
contains "create reports success" "$created" '\"created\":true'
# A cron string is meaningless without knowing when it actually fires; the echo
# is what caught a whole VM running on UTC.
contains "create echoes the next runs" "$created" 'next_runs'

ID=$(mcp '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"superai_schedule_list","arguments":{}}}' |
  sed -n 's/^data: //p' |
  python3 -c "
import sys, json
rows = json.loads(sys.stdin.read())['result']['structuredContent']['schedules']
print(next((r['id'] for r in rows if '$MARKER' in r.get('prompt','') + r.get('note','')), ''))
" 2>/dev/null)
if [ -n "$ID" ]; then pass "the new schedule appears in list"; else fail "the new schedule appears in list"; fi

if [ -n "$ID" ]; then
  started=$(mcp "{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"tools/call\",\"params\":{\"name\":\"superai_schedule_run_now\",\"arguments\":{\"id\":\"$ID\"}}}")
  contains "run_now starts the task" "$started" '"started"'

  # The run goes through a real model call, so give it room before asking.
  sleep 45

  deleted=$(mcp "{\"jsonrpc\":\"2.0\",\"id\":6,\"method\":\"tools/call\",\"params\":{\"name\":\"superai_schedule_delete\",\"arguments\":{\"id\":\"$ID\"}}}")
  contains "delete removes the schedule" "$deleted" '"deleted"'

  remaining=$(mcp '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"superai_schedule_list","arguments":{}}}')
  case "$remaining" in
  *"$MARKER"*) fail "the deleted schedule is gone from list" ;;
  *) pass "the deleted schedule is gone from list" ;;
  esac
fi

echo
if [ "$FAILED" -eq 0 ]; then
  echo "all checks passed"
  exit 0
fi
echo "$FAILED check(s) failed"
exit 1
