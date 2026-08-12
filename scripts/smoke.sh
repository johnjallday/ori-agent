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
# This worktree's feature: Workspace Map camera framing (#339 = #307 + #329).
#
# Usage:
#   ./scripts/smoke.sh serve [--port N] [--sandbox NAME] [--wipe]
#                                               # build + run an isolated demo server
#   ./scripts/smoke.sh wide [--span N] [--count N]
#                                               # seed a map too wide to fit at 50%
#   ./scripts/smoke.sh layout [--port N]        # print stored anchors + camera
#   ./scripts/smoke.sh viewport <zoom> [--port N]
#                                               # PATCH a camera; prints the status
#   ./scripts/smoke.sh reset-layout [--port N]  # drop every stored anchor + camera
#
# Options:
#   --port N       server port (default 8931, or $SMOKE_PORT)
#   --sandbox NAME which throwaway profile to serve (default "demo"). A second
#                  name gives a genuinely fresh profile, which the zero-
#                  workspace Personal HQ path needs.
#   --wipe         delete that sandbox before serving, so "fresh" really is
#   --span N       world units between the first and last building (default 9000)
#   --count N      how many workspaces the wide fixture creates (default 3)

set -uo pipefail

PORT="${SMOKE_PORT:-8931}"
SPAN="${SMOKE_SPAN:-9000}"
COUNT="${SMOKE_COUNT:-3}"
SANDBOX_NAME="${SMOKE_SANDBOX:-demo}"
WIPE=0
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="${TMPDIR:-/tmp}"
WORK="${WORK%/}"

die() {
  echo "smoke: $*" >&2
  exit 1
}

usage() {
  echo "usage: ./scripts/smoke.sh {serve|wide|layout|viewport <zoom>|reset-layout} [--port N] [--span N] [--count N]"
}

# require_server fails loudly rather than letting every check below report a
# confusing empty body.
require_server() {
  curl -sf -o /dev/null "http://localhost:$PORT/" ||
    die "no server on port $PORT - start one with: ./scripts/smoke.sh serve --port $PORT"
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
  local sandbox="$WORK/ori-$SANDBOX_NAME-$(basename "$REPO_ROOT")"

  echo "building $REPO_ROOT ..."
  (cd "$REPO_ROOT" && go build -o bin/ori-agent ./cmd/server) || die "build failed"

  # Only ever under $TMPDIR, and only a path this script composed itself.
  if ((WIPE)) && [[ "$sandbox" == "$WORK/ori-"* ]]; then
    echo "wiping $sandbox"
    rm -rf "$sandbox"
  fi

  mkdir -p "$sandbox" || die "could not create $sandbox"
  echo "sandbox: $sandbox"
  echo "serving on http://localhost:$PORT ..."

  cd "$sandbox" || die "could not enter $sandbox"
  HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$PORT" exec "$REPO_ROOT/bin/ori-agent"
}

