#!/usr/bin/env bash
# Seeds an isolated demo server with a small, fixed Workspace Map: four
# workspaces, one group, and three saved coordinates.
#
#   ./scripts/demo-seed-map.sh [base-url]
#
# Used by the #292 coordinate-map Demo: checkpoints so every screenshot shows
# the same arrangement — two buildings on the snap grid, one placed far away,
# and two records with no saved coordinate at all, which is what exercises
# deterministic fallback placement beside real anchors.
#
# `wt demo` discards its sandbox on exit, so this runs again after every
# rebuild. Keeping it in one script is also what keeps the demo loop from being
# a dozen ad-hoc curl invocations.
set -euo pipefail

BASE="${1:-http://localhost:8931}"

api() { # method, path, body
	curl -sS -o /dev/null -X "$1" "$BASE$2" -H 'Content-Type: application/json' -d "$3"
}

# Skip the first-run wizard: it covers the map with a modal.
api POST /api/onboarding/complete '{}'

for name in "Alpha Studio" "Beta Lab" "Gamma Works" "Delta Yard"; do
	api POST /api/workspaces "{\"name\":\"$name\"}"
done
api POST /api/workspaces '{"name":"Ops Group","kind":"group"}'

# Resolve the generated ids and save coordinates for three of them. Alpha sits
# far from the rest so panning and Fit All have something to do; Delta Yard and
# Ops Group are deliberately left unplaced.
python3 - "$BASE" <<'PY'
import json, sys, urllib.request

base = sys.argv[1]
with urllib.request.urlopen(base + "/api/workspaces") as response:
    folders = json.load(response)["folders"]
ids = {f["name"]: f["id"] for f in folders}

positions = {}
for name, point in (
    ("Beta Lab", {"x": 38, "y": 38}),
    ("Gamma Works", {"x": 418, "y": 38}),
    ("Alpha Studio", {"x": 760, "y": 456}),
):
    if name in ids:
        positions[ids[name]] = point

body = json.dumps({"operations": [{"op": "set_positions", "positions": positions}]}).encode()
request = urllib.request.Request(
    base + "/api/workspace-map/layout",
    data=body,
    method="PATCH",
    headers={"Content-Type": "application/json"},
)
with urllib.request.urlopen(request) as response:
    print("seeded", len(positions), "coordinates; revision",
          json.load(response)["result"]["revision"])
PY
