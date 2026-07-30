#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/e2e.sh [options] [spec ...] [-- playwright args]

Run Playwright specs against an already-running Ori server (normally the
isolated demo sandbox started by `wt demo`).

Wraps the PLAYWRIGHT_BASE_URL env prefix so the whole invocation is a single
stable command token — see "Shell Discipline" in CLAUDE.md.

Options:
  --port PORT       Port of the running server (default: 8931, the `wt demo` port).
  --url URL         Full base URL. Overrides --port.
  --tail N          Print only the last N lines of output (default: 60; 0 = all).
  --no-check        Skip the pre-flight reachability probe.
  -h, --help        Show this help text.

Any other argument is passed through to `playwright test`, so specs and flags
work as usual:

  ./scripts/e2e.sh tests/create-workspace-behavior.spec.ts
  ./scripts/e2e.sh --port 8765 tests/smoke.spec.ts -- --headed
  ./scripts/e2e.sh --tail 0 tests/workspace-backlog.spec.ts -- -g "backlog"

Exits with Playwright's own exit code.
EOF
}

fail() {
	printf 'Error: %s\n' "$*" >&2
	exit 2
}

port="${ORI_E2E_PORT:-8931}"
base_url=""
tail_lines=60
check_server=1
pw_args=()

while [[ $# -gt 0 ]]; do
	case "$1" in
	--port)
		[[ $# -ge 2 ]] || fail "--port needs a value"
		port="$2"
		shift 2
		;;
	--url)
		[[ $# -ge 2 ]] || fail "--url needs a value"
		base_url="$2"
		shift 2
		;;
	--tail)
		[[ $# -ge 2 ]] || fail "--tail needs a value"
		tail_lines="$2"
		shift 2
		;;
	--no-check)
		check_server=0
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	--)
		shift
		pw_args+=("$@")
		break
		;;
	*)
		pw_args+=("$1")
		shift
		;;
	esac
done

[[ "$tail_lines" =~ ^[0-9]+$ ]] || fail "--tail expects a non-negative integer, got '$tail_lines'"
[[ -n "$base_url" ]] || base_url="http://localhost:$port"

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || fail "must run inside a git worktree"
cd "$repo_root"

if ((check_server)); then
	if ! curl -sf -o /dev/null --max-time 3 "$base_url"; then
		fail "no server responding at $base_url — start one with 'wt demo ${port}' (or pass --url / --no-check)"
	fi
fi

# Default to the list reporter unless the caller picked their own; the config's
# html reporter is noise when the output is being read by an agent.
has_reporter=0
for arg in ${pw_args[@]+"${pw_args[@]}"}; do
	case "$arg" in
	--reporter | --reporter=*) has_reporter=1 ;;
	esac
done
((has_reporter)) || pw_args+=(--reporter=list)

out="$(mktemp "${TMPDIR:-/tmp}/ori-e2e.XXXXXX")"
trap 'rm -f "$out"' EXIT

printf 'Base URL: %s\n' "$base_url"
printf 'Running:  npx playwright test %s\n\n' "${pw_args[*]}"

status=0
PLAYWRIGHT_BASE_URL="$base_url" npx playwright test ${pw_args[@]+"${pw_args[@]}"} >"$out" 2>&1 || status=$?

if ((tail_lines > 0)); then
	tail -n "$tail_lines" "$out"
else
	tail -n +1 "$out"
fi

exit "$status"
