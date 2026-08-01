#!/usr/bin/env bash
#
# demo-server.sh — build the current worktree and serve it from an ISOLATED
# demo sandbox, exactly like `wt demo`, but as a plain script so an agent can
# run it as a tracked background process (see "Smoke Testing" in CLAUDE.md).
#
# Usage:
#   ./scripts/demo-server.sh [port] [sandbox_dir]
#
# Both HOME and ORI_DATA_DIR are redirected into the sandbox, and the server is
# started from INSIDE it so the plugin store is isolated too. Nothing is ever
# written under the real $HOME.
#
# The sandbox path is printed on the first line as `SANDBOX=<path>` so the
# caller can clean it up with a single `rm -rf` of a temp path.

set -euo pipefail

port="${1:-8931}"
sandbox="${2:-}"

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
	echo "must run inside a git worktree" >&2
	exit 2
}
cd "$repo_root"

go build -o bin/ori-agent ./cmd/server

if [[ -z "$sandbox" ]]; then
	sandbox="$(mktemp -d "${TMPDIR:-/tmp}/ori-demo.XXXXXX")"
fi
mkdir -p "$sandbox"

echo "SANDBOX=$sandbox"
echo "BRANCH=$(git branch --show-current)"
echo "URL=http://localhost:$port"

cd "$sandbox"
exec env HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$port" "$repo_root/bin/ori-agent"
