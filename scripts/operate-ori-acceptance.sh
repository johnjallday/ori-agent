#!/usr/bin/env bash
#
# operate-ori-acceptance.sh — run every browser acceptance run for Operate Ori
# (the assistant opening a workspace, preparing a task, and the user reviewing,
# starting and following it), one after another, and report each.
#
#   ./scripts/operate-ori-acceptance.sh [port]      default port: 8963
#
# Runs, in order:
#   operate          open a workspace and select an agent for one watching tab
#   operate-task     an editable task draft in the app's own form; nothing saved
#   operate-start    one reviewed task, one Start, real progress, a refusal
#   operate-result   the run's own result, a rerun, needs-input, and a run
#                    interrupted by restarting the same isolated host
#   operate-journey  the whole journey with a second tab open
#
# Each run builds this working tree and uses its own isolated sandbox
# (wt demo), never real app data. The assistant's model and the agent's model
# are the runner's local deterministic stand-ins: no vendor model is called.
#
# A port already in use is reported as BLOCKED and the run is not started: an
# ori-agent started on a busy port stops whatever was there. Logs go to a new
# directory under $TMPDIR, printed at the end. Exit status is 0 only when every
# run passed.

set -u
cd "$(dirname "$0")/.." || exit 2

case "${1:-}" in
-h | --help)
	grep '^#' "$0" | grep -v '^#!' | cut -c3-
	exit 0
	;;
esac
port="${1:-8963}"
case "$port" in
'' | *[!0-9]*)
	echo "port must be a number" >&2
	exit 2
	;;
esac

out="${TMPDIR:-/tmp}/operate-ori-acceptance.$$"
mkdir -p "$out" || exit 2
failed=0

# port_busy — whether something still listens on the port. A run that just
# finished here can take a few seconds to let go of it, so this waits for that
# before answering. It never stops whatever holds the port.
port_busy() {
	local tries=0
	while lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; do
		tries=$((tries + 1))
		if [ "$tries" -ge 20 ]; then
			return 0
		fi
		sleep 1
	done
	return 1
}

for run in operate operate-task operate-start operate-result operate-journey; do
	if port_busy; then
		echo "BLOCKED  $run  (port $port is in use; nothing was started)"
		failed=1
		continue
	fi
	if python3 scripts/assistant-workspace-demo.py "--$run" --port "$port" >"$out/$run.log" 2>&1; then
		echo "PASS     $run"
	else
		echo "FAIL     $run  (see $out/$run.log)"
		failed=1
	fi
done

echo "Logs: $out"
exit "$failed"
