#!/usr/bin/env bash
#
# demo-software-folder.sh — the whole software-project folder journey in a real
# browser, on one isolated demo server, with a real model.
#
# Usage:
#   ./scripts/demo-software-folder.sh [--ori] [--add-folder] [--port PORT] [--shots DIR]
#
# --ori seeds a copy of this repository as the project (laid out as a linked
# Git worktree, with a sibling worktree beside it). Without it the project is a
# small unrelated one. --shots is where the screenshots go (default: a `shots`
# folder inside the sandbox).
#
# --add-folder runs a different, shorter journey instead: a checkout is attached
# through the assistant composer's Add folder and sent to the real model. The
# reply proposes that one folder as a workspace, one click reaches its card, and
# its review and Set up finish there. One server, no restart, and the model is
# called for the assistant's reply only.
#
# The journey needs the server stopped and started twice, at exact moments. By
# hand that is a dozen commands. This is one:
#
#   1. a new sandbox under $TMPDIR, the demo server (scripts/demo-server.sh,
#      which builds the working tree and redirects HOME and ORI_DATA_DIR), and
#      the seeded folders (scripts/software-folder-seed.sh),
#   2. `setup`: Set up with no model chosen pauses before anything is created,
#   3. the server is restarted; `resume`: the paused setup is still there, a
#      model is chosen, and setup finishes on its receipt,
#   4. `assess-interrupt`: Start assessment is pressed and the run is left
#      going; the server is stopped under it and started again,
#   5. `assess`: the lost run reads "Did not finish"; Try again runs the same
#      task; the report, the workspace banner and the Plans page are followed,
#   6. `repeat`: the same folder opens its workspace; a sibling worktree is its
#      own project. The server is stopped.
#
# Steps 4 and 5 call a real model twice (the first call is the one that is
# interrupted). Like `wt demo`, the server is given your Codex home ($CODEX_HOME,
# else ~/.codex) so Ori's `codex` provider works; each call writes a session log
# there. Nothing else under the real HOME is read or written.
#
# The port must be free: nothing already listening is ever stopped. The sandbox
# is left under $TMPDIR (its path is printed) so the logs and screenshots can
# be read.

set -euo pipefail

port=8947
seed_args=()
shots=""
journey=home
while [[ $# -gt 0 ]]; do
	case "$1" in
	--ori)
		seed_args=(--ori)
		shift
		;;
	--add-folder)
		journey=add-folder
		shift
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
		echo "usage: $0 [--ori] [--add-folder] [--port PORT] [--shots DIR]"
		echo "Runs the software-project folder journey with a real model on one isolated"
		echo "demo server; --add-folder runs the composer's Add folder journey instead."
		echo "See the header of this script."
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
[[ -d "$codex_home" ]] || {
	echo "no Codex home at $codex_home: this journey needs a signed-in model provider" >&2
	exit 2
}
export CODEX_HOME="$codex_home"

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
sandbox="$tmp_root/ori-software-demo-$(date +%Y%m%d-%H%M%S)-$$"
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
[[ -n "$shots" ]] || shots="$sandbox/shots"
echo "SANDBOX=$sandbox"
echo "SHOTS=$shots"

stop_server() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
}
trap stop_server EXIT

starts=0
start_server() {
	starts=$((starts + 1))
	./scripts/demo-server.sh "$port" "$sandbox" >"$sandbox/server-$starts.log" 2>&1 &
	local _
	for _ in $(seq 1 300); do
		if curl -fsS -o /dev/null -m 2 "http://localhost:$port/" 2>/dev/null; then return 0; fi
		sleep 1
	done
	echo "the server did not answer on port $port; see $sandbox/server-$starts.log" >&2
	exit 1
}

run_phase() {
	local phase="$1" log="$sandbox/$1.log"
	echo "== $phase"
	if ! node scripts/demo-software-folder.mjs "http://localhost:$port" "$sandbox" "$shots" "$phase" >"$log" 2>&1; then
		tail -n 25 "$log"
		echo "FAIL  $phase (full log: $log)"
		exit 1
	fi
	tail -n 1 "$log"
}

start_server
./scripts/software-folder-seed.sh "$sandbox" ${seed_args[@]+"${seed_args[@]}"} >"$sandbox/seed.log"
if [[ "$journey" == add-folder ]]; then
	run_phase add-folder
	stop_server
	echo "PASS  add-folder"
	echo "What the assistant replied is in $sandbox/add-folder.log; screenshots are in $shots"
	exit 0
fi
run_phase setup
stop_server

start_server
run_phase resume
run_phase assess-interrupt
# The assessment is running now. Stopping the server here is the interruption.
stop_server

start_server
run_phase assess
run_phase repeat
stop_server

echo "PASS  setup, resume, assess-interrupt, assess, repeat"
echo "The report is in $sandbox/assess.log; screenshots are in $shots"
