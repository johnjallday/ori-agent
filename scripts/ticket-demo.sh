#!/usr/bin/env bash
# Group-1 demo/verification for the canonical Ticket API
# (tasks/prd-workspace-ticket-management.md).
#
# Exercises the acceptance path the checklist's Demo step calls for: create one
# Backlog and one Ready Ticket, read both back, confirm immutable numbers,
# attempt an illegal Backlog action, and prove no parallel work record appears.
#
# Usage: ./scripts/ticket-demo.sh <studio_id> [base_url]
#
# It is a script rather than a pasted pipeline because the same sequence is run
# after every fix; one stable entrypoint is one permission decision instead of
# a fresh prompt per invocation (repo CLAUDE.md > Shell Discipline).
set -euo pipefail

STUDIO_ID="${1:?usage: ticket-demo.sh <studio_id> [base_url]}"
BASE_URL="${2:-http://localhost:8931}"
TICKETS="$BASE_URL/api/workspaces/$STUDIO_ID/tickets"

say() { printf '\n=== %s ===\n' "$1"; }

say "1. Create a Backlog Ticket (explicit capture choice)"
BACKLOG=$(curl -sS -X POST "$TICKETS" -H 'Content-Type: application/json' \
  -d '{"state":"backlog","title":"Investigate the flaky test","description":"Fails ~1 in 20 runs.","tags":["infra"],"priority":2}')
echo "$BACKLOG"
BACKLOG_ID=$(echo "$BACKLOG" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

say "2. Create a Ready Ticket"
READY=$(curl -sS -X POST "$TICKETS" -H 'Content-Type: application/json' \
  -d '{"state":"ready","title":"Ship the ticket foundation"}')
echo "$READY"
READY_ID=$(echo "$READY" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

say "3. Creation cannot be used as a lifecycle shortcut (expect 400)"
curl -sS -o /dev/null -w 'POST state=done -> HTTP %{http_code}\n' \
  -X POST "$TICKETS" -H 'Content-Type: application/json' \
  -d '{"state":"done","title":"skip the queue"}'

say "4. Read both back (numbers are workspace-local and immutable)"
curl -sS "$TICKETS" | tr ',' '\n' | grep -E '"(id|number|display_number|state|title)"' || true

say "5. Illegal Backlog action: Backlog -> Done (expect 409 + legal transitions)"
curl -sS -X POST "$TICKETS/$BACKLOG_ID/transition" \
  -H 'Content-Type: application/json' -d '{"to":"done"}'
echo

say "6. Legal promotion: Backlog -> Ready (identity must survive)"
curl -sS -X POST "$TICKETS/$BACKLOG_ID/transition" \
  -H 'Content-Type: application/json' -d '{"to":"ready","reason":"committing"}' \
  | tr ',' '\n' | grep -E '"(id|number|state|version)"' || true

say "7. Repeat the same promotion (must be idempotent, not a duplicate)"
curl -sS -X POST "$TICKETS/$BACKLOG_ID/transition" \
  -H 'Content-Type: application/json' -d '{"to":"ready"}' \
  | tr ',' '\n' | grep -E '"(id|number|state|version)"' || true

say "8. Stale version is refused (expect 409)"
curl -sS -o /dev/null -w 'PATCH version=99 -> HTTP %{http_code}\n' \
  -X PATCH "$TICKETS/$READY_ID" -H 'Content-Type: application/json' \
  -d '{"title":"stale write","version":99}'

say "9. No parallel work record: legacy task API sees the SAME records"
curl -sS "$BASE_URL/api/orchestration/tasks?workspace_id=$STUDIO_ID" \
  | tr ',' '\n' | grep -cE '"id":' || true

say "10. Final ticket count"
curl -sS "$TICKETS" | tr ',' '\n' | grep -c '"id":' || true
