#!/usr/bin/env bash
# Builds a realistic Personal HQ on an isolated demo server through its public
# API, prepares its portable checkpoint and copies ONLY that folder somewhere
# else — the "other computer" half of a workspace continuity demo.
#
#   ./scripts/continuity-demo-seed.sh <base-url> <server-sandbox-dir> <transfer-dir>
#
# e.g. start a source with `./scripts/demo-server.sh 8931 "$TMPDIR/ori-cont-source"`,
# then: ./scripts/continuity-demo-seed.sh http://localhost:8931 "$TMPDIR/ori-cont-source" "$TMPDIR/ori-cont-transfer"
#
# Synthetic data only. No model is needed: briefs are generated without one.
# The copied folder is printed on the last line; import it on a second demo
# server with Import Folder.
set -euo pipefail

BASE="${1:?base url, e.g. http://localhost:8931}"
SANDBOX="${2:?sandbox directory of the source server}"
TRANSFER="${3:?directory to copy the workspace folder into}"

json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
get() { curl -sS "$BASE$1"; }
post() { curl -sS -X POST "$BASE$1" -H 'Content-Type: application/json' -d "$2"; }

# A just-started demo server takes a while to build and listen.
for _ in $(seq 1 90); do
  curl -s -o /dev/null "$BASE/" && break
  sleep 2
done
curl -sS -o /dev/null "$BASE/" || { echo "no server at $BASE" >&2; exit 1; }

post /api/onboarding/complete '{}' >/dev/null || true

# Hire Ada and build her HQ through the canonical endpoints.
post /api/personal-assistant/hire \
  '{"request_id":"demo-hire","if_version":0,"display_name":"Ada","mandate":"Keep the garden project on track.","focus_areas":["plan_my_day"]}' >/dev/null
version=$(get /api/personal-assistant | json 'd["personal_assistant"]["state_version"]')
post /api/personal-assistant/hq "{\"request_id\":\"demo-hq\",\"if_version\":$version,\"name\":\"Garden HQ\",\"timezone\":\"UTC\"}" >/dev/null
hq=$(get /api/personal-hq/status | json 'd["status"]["workspace_id"]')
echo "HQ $hq"

# Commitments in several states.
followup() {
  post /api/personal-hq/followups "{\"category\":\"$1\",\"direction\":\"$2\",\"title\":\"$3\"}" | json 'd["followup"]["id"]'
}
followup i_owe outbound 'Order compost for the spring beds' >/dev/null
done_id=$(followup i_owe outbound 'Send the seed order')
snooze_id=$(followup waiting_on inbound 'Hear back from the soil lab')
post /api/personal-hq/followups/complete "{\"id\":\"$done_id\"}" >/dev/null
until=$(python3 -c 'import datetime; print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(days=3)).strftime("%Y-%m-%dT%H:%M:%SZ"))')
post /api/personal-hq/followups/snooze "{\"id\":\"$snooze_id\",\"until\":\"$until\"}" >/dev/null

# Two Daily Brief revisions today (manual refreshes never dedupe).
for _ in 1 2; do
  post /api/personal-hq/brief/refresh '{}' >/dev/null
  for _ in $(seq 1 30); do
    state=$(get /api/personal-hq/brief/status | json 'd.get("status","idle")')
    [ "$state" = "running" ] || [ "$state" = "pending" ] || break
    sleep 1
  done
done

# A conversation with an uploaded file.
chat=$(post /api/sessions "{\"title\":\"Planning the beds\",\"folder_id\":\"$hq\"}" | json 'd.get("session",d).get("id")')
post "/api/sessions/$chat/messages" '{"role":"user","content":"Where should the raised beds go?"}' >/dev/null
post "/api/sessions/$chat/messages" '{"role":"assistant","content":"Along the south fence, where they get full sun."}' >/dev/null
scratch="${TMPDIR:-/tmp}/continuity-demo-$$"
mkdir -p "$scratch"
printf 'Soil test: pH 6.5, low nitrogen.\n' >"$scratch/soil-test.txt"
curl -sS -o /dev/null -X POST "$BASE/api/sessions/$chat/files/upload" -F "file=@$scratch/soil-test.txt"
rm -rf "$scratch"

# Prepare the checkpoint and wait for Ready.
post "/api/workspaces/$hq/continuity/prepare" '{}' >/dev/null
for _ in $(seq 1 60); do
  state=$(get "/api/workspaces/$hq/continuity" | json 'd["continuity"]["state"]')
  [ "$state" = "ready" ] && break
  sleep 2
done
echo "checkpoint: $state"
[ "$state" = "ready" ] || exit 1

# Copy ONLY the workspace folder.
find_folder() { # sandbox, workspace id
  python3 - "$1" "$2" <<'PY'
import json, os, sys
root, wanted = sys.argv[1], sys.argv[2]
for base, dirs, files in os.walk(root):
    if "workspace.json" in files and ".ori" in dirs:
        try:
            with open(os.path.join(base, "workspace.json")) as f:
                if json.load(f).get("id") == wanted:
                    print(base)
                    break
        except (OSError, ValueError):
            pass
PY
}
folder=$(find_folder "$SANDBOX" "$hq")
[ -n "$folder" ] || { echo "workspace folder not found under $SANDBOX" >&2; exit 1; }
mkdir -p "$TRANSFER"
target="$TRANSFER/$(basename "$folder")"
[ ! -e "$target" ] || { echo "$target already exists" >&2; exit 1; }
cp -R "$folder" "$target"
echo "$target"
