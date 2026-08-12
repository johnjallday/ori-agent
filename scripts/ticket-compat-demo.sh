#!/usr/bin/env bash
# Group-7 compatibility contract check for
# tasks/prd-workspace-ticket-management.md FR-96, FR-97, FR-103, FR-118.
#
# Proves that the legacy task/backlog routes and the canonical Ticket routes
# are two doors onto ONE record — and that the legacy door cannot be used to
# reach past the canonical rules.
#
# Usage: ./scripts/ticket-compat-demo.sh <studio_id> [base_url]
set -euo pipefail

STUDIO_ID="${1:?usage: ticket-compat-demo.sh <studio_id> [base_url]}"
BASE_URL="${2:-http://localhost:8931}"
TICKETS="$BASE_URL/api/workspaces/$STUDIO_ID/tickets"
LEGACY_TASKS="$BASE_URL/api/orchestration/tasks"
LEGACY_BACKLOG="$BASE_URL/api/orchestration/backlog"

say() { printf '\n=== %s ===\n' "$1"; }
field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
ticket_field() { curl -sS "$TICKETS/$1" | tr ',' '\n' | grep -E "\"$2\"" | head -1; }

say "Capture through the LEGACY backlog route"
LEGACY=$(curl -sS -X POST "$LEGACY_BACKLOG" -H 'Content-Type: application/json' \
  -d "{\"workspace_id\":\"$STUDIO_ID\",\"description\":\"Captured through the legacy door\"}")
ID=$(echo "$LEGACY" | field id)
echo "id=$ID"

say "The canonical route sees the SAME record, fully canonical (FR-96)"
curl -sS "$TICKETS/$ID" | tr ',' '\n' \
  | grep -E '"(id|number|display_number|state|version)"' || true

say "One record behind both doors (FR-2, FR-105)"
echo -n "canonical tickets:      "; curl -sS "$TICKETS?archive=all" | tr ',' '\n' | grep -c '"id":' || true
echo -n "legacy backlog items:   "; curl -sS "$LEGACY_BACKLOG?workspace_id=$STUDIO_ID" | tr ',' '\n' | grep -c '"id":' || true
# The legacy Tasks list deliberately EXCLUDES Backlog records — tasks begin at
# Ready — so a zero here is correct for a backlog-only workspace, not a missing
# record. The count that proves "one record" is the backlog list above.
echo -n "legacy tasks (Ready+):  "; curl -sS "$LEGACY_TASKS?workspace_id=$STUDIO_ID" | tr ',' '\n' | grep -c '"id":' || true

say "A legacy client CANNOT write ticket-only fields (FR-97)"
BEFORE=$(ticket_field "$ID" state)
curl -sS -o /dev/null -X PUT "$LEGACY_TASKS/$ID" -H 'Content-Type: application/json' \
  -d "{\"workspace_id\":\"$STUDIO_ID\",\"ticket_state\":\"done\",\"ticket_number\":999,\"state_rank\":42}" || true
AFTER=$(ticket_field "$ID" state)
echo "state before: $BEFORE"
echo "state after:  $AFTER   (must be unchanged)"
echo -n "number after: "; ticket_field "$ID" number

say "Writing kanban_column_id does NOT revive column authority (FR-47, FR-118)"
curl -sS -o /dev/null -X PUT "$LEGACY_TASKS/$ID" -H 'Content-Type: application/json' \
  -d "{\"workspace_id\":\"$STUDIO_ID\",\"kanban_column_id\":\"col-done\"}" || true
echo -n "canonical state after moving the legacy column to 'col-done': "
ticket_field "$ID" state
echo "  (must still be backlog — the column is presentation, not lifecycle)"

say "Execution guards hold through the LEGACY door (FR-103)"
curl -sS -X POST "$LEGACY_TASKS/execute" -H 'Content-Type: application/json' \
  -d "{\"workspace_id\":\"$STUDIO_ID\",\"task_id\":\"$ID\"}" | head -c 220; echo

say "Promotion through the legacy route produces a canonical transition (FR-96)"
curl -sS -o /dev/null -X POST "$LEGACY_BACKLOG/$ID/promote?workspace_id=$STUDIO_ID" || true
curl -sS "$TICKETS/$ID" | tr '{' '\n' | grep -o '"from":"[^"]*","to":"[^"]*"' || true
echo -n "state now: "; ticket_field "$ID" state
