#!/usr/bin/env bash
#
# e2e-folder-collection.sh — run the folder-of-projects suite in a real browser
# against isolated demo servers, one per phase.
#
# Usage:
#   ./scripts/e2e-folder-collection.sh [--port PORT] [--tail N] [-- playwright args]
#
# The suite (tests/folder-collection-proposal.spec.ts) has eight phases, and each
# has a sandbox and a server of its own, so neither depends on what the other
# leaves behind:
#
#   composer  legacy unmarked-sketch collection, explicit generic setup/repeat.
#   home      Explore uses the same attachment picker; no-model planning refuses,
#             while explicit rule-based review/adjust/setup remains usable.
#   declined  the folder carries an old No thanks (seeded with --tombstone after
#             the HQ exists): the card still comes, says when, and Ask again
#             brings Set up back; Set up creates the group.
#   model-down  the system model cannot be reached (Ollama pointed at a closed
#             port): a Send keeps the card, shows one line and Retry.
#   existing-home  exact Albums, reviewed packages, compatible Home completion,
#             source/root/catalog identity, replay, reload and reattachment.
#   no-home   exact Albums, named provider readiness and no premature effects.
#   generic   exact Albums, explicit group review and six-workspace completion.
#   pending-return  exact Albums, saved and unsaved draft/link/selection return.
#
# For each phase this:
#
#   1. makes a new sandbox under $TMPDIR and seeds the folder into it with
#      scripts/folder-collection-seed.sh,
#   2. starts scripts/demo-server.sh on it (HOME and ORI_DATA_DIR both
#      redirected; it also builds the working tree),
#   3. runs the phase, stops the server by its PID, and prints PASS or FAIL.
#
# Only existing-home configures a controlled loopback model for staffing; its
# calls are recorded, not live-model quality evidence. Other credentials are
# dropped. Tests are selected by phase and a JSON report must contain exactly
# the required passed tests: skipped or missing cases never count as a pass.
#
# The port must be free: nothing already listening is ever stopped. Both
# sandboxes are left under $TMPDIR (their paths are printed) so a failure can
# be read.

set -euo pipefail

port=8949
model_port="${ORI_COLLECTION_MODEL_PORT:-18951}"
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
for value in "$port" "$model_port" "$tail_lines"; do
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

for checked_port in "$port" "$model_port"; do
	if ((checked_port < 1 || checked_port > 65535)) || lsof -nP -iTCP:"$checked_port" -sTCP:LISTEN >/dev/null 2>&1; then
		echo "port $checked_port is invalid or in use; choose a free app/model port" >&2
		exit 2
	fi
done

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
base="$tmp_root/ori-collection-e2e-$(date +%Y%m%d-%H%M%S)-$$"

unset CODEX_HOME OPENAI_API_KEY ANTHROPIC_API_KEY GEMINI_API_KEY
export ORI_DEMO_NO_CODEX=1

stop_server() {
	./scripts/stop-demo-server.sh "$port" >/dev/null 2>&1 || true
}
trap stop_server EXIT

failed=0
# run_phase <phase> <folder under the sandboxed home> <required test count> [VAR=value ...]
# Any VAR=value is set for that phase's server only.
run_phase() {
	local phase="$1" folder="$2" expected="$3" status=0
	shift 3
	local server_env=("$@")
	local phase_sandbox="$base-$phase"
	if [[ -e "$phase_sandbox" ]]; then
		echo "$phase_sandbox already exists" >&2
		exit 2
	fi
	mkdir -p "$phase_sandbox"
	# An empty or missing sandbox would start the server in the worktree itself.
	[[ -n "$phase_sandbox" && -d "$phase_sandbox" ]] || {
		echo "could not create a sandbox under $tmp_root" >&2
		exit 2
	}
	echo "SANDBOX[$phase]=$phase_sandbox"
	local seed_args=()
	case "$phase" in existing-home | no-home | generic | pending-return) seed_args=(--exact-albums) ;; esac
	./scripts/folder-collection-seed.sh ${seed_args[@]+"${seed_args[@]}"} "$phase_sandbox" "$folder" >"$phase_sandbox/seed.log"
	local log="$phase_sandbox/playwright-$phase.log" report="$phase_sandbox/report-$phase.json"
	env ${server_env[@]+"${server_env[@]}"} ./scripts/demo-server.sh "$port" "$phase_sandbox" >"$phase_sandbox/server-$phase.log" 2>&1 &
	./scripts/e2e.sh --port "$port" --wait 300 --tail 0 \
		--env "ORI_COLLECTION_SANDBOX=$phase_sandbox" \
		--env "ORI_COLLECTION_PHASE=$phase" \
		--env "ORI_COLLECTION_MODEL_PORT=$model_port" \
		--env "PLAYWRIGHT_JSON_OUTPUT_FILE=$report" \
		tests/folder-collection-proposal.spec.ts -- ${pw_args[@]+"${pw_args[@]}"} \
		--project=chromium --workers=1 --grep "\\[collection:$phase\\]" --reporter=list,json --output "$phase_sandbox/results-$phase" \
		>"$log" 2>&1 || status=$?
	stop_server
	# Playwright succeeds when every selected test is skipped. Check the actual
	# report, not its exit status or a human-readable summary line.
	node - "$report" "$phase" "$expected" <<'JS' || status=1
const fs = require('node:fs');
const [file, phase, count] = process.argv.slice(2);
let report;
try { report = JSON.parse(fs.readFileSync(file, 'utf8')); }
catch (error) { console.error(`FAIL phase ${phase}: missing JSON report (${error.message})`); process.exit(1); }
const specs = [];
function visit(suite) {
  specs.push(...(suite.specs || []));
  for (const child of suite.suites || []) visit(child);
}
for (const suite of report.suites || []) visit(suite);
const tests = specs.flatMap(spec => spec.tests || []);
const failed = report.errors?.length || tests.length !== Number(count) ||
  tests.some(test => test.expectedStatus !== 'passed' || test.status !== 'expected' ||
    !test.results?.length || test.results.some(result => result.status !== 'passed'));
console.log(`COUNT phase ${phase}: ${tests.length}/${count} required tests, ${tests.filter(test => test.results?.some(result => result.status === 'skipped')).length} skipped`);
if (failed) { console.error(`FAIL phase ${phase}: required test count/status mismatch`); process.exit(1); }
JS
	if [[ "$tail_lines" != "0" ]]; then tail -n "$tail_lines" "$log"; else cat "$log"; fi
	if [[ "$status" == "0" ]]; then
		echo "PASS  phase $phase"
	else
		echo "FAIL  phase $phase (exit $status; full log: $log)"
		failed=$((failed + 1))
	fi
	return "$status"
}

run_phase composer Downloads 2 || true
run_phase home Music 1 || true
run_phase declined Downloads 1 || true
# Port 9 (discard) is closed on a normal machine: every model call fails fast.
run_phase model-down Downloads 1 OLLAMA_BASE_URL=http://127.0.0.1:9 || true
run_phase existing-home Downloads 1 "LM_STUDIO_BASE_URL=http://127.0.0.1:$model_port/v1" || true
run_phase no-home Downloads 1 || true
run_phase generic Downloads 1 || true
run_phase pending-return Downloads 2 || true

exit "$failed"
