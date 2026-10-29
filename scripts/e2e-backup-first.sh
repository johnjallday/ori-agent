#!/usr/bin/env bash
#
# e2e-backup-first.sh — run tests/backup-first-suggestions.spec.ts in a real
# browser against the inbox-folder sandbox, with goal runs answered by the
# fixture model (backup-first-suggestions).
#
# Usage:
#   ./scripts/e2e-backup-first.sh [--serve] [--port PORT] [--fixture-port PORT] [--tail N] [-- playwright args]
#
# --serve starts the same sandbox and fixture and keeps them running until
# interrupted, without running the spec, for a demo.
#
# This:
#
#   1. makes a new sandbox under $TMPDIR with mission-response.txt in it,
#   2. starts scripts/fixture-ollama.py on --fixture-port; every goal run is
#      answered with that file, which the spec rewrites between runs,
#   3. starts scripts/demo-inbox-folder.sh on the sandbox with OLLAMA_BASE_URL
#      pointing at the fixture: the same seeded Downloads, Documents and
#      Desktop, the same personal assistant and HQ, no Codex home and no
#      provider API key. The fixture only answers once the spec (or the demo),
#      after the inbox hire, gives the workspace's own File Curator copy the
#      fixture model: the blueprint staffs it with an OpenAI model, which this
#      sandbox cannot run.
#        PATCH /api/workspaces/<id>/agents/File%20Curator
#              {"llm_provider": "ollama", "model": "fixture-model:latest"}
#   4. runs the spec, stops the server and the fixture by PID, and prints PASS
#      or FAIL.
#
# No real model is called. Both ports must be free: nothing already listening
# is stopped. The sandbox is left under $TMPDIR (its path is printed).

set -euo pipefail

port=8968
fixture_port=11531
tail_lines=40
serve=0
pw_args=()
while [[ $# -gt 0 ]]; do
	case "$1" in
	--serve)
		serve=1
		shift
		;;
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
		echo "usage: $0 [--serve] [--port PORT] [--fixture-port PORT] [--tail N] [-- playwright args]"
		echo "Runs tests/backup-first-suggestions.spec.ts against the inbox-folder sandbox"
		echo "and the fixture model. See the header of this script."
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
tmp_root="$(cd "${tmp_root%/}" && pwd -P)"
sandbox="$tmp_root/ori-backup-first-$(date +%Y%m%d-%H%M%S)-$$"
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

fixture_pid=""
inbox_pid=""
cleanup() {
	if [[ -n "$inbox_pid" ]]; then
		kill "$inbox_pid" >/dev/null 2>&1 || true
	fi
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
	if [[ -n "$fixture_pid" ]]; then
		kill "$fixture_pid" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

python3 scripts/fixture-ollama.py --port "$fixture_port" \
	--log "$sandbox/fixture-requests.jsonl" \
	--mission-response "$sandbox/mission-response.txt" \
	--mission-delay 3 >"$sandbox/fixture.log" 2>&1 &
fixture_pid=$!

# The inbox sandbox's own seeding and assistant setup; it prints READY once
# Home can attach Downloads.
OLLAMA_BASE_URL="http://127.0.0.1:$fixture_port" \
	./scripts/demo-inbox-folder.sh --port "$port" --sandbox "$sandbox" >"$sandbox/inbox.log" 2>&1 &
inbox_pid=$!
ready=0
for _ in $(seq 1 360); do
	if grep -q '^READY:' "$sandbox/inbox.log" 2>/dev/null; then
		ready=1
		break
	fi
	if ! kill -0 "$inbox_pid" 2>/dev/null; then break; fi
	sleep 1
done
if [[ "$ready" != "1" ]]; then
	echo "the inbox sandbox did not become ready; see $sandbox/inbox.log" >&2
	tail -n 20 "$sandbox/inbox.log" >&2 || true
	exit 1
fi
echo "URL=http://localhost:$port"
echo "FIXTURE=http://127.0.0.1:$fixture_port"

if [[ "$serve" == "1" ]]; then
	echo "SERVING: write goal-run answers to $sandbox/mission-response.txt; Ctrl-C stops both."
	wait "$inbox_pid"
	exit 0
fi

log="$sandbox/playwright.log"
status=0
./scripts/e2e.sh --port "$port" --tail 0 \
	--env "ORI_INBOX_SANDBOX=$sandbox" --env "ORI_BACKUP_FIRST_SANDBOX=$sandbox" \
	${ORI_FOLDER_PROTECTION_COVERED_CONTROL:+--env "ORI_FOLDER_PROTECTION_COVERED_CONTROL=$ORI_FOLDER_PROTECTION_COVERED_CONTROL"} \
	tests/backup-first-suggestions.spec.ts -- --workers=1 ${pw_args[@]+"${pw_args[@]}"} >"$log" 2>&1 || status=$?
if [[ "$tail_lines" != "0" ]]; then tail -n "$tail_lines" "$log"; else cat "$log"; fi
cleanup
if [[ "$status" == "0" ]]; then
	echo "PASS  backup-first suggestions"
else
	echo "FAIL  backup-first suggestions (exit $status; full log: $log)"
fi
exit "$status"
