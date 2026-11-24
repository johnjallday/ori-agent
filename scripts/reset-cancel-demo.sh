#!/usr/bin/env bash
#
# reset-cancel-demo.sh — reproduce the "workspace changed after Start Fresh was
# accepted" incident in an ISOLATED demo sandbox, step by step, so the recovery
# page's Cancel reset action can be driven and observed in a real browser.
#
# The sandbox is the one `scripts/demo-server.sh <port> <sandbox>` serves, so it
# is never the real HOME or data directory. Nothing here touches anything
# outside that sandbox; every path is checked against it before it is removed.
#
# Usage (the demo server is started and stopped separately, as tracked
# background processes — see "Smoke Testing" in CLAUDE.md):
#
#   ./scripts/reset-cancel-demo.sh accept <port> <sandbox>
#       With the demo server RUNNING on the sandbox: put a retained file in the
#       workspace folder, review Start Fresh, accept it and print the operation
#       id. The receipt is now awaiting a restart. Then stop the server.
#
#   ./scripts/reset-cancel-demo.sh empty <sandbox> [--remove | --finder-only | --with-vault]
#       With the server STOPPED: empty the retained workspace folder the way a
#       person would outside Ori (--remove deletes the folder itself, --finder-only
#       leaves just a .DS_Store, --with-vault also changes the vault folder, which
#       makes the block NOT cancelable). Then start the server again: it opens the
#       recovery page instead of the app.
#
#   ./scripts/reset-cancel-demo.sh receipt <sandbox>
#       Read-only: print the receipt's state, revision, blocker and how many
#       categories are still pending.

set -euo pipefail

die() {
	echo "$*" >&2
	exit 2
}

state_file() { printf '%s/.reset-cancel-demo.json' "$1"; }

canonical_sandbox() {
	local sandbox="${1:-}"
	[[ -n "$sandbox" && -d "$sandbox" ]] || die "sandbox directory not found: ${sandbox:-<none>}"
	[[ -e "$sandbox/.ori-demo-owner" ]] || die "not a demo sandbox (no .ori-demo-owner): $sandbox"
	(cd "$sandbox" && pwd -P)
}

cmd="${1:-}"
[[ -n "$cmd" ]] || die "usage: $0 accept <port> <sandbox> | empty <sandbox> [--remove|--finder-only] | receipt <sandbox>"
shift

case "$cmd" in
accept)
	port="${1:-}"
	[[ "$port" =~ ^[0-9]+$ ]] || die "port must be a number"
	sandbox="$(canonical_sandbox "${2:-}")"
	base="http://localhost:$port"
	curl -fsS --max-time 5 -o /dev/null "$base/" || die "no demo server answers on $base"

	preview="$(curl -fsS --max-time 30 -H 'X-Requested-With: XMLHttpRequest' "$base/api/reset/preview?intent=start_fresh")"
	[[ "$(jq '.blockers | length' <<<"$preview")" == 0 ]] || die "Start Fresh review has blockers: $(jq -c .blockers <<<"$preview")"
	# The retained workspace is the absolute retained location that is not the vault folder.
	workspace="$(jq -r '[.categories[0].retained[].display_path | select(startswith("/") and (endswith("/vaults") | not))][0]' <<<"$preview")"
	case "$workspace" in "$sandbox"/*) ;; *) die "the retained workspace is outside the sandbox: $workspace" ;; esac
	mkdir -p "$workspace"
	printf 'A retained file. Start Fresh must never remove this.\n' >"$workspace/keep-me.md"
	# The review hashes retained folders, so review AFTER the file exists.
	preview="$(curl -fsS --max-time 30 -H 'X-Requested-With: XMLHttpRequest' "$base/api/reset/preview?intent=start_fresh")"
	preview_id="$(jq -r .id <<<"$preview")"
	operation_id="$(jq -r .operation_id <<<"$preview")"
	agents_folder="$(jq -r '.agents_folder.path // empty' <<<"$preview")"
	body="$(jq -nc --arg preview "$preview_id" --arg agents "$agents_folder" \
		'{preview_id: $preview, request_id: "reset-cancel-demo-request", confirmation: "RESET"} + (if $agents == "" then {} else {confirm_agents_folder: $agents} end)')"
	reply="$(curl -fsS --max-time 60 -X POST -H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest' \
		-H "Origin: $base" --data "$body" "$base/api/reset")"
	state="$(jq -r '.operation.state' <<<"$reply")"
	[[ "$state" == "awaiting_restart" ]] || die "Start Fresh was not staged: $reply"
	vault="$(jq -r '[.categories[0].retained[].display_path | select(startswith("/") and endswith("/vaults"))][0] // empty' <<<"$preview")"
	jq -nc --arg op "$operation_id" --arg ws "$workspace" --arg vault "$vault" '{operation_id: $op, workspace: $ws, vault: $vault}' >"$(state_file "$sandbox")"
	echo "OPERATION=$operation_id"
	echo "WORKSPACE=$workspace"
	echo "STATE=$state"
	echo "Now stop the demo server, then: $0 empty $sandbox"
	;;

empty)
	sandbox="$(canonical_sandbox "${1:-}")"
	mode="${2:-}"
	[[ -f "$(state_file "$sandbox")" ]] || die "run '$0 accept' first"
	workspace="$(jq -r .workspace "$(state_file "$sandbox")")"
	case "$workspace" in "$sandbox"/*) ;; *) die "refusing to touch a folder outside the sandbox: $workspace" ;; esac
	[[ "$workspace" != "$sandbox" && "$workspace" != "$sandbox/" ]] || die "refusing to touch the sandbox root"
	if lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | grep -q "$sandbox"; then
		die "a server still runs from this sandbox; stop it first"
	fi
	# Everything removed here is inside the sandbox's own workspace folder.
	case "$mode" in
	--remove)
		rm -rf -- "$workspace"
		echo "REMOVED=$workspace"
		;;
	--finder-only)
		find "$workspace" -mindepth 1 -delete
		printf 'finder metadata\n' >"$workspace/.DS_Store"
		echo "EMPTIED (Finder metadata only)=$workspace"
		;;
	--with-vault)
		# The vault folder changes too, so Ori can no longer attribute the block
		# to the workspace alone: the recovery page must NOT offer Cancel.
		vault="$(jq -r '.vault // empty' "$(state_file "$sandbox")")"
		case "$vault" in "$sandbox"/*) ;; *) die "the vault folder is outside the sandbox: ${vault:-<none>}" ;; esac
		find "$workspace" -mindepth 1 -delete
		find "$vault" -mindepth 1 -delete 2>/dev/null || true
		printf 'changed\n' >"$vault/changed-outside-ori.txt"
		echo "EMPTIED workspace=$workspace and changed vault=$vault"
		;;
	"")
		find "$workspace" -mindepth 1 -delete
		echo "EMPTIED=$workspace"
		;;
	*) die "unknown mode: $mode" ;;
	esac
	echo "Now start the demo server on the same sandbox: it opens the recovery page."
	;;

receipt)
	sandbox="$(canonical_sandbox "${1:-}")"
	receipt="$sandbox/.ori-reset/operation.json"
	[[ -f "$receipt" ]] || die "no receipt at $receipt"
	jq -c '{state: .operation.state, revision: .operation.revision, blockers: [.operation.blockers[].code],
		pending: ([.operation.results[] | select(.outcome == "pending")] | length), total: (.operation.results | length),
		cancellation: .operation.cancellation}' "$receipt"
	;;

*)
	die "unknown command: $cmd"
	;;
esac
