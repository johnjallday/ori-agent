#!/usr/bin/env bash
#
# seed-agents-demo.sh — populate a throwaway demo/smoke server with a realistic
# mixed agent roster so the Agents Gallery can be reviewed against real data.
#
# Usage:
#   ./scripts/seed-agents-demo.sh [port] [count]
#
# Only ever point this at an isolated sandbox (`wt demo` / scripts/demo-server.sh).
# It creates agents, workspaces, and memberships through the normal public API.

set -euo pipefail

port="${1:-8931}"
count="${2:-0}"
base="http://localhost:$port"

if ! curl -sf -o /dev/null --max-time 3 "$base"; then
	printf 'Error: no server responding at %s\n' "$base" >&2
	exit 2
fi

post() {
	curl -sf -o /dev/null -X POST "$base$1" -H 'Content-Type: application/json' -d "$2" ||
		printf 'warn: POST %s failed\n' "$1" >&2
}

# A configured system model is what causes the server to materialise the
# reserved "Ori" system assistant; without it a fresh sandbox has none.
post /api/settings/system-model '{"provider":"openai","model":"gpt-4o-mini"}'

# Named agents spanning every role, both health states, favourites, tags, and
# a custom avatar colour, so each card variant is visible at a glance.
post /api/agents '{"name":"Atlas","type":"orchestration","role":"orchestrator","model":"gpt-4o-mini","description":"Turns broad outcomes into sequenced plans, then keeps specialist work moving toward the result.","tags":["planning","delivery","lead"],"favorite":true}'
post /api/agents '{"name":"Scout","type":"research","role":"researcher","model":"gpt-4o-mini","description":"Finds primary sources, tests competing claims, and leaves a concise evidence trail for the team.","tags":["research"]}'
post /api/agents '{"name":"Ledger","type":"tool-calling","role":"analyzer","description":"Reconciles messy operational data and turns it into decisions with visible assumptions and checks.","tags":["finance"]}'
post /api/agents '{"name":"Muse","type":"general","role":"synthesizer","model":"gpt-4o-mini","description":"Shapes scattered notes and research into clear narratives, briefs, and polished client-ready language.","tags":["writing","editorial"],"favorite":true}'
post /api/agents '{"name":"Sentinel","type":"tool-calling","role":"validator","model":"gpt-4o-mini","description":"Challenges plans, checks edge cases, and verifies that what looks complete actually holds together.","tags":["qa"]}'
post /api/agents '{"name":"Forge","type":"tool-calling","role":"specialist","model":"gpt-4o-mini","description":"Builds narrow, testable product slices and keeps implementation details tied to the user-facing outcome.","tags":["build","product"],"avatar_color":"#7c3aed"}'
post /api/agents '{"name":"Beacon","type":"general","model":"gpt-4o-mini"}'
post /api/agents '{"name":"Meridian","type":"research","role":"researcher","model":"gpt-4o-mini","description":"Long-range market and competitor scanning with a bias toward primary filings over commentary.","tags":["market","research","weekly","external"]}'

# Two workspaces so the card overflow indicator and the workspace picker both
# have real membership references to render.
post /api/workspaces '{"name":"Product Launch","entry_agent_name":"Atlas"}'
post /api/workspaces '{"name":"Client Ops","entry_agent_name":"Muse"}'
post /api/workspaces '{"name":"Personal HQ","entry_agent_name":"Scout"}'

# Optional bulk filler for the large-roster responsiveness checks.
if [[ "$count" -gt 0 ]]; then
	for i in $(seq 1 "$count"); do
		post /api/agents "{\"name\":\"Bulk Agent $i\",\"type\":\"tool-calling\",\"model\":\"gpt-4o-mini\",\"tags\":[\"bulk\"]}"
	done
fi

printf 'Seeded %s\n' "$base"
curl -s "$base/api/agents/dashboard/list?sort_by=name&order=asc" |
	python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["total"],"agents:",", ".join(a["name"] for a in d["agents"]))'
