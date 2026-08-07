#!/usr/bin/env bash
#
# smoke.sh - manual checks against a running isolated demo server.
#
# Every worktree keeps its feature's smoke checks here, under this one stable
# name, so a single allowlist entry - Bash(./scripts/smoke.sh:*) - covers them
# all. Inline multi-step curl pipelines cannot be allowlisted: shell variables
# and $(...) substitution make the command unresolvable to the permission
# analyzer, so it prompts no matter how many rules exist. A script is one
# stable token. Put the shell in here, not in the tool call.
#
# This worktree's feature: the GitHub Ops workspace template.
#
# Usage:
#   ./scripts/smoke.sh serve [--port N]         # build + run an isolated demo server
#   ./scripts/smoke.sh github [--port N]        # connection status
#   ./scripts/smoke.sh github-connect <token-file> [--port N]
#   ./scripts/smoke.sh github-disconnect [--port N]
#
# Options:
#   --port N   server port (default 8931, or $SMOKE_PORT)

set -uo pipefail

PORT="${SMOKE_PORT:-8931}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() {
  echo "smoke: $*" >&2
  exit 1
}

usage() {
  echo "usage: ./scripts/smoke.sh {serve|github|github-connect <token-file>|github-disconnect|wizard <owner/repo>} [--port N]"
}

# require_server fails loudly rather than letting every check below report a
# confusing empty body.
require_server() {
  curl -sf -o /dev/null "http://localhost:$PORT/" ||
    die "no server on port $PORT - start one with: ./scripts/smoke.sh serve --port $PORT"
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

# smoke_serve builds this worktree and runs it against a throwaway sandbox.
#
# Isolation rules (repo CLAUDE.md, "Smoke Testing"): HOME is overridden so
# "Ori Workspaces" never touches the real tree, ORI_DATA_DIR is overridden so
# the DB/vaults/templates are sandboxed, and the binary is launched from
# INSIDE the sandbox because the plugin store resolves relative to the launch
# directory. The sandbox lives under $TMPDIR rather than coming from
# `mktemp -d`, which fails silently under the agent sandbox and yields an
# empty path - which would land real state in the worktree.
smoke_serve() {
  local sandbox="${TMPDIR:-/tmp}"
  sandbox="${sandbox%/}/ori-demo-$(basename "$REPO_ROOT")"

  echo "building $REPO_ROOT ..."
  (cd "$REPO_ROOT" && go build -o bin/ori-agent ./cmd/server) || die "build failed"

  mkdir -p "$sandbox" || die "could not create $sandbox"
  echo "sandbox: $sandbox"
  echo "serving on http://localhost:$PORT ..."

  cd "$sandbox" || die "could not enter $sandbox"
  HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$PORT" exec "$REPO_ROOT/bin/ori-agent"
}

smoke_github_status() {
  require_server
  echo "=== GET /api/connections/github/status ==="
  curl -s "http://localhost:$PORT/api/connections/github/status" |
    show connected login token_type scopes error error_category
}

# smoke_github_connect reads the token from a FILE rather than an argument so
# the credential never appears in a command line, a process list, or a shell
# history entry.
smoke_github_connect() {
  local token_file="$1"
  [[ -f "$token_file" ]] || die "token file not found: $token_file"
  require_server

  local token
  token="$(tr -d '\r\n' <"$token_file")"
  [[ -n "$token" ]] || die "token file is empty: $token_file"

  echo "=== POST /api/connections/github/connect ==="
  python3 -c 'import json,sys; print(json.dumps({"token": sys.argv[1]}))' "$token" >"$token_file.body"
  curl -s -X POST "http://localhost:$PORT/api/connections/github/connect" \
    -H 'Content-Type: application/json' \
    --data-binary "@$token_file.body" |
    show connected login token_type scopes error message
  rm -f "$token_file.body"
}

smoke_github_disconnect() {
  require_server
  echo "=== POST /api/connections/github/disconnect ==="
  curl -s -X POST "http://localhost:$PORT/api/connections/github/disconnect" |
    show connected error message
}

# smoke_wizard creates a GitHub Ops workspace and walks its Setup Wizard,
# printing each step's readiness. Takes the repo to bind, e.g. owner/name.
smoke_wizard() {
  local repo="$1"
  require_server
  local base="http://localhost:$PORT"

  echo "=== create workspace from the github-ops blueprint ==="
  curl -s -X POST "$base/api/workspaces" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"GitHub Ops Demo $(date +%H%M%S)\",\"template_id\":\"github-ops\"}" \
    -o "$TMPDIR/ws.json"
  local ws
  ws="$(python3 -c '
import json, os
d = json.load(open(os.path.join(os.environ["TMPDIR"], "ws.json")))
w = d.get("workspace") or d.get("folder") or d
print(w.get("id", ""))
')"
  [[ -n "$ws" ]] || die "workspace was not created: $(head -c 300 "$TMPDIR/ws.json")"
  echo "  workspace: $ws"

  echo
  echo "=== open the wizard ==="
  curl -s -X POST "$base/api/workspaces/$ws/setup-wizard/open" -o /dev/null
  show_wizard "$base" "$ws"

  echo
  echo "=== confirm the repository step with $repo ==="
  curl -s -X POST "$base/api/workspaces/$ws/setup-wizard/steps/repository/confirm" \
    -H 'Content-Type: application/json' \
    -d "{\"option\":\"$repo\"}" -o /dev/null
  show_wizard "$base" "$ws"

  echo
  echo "workspace id: $ws"
}

show_wizard() {
  local base="$1" ws="$2"
  curl -s "$base/api/workspaces/$ws/setup-wizard" -o "$TMPDIR/wiz.json"
  python3 -c '
import json, os
d = json.load(open(os.path.join(os.environ["TMPDIR"], "wiz.json")))
w = d.get("setup") or d.get("setup_wizard") or d
print("  overall state:", w.get("state"))
for s in w.get("steps", []):
    print("  [%-9s] %-12s %s" % (s.get("status", "?"), s.get("id"), s.get("summary", "")))
    if s.get("error_category"):
        print("               category: %s" % s["error_category"])
    for o in (s.get("options") or [])[:4]:
        mark = "*" if o.get("selected") else " "
        print("             %s %s — %s" % (mark, o.get("id"), o.get("description", "")))
'
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
    serve)
      smoke_serve
      ;;
    github)
      smoke_github_status
      ;;
    github-connect)
      [[ ${#args[@]} -eq 2 ]] || {
        usage
        exit 2
      }
      smoke_github_connect "${args[1]}"
      ;;
    github-disconnect)
      smoke_github_disconnect
      ;;
    wizard)
      [[ ${#args[@]} -eq 2 ]] || {
        usage
        exit 2
      }
      smoke_wizard "${args[1]}"
      ;;
    *)
      die "unknown check: ${args[0]}"
      ;;
  esac
}

main "$@"
