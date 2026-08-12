#!/usr/bin/env bash
# Group-5 demo for tasks/prd-workspace-ticket-management.md: Note ↔ Ticket
# creation and linking, and the independence guarantees that make it safe.
#
# The point of this script is the NEGATIVE assertions: creating from a Note
# must not convert it, linking must not modify it, and deleting either side
# must not delete the other.
#
# Usage: ./scripts/ticket-notes-demo.sh <studio_id> [base_url]
set -euo pipefail

STUDIO_ID="${1:?usage: ticket-notes-demo.sh <studio_id> [base_url]}"
BASE_URL="${2:-http://localhost:8931}"
TICKETS="$BASE_URL/api/workspaces/$STUDIO_ID/tickets"
NOTES="$BASE_URL/api/workspaces/$STUDIO_ID/notes"

say() { printf '\n=== %s ===\n' "$1"; }
field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }

say "Create two notes"
NOTE1=$(curl -sS -X POST "$NOTES" -H 'Content-Type: application/json' \
  -d '{"name":"Research spike","content":"# Findings\nThe cache is cold on boot."}')
NOTE2=$(curl -sS -X POST "$NOTES" -H 'Content-Type: application/json' \
  -d '{"name":"Meeting notes","content":"Agreed to fix the cache."}')
N1=$(echo "$NOTE1" | field id)
N2=$(echo "$NOTE2" | field id)
echo "note1=$N1  note2=$N2"

say "Create a Ticket FROM note 1 (reviewed values, explicit capture state)"
TICKET=$(curl -sS -X POST "$NOTES/$N1/tickets" -H 'Content-Type: application/json' \
  -d '{"state":"backlog","title":"Fix the cold cache","description":"Reviewed by the user"}')
echo "$TICKET"
TID=$(echo "$TICKET" | field id)

say "The note was NOT converted, moved, or modified (FR-74, FR-18)"
curl -sS "$NOTES/$N1" | tr ',' '\n' | grep -E '"(id|name|content)"' || true

say "Creating without an explicit capture state is refused (FR-19)"
curl -sS -o /dev/null -w 'POST without state -> HTTP %{http_code}  (expect 400)\n' \
  -X POST "$NOTES/$N1/tickets" -H 'Content-Type: application/json' -d '{"title":"no state"}'

say "Link the second note; repeat it to prove idempotence (FR-77)"
curl -sS -X POST "$TICKETS/$TID/notes" -H 'Content-Type: application/json' \
  -d "{\"note_id\":\"$N2\"}" | tr ',' '\n' | grep -E '"(linked_note_ids|version)"' || true
curl -sS -X POST "$TICKETS/$TID/notes" -H 'Content-Type: application/json' \
  -d "{\"note_id\":\"$N2\"}" | tr ',' '\n' | grep -E '"(linked_note_ids|version)"' || true

say "Both directions of the relationship agree (FR-75, FR-78)"
echo "ticket -> notes:"
curl -sS "$TICKETS/$TID/notes" | tr ',' '\n' | grep -E '"(id|title)"' || true
echo "note1 -> tickets:"
curl -sS "$NOTES/$N1/tickets" | tr ',' '\n' | grep -E '"(id|display_number|title|state)"' || true

say "Linking a note from ANOTHER workspace is refused (FR-17)"
OTHER=$(curl -sS -X POST "$BASE_URL/api/workspaces" -H 'Content-Type: application/json' \
  -d '{"name":"Other Studio For Notes"}')
OTHER_ID=$(echo "$OTHER" | sed -n 's/.*"folder":{"id":"\([^"]*\)".*/\1/p')
FOREIGN=$(curl -sS -X POST "$BASE_URL/api/workspaces/$OTHER_ID/notes" \
  -H 'Content-Type: application/json' -d '{"name":"Foreign note","content":"nope"}')
FID=$(echo "$FOREIGN" | field id)
curl -sS -o /dev/null -w 'link foreign note -> HTTP %{http_code}  (expect 404)\n' \
  -X POST "$TICKETS/$TID/notes" -H 'Content-Type: application/json' -d "{\"note_id\":\"$FID\"}"

say "Editing the Ticket does not rewrite the Note (FR-76 — one-time copy)"
curl -sS -o /dev/null -X PATCH "$TICKETS/$TID" -H 'Content-Type: application/json' \
  -d '{"title":"Ticket went its own way","description":"ticket body only"}'
echo "note1 name after ticket edit:"
curl -sS "$NOTES/$N1" | tr ',' '\n' | grep -E '"name"' || true
echo "ticket title after edit:"
curl -sS "$TICKETS/$TID" | tr ',' '\n' | grep -E '"title"' || true

say "Unlink removes the reference only; the note survives (FR-18)"
curl -sS -X DELETE "$TICKETS/$TID/notes" -H 'Content-Type: application/json' \
  -d "{\"note_id\":\"$N2\"}" | tr ',' '\n' | grep -E '"linked_note_ids"' || true
curl -sS -o /dev/null -w 'note2 still readable -> HTTP %{http_code}  (expect 200)\n' "$NOTES/$N2"

say "Deleting the Ticket does not delete its linked Note (FR-72)"
curl -sS -o /dev/null -X DELETE "$TICKETS/$TID"
curl -sS -o /dev/null -w 'note1 still readable -> HTTP %{http_code}  (expect 200)\n' "$NOTES/$N1"
