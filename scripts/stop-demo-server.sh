#!/usr/bin/env bash
#
# stop-demo-server.sh — stop the isolated Ori demo server listening on a port.
#
# Usage:
#   ./scripts/stop-demo-server.sh [port]      (default 8931)
#
# It stops a process by PID, never by pattern, and only when that PID is an
# `ori-agent` listening on the port. Anything else on the port is left alone
# and reported, so an unrelated service is never killed. Pairs with
# `scripts/demo-server.sh` and `scripts/reaper-demo.sh serve`, which both run
# the server as a tracked background process.

set -euo pipefail

port="${1:-8931}"
if [[ ! "$port" =~ ^[0-9]+$ ]]; then
	echo "port must be a number: $port" >&2
	exit 2
fi

pids="$(lsof -nP -t -iTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)"
if [[ -z "$pids" ]]; then
	echo "nothing is listening on port $port"
	exit 0
fi

status=0
for pid in $pids; do
	command_name="$(ps -o comm= -p "$pid" 2>/dev/null || true)"
	if [[ "$(basename "$command_name")" != "ori-agent" ]]; then
		echo "port $port is held by PID $pid ($command_name), not ori-agent; leaving it running" >&2
		status=1
		continue
	fi
	kill "$pid"
	# Graceful shutdown routinely takes longer than a few seconds, and until the
	# process is gone it still owns the sandbox's installation lock: a restart
	# on the same sandbox fails with "another Ori process owns this
	# installation". Wait long enough that "stopped" means restartable.
	for _ in $(seq 1 300); do
		kill -0 "$pid" 2>/dev/null || break
		sleep 0.1
	done
	if kill -0 "$pid" 2>/dev/null; then
		echo "ori-agent PID $pid on port $port did not exit" >&2
		status=1
	else
		echo "stopped ori-agent PID $pid on port $port"
	fi
done
exit "$status"
