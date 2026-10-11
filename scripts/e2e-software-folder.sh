#!/usr/bin/env bash
#
# e2e-software-folder.sh — run the software-project folder suite in a real
# browser against one isolated demo server, restarted once in the middle.
#
# Usage:
#   ./scripts/e2e-software-folder.sh [--port PORT] [--fixture-port PORT] [--tail N] [-- playwright args]
#
# The suite (tests/software-project-folder-setup.spec.ts) has two phases that
# share one sandbox: "first" on a fresh server, and "restarted" after that
# server was stopped and started again, which is how a paused setup surviving a
# restart is checked for real. By hand that is start a server in the background,
# wait, run, stop, start, wait, run, stop. This does it in one command:
#
#   1. a new sandbox under $TMPDIR (HOME and ORI_DATA_DIR both redirected by
#      scripts/demo-server.sh, which also builds the working tree),
#   2. phase "first", then the server is stopped by its PID,
#   3. the server is started again on the same sandbox, then phase "restarted",
#   4. the server is stopped and one PASS/FAIL line is printed per phase.
#
# No model is called. The server's own `lmstudio` provider is pointed at
# --fixture-port, where the suite itself answers the model API (its FIXTURE
# PROVIDER); CODEX_HOME is dropped so no signed-in provider is registered.
#
# Both ports must be free: nothing already listening is ever stopped. The
# sandbox is left under $TMPDIR (its path is printed) so a failure can be read.

set -euo pipefail

port=8948
fixture_port=18948
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
		echo "Runs tests/software-project-folder-setup.spec.ts against one isolated demo"
		echo "server, restarted once between its two phases. See the header of this script."
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
		echo "ports and --tail must be numbers, got '$value'" >&2
		exit 2
	}
done

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
	echo "must run inside a git worktree" >&2
	exit 2
}
cd "$repo_root"

for busy in "$port" "$fixture_port"; do
	if lsof -nP -iTCP:"$busy" -sTCP:LISTEN >/dev/null 2>&1; then
		echo "port $busy is in use; pick another with --port or --fixture-port" >&2
		exit 2
	fi
done

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
sandbox="$tmp_root/ori-software-e2e-$(date +%Y%m%d-%H%M%S)-$$"
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

unset CODEX_HOME
export LM_STUDIO_BASE_URL="http://127.0.0.1:$fixture_port/v1"

stop_server() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
}
trap stop_server EXIT

failed=0
run_phase() {
	local phase="$1" log="$sandbox/playwright-$1.log" status=0
	./scripts/demo-server.sh "$port" "$sandbox" >"$sandbox/server-$phase.log" 2>&1 &
	./scripts/e2e.sh --port "$port" --wait 300 --tail 0 \
		--env "ORI_SOFTWARE_SANDBOX=$sandbox" \
		--env "ORI_SOFTWARE_FIXTURE_PORT=$fixture_port" \
		--env "ORI_SOFTWARE_PHASE=$phase" \
		tests/software-project-folder-setup.spec.ts -- --workers=1 ${pw_args[@]+"${pw_args[@]}"} \
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

# The second phase continues what the first left behind, so it is not run after
# a first phase that failed.
if run_phase first; then
	run_phase restarted || true
else
	echo "SKIP  phase restarted (phase first failed)"
fi

exit "$failed"
