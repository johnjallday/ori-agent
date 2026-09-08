#!/usr/bin/env bash
# Build the coordinated local REAPER plugin and launch an isolated Ori demo.
#
# Usage:
#   ./scripts/reaper-demo.sh                         # manual demo on port 8931
#   ./scripts/reaper-demo.sh serve --open            # also open the browser
#   ./scripts/reaper-demo.sh test                    # run coordinated browser tests
#   ./scripts/reaper-demo.sh artifact                # only refresh the root artifact
#   ./scripts/reaper-demo.sh test -- --headed        # pass flags to Playwright

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/reaper-demo.sh [serve|test|artifact] [options] [-- playwright args]

Build and verify the nested REAPER plugin artifact. By default, also build Ori,
start it with disposable HOME/ORI_DATA_DIR state, and install and enable the
local plugin so the Reaper Song flow is ready to exercise.

Commands:
  serve       Launch the prepared manual demo (default); Ctrl-C stops it.
  test        Launch a disposable server and run the coordinated REAPER specs.
  artifact    Only build, verify, and copy the plugin binary to this worktree.

Options:
  --port PORT       Server port (default: 8931).
  --sandbox DIR     Use and preserve this data directory instead of a temp one.
  --keep            Preserve the generated temporary sandbox after exit.
  --open            Open the manual demo in the default browser (macOS).
  -h, --help        Show this help.

Environment:
  ORI_REAPER_DEMO_PORT=PORT       Change the default port.
  ORI_KEEP_REAPER_SANDBOX=1      Preserve generated state after exit.

The refreshed binary is written to ./reaper-plugin-darwin-arm64. The canonical
plugin checkout and artifact remain under plugins/src/reaper-plugin. Serve/test
allow only their staged local copy to satisfy setup, labelled as a development
copy rather than a release-verified integration.
EOF
}

fail() {
	printf 'reaper-demo: %s\n' "$*" >&2
	exit 2
}

mode="serve"
case "${1:-}" in
serve | test | artifact)
	mode="$1"
	shift
	;;
esac

port="${ORI_REAPER_DEMO_PORT:-8931}"
sandbox=""
keep_sandbox="${ORI_KEEP_REAPER_SANDBOX:-0}"
open_browser=0
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
		keep_sandbox=1
		shift 2
		;;
	--keep)
		keep_sandbox=1
		shift
		;;
	--open)
		open_browser=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	--)
		shift
		playwright_args=("$@")
		break
		;;
	*)
		fail "unknown argument '$1'"
		;;
	esac
done

[[ "$port" =~ ^[0-9]+$ ]] || fail "port must be numeric"
((port >= 1 && port <= 65535)) || fail "port must be between 1 and 65535"
[[ "$keep_sandbox" == "0" || "$keep_sandbox" == "1" ]] || \
	fail "ORI_KEEP_REAPER_SANDBOX must be 0 or 1"
