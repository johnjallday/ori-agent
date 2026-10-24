#!/usr/bin/env bash
#
# folder-hire-live-check.sh — hire an agent for a folder offer on a running
# sandboxed demo server and follow its first look to the end, through the same
# HTTP routes the card uses.
#
# Usage:
#   ./scripts/folder-hire-live-check.sh <port> <offer-id> [brief] [timeout-seconds]
#
# A software offer is reviewed first (Review setup reads files only). Then Hire
# and start is pressed on the plan the offer shows, with the brief when one is
# given, and the offer is read every few seconds until its first look finishes,
# fails, or the timeout passes. It prints the hire, the goal the workspace got,
# what the hire did about the look, the look's state, its suggested next task
# and the start of its result, as KEY=value lines.
#
# For an inbox folder (File Curator, folder-inbox-hire) it needs no model: it
# waits for the hire to resolve and prints the first batch's outcome and counts
# (INBOX=1, BATCH_*), and fails if a model task was started for it.
#
# It only talks to http://localhost:<port>; point it at a sandboxed server
# (scripts/demo-server.sh or `wt demo`), never at one over your real data.

set -euo pipefail

port="${1:-}"
offer="${2:-}"
brief="${3:-}"
timeout="${4:-600}"
if [[ -z "$port" || -z "$offer" ]]; then
	echo "usage: $(basename "$0") <port> <offer-id> [brief] [timeout-seconds]" >&2
	exit 2
fi
base="http://localhost:${port}/api/personal-assistant/folder-digest"

read_offer() {
	curl -fsS "${base}?offer_id=${offer}"
}

field() {
	python3 -c 'import json,sys
d=json.load(sys.stdin)
o=(d.get("folder_digest") or d).get("offer") or {}
v=o
for k in sys.argv[1].split("."):
    v=(v or {}).get(k) if isinstance(v,dict) else None
print("" if v is None else (json.dumps(v) if isinstance(v,(dict,list)) else v))' "$1"
}

if [[ -n "$(read_offer | field software)" && -z "$(read_offer | field software.review.evidence_revision)" ]]; then
	curl -fsS -X POST "${base}/offers/${offer}/review" -H 'Content-Type: application/json' \
		-d "{\"request_id\":\"live-review-$$\"}" >/dev/null
fi

digest="$(read_offer | field plan.digest)"
if [[ -z "$digest" ]]; then
	echo "NO_PLAN=the offer has no plan to confirm" >&2
	exit 1
fi
echo "HIRE=$(read_offer | field plan.hire)"
body="$(python3 -c 'import json,sys; print(json.dumps({"request_id":"live-hire-"+sys.argv[3],"plan_digest":sys.argv[1],"brief":sys.argv[2]}))' "$digest" "$brief" "$$")"
curl -fsS -X POST "${base}/offers/${offer}/setup" -H 'Content-Type: application/json' -d "$body" >/dev/null

# An inbox folder's first look is its first batch, prepared by a metadata-only
# scan: no model task exists, so this waits for the offer to resolve and reads
# the batch outcome instead of a look's state. It needs no model.
if [[ "$(read_offer | field subject.shape)" == "inbox" ]]; then
	deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do
		[[ "$(read_offer | field status)" == "resolved" ]] && break
		if [[ "$(read_offer | field setup.status)" == "stopped" ]]; then
			echo "SETUP_STOPPED=$(read_offer | field setup.stop_reason)"
			exit 1
		fi
		sleep 2
	done
	echo "INBOX=1"
	echo "STATUS=$(read_offer | field status)"
	echo "WORKSPACE_ROUTE=$(read_offer | field outcome.route)"
	echo "BATCH_OUTCOME=$(read_offer | field outcome.first_look.batch.outcome)"
	echo "BATCH_ELIGIBLE=$(read_offer | field outcome.first_look.batch.eligible)"
	echo "BATCH_NEEDS_REVIEW=$(read_offer | field outcome.first_look.batch.needs_review)"
	echo "BATCH_ROUTE=$(read_offer | field outcome.first_look.batch.route)"
	echo "TASK_STARTED=$(read_offer | field outcome.first_look.started)"
	echo "TASK_ID=$(read_offer | field outcome.first_look.task_id)"
	[[ "$(read_offer | field status)" == "resolved" && -n "$(read_offer | field outcome.first_look.batch.outcome)" &&
		-z "$(read_offer | field outcome.first_look.task_id)" ]] || exit 1
	exit 0
fi

deadline=$((SECONDS + timeout))
state=""
while ((SECONDS < deadline)); do
	state="$(read_offer | field first_task.state)"
	case "$state" in
	finished | failed) break ;;
	esac
	if [[ "$(read_offer | field setup.status)" == "stopped" ]]; then
		echo "SETUP_STOPPED=$(read_offer | field setup.stop_reason)"
		break
	fi
	sleep 5
done

echo "STATUS=$(read_offer | field status)"
echo "FIRST_LOOK_RECORD=$(read_offer | field outcome.first_look)"
echo "STATE=${state}"
echo "AGENT=$(read_offer | field first_task.agent)"
echo "REASON=$(read_offer | field first_task.reason)"
echo "NEXT=$(read_offer | field first_task.next_request)"
echo "EXCERPT=$(read_offer | field first_task.result_excerpt)"
workspace="$(read_offer | field outcome.workspace_id)"
if [[ -n "$workspace" ]]; then
	echo "GOAL=$(curl -fsS "http://localhost:${port}/api/workspaces/${workspace}" | python3 -c 'import json,sys
d=json.load(sys.stdin)
w=d.get("workspace") or d.get("folder") or d
print(w.get("mission") or (w.get("mission_state") or {}).get("mission") or (w.get("shared_data") or {}).get("workspace_bootstrap",{}).get("goal",""))')"
fi
