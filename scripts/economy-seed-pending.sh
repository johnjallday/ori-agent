#!/bin/bash
# Seed pending-harvest rows straight into a DEMO sandbox database.
#
# Why this exists: a genuine pending Harvest needs a Farm run to finish, which
# needs a working LLM provider. When the demo machine has no usable provider
# (no key, or an exhausted quota) the Home surface still has to be driven in a
# real browser, so this writes the rows a finished run would have written.
#
# It is a DEMO FIXTURE, never part of the product: nothing in the app calls it,
# and it only ever touches a throwaway `wt demo` sandbox. A row seeded here is
# indistinguishable from a real one to everything downstream, which is the
# point — it exercises the same read, the same pile, and the same banking path.
#
# Usage:  ./scripts/economy-seed-pending.sh <sandbox-db> <workspace-id> <task-id> [count]
set -euo pipefail

DB="${1:?usage: economy-seed-pending.sh <sandbox-db> <workspace-id> <task-id> [count]}"
WORKSPACE_ID="${2:?workspace id required}"
TASK_ID="${3:?task id required}"
COUNT="${4:-3}"

case "$DB" in
  *ori-demo.*) ;;
  *)
    echo "refusing: $DB is not a wt demo sandbox database" >&2
    exit 1
    ;;
esac

NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
# Run keys carry the seeding time: a second seeding run must add NEW pending
# rows, not silently collide with rows the previous one already banked.
BATCH="$(date -u +%s)"
for i in $(seq 1 "$COUNT"); do
  sqlite3 "$DB" "INSERT OR IGNORE INTO economy_harvest_pending
    (task_id, workspace_id, run_key, produced_at, harvested_at)
    VALUES ('$TASK_ID', '$WORKSPACE_ID', 'seeded-$BATCH-$i', '$NOW', NULL);"
done

echo "seeded $COUNT pending runs for task $TASK_ID"
sqlite3 "$DB" "SELECT task_id, run_key, harvested_at FROM economy_harvest_pending;"
