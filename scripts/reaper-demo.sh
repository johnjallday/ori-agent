#!/usr/bin/env bash
# Stage exact REAPER and optional Music Project Management candidates, then
# launch an isolated Ori demo.
#
# Usage:
#   ./scripts/reaper-demo.sh                         # manual REAPER demo on port 8931
#   ./scripts/reaper-demo.sh serve --open            # also open the browser
#   ./scripts/reaper-demo.sh test                    # run legacy coordinated specs
#   ./scripts/reaper-demo.sh artifact                # only refresh the root artifact
#   ./scripts/reaper-demo.sh test --music-source DIR --install-order music-first
#   ./scripts/reaper-demo.sh test --install-order reaper-only -- --headed

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/reaper-demo.sh [serve|test|artifact] [options] [-- playwright args]

Build and verify an exact clean REAPER candidate. By default, also build Ori,
start it with disposable HOME/ORI_DATA_DIR state, and install and enable the
local plugin so the Reaper Song flow is ready to exercise. With --music-source,
stage the exact Music Project Management commit too and run split-ownership
acceptance in the selected installation order.

Commands:
  serve       Launch the prepared manual demo (default); Ctrl-C stops it.
  test        Launch a disposable server and run the coordinated REAPER specs.
  artifact    Only build, verify, and copy the plugin binary to this worktree.

Options:
  --reaper-source DIR   Clean REAPER plugin candidate worktree.
  --music-source DIR    Clean Music Project Management candidate worktree.
  --install-order MODE  music-first, reaper-first, or reaper-only. Providing a
                        music source defaults to music-first.
  --port PORT           Server port (default: 8931).
  --sandbox DIR         Use and preserve this data directory instead of a temp one.
  --keep                Preserve the generated temporary sandbox after exit.
  --open                Open the manual demo in the default browser (macOS).
  -h, --help            Show this help.

Environment:
  ORI_REAPER_DEMO_PORT=PORT          Change the default port.
  ORI_KEEP_REAPER_SANDBOX=1         Preserve generated state after exit.
  ORI_REAPER_PLUGIN_SOURCE=DIR      Same as --reaper-source.
  ORI_MUSIC_PLUGIN_SOURCE=DIR       Same as --music-source.
  ORI_MUSIC_REAPER_INSTALL_ORDER=MODE

The refreshed binary is written to ./reaper-plugin-darwin-arm64. Candidate
commits are exported into disposable plugin-source paths; only the staged REAPER
manifest is rewritten to use its deterministic bundled artifact. Serve/test
label that copy as local development evidence, not a release-verified integration.
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
reaper_source="${ORI_REAPER_PLUGIN_SOURCE:-}"
music_source="${ORI_MUSIC_PLUGIN_SOURCE:-}"
install_order="${ORI_MUSIC_REAPER_INSTALL_ORDER:-}"
playwright_args=()