if [[ "$mode" != "test" && ${#playwright_args[@]} -gt 0 ]]; then
	fail "Playwright arguments are only valid with the test command"
fi
if [[ "$mode" != "serve" && "$open_browser" -eq 1 ]]; then
	fail "--open is only valid with the serve command"
fi

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || fail "must run from an Ori Git worktree"
plugin_root="$repo_root/plugins/src/reaper-plugin"
wrapper="$plugin_root/scripts/with-local-artifact.sh"
verify="$plugin_root/scripts/verify-artifact.sh"
plugin_artifact="$plugin_root/artifacts/reaper-plugin-darwin-arm64"
root_artifact="$repo_root/reaper-plugin-darwin-arm64"

[[ -x "$wrapper" ]] || fail "missing coordinated plugin checkout at $plugin_root"

refresh_artifact() {
	"$verify"
	install -m 0755 "$plugin_artifact" "$root_artifact"
	printf 'REAPER_ARTIFACT=%s\n' "$root_artifact"
	"$root_artifact" version | awk '{ print "REAPER_PLUGIN_VERSION=" $0 }'
}

if [[ "$mode" == "artifact" ]]; then
	# This helper restores the committed manifest immediately after building.
	"$wrapper" true
	refresh_artifact
	exit 0
fi

base_url="http://127.0.0.1:$port"
if curl -fsS -o /dev/null --max-time 1 "$base_url/health" 2>/dev/null; then
	fail "port $port already has an Ori server; choose another with --port"
fi

if [[ -n "$sandbox" ]]; then
	mkdir -p "$sandbox"
	sandbox="$(CDPATH= cd -- "$sandbox" && pwd)"
else
	sandbox="$(mktemp -d "${TMPDIR:-/tmp}/ori-reaper-demo.XXXXXX")"
fi
mkdir -p "$sandbox/evidence" "$sandbox/plugin-source"
server_log="$sandbox/ori.log"
server_pid=""

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	if [[ "$keep_sandbox" == "1" ]]; then
		printf 'Preserved REAPER demo sandbox: %s\n' "$sandbox"
	else
		rm -rf -- "$sandbox"
	fi
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

# Keep the canonical plugin checkout clean: the helper briefly prepares its
# bundled manifest, copies that exact state into the disposable sandbox, and
# restores the source manifest before the long-running server starts.
bundled_plugin="$sandbox/plugin-source/reaper-plugin"
rm -rf -- "$bundled_plugin"
"$wrapper" cp -R "$plugin_root" "$bundled_plugin"
refresh_artifact

cd "$repo_root"
printf 'Building Ori server...\n'
go build -o bin/ori-agent ./cmd/server

(
	cd "$sandbox"
	# This process-local path authorizes only the exact staged demo copy to
	# satisfy the journey prerequisite. It does not publish or release-verify it.
	exec env HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$port" \
		ORI_REVIEWED_INTEGRATION_DEV_SOURCE="$bundled_plugin" \
		"$repo_root/bin/ori-agent"
) >"$server_log" 2>&1 &
server_pid=$!

ready=0
for _ in {1..120}; do
	if curl -fsS -o /dev/null --max-time 1 "$base_url/health" 2>/dev/null; then
		ready=1
		break
	fi
	if ! kill -0 "$server_pid" 2>/dev/null; then
		printf 'Ori exited before becoming ready. Log:\n' >&2
		tail -n 80 "$server_log" >&2 || true
		exit 1
	fi
	sleep 0.5
done
if ((ready == 0)); then
	printf 'Ori did not become ready. Log:\n' >&2
	tail -n 80 "$server_log" >&2 || true
	exit 1
fi

printf 'SANDBOX=%s\n' "$sandbox"
printf 'ORI_URL=%s\n' "$base_url"
printf 'ORI_LOG=%s\n' "$server_log"

if [[ "$mode" == "test" ]]; then
	printf 'Running coordinated REAPER browser tests...\n'
	set +e
	env PLAYWRIGHT_BASE_URL="$base_url" \
		ORI_REAPER_PLUGIN_PATH="$bundled_plugin" \
		ORI_REAPER_EVIDENCE_DIR="$sandbox/evidence" \
		ORI_REAPER_EXPECT_DEVELOPMENT_COPY=1 \
		npx playwright test \
		tests/reaper-plugin-surface.spec.ts \
		tests/reaper-project-tidy.spec.ts \
		--project=chromium --workers=1 ${playwright_args[@]+"${playwright_args[@]}"}
	test_status=$?
	set -e
	exit "$test_status"
fi

install_body="$(python3 - "$bundled_plugin" <<'PY'
import json
import sys
print(json.dumps({"source": sys.argv[1], "confirm": True}))
PY
)"
curl -fsS -X POST "$base_url/api/plugins/install" \
	-H 'Content-Type: application/json' --data "$install_body" \
	>"$sandbox/plugin-install.json"
curl -fsS -X POST "$base_url/api/plugins/reaper-plugin/enable" \
	>"$sandbox/plugin-enable.json"

printf '\nREAPER local demo is ready.\n'
printf 'Open: %s\n' "$base_url"
printf 'Create a Reaper Song workspace and review its Team; Producer Home is optional.\n'
printf 'Press Ctrl-C to stop the server.\n\n'

if ((open_browser == 1)); then
	command -v open >/dev/null 2>&1 || fail "--open requires the macOS open command"
	open "$base_url"
fi

set +e
wait "$server_pid"
server_status=$?
server_pid=""
set -e
if ((server_status != 0)); then
	printf 'Ori exited with status %d. Log:\n' "$server_status" >&2
	tail -n 80 "$server_log" >&2 || true
fi
exit "$server_status"
