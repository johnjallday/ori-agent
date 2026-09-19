#!/usr/bin/env bash
#
# e2e-fresh.sh — run Playwright specs, each against its own freshly started,
# isolated demo server.
#
# Usage:
#   ./scripts/e2e-fresh.sh [--port PORT] [--tail N] [--rev REV] spec [spec ...] [-- playwright args]
#
# --rev REV serves a build of another commit (for example origin/dev) instead
# of the working tree, so the same specs give the baseline to compare with.
#
# Many specs change durable state a later spec would trip over (a hire cannot
# be undone, onboarding is completed once), so comparing a branch with its
# baseline means one clean sandbox per spec. By hand that is start a server in
# the background, wait, run, stop it, repeat. This does it in one command:
#
#   1. builds the working tree once (bin/ori-agent),
#   2. for each spec: a new sandbox under $TMPDIR (HOME and ORI_DATA_DIR both
#      redirected, started from inside it, exactly as scripts/demo-server.sh
#      does), then ./scripts/e2e.sh against it, then the server is stopped by
#      its PID,
#   3. prints one PASS/FAIL line per spec with Playwright's own summary.
#
# The port must be free: nothing already listening is ever stopped. Sandboxes
# are left under $TMPDIR (their paths are printed) so a failure can be read.
#
# Example:
#   ./scripts/e2e-fresh.sh tests/personal-assistant-foundation.spec.ts \
#     tests/starter-missions.spec.ts -- --workers=1

set -euo pipefail

port=8947
tail_lines=40
rev=""
specs=()
pw_args=()
while [[ $# -gt 0 ]]; do
	case "$1" in
	--port)
		port="${2:-}"
		shift 2
		;;
	--rev)
		rev="${2:-}"
		shift 2
		;;
	--tail)
		tail_lines="${2:-}"
		shift 2
		;;
	--)
		shift
		pw_args=("$@")
		break
		;;
	*)
		specs+=("$1")
		shift
		;;
	esac
done

[[ "$port" =~ ^[0-9]+$ ]] || {
	echo "port must be a number: $port" >&2
	exit 2
}
[[ ${#specs[@]} -gt 0 ]] || {
	echo "usage: $0 [--port PORT] [--tail N] [--rev REV] spec [spec ...] [-- playwright args]" >&2
	exit 2
}

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if lsof -nP -t -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
	echo "port $port is already in use; pick another with --port" >&2
	exit 2
fi

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
if [[ -n "$rev" ]]; then
	# The same cached build scripts/demo-server.sh --rev uses; the specs still
	# come from the working tree, so a branch's specs run against its baseline.
	sha="$(git rev-parse --verify --quiet "${rev}^{commit}" || true)"
	[[ -n "$sha" ]] || {
		echo "unknown commit: $rev" >&2
		exit 2
	}
	build_root="$tmp_root/ori-rev-${sha:0:12}"
	binary="$build_root/ori-agent"
	if [[ ! -x "$binary" ]]; then
		mkdir -p "$build_root/src"
		git archive "$sha" | tar -x -C "$build_root/src"
		(cd "$build_root/src" && go build -o "$binary" ./cmd/server)
	fi
	echo "server: rev ${sha:0:12}"
else
	go build -o bin/ori-agent ./cmd/server
	binary="$repo_root/bin/ori-agent"
fi

server_pid=""
stop_server() {
	[[ -n "$server_pid" ]] || return 0
	kill "$server_pid" 2>/dev/null || true
	# Until it exits the server owns its sandbox's installation lock.
	for _ in $(seq 1 300); do
		kill -0 "$server_pid" 2>/dev/null || break
		sleep 0.1
	done
	server_pid=""
}
trap stop_server EXIT

summary=()
overall=0
for spec in "${specs[@]}"; do
	sandbox="$(mktemp -d "$tmp_root/ori-e2e.XXXXXX")"
	# An empty path would point HOME and the data dir at the working tree.
	[[ -n "$sandbox" && -d "$sandbox" ]] || {
		echo "could not create a sandbox under $tmp_root" >&2
		exit 1
	}
	echo "== $spec"
	echo "   sandbox $sandbox"
	(cd "$sandbox" && exec env HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$port" "$binary") \
		>"$sandbox/server.log" 2>&1 &
	server_pid=$!

	log="$sandbox/playwright.log"
	status=0
	# The ${a[@]+...} form: bash 3.2 calls an empty array unbound under set -u.
	./scripts/e2e.sh --port "$port" --wait 180 --tail 0 "$spec" -- ${pw_args[@]+"${pw_args[@]}"} \
		>"$log" 2>&1 || status=$?
	if [[ "$tail_lines" != "0" ]]; then
		tail -n "$tail_lines" "$log"
	fi
	stop_server

	result="$(grep -E '^\s+[0-9]+ (passed|failed|flaky|skipped)' "$log" | tr -s ' ' | tr '\n' ',' | sed 's/,$//')"
	if [[ "$status" -eq 0 ]]; then
		summary+=("PASS $spec —${result}")
	else
		summary+=("FAIL $spec —${result:- exit $status} (log: $log)")
		overall=1
	fi
done

echo
printf '%s\n' "${summary[@]}"
exit "$overall"
