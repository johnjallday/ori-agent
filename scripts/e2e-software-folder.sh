#!/usr/bin/env bash
#
# e2e-software-folder.sh — run the software-project folder suite in a real
# browser against one isolated demo server, restarted once in the middle.
#
# Usage:
#   ./scripts/e2e-software-folder.sh [--port PORT] [--fixture-port PORT] [--tail N] [-- playwright args]
#
# The suite (tests/software-project-folder-setup.spec.ts) has four phases. Two
# share one sandbox: "first" on a fresh server, and "restarted" after that
# server was stopped and started again, which is how a paused setup surviving a
# restart is checked for real. The third, "conversation", attaches a repository
# through the composer's Add folder on a server and sandbox of its own, so it
# depends on nothing the other two leave behind. The fourth provisions unread
# reports with the same scripted provider in another fresh sandbox. This runs
# their server lifetimes and browser checks in one command:
#
#   1. a new sandbox under $TMPDIR (HOME and ORI_DATA_DIR both redirected by
#      scripts/demo-server.sh, which also builds the working tree),
#   2. phase "first", then the server is stopped by its PID,
#   3. the server is started again on the same sandbox, then phase "restarted",
#   4. a second new sandbox and server, then phase "conversation",
#   5. a separate reports sandbox runs two actual first looks with the scripted
#      provider, then checks Home/drawer report handoff, Start this and Not now,
#   6. the server is stopped and one PASS/FAIL line is printed per phase.
#
# No model is called. The server's own `lmstudio` provider is pointed at
# --fixture-port, where the suite itself answers the model API (its FIXTURE
# PROVIDER); CODEX_HOME is dropped so no signed-in provider is registered.
#
# Both ports must be free: nothing already listening is ever stopped. All
# sandboxes are left under $TMPDIR (their paths are printed) so a failure can
# be read.

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
conversation_sandbox="$sandbox-conversation"
reports_sandbox="$sandbox-reports"
for dir in "$sandbox" "$conversation_sandbox" "$reports_sandbox"; do
	if [[ -e "$dir" ]]; then
		echo "$dir already exists" >&2
		exit 2
	fi
	mkdir -p "$dir"
	# An empty or missing sandbox would start the server in the worktree itself.
	[[ -n "$dir" && -d "$dir" ]] || {
		echo "could not create a sandbox under $tmp_root" >&2
		exit 2
	}
done
echo "SANDBOX=$sandbox"
echo "CONVERSATION_SANDBOX=$conversation_sandbox"
echo "REPORTS_SANDBOX=$reports_sandbox"

unset CODEX_HOME OPENAI_API_KEY ANTHROPIC_API_KEY GEMINI_API_KEY
export ORI_DEMO_NO_CODEX=1
export LM_STUDIO_BASE_URL="http://127.0.0.1:$fixture_port/v1"

stop_server() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
}
trap stop_server EXIT

failed=0
run_phase() {
	local phase="$1" home="${2:-$sandbox}" status=0
	local log="$home/playwright-$phase.log"
	./scripts/demo-server.sh "$port" "$home" >"$home/server-$phase.log" 2>&1 &
	./scripts/e2e.sh --port "$port" --wait 300 --tail 0 \
		--env "ORI_SOFTWARE_SANDBOX=$home" \
		--env "ORI_SOFTWARE_FIXTURE_PORT=$fixture_port" \
		--env "ORI_SOFTWARE_PHASE=$phase" \
		tests/software-project-folder-setup.spec.ts -- --workers=1 --output "$home/results-$phase" ${pw_args[@]+"${pw_args[@]}"} \
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
# The third phase has a server and a sandbox of its own, so it runs either way.
run_phase conversation "$conversation_sandbox" || true
run_phase reports "$reports_sandbox" || true

exit "$failed"
