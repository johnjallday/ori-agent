#!/usr/bin/env bash
#
# Runtime negative-route matrix for the retired Ask Ori persona.
#
# Ori's protected system assistant is gone: Help answers product questions,
# the configured Personal Assistant handles cross-workspace work, and each
# workspace's own manager handles that workspace's work. This drives a real
# server, in a throwaway sandbox, through the cases where the persona could
# leak back in, and fails on any live route to it:
#
#   fresh    a fresh start with a Default model creates no assistant record;
#            chat outside a workspace with no Personal Assistant says how to
#            set one up instead of answering as anyone.
#   existing a restart on a profile that still holds the old marked record:
#            the record stays on disk byte-for-byte but no list, lookup or
#            chat reaches it; a workspace whose entry was that record has no
#            runnable entry agent; a new Blank workspace proposes its own
#            manager.
#   names    a person's own agent called "Ask Ori" is ordinary: it can be
#            created, read and deleted, and the old record is untouched.
#
# Nothing outside the sandbox is read or written. The sandbox is removed on
# success and kept (path printed) on failure.
#
# Usage: ./scripts/verify-ask-ori-retired.sh [port]

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/ori-agent"
PORT="${1:-8962}"
URL="http://127.0.0.1:$PORT"
ROOT="${TMPDIR:-/tmp}/ask-ori-retired-$$"

if [[ ! -x "$BIN" ]]; then
  echo "Server binary missing at $BIN — run ./scripts/build-server.sh first." >&2
  exit 1
fi
if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Port $PORT is already in use; pass a free port." >&2
  exit 1
fi

mkdir -p "$ROOT"
cat >"$ROOT/settings.json" <<'JSON'
{ "system_provider": "claude_code", "system_model": "sonnet" }
JSON
echo "Sandbox: $ROOT"

FAILURES=0
pass() { echo "  PASS  $1"; }
fail() {
  echo "  FAIL  $1"
  FAILURES=$((FAILURES + 1))
}

SERVER_PID=""
boot() {
  local label="$1"
  (cd "$ROOT" && HOME="$ROOT" ORI_DATA_DIR="$ROOT" PORT="$PORT" ORI_NO_DESKTOP_OPEN=1 exec "$BIN") \
    >"$ROOT/server-$label.log" 2>&1 &
  SERVER_PID=$!
  for _ in $(seq 1 120); do
    curl -sf "$URL/" -o /dev/null 2>/dev/null && return 0
    sleep 0.25
  done
  echo "server did not come up; see $ROOT/server-$label.log" >&2
  return 1
}

stop() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
  fi
  SERVER_PID=""
  sleep 0.5
}
trap stop EXIT

agent_names() { curl -s "$URL/api/agents" | grep -o '"name":"[^"]*"' | cut -d'"' -f4; }
agent_status() { curl -s -o /dev/null -w '%{http_code}' "$URL/api/agents?name=$1"; }
chat() { curl -s -X POST "$URL/api/chat" -H 'Content-Type: application/json' -H "Origin: $URL" -d "$1"; }

echo
echo "=== fresh: start with a Default model configured ==="
boot fresh || exit 1
curl -s -X POST "$URL/api/onboarding/skip" -o /dev/null
if agent_names | grep -qx 'Ask Ori'; then fail "a fresh start lists Ask Ori"; else pass "a fresh start lists no Ask Ori"; fi
if [[ -e "$ROOT/agents/Ask Ori" ]]; then fail "a fresh start wrote an Ask Ori record"; else pass "no Ask Ori record on disk"; fi
REPLY="$(chat '{"question":"hello"}')"
if grep -q 'Personal Assistant' <<<"$REPLY" && grep -q 'no personal assistant configured' <<<"$REPLY"; then
  pass "global chat without a Personal Assistant gives the setup message"
else
  fail "global chat without a Personal Assistant: $REPLY"
fi
stop

echo
echo "=== existing: restart on a profile holding the old marked record ==="
mkdir -p "$ROOT/agents/Ask Ori"
cat >"$ROOT/agents/Ask Ori/agent_settings.json" <<'JSON'
{
  "role": "orchestrator",
  "Settings": { "model": "sonnet", "provider": "claude_code", "system_prompt": "You are Ori's system orchestrator assistant." },
  "metadata": { "tags": ["system", "orchestrator", "assistant", "ori:system-assistant"] }
}
JSON
boot existing || exit 1
if agent_names | grep -qx 'Ask Ori'; then fail "the old record is listed"; else pass "the old record is not listed"; fi
if [[ "$(agent_status 'Ask%20Ori')" == "404" ]]; then pass "the old record is not returned by name"; else fail "GET Ask Ori returned the old record"; fi
for retired in 'Workspace%20Manager' 'Ori'; do
  if [[ "$(agent_status "$retired")" == "404" ]]; then pass "$retired is not an alias"; else fail "$retired resolved"; fi
