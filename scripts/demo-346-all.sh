#!/usr/bin/env bash
# Run every Issue #346 district demo, each against its OWN fresh sandbox.
#
# They cannot share one server. Each drives the camera with Fit all, which
# frames everything on the map, so a fixture left by an earlier demo pushes the
# zoom to its 10% floor and shrinks districts until their controls are
# sub-pixel. One demo per sandbox keeps every run reproducible.
#
# Usage: ./scripts/demo-346-all.sh [first-port]
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
PORT="${1:-8980}"
FAILED=()

run_demo() {
  local spec="$1" seed="${2:-}"
  echo ""
  echo "═══ ${spec} (port ${PORT}) ═══"

  "${ROOT}/scripts/demo-346.sh" "${PORT}" --fresh >/dev/null 2>&1 &
  local server=$!
  local waited=0
  until curl -sf -o /dev/null "http://localhost:${PORT}/api/workspaces"; do
    sleep 2
    waited=$((waited + 2))
    if [[ ${waited} -gt 90 ]]; then
      echo "server on ${PORT} never came up"
      FAILED+=("${spec} (server)")
      kill "${server}" 2>/dev/null
      PORT=$((PORT + 1))
      return
    fi
  done

  [[ -n "${seed}" ]] && "${ROOT}/scripts/${seed}" "${PORT}" >/dev/null 2>&1

  if "${ROOT}/scripts/e2e.sh" --port "${PORT}" --tail 6 "tests/${spec}"; then
    echo "PASS ${spec}"
  else
    echo "FAIL ${spec}"
    FAILED+=("${spec}")
  fi

  kill "${server}" 2>/dev/null
  wait "${server}" 2>/dev/null
  PORT=$((PORT + 1))
}

run_demo district-demo.spec.ts seed-346-demo.sh
run_demo district-grouping-demo.spec.ts
run_demo district-resize-demo.spec.ts
run_demo district-movement-demo.spec.ts
run_demo district-collapse-demo.spec.ts
run_demo district-appearance-demo.spec.ts
run_demo district-lifecycle-demo.spec.ts

echo ""
if [[ ${#FAILED[@]} -eq 0 ]]; then
  echo "All #346 demos passed."
else
  printf 'FAILED: %s\n' "${FAILED[@]}"
  exit 1
fi