while [[ $# -gt 0 ]]; do
	case "$1" in
	--reaper-source)
		[[ $# -ge 2 ]] || fail "--reaper-source needs a directory"
		reaper_source="$2"
		shift 2
		;;
	--music-source)
		[[ $# -ge 2 ]] || fail "--music-source needs a directory"
		music_source="$2"
		shift 2
		;;
	--install-order)
		[[ $# -ge 2 ]] || fail "--install-order needs a value"
		install_order="$2"
		shift 2
		;;
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
if [[ -n "$install_order" && "$install_order" != "music-first" && "$install_order" != "reaper-first" && "$install_order" != "reaper-only" ]]; then
	fail "--install-order must be music-first, reaper-first, or reaper-only"
fi
if [[ -n "$music_source" && -z "$install_order" ]]; then
	install_order="music-first"
fi
if [[ "$install_order" == "music-first" || "$install_order" == "reaper-first" ]]; then
	[[ -n "$music_source" ]] || fail "$install_order requires --music-source"
fi
if [[ "$install_order" == "reaper-only" && -n "$music_source" ]]; then
	fail "reaper-only does not accept --music-source"
fi
if [[ "$mode" != "test" && ${#playwright_args[@]} -gt 0 ]]; then
	fail "Playwright arguments are only valid with the test command"
fi
if [[ "$mode" != "serve" && "$open_browser" -eq 1 ]]; then
	fail "--open is only valid with the serve command"
fi

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || fail "must run from an Ori Git worktree"
plugin_root="${reaper_source:-$repo_root/plugins/src/reaper-plugin}"
plugin_root="$(CDPATH= cd -- "$plugin_root" 2>/dev/null && pwd)" || \
	fail "REAPER source does not exist; set --reaper-source to the clean candidate worktree"
wrapper="$plugin_root/scripts/with-local-artifact.sh"
verify="$plugin_root/scripts/verify-artifact.sh"
plugin_artifact="$plugin_root/artifacts/reaper-plugin-darwin-arm64"
root_artifact="$repo_root/reaper-plugin-darwin-arm64"

[[ -x "$wrapper" ]] || fail "missing coordinated plugin checkout at $plugin_root"
git -C "$plugin_root" rev-parse --is-inside-work-tree >/dev/null 2>&1 || \
	fail "REAPER source is not a Git worktree"
[[ -z "$(git -C "$plugin_root" status --porcelain --untracked-files=all)" ]] || \
	fail "REAPER source must be clean so its exact commit can be staged"
reaper_revision="$(git -C "$plugin_root" rev-parse HEAD)"
reaper_tree="$(git -C "$plugin_root" rev-parse HEAD^{tree})"

music_root=""
music_revision=""
music_tree=""
if [[ -n "$music_source" ]]; then
	music_root="$(CDPATH= cd -- "$music_source" 2>/dev/null && pwd)" || \
		fail "music source does not exist: $music_source"
	git -C "$music_root" rev-parse --is-inside-work-tree >/dev/null 2>&1 || \
		fail "music source is not a Git worktree"
	[[ -z "$(git -C "$music_root" status --porcelain --untracked-files=all)" ]] || \
		fail "music source must be clean so its exact commit can be staged"
	[[ -x "$music_root/scripts/validate-package.py" ]] || \
		fail "music candidate has no executable scripts/validate-package.py"
	music_revision="$(git -C "$music_root" rev-parse HEAD)"
	music_tree="$(git -C "$music_root" rev-parse HEAD^{tree})"
fi

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

# Build deterministic service bytes while the helper restores any temporary
# source-manifest change before the server starts. Export the committed tree,
# then make only the staged manifest point at those bundled local bytes.
"$wrapper" true
refresh_artifact
reaper_archive="$sandbox/evidence/reaper-plugin.tar"
bundled_plugin="$sandbox/plugin-source/reaper-plugin"
git -C "$plugin_root" archive --format=tar --output="$reaper_archive" "$reaper_revision"
reaper_archive_sha256="$(shasum -a 256 "$reaper_archive" | awk '{print $1}')"
rm -rf -- "$bundled_plugin"
mkdir -p "$bundled_plugin"
tar -xf "$reaper_archive" -C "$bundled_plugin"
mkdir -p "$bundled_plugin/artifacts"
install -m 0755 "$plugin_artifact" "$bundled_plugin/artifacts/reaper-plugin-darwin-arm64"
python3 - "$bundled_plugin/.ori-plugin/plugin.json" <<'PY'
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = json.loads(path.read_text())
artifact = data["services"][0]["artifacts"][0]
artifact["source"] = {
    "kind": "bundled",
    "path": "artifacts/reaper-plugin-darwin-arm64",
}
path.write_text(json.dumps(data, indent=2) + "\n")
PY
printf '%s\n' "$reaper_revision" >"$sandbox/evidence/reaper-candidate-revision.txt"
printf '%s\n' "$reaper_tree" >"$sandbox/evidence/reaper-candidate-tree.txt"
printf '%s\n' "$reaper_archive_sha256" >"$sandbox/evidence/reaper-candidate-archive-sha256.txt"

bundled_music=""
music_archive_sha256=""
if [[ -n "$music_root" ]]; then
	music_archive="$sandbox/evidence/music-project-management.tar"
	bundled_music="$sandbox/plugin-source/music-project-management"
	git -C "$music_root" archive --format=tar --output="$music_archive" "$music_revision"
	music_archive_sha256="$(shasum -a 256 "$music_archive" | awk '{print $1}')"
	mkdir -p "$bundled_music"
	tar -xf "$music_archive" -C "$bundled_music"
	"$bundled_music/scripts/validate-package.py" "$bundled_music" \
		| tee "$sandbox/evidence/music-package-validation.txt"
	printf '%s\n' "$music_revision" >"$sandbox/evidence/music-candidate-revision.txt"
	printf '%s\n' "$music_tree" >"$sandbox/evidence/music-candidate-tree.txt"
	printf '%s\n' "$music_archive_sha256" >"$sandbox/evidence/music-candidate-archive-sha256.txt"
fi

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

install_candidate() {
	local plugin_id="$1"
	local source_path="$2"
	local format="$3"
	local evidence_name="$4"
	local install_body
	install_body="$(python3 - "$source_path" "$format" <<'PY'
import json
import sys
body = {"source": sys.argv[1], "confirm": True}
if sys.argv[2]:
    body["format"] = sys.argv[2]
print(json.dumps(body))
PY
)"
	curl -fsS -X POST "$base_url/api/plugins/install" \
		-H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest' \
		--data "$install_body" >"$sandbox/evidence/${evidence_name}-install.json"
	curl -fsS -X POST "$base_url/api/plugins/$plugin_id/enable" \
		-H 'X-Requested-With: XMLHttpRequest' \
		>"$sandbox/evidence/${evidence_name}-enable.json"
}

capture_snapshot() {
	local destination="$1"
	python3 - "$base_url" "$destination" <<'PY'
import json
import sys
import urllib.request

base, destination = sys.argv[1:]
paths = {
    "plugins": "/api/plugins",
    "project_templates": "/api/project-templates",
    "group_templates": "/api/workspaces/group-templates",
    "workspaces": "/api/workspaces",
}
result = {}
for key, path in paths.items():
    with urllib.request.urlopen(base + path, timeout=10) as response:
        result[key] = json.load(response)
with open(destination, "w") as handle:
    json.dump(result, handle, indent=2, sort_keys=True)
PY
}

first_snapshot=""
final_snapshot=""
if [[ -n "$install_order" ]]; then
	case "$install_order" in
	music-first)
		install_candidate music-project-management "$bundled_music" claude first-music
		;;
	reaper-first | reaper-only)
		install_candidate reaper-plugin "$bundled_plugin" "" first-reaper
		;;
	esac
	first_snapshot="$sandbox/evidence/after-first-install.json"
	capture_snapshot "$first_snapshot"
	case "$install_order" in
	music-first)
		install_candidate reaper-plugin "$bundled_plugin" "" second-reaper
		;;
	reaper-first)
		install_candidate music-project-management "$bundled_music" claude second-music
		;;
	esac
	final_snapshot="$sandbox/evidence/after-final-install.json"
	capture_snapshot "$final_snapshot"
elif [[ "$mode" == "serve" ]]; then
	install_candidate reaper-plugin "$bundled_plugin" "" reaper
fi

printf 'SANDBOX=%s\n' "$sandbox"
printf 'ORI_URL=%s\n' "$base_url"
printf 'ORI_LOG=%s\n' "$server_log"
printf 'REAPER_PLUGIN_SOURCE=%s\n' "$bundled_plugin"
printf 'REAPER_PLUGIN_REVISION=%s\n' "$reaper_revision"
printf 'REAPER_PLUGIN_TREE=%s\n' "$reaper_tree"
printf 'REAPER_PLUGIN_ARCHIVE_SHA256=%s\n' "$reaper_archive_sha256"
if [[ -n "$bundled_music" ]]; then
	printf 'MUSIC_PLUGIN_SOURCE=%s\n' "$bundled_music"
	printf 'MUSIC_PLUGIN_REVISION=%s\n' "$music_revision"
	printf 'MUSIC_PLUGIN_TREE=%s\n' "$music_tree"
	printf 'MUSIC_PLUGIN_ARCHIVE_SHA256=%s\n' "$music_archive_sha256"
fi

if [[ "$mode" == "test" ]]; then
	set +e
	if [[ -n "$install_order" ]]; then
		printf 'Running exact Music/REAPER candidate acceptance (%s)...\n' "$install_order"
		env PLAYWRIGHT_BASE_URL="$base_url" \
			ORI_MUSIC_REAPER_ACCEPTANCE=1 \
			ORI_MUSIC_REAPER_INSTALL_ORDER="$install_order" \
			ORI_REAPER_PLUGIN_PATH="$bundled_plugin" \
			ORI_REAPER_PLUGIN_REVISION="$reaper_revision" \
			ORI_MUSIC_PLUGIN_PATH="$bundled_music" \
			ORI_MUSIC_PLUGIN_REVISION="$music_revision" \
			ORI_MUSIC_REAPER_FIRST_SNAPSHOT="$first_snapshot" \
			ORI_MUSIC_REAPER_FINAL_SNAPSHOT="$final_snapshot" \
			ORI_MUSIC_REAPER_EVIDENCE_DIR="$sandbox/evidence/screenshots" \
			ORI_MUSIC_REAPER_SANDBOX="$sandbox" \
			npx playwright test tests/music-reaper-candidates.spec.ts \
			--project=chromium --workers=1 ${playwright_args[@]+"${playwright_args[@]}"}
	else
		printf 'Running coordinated REAPER browser tests...\n'
		env PLAYWRIGHT_BASE_URL="$base_url" \
			ORI_REAPER_PLUGIN_PATH="$bundled_plugin" \
			ORI_REAPER_EVIDENCE_DIR="$sandbox/evidence" \
			ORI_REAPER_EXPECT_DEVELOPMENT_COPY=1 \
			npx playwright test \
			tests/reaper-plugin-surface.spec.ts \
			tests/reaper-project-tidy.spec.ts \
			--project=chromium --workers=1 ${playwright_args[@]+"${playwright_args[@]}"}
	fi
	test_status=$?
	set -e
	exit "$test_status"
fi

printf '\nREAPER local demo is ready.\n'
printf 'Open: %s\n' "$base_url"
if [[ "$install_order" == "music-first" || "$install_order" == "reaper-first" ]]; then
	printf 'Create a Reaper Song workspace and review its independently owned Music Production Home and project team.\n'
elif [[ "$install_order" == "reaper-only" ]]; then
	printf 'Customize Reaper Song for a Home-free standalone project.\n'
else
	printf 'Create a Reaper Song workspace and review its Required Music Production Home and project team.\n'
fi
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
