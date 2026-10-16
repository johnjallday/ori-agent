#!/usr/bin/env bash
#
# e2e-goal-suggestions.sh — run tests/workspace-goal-suggestions.spec.ts in a
# real browser against an isolated demo server backed by the fixture model.
#
# Usage:
#   ./scripts/e2e-goal-suggestions.sh [--port PORT] [--fixture-port PORT] [--tail N] [-- playwright args]
#
# This:
#
#   1. makes a new sandbox under $TMPDIR,
#   2. starts scripts/fixture-ollama.py on --fixture-port; every goal run is
#      answered with the sandbox file mission-response.txt, which the spec
#      rewrites between runs, and held for a few seconds so a run is visibly
#      in flight,
#   3. starts scripts/demo-server.sh on the sandbox (HOME and ORI_DATA_DIR both
#      redirected; it also builds the working tree) with OLLAMA_BASE_URL
#      pointing at the fixture,
#   4. runs the spec, stops the server and the fixture by PID, and prints PASS
#      or FAIL.
#
# No real model is called: CODEX_HOME is dropped so no signed-in provider is
# registered. Both ports must be free: nothing already listening is stopped.
# The sandbox is left under $TMPDIR (its path is printed) so a failure can be
# read.

set -euo pipefail

port=8967
fixture_port=11530
tail_lines=40
pw_args=()
while [[ $# -gt 0 ]]; do
	case "$1" in
	--port)
		port="${2:-}"
		shift 2
		;;
	--fixture-port)
		fixture_port="${2:-}"
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
		echo "usage: $0 [--port PORT] [--fixture-port PORT] [--tail N] [-- playwright args]"
		echo "Runs tests/workspace-goal-suggestions.spec.ts against an isolated demo"
		echo "server and the fixture model. See the header of this script."
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		exit 2
		;;
	esac
done
for value in "$port" "$fixture_port" "$tail_lines"; do
	[[ "$value" =~ ^[0-9]+$ ]] || {
		echo "--port, --fixture-port and --tail must be numbers, got '$value'" >&2
		exit 2
	}
done

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
	echo "must run inside a git worktree" >&2
	exit 2
}
cd "$repo_root"

for p in "$port" "$fixture_port"; do
	if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
		echo "port $p is in use; pick another with --port / --fixture-port" >&2
		exit 2
	fi
done

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
sandbox="$tmp_root/ori-goal-e2e-$(date +%Y%m%d-%H%M%S)-$$"
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
echo "SANDBOX=$sandbox"
printf '{"items": []}\n' >"$sandbox/mission-response.txt"

unset CODEX_HOME

fixture_pid=""
cleanup() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
	if [[ -n "$fixture_pid" ]]; then
		kill "$fixture_pid" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

python3 scripts/fixture-ollama.py --port "$fixture_port" \
	--log "$sandbox/fixture-requests.jsonl" \
	--mission-response "$sandbox/mission-response.txt" \
	--mission-delay 10 >"$sandbox/fixture.log" 2>&1 &
fixture_pid=$!

OLLAMA_BASE_URL="http://127.0.0.1:$fixture_port" \
	./scripts/demo-server.sh "$port" "$sandbox" >"$sandbox/server.log" 2>&1 &

status=0
log="$sandbox/playwright.log"
./scripts/e2e.sh --port "$port" --wait 300 --tail 0 \
	--env "ORI_GOAL_E2E_SANDBOX=$sandbox" \
	tests/workspace-goal-suggestions.spec.ts -- --workers=1 ${pw_args[@]+"${pw_args[@]}"} \
	>"$log" 2>&1 || status=$?
cleanup
if [[ "$tail_lines" != "0" ]]; then tail -n "$tail_lines" "$log"; else cat "$log"; fi
if [[ "$status" == "0" ]]; then
	echo "PASS  workspace goal suggestions"
else
	echo "FAIL  workspace goal suggestions (exit $status; full log: $log)"
fi
exit "$status"
