#!/usr/bin/env bash
#
# wait-demo-ready.sh — wait for a demo script's READY line.
#
# Usage:
#   ./scripts/wait-demo-ready.sh <output-file> [seconds]
#
# `scripts/second-brain-demo.sh` (and its siblings) run in the foreground, so an
# agent starts one as a tracked background process with its output going to a
# file. The server answering (`wait-demo-server.sh`) is not the end of that
# script: it still seeds the sandbox, then prints one `READY …` line. This waits
# for that line, prints it, and exits 0. If the script's own output says it
# exited or failed first, or `seconds` (default 400) pass, it prints what it saw
# and exits 1. It reads the file and starts nothing.

set -euo pipefail

file="${1:-}"
limit="${2:-400}"
if [[ -z "$file" ]] || ! [[ "$limit" =~ ^[0-9]+$ ]]; then
	echo "usage: $0 <output-file> [seconds]" >&2
	exit 2
fi

deadline=$((SECONDS + limit))
while ((SECONDS < deadline)); do
	if [[ -f "$file" ]]; then
		if line="$(grep -m1 '^READY ' "$file")"; then
			echo "$line"
			exit 0
		fi
		if grep -q '^\[exited with code' "$file"; then
			echo "the demo script exited before it was ready:" >&2
			tail -n 20 "$file" >&2
			exit 1
		fi
	fi
	sleep 1
done

echo "no READY line in $file within ${limit}s" >&2
[[ -f "$file" ]] && tail -n 20 "$file" >&2
exit 1
