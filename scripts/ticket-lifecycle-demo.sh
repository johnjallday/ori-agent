#!/usr/bin/env bash
# Group-4 lifecycle demo for tasks/prd-workspace-ticket-management.md.
#
# Walks a Ticket through the full workflow against a running demo server and
# checks the rules that matter: a run may not mark work Done, acceptance is
# explicit, closed work must be reopened before it can run again, and history
# survives every step.
#
# Usage: ./scripts/ticket-lifecycle-demo.sh <studio_id> [base_url]
set -euo pipefail

STUDIO_ID="${1:?usage: ticket-lifecycle-demo.sh <studio_id> [base_url]}"
BASE_URL="${2:-http://localhost:8931}"
TICKETS="$BASE_URL/api/workspaces/$STUDIO_ID/tickets"

say() { printf '\n=== %s ===\n' "$1"; }
# Pull one top-level field out of a JSON object without a JSON parser.
field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
state_of() { curl -sS "$TICKETS/$1" | sed -n 's/.*"state":"\([^"]*\)".*/\1/p'; }
transition() {
  curl -sS -o /dev/null -w '%{http_code}' -X POST "$TICKETS/$1/transition" \
    -H 'Content-Type: application/json' -d "{\"to\":\"$2\"}"
}

say "Create a Ready ticket"
TICKET=$(curl -sS -X POST "$TICKETS" -H 'Content-Type: application/json' \
  -d '{"state":"ready","title":"Lifecycle demo ticket"}')
echo "$TICKET"
ID=$(echo "$TICKET" | field id)

say "Assigning does not start work (FR-25)"
echo "state after create: $(state_of "$ID")  (awaiting intent, no run)"

say "Ready -> In Progress (explicit start)"
echo "HTTP $(transition "$ID" in_progress) · state=$(state_of "$ID")"

say "In Progress -> Done is REFUSED; acceptance happens from Review (FR-29)"
echo "HTTP $(transition "$ID" done)  (expect 409) · state=$(state_of "$ID")"

say "In Progress -> Review"
echo "HTTP $(transition "$ID" review) · state=$(state_of "$ID")"

say "Review -> In Progress (request changes / retry, FR-31)"
echo "HTTP $(transition "$ID" in_progress) · state=$(state_of "$ID")"

say "Back to Review, then explicit Done (FR-30)"
echo "HTTP $(transition "$ID" review) · state=$(state_of "$ID")"
echo "HTTP $(transition "$ID" done) · state=$(state_of "$ID")"

say "A Done ticket cannot resume execution; it reopens to Ready (FR-37)"
echo "HTTP $(transition "$ID" in_progress)  (expect 409) · state=$(state_of "$ID")"
echo "HTTP $(transition "$ID" ready) · state=$(state_of "$ID")"

say "Cancel, then reopen (FR-38, FR-39)"
echo "HTTP $(transition "$ID" cancelled) · state=$(state_of "$ID")"
echo "HTTP $(transition "$ID" ready) · state=$(state_of "$ID")"

say "Identity and full history survived every step (FR-3, FR-40)"
curl -sS "$TICKETS/$ID" | tr ',' '\n' | grep -E '"(id|number|display_number|state|version)"' || true
echo "--- state history ---"
curl -sS "$TICKETS/$ID" | tr '{' '\n' | grep -o '"from":"[^"]*","to":"[^"]*"' || true

say "Backlog work is not runnable through ANY path (FR-21, FR-103)"
BACKLOG=$(curl -sS -X POST "$TICKETS" -H 'Content-Type: application/json' \
  -d '{"state":"backlog","title":"not runnable"}')
BID=$(echo "$BACKLOG" | field id)
echo "canonical route  -> HTTP $(transition "$BID" in_progress)  (expect 409)"

# The real legacy execution endpoint. This is the FR-103 check that matters:
# the same Backlog guard must fire even though the call never touches a
# canonical Ticket route.
LEGACY_BODY=$(curl -sS -X POST "$BASE_URL/api/orchestration/tasks/execute" \
  -H 'Content-Type: application/json' \
  -d "{\"workspace_id\":\"$STUDIO_ID\",\"task_id\":\"$BID\"}")
echo "legacy execute route -> $LEGACY_BODY"
echo "  refers to Backlog: $(echo "$LEGACY_BODY" | grep -ci 'backlog' || true)  (expect >=1)"
echo "  backlog ticket state is unchanged: $(state_of "$BID")"
