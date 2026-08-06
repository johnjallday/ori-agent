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
  --wait N          Poll the server for up to N seconds before giving up.
  --each            Run each spec as its own Playwright invocation, with a
                    labelled header and a pass/fail summary line per spec.
  --env KEY=VAL     Set an env var for the Playwright run. Repeatable.
  --fmt             Run prettier over the named specs before running them.
  -h, --help        Show this help text.

Any other argument is passed through to `playwright test`, so specs and flags
work as usual:

  ./scripts/e2e.sh tests/create-workspace-behavior.spec.ts
  ./scripts/e2e.sh --port 8765 tests/smoke.spec.ts -- --headed
  ./scripts/e2e.sh --tail 0 tests/workspace-backlog.spec.ts -- -g "backlog"

The last four options exist so common multi-step shell collapses into this one
allowlisted command instead of a `for` loop, an env prefix, a `; npx prettier`
chain, or an `until curl` wait — each of which forces a permission prompt:

  ./scripts/e2e.sh --each --tail 4 tests/a.spec.ts tests/b.spec.ts
  ./scripts/e2e.sh --env PERF_PLAIN_IDENTITIES=1 tests/agents-roster-perf.spec.ts
  ./scripts/e2e.sh --fmt tests/workspace-map-coordinate.spec.ts
  ./scripts/e2e.sh --wait 30 tests/smoke.spec.ts

Exits with Playwright's own exit code; under --each, the number of failed specs.
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
wait_secs=0
each=0
fmt=0
env_pairs=()
specs=()
pw_flags=()

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
	--wait)
		[[ $# -ge 2 ]] || fail "--wait needs a value"
		wait_secs="$2"
		shift 2
		;;
	--each)
		each=1
		shift
		;;
	--fmt)
		fmt=1
		shift
		;;
	--env)
		[[ $# -ge 2 ]] || fail "--env needs a KEY=VALUE pair"
		[[ "$2" =~ ^[A-Za-z_][A-Za-z0-9_]*= ]] || fail "--env expects KEY=VALUE, got '$2'"
		env_pairs+=("$2")
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	--)
		shift
		# Everything after `--` belongs to Playwright verbatim, and is never
		# scanned for specs: a `-g "some title"` value is a bare word that would
		# otherwise look like one.
		pw_flags+=("$@")
		break
		;;
	-*)
		pw_flags+=("$1")
		shift
		;;
	*)
		specs+=("$1")
		shift
		;;
	esac
done

[[ "$tail_lines" =~ ^[0-9]+$ ]] || fail "--tail expects a non-negative integer, got '$tail_lines'"
[[ "$wait_secs" =~ ^[0-9]+$ ]] || fail "--wait expects a non-negative integer, got '$wait_secs'"
[[ -n "$base_url" ]] || base_url="http://localhost:$port"

if ((each)) && [[ ${#specs[@]} -eq 0 ]]; then
	fail "--each needs at least one spec"
fi

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || fail "must run inside a git worktree"
cd "$repo_root"

server_up() { curl -sf -o /dev/null --max-time 3 "$base_url"; }

if ((check_server)); then
	if ((wait_secs > 0)); then
		waited=0
		until server_up; do
			((waited < wait_secs)) || fail "server at $base_url did not come up within ${wait_secs}s"
			sleep 1
			waited=$((waited + 1))
		done
	elif ! server_up; then
		fail "no server responding at $base_url — start one with 'wt demo ${port}' (or pass --url / --wait / --no-check)"
	fi
fi

if ((fmt)); then
	[[ ${#specs[@]} -gt 0 ]] || fail "--fmt needs at least one spec to format"
	npx prettier --write "${specs[@]}" >/dev/null
fi

# Default to the list reporter unless the caller picked their own; the config's
# html reporter is noise when the output is being read by an agent.
has_reporter=0
for arg in ${pw_flags[@]+"${pw_flags[@]}"}; do
	case "$arg" in
	--reporter | --reporter=*) has_reporter=1 ;;
	esac
done
((has_reporter)) || pw_flags+=(--reporter=list)

# Runs Playwright over the specs given as arguments, printing only the tail.
# Returns Playwright's exit code.
run_specs() {
	local out status=0
	out="$(mktemp "${TMPDIR:-/tmp}/ori-e2e.XXXXXX")"

	env ${env_pairs[@]+"${env_pairs[@]}"} PLAYWRIGHT_BASE_URL="$base_url" \
		npx playwright test "$@" ${pw_flags[@]+"${pw_flags[@]}"} >"$out" 2>&1 || status=$?

	if ((tail_lines > 0)); then
		tail -n "$tail_lines" "$out"
	else
		tail -n +1 "$out"
	fi
	rm -f "$out"
	return "$status"
}

printf 'Base URL: %s\n' "$base_url"

if ((each)); then
	failed=0
	for spec in "${specs[@]}"; do
		printf '\n=== %s ===\n' "$spec"
		if run_specs "$spec"; then
			printf '%-46s PASS\n' "$(basename "$spec")"
		else
			printf '%-46s FAIL\n' "$(basename "$spec")"
			failed=$((failed + 1))
		fi
	done
	printf '\n%d/%d specs passed\n' "$((${#specs[@]} - failed))" "${#specs[@]}"
	exit "$failed"
fi

printf 'Running:  npx playwright test %s %s\n\n' \
	"${specs[*]-}" "${pw_flags[*]-}"

status=0
run_specs ${specs[@]+"${specs[@]}"} || status=$?
exit "$status"
