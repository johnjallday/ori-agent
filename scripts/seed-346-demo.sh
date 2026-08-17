#!/usr/bin/env bash
# Seed the Issue #346 demo scenario against an isolated demo server.
#
# It reproduces the reported failure exactly: three workspaces arranged near
# each other, grouped afterwards, and a group anchor left far below them by an
# earlier layout. Before this feature the district spanned that whole empty gap
# and Fit all zoomed the whole map out to reach it.
#
# Re-running is safe: records are looked up by name and only created when
# missing, so the scenario can be reset without deleting anything.
#
# Only ever point this at a sandboxed demo server (see repo CLAUDE.md
# "Smoke Testing").
#
# Usage: ./scripts/seed-346-demo.sh [port]
set -euo pipefail

PORT="${1:-8947}"
BASE="http://localhost:${PORT}"

# ensure "<name>" "<kind>" -> prints the id of that workspace, creating it first
# if no workspace by that name exists yet.
ensure() {
  local name="$1" kind="$2" id
  id=$(curl -sS "${BASE}/api/workspaces" |
    NAME="${name}" node -e "let s='';process.stdin.on('data',d=>s+=d).on('end',()=>{
      const hit=(JSON.parse(s).folders||[]).find(f=>f.name===process.env.NAME);
      if(hit)console.log(hit.id);
    })")
  if [[ -z "${id}" ]]; then
    id=$(curl -sS -X POST "${BASE}/api/workspaces" \
      -H 'Content-Type: application/json' \
      -d "{\"name\":\"${name}\",\"kind\":\"${kind}\"}" |
      node -e "let s='';process.stdin.on('data',d=>s+=d).on('end',()=>{
        console.log(JSON.parse(s).folder.id);
      })")
  fi
  echo "${id}"
}

reparent() {
  curl -sS -o /dev/null -X PUT "${BASE}/api/workspaces/$1" \
    -H 'Content-Type: application/json' \
    -d "{\"parent_id\":\"$2\"}"
}

GROUP=$(ensure "Campaign Ops" group)
A=$(ensure "Launch Brief" workspace)
B=$(ensure "Ad Copy" workspace)
C=$(ensure "Budget Model" workspace)
LOOSE=$(ensure "Unrelated Research" workspace)

echo "group=${GROUP}"
echo "members=${A} ${B} ${C}"
echo "outsider=${LOOSE}"

reparent "${A}" "${GROUP}"
reparent "${B}" "${GROUP}"
reparent "${C}" "${GROUP}"

# Members sit near the top of the world, tidily arranged. The group's own anchor
# is left 4000 units below them, which is what the fallback used to hand out
# when a group was created after the map had already been arranged.
curl -sS -o /dev/null -X PATCH "${BASE}/api/workspace-map/layout" \
  -H 'Content-Type: application/json' \
  -d "{\"operations\":[{\"op\":\"set_positions\",\"positions\":{
        \"${A}\":{\"x\":152,\"y\":152},
        \"${B}\":{\"x\":380,\"y\":152},
        \"${C}\":{\"x\":152,\"y\":380},
        \"${LOOSE}\":{\"x\":836,\"y\":152},
        \"${GROUP}\":{\"x\":152,\"y\":4180}
      }}]}"

echo "seeded: members near (152,152); stale group anchor at (152,4180)"
