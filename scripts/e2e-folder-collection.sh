#!/usr/bin/env bash
#
# e2e-folder-collection.sh — run the folder-of-projects suite in a real browser
# against isolated demo servers, one per phase.
#
# Usage:
#   ./scripts/e2e-folder-collection.sh [--port PORT] [--tail N] [-- playwright args]
#
# The suite (tests/folder-collection-proposal.spec.ts) has two phases, and each
# has a sandbox and a server of its own, so neither depends on what the other
# leaves behind:
#
#   composer  the folder is attached in the assistant's composer, proposed as a
#             group at once, reviewed and set up; attaching it again proposes
#             nothing.
#   home      the folder is shown through Home's "Explore a folder", adjusted on
#             its card and set up.
#
# For each phase this:
#
#   1. makes a new sandbox under $TMPDIR and seeds the folder into it with
#      scripts/folder-collection-seed.sh,
#   2. starts scripts/demo-server.sh on it (HOME and ORI_DATA_DIR both
#      redirected; it also builds the working tree),
#   3. runs the phase, stops the server by its PID, and prints PASS or FAIL.
#
# No model is called and none is configured: CODEX_HOME is dropped so no
# signed-in provider is registered, and the suite fails if the page asks the
# assistant's model anything.
#
# The port must be free: nothing already listening is ever stopped. Both
# sandboxes are left under $TMPDIR (their paths are printed) so a failure can
# be read.

set -euo pipefail

port=8949
tail_lines=40
pw_args=()
while [[ $# -gt 0 ]]; do
	case "$1" in
	--port)
		port="${2:-}"
		shift 2
		;;
	--tail)
		tail_lines="${2:-}"
		shift 2
		;;
	--)
		shift
		pw_args=("$@")
		break
		;;
	-h | --help)
		echo "usage: $0 [--port PORT] [--tail N] [-- playwright args]"
		echo "Runs tests/folder-collection-proposal.spec.ts against one isolated demo"
		echo "server per phase. See the header of this script."
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		exit 2
		;;
	esac
done
for value in "$port" "$tail_lines"; do
	[[ "$value" =~ ^[0-9]+$ ]] || {
		echo "--port and --tail must be numbers, got '$value'" >&2
		exit 2
	}
done

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

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
base="$tmp_root/ori-collection-e2e-$(date +%Y%m%d-%H%M%S)-$$"

unset CODEX_HOME

stop_server() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
}
trap stop_server EXIT

failed=0
# run_phase <phase> <folder under the sandboxed home>
run_phase() {
	local phase="$1" folder="$2" status=0
	local home="$base-$phase"
	if [[ -e "$home" ]]; then
		echo "$home already exists" >&2
		exit 2
	fi
	mkdir -p "$home"
	# An empty or missing sandbox would start the server in the worktree itself.
	[[ -n "$home" && -d "$home" ]] || {
		echo "could not create a sandbox under $tmp_root" >&2
		exit 2
	}
	echo "SANDBOX[$phase]=$home"
	./scripts/folder-collection-seed.sh "$home" "$folder" >"$home/seed.log"
	local log="$home/playwright-$phase.log"
	./scripts/demo-server.sh "$port" "$home" >"$home/server-$phase.log" 2>&1 &
	./scripts/e2e.sh --port "$port" --wait 300 --tail 0 \
		--env "ORI_COLLECTION_SANDBOX=$home" \
		--env "ORI_COLLECTION_PHASE=$phase" \
		tests/folder-collection-proposal.spec.ts -- --workers=1 ${pw_args[@]+"${pw_args[@]}"} \
		>"$log" 2>&1 || status=$?
	stop_server
	if [[ "$tail_lines" != "0" ]]; then tail -n "$tail_lines" "$log"; else cat "$log"; fi
	if [[ "$status" == "0" ]]; then
		echo "PASS  phase $phase"
	else
		echo "FAIL  phase $phase (exit $status; full log: $log)"
		failed=$((failed + 1))
	fi
	return "$status"
}

run_phase composer Downloads || true
run_phase home Music || true

exit "$failed"
