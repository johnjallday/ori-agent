#!/usr/bin/env bash
# Stateful inbox compatibility story, always on a fresh no-model sandbox.
set -euo pipefail
cd "$(dirname "$0")/.."
port="${ORI_INBOX_APP_PORT:-8973}"
[[ "$port" =~ ^[0-9]+$ ]] && (( port > 0 && port <= 65535 )) || exit 2
if lsof -nP -t -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Port $port is occupied; refusing to contact it." >&2; exit 2
fi
sandbox="$(mktemp -d "${TMPDIR:-/tmp}/ori-inbox-e2e.XXXXXX")"
server_pid=""
demo_pid=""
cleanup() {
  [[ -z "$server_pid" ]] || kill "$server_pid" 2>/dev/null || true
  [[ -z "$demo_pid" ]] || wait "$demo_pid" 2>/dev/null || true
}
trap cleanup EXIT
./scripts/demo-inbox-folder.sh --port "$port" --sandbox "$sandbox" --desktop-project-leads > "$sandbox/bootstrap.log" 2>&1 &
demo_pid=$!
for _ in $(seq 1 300); do
  grep -q '^READY:' "$sandbox/bootstrap.log" && break
  kill -0 "$demo_pid" 2>/dev/null || { echo "Bootstrap failed: $sandbox/bootstrap.log" >&2; exit 1; }
  sleep 1
done
grep -q '^READY:' "$sandbox/bootstrap.log" || { echo "Bootstrap timed out: $sandbox/bootstrap.log" >&2; exit 1; }
server_pid="$(lsof -nP -t -iTCP:"$port" -sTCP:LISTEN | sort -u)"
[[ "$server_pid" =~ ^[0-9]+$ ]] || exit 1
echo "SANDBOX=$sandbox"
./scripts/e2e.sh --port "$port" --env "ORI_INBOX_SANDBOX=$sandbox" \
  tests/folder-inbox-hire.spec.ts -- --project=chromium --workers=1 --output "$sandbox/results"
