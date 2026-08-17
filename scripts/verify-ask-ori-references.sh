#!/usr/bin/env bash
#
# Verifies that persisted references to the system assistant survive the Ask Ori
# identity migration (Issue #350, FR52/FR53/FR57).
#
# Migration deliberately does NOT rewrite stored references — that would touch
# ids, timestamps and history ordering it promises to leave alone — so a stored
# "Workspace Manager" has to keep resolving forward at read time instead. This
# rehearses exactly that: a workspace saved before the rename, opened after it.
#
# Usage: ./scripts/verify-ask-ori-references.sh [port]

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/ori-agent"
PORT="${1:-8961}"
ROOT="${TMPDIR:-/tmp}/ask-ori-refs-$$"

if [[ ! -x "$BIN" ]]; then
  echo "Server binary missing at $BIN — run ./scripts/build-server.sh first." >&2
  exit 1
fi

mkdir -p "$ROOT"
cat > "$ROOT/settings.json" <<'JSON'
{ "system_provider": "claude_code", "system_model": "sonnet" }
JSON
echo "Sandbox: $ROOT"

SERVER_PID=""
boot() {
  local label="$1"
  ( cd "$ROOT" && HOME="$ROOT" ORI_DATA_DIR="$ROOT" PORT="$PORT" exec "$BIN" ) \
    > "$ROOT/server-$label.log" 2>&1 &
  SERVER_PID=$!
  for _ in $(seq 1 80); do
    curl -sf "http://127.0.0.1:$PORT/" -o /dev/null 2>/dev/null && return 0
    sleep 0.25
  done
  echo "server did not come up; see $ROOT/server-$label.log" >&2
  return 1
}

stop() {
  [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null
  wait "$SERVER_PID" 2>/dev/null
  SERVER_PID=""
  sleep 0.5
}

echo
echo "=== boot 1: create a workspace on the migrated identity ==="
boot first || exit 1

WS_ID="$(curl -s -X POST "http://127.0.0.1:$PORT/api/workspaces" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Legacy Refs","agents":["Ask Ori"],"entry_agent_name":"Ask Ori"}' \
  | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)"
echo "  workspace id: ${WS_ID:-<none>}"
if [[ -z "$WS_ID" ]]; then
  echo "  could not create the workspace" >&2
  stop
  exit 1
fi
curl -s "http://127.0.0.1:$PORT/api/orchestration/workspace?id=$WS_ID" \
  | grep -o '"entry_agent_name":"[^"]*"' | head -1 | sed 's/^/  before: /'
stop

echo
echo "=== rewrite the stored reference to the RETIRED name (pre-rename install) ==="
CHANGED=0
while IFS= read -r f; do
  if grep -q '"entry_agent_name"' "$f" 2>/dev/null; then
    perl -pi -e 's/("entry_agent_name"\s*:\s*)"Ask Ori"/$1"Workspace Manager"/g' "$f"
    echo "  patched: ${f#"$ROOT/"}"
    CHANGED=$((CHANGED + 1))
  fi
done < <(find "$ROOT" -name '*.json' -not -path '*/agents/*' 2>/dev/null)
echo "  files patched: $CHANGED"

echo
echo "=== boot 2: the stored legacy reference must still resolve ==="
boot second || exit 1

echo "  agents on disk: $(ls "$ROOT/agents" 2>/dev/null | tr '\n' ' ')"
AFTER="$(curl -s "http://127.0.0.1:$PORT/api/orchestration/workspace?id=$WS_ID")"
if printf '%s' "$AFTER" | grep -q "\"id\":\"$WS_ID\""; then
  echo "  workspace still loads under the SAME id: yes"
else
  echo "  workspace still loads under the same id: NO"
fi
printf '%s' "$AFTER" | grep -o '"entry_agent_name":"[^"]*"' | head -1 | sed 's/^/  after:  /'
printf '%s' "$AFTER" | grep -o '"name":"[^"]*"' | head -1 | sed 's/^/  name:   /'

echo "  the retired name still resolves to a runnable agent:"
curl -s "http://127.0.0.1:$PORT/api/agents?name=Workspace%20Manager" \
  -o /dev/null -w "    GET /api/agents?name=Workspace Manager -> %{http_code}\n"
curl -s "http://127.0.0.1:$PORT/api/agents?name=Ask%20Ori" \
  -o /dev/null -w "    GET /api/agents?name=Ask Ori           -> %{http_code}\n"

stop
echo
echo "Sandbox preserved: $ROOT"
