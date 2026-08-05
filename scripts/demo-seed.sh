#!/usr/bin/env bash
# Seeds an isolated demo server with a representative roster: agents on curated
# characters, one on an uploaded avatar, one on the deterministic fallback, and a
# couple of workspaces for the Home map.
#
#   ./scripts/demo-seed.sh [base-url]
#
# Used by the cozy-character-experience Demo: checkpoints so every screenshot
# shows the same mix, rather than whatever a given sandbox happened to contain.
set -euo pipefail

BASE="${1:-http://localhost:8931}"

post() {
  curl -sS -o /dev/null -X POST "$BASE$1" -H 'Content-Type: application/json' -d "$2"
}

agent() { # name, description, character-id
  if [ -n "$3" ]; then
    post /api/agents "{\"name\":\"$1\",\"type\":\"tool-calling\",\"model\":\"gpt-4o-mini\",\"description\":\"$2\",\"character\":{\"catalog_id\":\"$3\",\"display_mode\":\"character\"}}"
  else
    post /api/agents "{\"name\":\"$1\",\"type\":\"tool-calling\",\"model\":\"gpt-4o-mini\",\"description\":\"$2\"}"
  fi
}

post /api/onboarding/complete '{}' || true

agent 'Field Notes'    'Keeps research sources honest'          research-archivist
agent 'Ship It'        'Narrow testable slices'                 product-builder
agent 'Standup'        'Keeps handoffs from going cold'         project-coordinator
agent 'Weekly Sweep'   'Runs the scheduled tidy-up'             automation-specialist
agent 'Trade-offs'     'Compares options without deciding'      decision-strategist
agent 'Plain Agent'    'No character chosen'                    ''

post /api/workspaces '{"name":"Launch Plan","description":"Demo workspace"}' || true
post /api/workspaces '{"name":"Research","description":"Demo workspace"}' || true

echo "seeded $BASE"