done

# A workspace whose stored entry is the old record (created before retirement).
curl -s -X POST "$URL/api/agents" -H 'Content-Type: application/json' -d '{"name":"Temp Lead"}' -o /dev/null
WS_ID="$(curl -s -X POST "$URL/api/workspaces" -H 'Content-Type: application/json' \
  -d '{"name":"Old Blank","agents":["Temp Lead"],"entry_agent_name":"Temp Lead"}' |
  grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)"
stop
# The first load canonicalizes every stored definition (as earlier releases
# already did on a real profile); from here on nothing may touch the record.
RECORD_SUM="$(shasum "$ROOT/agents/Ask Ori/agent_settings.json" | cut -d' ' -f1)"
# Point the workspace's stored lead at the old record, as a workspace created
# before retirement holds it: in the primary database and on disk.
sqlite3 "$ROOT/sessions.db" "UPDATE workspaces SET
  agents = replace(agents, 'Temp Lead', 'Ask Ori'),
  agent_instances = replace(agent_instances, 'Temp Lead', 'Ask Ori'),
  shared_data = replace(shared_data, 'Temp Lead', 'Ask Ori')
  WHERE id = '$WS_ID';"
while IFS= read -r f; do
  perl -pi -e 's/Temp Lead/Ask Ori/g' "$f"
done < <(grep -rl --include='workspace*.json' 'Temp Lead' "$ROOT/Ori Workspaces" "$ROOT/workspaces" 2>/dev/null)
boot existing-workspace || exit 1
ROSTER="$(curl -s "$URL/api/folders" | python3 -c '
import json, sys
data = json.load(sys.stdin)
items = data if isinstance(data, list) else data.get("folders") or data.get("workspaces") or []
ws = next((w for w in items if w.get("id") == sys.argv[1]), {})
print(json.dumps({"entry": ws.get("entry_agent_name") or "", "agents": ws.get("agents") or []}))
' "$WS_ID")"
if grep -q 'Ask Ori' <<<"$ROSTER"; then
  fail "the old workspace still names the retired assistant: $ROSTER"
else
  pass "the old workspace's roster no longer names the retired assistant"
fi
REPLY="$(chat "{\"question\":\"hello\",\"route_context\":{\"workspace_id\":\"$WS_ID\"}}")"
if grep -q 'no runnable entry agent' <<<"$REPLY"; then
  pass "a workspace led by the old record has no runnable entry agent"
else
  fail "a workspace led by the old record answered: $REPLY"
fi
PLAN="$(curl -s -X POST "$URL/api/workspaces/template-agent-plan" -H 'Content-Type: application/json' -d '{"blank":true,"workspace_name":"Legacy"}')"
if grep -q '"entry_agent_name":"Legacy Manager"' <<<"$PLAN" && ! grep -q 'Ask Ori' <<<"$PLAN"; then
  pass "a new Blank workspace proposes its own manager"
else
  fail "Blank plan: $PLAN"
fi

echo
echo "=== names: a person's own Ask Ori is ordinary ==="
# Start from a profile with no workspace at all, so the person's new agent is
# unattached and its delete exercises only the name.
curl -s -o /dev/null -X DELETE "$URL/api/workspaces/$WS_ID?confirm=true"
CREATED="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$URL/api/agents" -H 'Content-Type: application/json' -d '{"name":"Ask Ori"}')"
if [[ "$CREATED" == "200" || "$CREATED" == "201" ]]; then pass "a person can create their own Ask Ori"; else fail "creating a user Ask Ori returned $CREATED"; fi
if [[ "$(agent_status 'Ask%20Ori')" == "200" ]] && ! curl -s "$URL/api/agents?name=Ask%20Ori" | grep -q 'ori:system-assistant'; then
  pass "the person's Ask Ori is theirs, not the old record"
else
  fail "the person's Ask Ori did not read back as their own"
fi
DELETED="$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$URL/api/agents?name=Ask%20Ori")"
if [[ "$DELETED" == "200" || "$DELETED" == "204" ]]; then pass "the person's Ask Ori can be deleted"; else fail "deleting a user Ask Ori returned $DELETED"; fi
stop

if [[ -f "$ROOT/agents/Ask Ori/agent_settings.json" ]] &&
  [[ "$(shasum "$ROOT/agents/Ask Ori/agent_settings.json" | cut -d' ' -f1)" == "$RECORD_SUM" ]]; then
  pass "the old record is still on disk, unchanged"
else
  fail "the old record was removed or rewritten"
fi

echo
if ((FAILURES > 0)); then
  echo "$FAILURES check(s) failed. Sandbox kept: $ROOT"
  exit 1
fi
echo "All checks passed."
rm -rf "$ROOT"
