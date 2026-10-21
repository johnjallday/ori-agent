#!/usr/bin/env bash
#
# resource-studio-demo.sh — serve this worktree from an isolated sandbox with
# the REAPER Studio TEST FIXTURE plugins installed, or run the fixture browser
# spec against it.
#
# Usage:
#   ./scripts/resource-studio-demo.sh [serve|test] [--port PORT] [--sandbox DIR] [-- playwright args]
#
# The fixtures in tests/fixtures/resource-studio/ stand in for the Music and
# REAPER candidates: a content-only Music Production Home that allows one
# REAPER Studio, and a REAPER provider whose catalog service answers from fixed
# data (it reads no folder and talks to no REAPER). They are copied into the
# sandbox and installed through Ori's real plugin API. This is host evidence
# only; it is never candidate or real-reader evidence.
#
# The server runs through scripts/demo-server.sh (sandboxed HOME and
# ORI_DATA_DIR, ORI_NO_DESKTOP_OPEN=1), so nothing is written under the real
# HOME. The sandbox is kept and recorded for `wt done`; remove it with one
# `rm -rf` of the printed path. `serve` stays in the foreground until Ctrl-C or
# `./scripts/stop-demo-server.sh PORT`; `test` stops the server when it ends.

set -euo pipefail

fail() {
	printf 'resource-studio-demo: %s\n' "$*" >&2
	exit 2
}

mode="serve"
case "${1:-}" in
serve | test)
	mode="$1"
	shift
	;;
esac

port="8931"
sandbox=""
playwright_args=()
while [[ $# -gt 0 ]]; do
	case "$1" in
	--port)
		[[ $# -ge 2 ]] || fail "--port needs a value"
		port="$2"
		shift 2
		;;
	--sandbox)
		[[ $# -ge 2 ]] || fail "--sandbox needs a directory"
		sandbox="$2"
		shift 2
		;;
	--)
		shift
		playwright_args=("$@")
		break
		;;
	-h | --help)
		sed -n '3,21p' "$0"
		exit 0
		;;
	*)
		fail "unknown argument '$1'"
		;;
	esac
done
[[ "$port" =~ ^[0-9]+$ ]] || fail "port must be numeric"
if [[ "$mode" != "test" && ${#playwright_args[@]} -gt 0 ]]; then
	fail "Playwright arguments are only valid with the test command"
fi

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || fail "must run inside an Ori Git worktree"
cd "$repo_root"
base_url="http://127.0.0.1:$port"
if curl -fsS -o /dev/null --max-time 1 "$base_url/health" 2>/dev/null; then
	fail "port $port already has an Ori server; choose another with --port"
fi

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"
if [[ -z "$sandbox" ]]; then
	sandbox="$(mktemp -d "$tmp_root/ori-demo.XXXXXX")"
fi
mkdir -p "$sandbox/plugin-source"
sandbox="$(CDPATH= cd -- "$sandbox" && pwd)"
music_plugin="$sandbox/plugin-source/music-project-management"
reaper_plugin="$sandbox/plugin-source/reaper-plugin"
[[ -e "$music_plugin" || -e "$reaper_plugin" ]] && fail "sandbox already holds staged fixtures: $sandbox"
cp -R tests/fixtures/resource-studio/music-home "$music_plugin"
cp -R tests/fixtures/resource-studio/reaper-studio "$reaper_plugin"

server_log="$sandbox/ori.log"
./scripts/demo-server.sh "$port" "$sandbox" >"$server_log" 2>&1 &
server_pid=$!
stop_server() {
	trap - EXIT HUP INT TERM
	if kill -0 "$server_pid" 2>/dev/null; then
		./scripts/stop-demo-server.sh "$port" >/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	printf 'Kept sandbox: %s\n' "$sandbox"
}
trap stop_server EXIT
trap 'exit 130' HUP INT TERM

ready=0
for _ in {1..240}; do
	if curl -fsS -o /dev/null --max-time 1 "$base_url/health" 2>/dev/null; then
		ready=1
		break
	fi
	kill -0 "$server_pid" 2>/dev/null || break
	sleep 0.5
done
if ((ready == 0)); then
	tail -n 60 "$server_log" >&2 || true
	fail "Ori did not become ready"
fi

install() {
	local source="$1" name="$2"
	curl -fsS -X POST "$base_url/api/plugins/install" \
		-H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest' \
		--data "{\"source\":\"$source\",\"format\":\"claude\",\"confirm\":true}" >"$sandbox/install-$name.json"
	curl -fsS -X POST "$base_url/api/plugins/$name/enable" \
		-H 'X-Requested-With: XMLHttpRequest' >"$sandbox/enable-$name.json"
}
curl -fsS -X POST "$base_url/api/onboarding/skip" -H 'X-Requested-With: XMLHttpRequest' >/dev/null
install "$music_plugin" music-project-management
install "$reaper_plugin" reaper-plugin

printf 'SANDBOX=%s\nORI_URL=%s\nORI_LOG=%s\n' "$sandbox" "$base_url" "$server_log"
printf 'Installed the TEST FIXTURE Music Home and REAPER Studio provider (not the published packages).\n'

if [[ "$mode" == "test" ]]; then
	set +e
	env PLAYWRIGHT_BASE_URL="$base_url" ORI_RESOURCE_STUDIO_FIXTURES=1 \
		ORI_RESOURCE_STUDIO_SANDBOX="$sandbox" \
		npx playwright test tests/resource-studio-fixture.spec.ts \
		--project=chromium --workers=1 ${playwright_args[@]+"${playwright_args[@]}"}
	status=$?
	set -e
	exit "$status"
fi

printf 'Open %s, use Create Group for the Music Production Home, then Set up REAPER Studio on the Home.\n' "$base_url"
printf 'Press Ctrl-C to stop the server.\n'
set +e
wait "$server_pid"
