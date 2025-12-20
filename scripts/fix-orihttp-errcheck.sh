#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Usage:
#   ./scripts/fix-orihttp-errcheck.sh [-w] [root...]
#
# Examples:
#   ./scripts/fix-orihttp-errcheck.sh internal
#   ./scripts/fix-orihttp-errcheck.sh -w internal/agenthttp internal/pluginhttp
#
# Notes:
# - This is intentionally narrow: it only wraps orihttp.Respond* calls.
# - It may produce larger diffs than expected; run on targeted directories/files.

WRITE=0
if [ "${1:-}" = "-w" ]; then
  WRITE=1
  shift
fi

ROOTS=("$@")
if [ ${#ROOTS[@]} -eq 0 ]; then
  echo "Usage: $0 [-w] [root...]" >&2
  echo "Example: $0 -w internal/agenthttp internal/pluginhttp" >&2
  exit 2
fi

if [ $WRITE -eq 1 ]; then
  go run ./scripts/fix-orihttp-errcheck.go -w "${ROOTS[@]}"
  gofmt -w "${ROOTS[@]}"
else
  echo "Dry run (no files changed). Re-run with -w to apply." >&2
  go run ./scripts/fix-orihttp-errcheck.go "${ROOTS[@]}"
fi
