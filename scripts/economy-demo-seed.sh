#!/bin/bash
# Seed a running demo server with a workspace, a hand-run task, and a Farm, then
# print the economy after each step.
#
# This exists because the City Economy's checkpoints all need the same four
# curls in the same order, and typing them as one compound shell command
# fragments differently every time (repo CLAUDE.md, "Shell Discipline"). One
# script is one stable name.
#
# Usage:  ./scripts/economy-demo-seed.sh [port]
# The server must already be running, e.g. from `wt demo`.
set -euo pipefail

PORT="${1:-8931}"
BASE="http://localhost:${PORT}"

economy() {
  printf '  economy: '
  curl -s "${BASE}/api/economy"
  printf '\n'
}

json_field() {
  # Reads one top-level-ish string field out of a JSON blob on stdin without
  # requiring jq, which is not guaranteed on a fresh machine.
  python3 -c 'import json,sys; print(json.loads(sys.stdin.read())["'"$1"'"]["'"$2"'"])'
}

echo "== starting point =="
economy

echo "== chat message (earns Craft) =="
curl -s -o /dev/null -X POST "${BASE}/api/chat" \
  -H 'Content-Type: application/json' \
  -d '{"question":"seed the economy demo","agent_name":""}'
economy

echo "== workspace =="
WORKSPACE_ID="$(curl -s -X POST "${BASE}/api/workspaces" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Economy Demo","description":"city-economy checkpoint"}' \
  | json_field folder id)"
echo "  workspace_id: ${WORKSPACE_ID}"

echo "== hand-run task, completed (earns Craft) =="
TASK_ID="$(curl -s -X POST "${BASE}/api/orchestration/tasks" \
  -H 'Content-Type: application/json' \
  -d "{\"workspace_id\":\"${WORKSPACE_ID}\",\"description\":\"A hand-run task\",\"priority\":2}" \
  | json_field task id)"
curl -s -o /dev/null -X POST "${BASE}/api/orchestration/tasks/${TASK_ID}/complete" \
  -H 'Content-Type: application/json' -d '{"result":"done by hand"}'
economy

echo "== Farm: a daily recurring task =="
FARM_ID="$(curl -s -X POST "${BASE}/api/orchestration/tasks" \
  -H 'Content-Type: application/json' \
  -d "{\"workspace_id\":\"${WORKSPACE_ID}\",\"description\":\"Daily inbox triage\",\"to\":\"Claude Code\",\"schedule\":{\"type\":\"daily\",\"time\":\"09:00\"},\"schedule_enabled\":true,\"schedule_name\":\"Inbox triage\"}" \
  | json_field task id)"
echo "  farm task_id: ${FARM_ID}"
economy

echo
echo "workspace_id=${WORKSPACE_ID}"
echo "task_id=${TASK_ID}"
echo "farm_id=${FARM_ID}"