# smoke_wide seeds the layout Fit All could not frame before #307: buildings
# spread further apart than two viewports, so fitting them requires a zoom
# below the interactive 50% floor.
smoke_wide() {
  require_server
  local base="http://localhost:$PORT"
  local ids=()

  # Reuse what is already there. Re-running the fixture at a different span is
  # the normal case while demoing, and creating three more workspaces every
  # time leaves a map nobody asked for.
  curl -s "$base/api/workspaces" -o "$WORK/ori-wide-list.json"
  local existing
  existing="$(WIDE_FILE="$WORK/ori-wide-list.json" WIDE_COUNT="$COUNT" python3 -c '
import json, os
doc = json.load(open(os.environ["WIDE_FILE"]))
items = doc.get("workspaces") if isinstance(doc, dict) else doc
items = items or []
ids = [w.get("id") for w in items if isinstance(w, dict) and w.get("id") and w.get("kind") != "group"]
print(" ".join(ids[: int(os.environ["WIDE_COUNT"])]))
' 2>/dev/null)"
  rm -f "$WORK/ori-wide-list.json"
  local id
  for id in $existing; do
    ids+=("$id")
  done
  ((${#ids[@]} == 0)) || echo "=== reusing ${#ids[@]} existing workspaces ==="

  echo "=== creating $((COUNT - ${#ids[@]})) workspaces ==="
  local i=${#ids[@]}
  while ((i < COUNT)); do
    curl -s -X POST "$base/api/workspaces" \
      -H 'Content-Type: application/json' \
      -d "{\"name\":\"Wide $i $(date +%H%M%S)\"}" \
      -o "$WORK/ori-wide-ws.json"
    id="$(WIDE_FILE="$WORK/ori-wide-ws.json" python3 -c '
import json, os
doc = json.load(open(os.environ["WIDE_FILE"]))
ws = doc.get("workspace") or doc.get("folder") or doc
print(ws.get("id", ""))
')"
    [[ -n "$id" ]] || die "workspace $i was not created: $(head -c 300 "$WORK/ori-wide-ws.json")"
    echo "  $id"
    ids+=("$id")
    i=$((i + 1))
  done

  echo
  echo "=== spreading them $SPAN world units apart ==="
  WIDE_IDS="${ids[*]}" WIDE_SPAN="$SPAN" python3 -c '
import json, os

ids = os.environ["WIDE_IDS"].split()
span = float(os.environ["WIDE_SPAN"])
step = span / max(1, len(ids) - 1) if len(ids) > 1 else 0
positions = {}
for index, node in enumerate(ids):
    # A diagonal spread, so the fit is constrained on both axes rather than
    # only horizontally.
    positions[node] = {"x": round(index * step, 3), "y": round(index * step / 4, 3)}
print(json.dumps({"operations": [{"op": "set_positions", "positions": positions}]}))
' >"$WORK/ori-wide-patch.json"
  curl -s -X PATCH "$base/api/workspace-map/layout" \
    -H 'Content-Type: application/json' \
    --data-binary "@$WORK/ori-wide-patch.json" \
    -o "$WORK/ori-wide-result.json"
  rm -f "$WORK/ori-wide-patch.json" "$WORK/ori-wide-ws.json"
  show_layout_file "$WORK/ori-wide-result.json" result
  rm -f "$WORK/ori-wide-result.json"

  echo
  echo "open http://localhost:$PORT/ and use the canvas right-click menu -> Fit all"
}

smoke_layout() {
  require_server
  echo "=== GET /api/workspace-map/layout ==="
  curl -s "http://localhost:$PORT/api/workspace-map/layout" -o "$WORK/ori-layout.json"
  show_layout_file "$WORK/ori-layout.json" layout
  rm -f "$WORK/ori-layout.json"
}

# smoke_viewport proves the server's own validation of a framing-floor camera,
# which is the half of #307 no browser click can show.
smoke_viewport() {
  local zoom="$1"
  require_server
  echo "=== PATCH set_viewport zoom=$zoom ==="
  local status
  status="$(curl -s -o "$WORK/ori-viewport.json" -w '%{http_code}' \
    -X PATCH "http://localhost:$PORT/api/workspace-map/layout" \
    -H 'Content-Type: application/json' \
    -d "{\"operations\":[{\"op\":\"set_viewport\",\"viewport\":{\"center_x\":4500,\"center_y\":1100,\"zoom\":$zoom}}]}")"
  echo "  HTTP $status"
  show_layout_file "$WORK/ori-viewport.json" result
  rm -f "$WORK/ori-viewport.json"
}

smoke_reset_layout() {
  require_server
  echo "=== DELETE /api/workspace-map/layout ==="
  curl -s -X DELETE "http://localhost:$PORT/api/workspace-map/layout" -o "$WORK/ori-reset.json"
  show_layout_file "$WORK/ori-reset.json" result
  rm -f "$WORK/ori-reset.json"
}

# show_layout_file pretty-prints a layout or patch-result body. The python is
# kept here, in a file, precisely so no caller has to inline it.
show_layout_file() {
  LAYOUT_FILE="$1" LAYOUT_KEY="$2" python3 -c '
import json, os, sys

try:
    doc = json.load(open(os.environ["LAYOUT_FILE"]))
except (json.JSONDecodeError, FileNotFoundError):
    print("  (non-JSON response)")
    sys.exit(0)

if doc.get("error"):
    print("  error:", doc["error"])
body = doc.get(os.environ["LAYOUT_KEY"]) or doc
positions = body.get("positions") or {}
print("  revision:", body.get("revision"))
print("  anchors: ", len(positions))
xs = [p.get("x", 0) for p in positions.values()]
ys = [p.get("y", 0) for p in positions.values()]
if xs:
    print("  span:     x %.0f..%.0f  y %.0f..%.0f" % (min(xs), max(xs), min(ys), max(ys)))
viewport = body.get("viewport")
if viewport:
    print("  camera:   center (%.1f, %.1f) at %.0f%%" % (
        viewport.get("center_x", 0), viewport.get("center_y", 0),
        viewport.get("zoom", 0) * 100))
else:
    print("  camera:   none stored")
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
      --span)
        [[ $# -ge 2 ]] || die "--span needs a value"
        SPAN="$2"
        shift 2
        ;;
      --count)
        [[ $# -ge 2 ]] || die "--count needs a value"
        COUNT="$2"
        shift 2
        ;;
      --sandbox)
        [[ $# -ge 2 ]] || die "--sandbox needs a value"
        [[ "$2" =~ ^[A-Za-z0-9_-]+$ ]] || die "--sandbox expects a plain name, got '$2'"
        SANDBOX_NAME="$2"
        shift 2
        ;;
      --wipe)
        WIPE=1
        shift
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
    wide)
      smoke_wide
      ;;
    layout)
      smoke_layout
      ;;
    viewport)
      [[ ${#args[@]} -eq 2 ]] || {
        usage
        exit 2
      }
      smoke_viewport "${args[1]}"
      ;;
    reset-layout)
      smoke_reset_layout
      ;;
    *)
      die "unknown check: ${args[0]}"
      ;;
  esac
}

main "$@"
