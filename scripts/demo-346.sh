#!/usr/bin/env bash
# Rebuild this worktree and (re)start the isolated Issue #346 demo server.
#
# Static assets are embedded with go:embed, so a CSS or JS change is invisible
# until the binary is rebuilt — this exists so that step cannot be skipped
# between a UI edit and the demo that is supposed to prove it.
#
# Isolation follows repo CLAUDE.md "Smoke Testing": HOME and ORI_DATA_DIR both
# point at a throwaway sandbox, and the server is launched from inside it so the
# plugin store is sandboxed too. $TMPDIR is used rather than `mktemp -d`, which
# agent sandboxes deny silently.
#
# Usage: ./scripts/demo-346.sh [port]
set -euo pipefail

PORT="${1:-8947}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SANDBOX="${ORI_DEMO_346_DIR:-/private/tmp/claude-502/smoke-346}"

echo "Building ${ROOT} ..."
(cd "${ROOT}" && go build -o bin/ori-agent ./cmd/server)

mkdir -p "${SANDBOX}"
if [[ ! -d "${SANDBOX}" ]]; then
  echo "Demo sandbox ${SANDBOX} could not be created" >&2
  exit 1
fi

# Stop a previous demo server started from this same binary, by PID. `pkill -f`
# is deliberately not used (repo CLAUDE.md).
existing="$(pgrep -f "${ROOT}/bin/ori-agent" || true)"
for pid in ${existing}; do
  echo "Stopping previous demo server ${pid}"
  kill "${pid}" || true
done
[[ -n "${existing}" ]] && sleep 2

echo "Sandbox: ${SANDBOX}"
echo "URL:     http://localhost:${PORT}"
cd "${SANDBOX}"
exec env HOME="${SANDBOX}" ORI_DATA_DIR="${SANDBOX}" PORT="${PORT}" "${ROOT}/bin/ori-agent"
