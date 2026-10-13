#!/usr/bin/env bash
#
# demo-folder-collection.sh — a folder that holds several projects, shown to Ori
# through each entry that proposes a group for it, in a real browser on isolated
# demo servers, with a real model for the assistant's reply.
#
# Usage:
#   ./scripts/demo-folder-collection.sh [--only composer|home] [--port PORT] [--shots DIR]
#
# Two journeys, each on a sandbox and a server of its own:
#
#   composer  Add folder in the assistant's composer. The proposed group is on
#             screen before anything is sent; Send asks the real model, whose
#             reply is printed; then Review group setup, Set up and the receipt.
#   home      Home's "Explore a folder". The same proposal as one offer; Adjust
#             renames the group; Set up and the receipt. No model is asked.
#
# Create Workspace's assisted chat does not propose a group yet, so it has no
# journey here.
#
# For each journey this makes a new sandbox under $TMPDIR, seeds the folder into
# it (scripts/folder-collection-seed.sh), starts scripts/demo-server.sh on it
# (which builds the working tree and redirects HOME and ORI_DATA_DIR), runs
# scripts/demo-folder-collection.mjs, and stops the server by its port. Each
# journey prints the card, the plan, the receipt and the folder's hash before
# and after.
#
# The composer journey calls a real model once, for the assistant's reply; the
# setup itself calls none. Like `wt demo`, its server is given your Codex home
# ($CODEX_HOME, else ~/.codex) so Ori's `codex` provider works; the call writes
# a session log there. Nothing else under the real HOME is read or written. The
# home journey's server is started without a Codex home.
#
# The port must be free: nothing already listening is ever stopped. The
# sandboxes are left under $TMPDIR (their paths are printed) so the logs and
# screenshots can be read.

set -euo pipefail

port=8946
shots=""
only=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--only)
		only="${2:-}"
		shift 2
		;;
	--port)
		port="${2:-}"
		shift 2
		;;
	--shots)
		shots="${2:-}"
		shift 2
		;;
	-h | --help)
		echo "usage: $0 [--only composer|home] [--port PORT] [--shots DIR]"
		echo "Shows a folder of projects to Ori through the assistant's Add folder (with a"
		echo "real model) and Home's Explore a folder. See the header of this script."
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		exit 2
		;;
	esac
done
[[ "$port" =~ ^[0-9]+$ ]] || {
	echo "--port must be a number, got '$port'" >&2
	exit 2
}
case "$only" in
"" | composer | home) ;;
*)
	echo "--only must be composer or home, got '$only'" >&2
	exit 2
	;;
esac

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
	echo "must run inside a git worktree" >&2
	exit 2
}
cd "$repo_root"

if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
	echo "port $port is in use; pick another with --port" >&2
	exit 2
fi

codex_home="${CODEX_HOME:-${HOME:-}/.codex}"
if [[ "$only" != home && ! -d "$codex_home" ]]; then
	echo "no Codex home at $codex_home: the composer journey needs a signed-in model provider" >&2
	echo "(--only home runs the journey that asks no model)" >&2
	exit 2
fi

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
base="$tmp_root/ori-collection-demo-$(date +%Y%m%d-%H%M%S)-$$"

stop_server() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
}
trap stop_server EXIT

# run_journey <journey> <folder under the sandboxed home>
run_journey() {
	local journey="$1" folder="$2"
	local sandbox="$base-$journey"
	if [[ -e "$sandbox" ]]; then
		echo "$sandbox already exists" >&2
		exit 2
	fi
	mkdir -p "$sandbox"
	# An empty or missing sandbox would start the server in the worktree itself.
	[[ -n "$sandbox" && -d "$sandbox" ]] || {
		echo "could not create a sandbox under $tmp_root" >&2
		exit 2
	}
	local out="${shots:-$sandbox/shots}" log="$sandbox/$journey.log"
	echo "== $journey"
	echo "SANDBOX=$sandbox"
	echo "SHOTS=$out"
	./scripts/folder-collection-seed.sh "$sandbox" "$folder" >"$sandbox/seed.log"
	if [[ "$journey" == composer ]]; then
		CODEX_HOME="$codex_home" ./scripts/demo-server.sh "$port" "$sandbox" >"$sandbox/server.log" 2>&1 &
	else
		env -u CODEX_HOME ./scripts/demo-server.sh "$port" "$sandbox" >"$sandbox/server.log" 2>&1 &
	fi
	./scripts/wait-demo-server.sh "$port" 300 >/dev/null || {
		echo "the server did not answer on port $port; see $sandbox/server.log" >&2
		exit 1
	}
	if ! node scripts/demo-folder-collection.mjs "http://localhost:$port" "$sandbox" "$out" "$journey" >"$log" 2>&1; then
		tail -n 30 "$log"
		echo "FAIL  $journey (full log: $log)"
		exit 1
	fi
	cat "$log"
	stop_server
	echo "PASS  $journey"
}

[[ "$only" == home ]] || run_journey composer Downloads
[[ "$only" == composer ]] || run_journey home Music
