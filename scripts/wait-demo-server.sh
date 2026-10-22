#!/usr/bin/env bash
#
# wait-demo-server.sh — wait until a demo server answers on its port.
#
# Usage:
#   ./scripts/wait-demo-server.sh [port] [seconds]
#
# `scripts/demo-server.sh` builds before it serves and stays in the foreground,
# so an agent starts it as a tracked background process and then needs to know
# when it is up. This is that wait, as one stable command instead of an inline
# loop: it polls the port (default 8931) for up to `seconds` (default 180),
# prints the listening ori-agent's PID line and binary, and exits 1 if nothing
# answered. It starts nothing and stops nothing.
#
# The binary line matters when several worktrees run demos: another session can
# take the port between your `lsof` check and your server's start, and "up"
# then means THEIR server. A binary outside this worktree is called out.

set -euo pipefail

port="${1:-8931}"
limit="${2:-180}"
if ! [[ "$port" =~ ^[0-9]+$ && "$limit" =~ ^[0-9]+$ ]]; then
	echo "usage: $0 [port] [seconds]" >&2
	exit 2
fi

deadline=$((SECONDS + limit))
while ((SECONDS < deadline)); do
	if curl -fsS -o /dev/null --max-time 2 "http://localhost:$port/" 2>/dev/null; then
		echo "up: http://localhost:$port"
		lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | tail -1 || true
		pid="$(lsof -t -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | head -1 || true)"
		if [[ -n "$pid" ]]; then
			binary="$(ps -o command= -p "$pid" 2>/dev/null | awk '{print $1}' || true)"
			echo "binary: ${binary:-unknown}"
			root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
			case "$binary" in
			"$root"/*) ;;
			*) echo "note: this server was not built from $root" ;;
			esac
		fi
		exit 0
	fi
	sleep 2
done

echo "no server answered on port $port within ${limit}s" >&2
exit 1
