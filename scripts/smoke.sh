#!/usr/bin/env bash
#
# smoke.sh - manual API checks against a running isolated demo server.
#
# Every worktree keeps its feature's smoke checks here, under this one stable
# name, so a single allowlist entry - Bash(./scripts/smoke.sh:*) - covers them
# all. Inline multi-step curl pipelines cannot be allowlisted: shell variables
# and $(...) substitution make the command unresolvable to the permission
# analyzer, so it prompts no matter how many rules exist. A script is one
# stable token. Put the shell in here, not in the tool call.
#
# Start the server first (from inside this worktree):
#
#   ./scripts/wt.sh demo 8931
#
# Usage:
#   ./scripts/smoke.sh toolbox <workspace-id> <agent-instance-id> <toolbox-id>
#
# Options:
#   --port N   server port (default 8931, or $SMOKE_PORT)

set -euo pipefail

PORT="${SMOKE_PORT:-8931}"

die() {
  echo "smoke: $*" >&2
  exit 1
}

usage() {
  echo "usage: ./scripts/smoke.sh toolbox <workspace-id> <agent-instance-id> <toolbox-id> [--port N]"
}

# require_server fails loudly rather than letting every check below report a
# confusing empty body.
require_server() {
  curl -sf -o /dev/null "http://localhost:$PORT/" ||
    die "no server on port $PORT - start one with: ./scripts/wt.sh demo $PORT"
}

# show pretty-prints selected fields from a JSON body on stdin. The python is
# kept here, in a file, precisely so no caller has to inline it.
show() {
  python3 -c '
import json, sys

fields = sys.argv[1:]
try:
    doc = json.load(sys.stdin)
except json.JSONDecodeError:
    print("  (non-JSON response)")
    sys.exit(0)


def dig(node, path):
    for part in path.split("."):
        if not isinstance(node, dict):
            return None
        node = node.get(part)
    return node


for field in fields:
    value = dig(doc, field)
    if isinstance(value, (dict, list)):
        value = json.dumps(value, sort_keys=True)
    print(f"  {field}: {value}")
' "$@"
}

smoke_toolbox() {
  local workspace="$1" instance="$2" toolbox="$3"
  local base="http://localhost:$PORT/api/workspaces/$workspace"
  local endpoint="$base/agent-toolboxes/$instance"

  require_server

  echo "=== preview ==="
  curl -s "$endpoint/preview?toolbox_id=$toolbox" |
    show action preview.can_use_directly workspace_version

  echo "=== use (one-click) ==="
  curl -s -X POST "$endpoint/use" \
    -H 'Content-Type: application/json' \
    -d "{\"toolbox_id\":\"$toolbox\"}" |
    show message receipt.focus.state receipt.capacity receipt.permissions receipt.previous

  echo "=== undo preview ==="
  curl -s "$endpoint/undo" | show available action previous

  echo "=== stale write rejected (expect 409) ==="
  curl -s -o /dev/null -w "  status: %{http_code}\n" \
    -X POST "$endpoint/use" \
    -H 'Content-Type: application/json' \
    -d "{\"toolbox_id\":\"$toolbox\",\"expected_workspace_version\":1}"
}

main() {
  local args=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --port)
        [[ $# -ge 2 ]] || die "--port needs a value"
        PORT="$2"
        shift 2
        ;;
      -h | --help)
        usage
        exit 0
        ;;
      *)
        args+=("$1")
        shift
        ;;
    esac
  done

  [[ ${#args[@]} -gt 0 ]] || {
    usage
    exit 2
  }

  case "${args[0]}" in
    toolbox)
      [[ ${#args[@]} -eq 4 ]] || {
        usage
        exit 2
      }
      smoke_toolbox "${args[1]}" "${args[2]}" "${args[3]}"
      ;;
    *)
      die "unknown check: ${args[0]}"
      ;;
  esac
}

main "$@"
