#!/usr/bin/env bash
# Stage one exact Music Project Management candidate and launch isolated Ori.
#
# Nothing is installed under the real HOME. The clean candidate commit is
# exported into the disposable sandbox, validated there, and installed through
# Ori's normal plugin API after the server starts.

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/music-home-demo.sh [serve|test] --source DIR [options] [-- playwright args]

Stage an exact clean Music Project Management commit, build this Ori worktree,
start it with disposable HOME/ORI_DATA_DIR state, and install and enable the
staged package through the real plugin API.

Commands:
  serve       Launch the prepared manual demo (default); Ctrl-C stops it.
  test        Run the real-candidate Music Production Home browser acceptance.

Options:
  --source DIR      Clean Music Project Management candidate worktree.
  --port PORT       Server port (default: 8931).
  --sandbox DIR     Use and preserve this sandbox instead of a temporary one.
  --keep            Preserve the generated temporary sandbox after exit.
  --open            Open the manual demo in the default browser (macOS).
  -h, --help        Show this help.

Environment:
  ORI_MUSIC_PLUGIN_SOURCE=DIR     Same as --source.
  ORI_MUSIC_HOME_DEMO_PORT=PORT  Change the default port.
  ORI_KEEP_MUSIC_SANDBOX=1       Preserve generated state after exit.

The script prints and records the exact candidate commit, Git tree, archive
SHA-256, and package-validator output. It creates no branch, commit, tag,
release, registry entry, or real user installation.
EOF
}

fail() {
	printf 'music-home-demo: %s\n' "$*" >&2
	exit 2
}

mode="serve"
case "${1:-}" in
serve | test)
	mode="$1"
	shift
	;;
esac

plugin_source="${ORI_MUSIC_PLUGIN_SOURCE:-}"
port="${ORI_MUSIC_HOME_DEMO_PORT:-8931}"
sandbox=""
keep_sandbox="${ORI_KEEP_MUSIC_SANDBOX:-0}"
open_browser=0
playwright_args=()

while [[ $# -gt 0 ]]; do
	case "$1" in
	--source)
		[[ $# -ge 2 ]] || fail "--source needs a directory"
		plugin_source="$2"
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

[[ -n "$plugin_source" ]] || fail "--source or ORI_MUSIC_PLUGIN_SOURCE is required"
[[ "$port" =~ ^[0-9]+$ ]] || fail "port must be numeric"
((port >= 1 && port <= 65535)) || fail "port must be between 1 and 65535"
[[ "$keep_sandbox" == "0" || "$keep_sandbox" == "1" ]] || \
	fail "ORI_KEEP_MUSIC_SANDBOX must be 0 or 1"
if [[ "$mode" != "test" && ${#playwright_args[@]} -gt 0 ]]; then
	fail "Playwright arguments are only valid with the test command"
fi
if [[ "$mode" != "serve" && "$open_browser" -eq 1 ]]; then
	fail "--open is only valid with the serve command"
fi

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || fail "must run from an Ori Git worktree"
plugin_root="$(CDPATH= cd -- "$plugin_source" 2>/dev/null && pwd)" || \
	fail "music plugin source does not exist: $plugin_source"
git -C "$plugin_root" rev-parse --is-inside-work-tree >/dev/null 2>&1 || \
	fail "music plugin source is not a Git worktree"
[[ -z "$(git -C "$plugin_root" status --porcelain --untracked-files=all)" ]] || \
	fail "music plugin source must be clean so its exact commit can be staged"
[[ -x "$plugin_root/scripts/validate-package.py" ]] || \
	fail "candidate has no executable scripts/validate-package.py"

candidate_revision="$(git -C "$plugin_root" rev-parse HEAD)"
candidate_tree="$(git -C "$plugin_root" rev-parse HEAD^{tree})"

base_url="http://127.0.0.1:$port"
if curl -fsS -o /dev/null --max-time 1 "$base_url/health" 2>/dev/null; then
	fail "port $port already has an Ori server; choose another with --port"
fi

if [[ -n "$sandbox" ]]; then
	mkdir -p "$sandbox"
	sandbox="$(CDPATH= cd -- "$sandbox" && pwd)"
else
	sandbox="$(mktemp -d "${TMPDIR:-/tmp}/ori-music-home-demo.XXXXXX")"
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
		printf 'Preserved Music Home demo sandbox: %s\n' "$sandbox"
	else
		rm -rf -- "$sandbox"
	fi
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

archive="$sandbox/evidence/music-project-management.tar"
bundled_plugin="$sandbox/plugin-source/music-project-management"
git -C "$plugin_root" archive --format=tar --output="$archive" "$candidate_revision"
archive_sha256="$(shasum -a 256 "$archive" | awk '{print $1}')"
mkdir -p "$bundled_plugin"
tar -xf "$archive" -C "$bundled_plugin"
"$bundled_plugin/scripts/validate-package.py" "$bundled_plugin" \
	| tee "$sandbox/evidence/package-validation.txt"
printf '%s\n' "$candidate_revision" >"$sandbox/evidence/candidate-revision.txt"
printf '%s\n' "$candidate_tree" >"$sandbox/evidence/candidate-tree.txt"
printf '%s\n' "$archive_sha256" >"$sandbox/evidence/candidate-archive-sha256.txt"

cd "$repo_root"
printf 'Building Ori server...\n'
go build -o bin/ori-agent ./cmd/server

(
	cd "$sandbox"
	exec env HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$port" \
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

install_body="$(python3 - "$bundled_plugin" <<'PY'
import json
import sys
print(json.dumps({"source": sys.argv[1], "format": "claude", "confirm": True}))
PY
)"
curl -fsS -X POST "$base_url/api/plugins/install" \
	-H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest' \
	--data "$install_body" >"$sandbox/evidence/plugin-install.json"
curl -fsS -X POST "$base_url/api/plugins/music-project-management/enable" \
	-H 'X-Requested-With: XMLHttpRequest' >"$sandbox/evidence/plugin-enable.json"

skill_root="$sandbox/.agents/skills/music-project-management"
[[ -f "$skill_root/SKILL.md" ]] || fail "Ori did not stage the packaged skill under disposable HOME"
[[ -f "$skill_root/.ori-plugin-skill.json" ]] || fail "staged skill has no ownership receipt"
source_skill_sha256="$(shasum -a 256 "$bundled_plugin/skills/music-project-management/SKILL.md" | awk '{print $1}')"
staged_skill_sha256="$(shasum -a 256 "$skill_root/SKILL.md" | awk '{print $1}')"
[[ "$source_skill_sha256" == "$staged_skill_sha256" ]] || fail "staged skill differs from the candidate"

printf 'SANDBOX=%s\n' "$sandbox"
printf 'ORI_URL=%s\n' "$base_url"
printf 'ORI_LOG=%s\n' "$server_log"
printf 'MUSIC_PLUGIN_SOURCE=%s\n' "$bundled_plugin"
printf 'MUSIC_PLUGIN_REVISION=%s\n' "$candidate_revision"
printf 'MUSIC_PLUGIN_TREE=%s\n' "$candidate_tree"
printf 'MUSIC_PLUGIN_ARCHIVE_SHA256=%s\n' "$archive_sha256"
printf 'MUSIC_SKILL_SHA256=%s\n' "$staged_skill_sha256"

if [[ "$mode" == "test" ]]; then
	printf 'Running Music Production Home real-candidate acceptance...\n'
	set +e
	env PLAYWRIGHT_BASE_URL="$base_url" \
		ORI_MUSIC_HOME_ACCEPTANCE=1 \
		ORI_MUSIC_PLUGIN_PATH="$bundled_plugin" \
		ORI_MUSIC_PLUGIN_REVISION="$candidate_revision" \
		ORI_MUSIC_PLUGIN_TREE="$candidate_tree" \
		ORI_MUSIC_PLUGIN_ARCHIVE_SHA256="$archive_sha256" \
		ORI_MUSIC_HOME_EVIDENCE_DIR="$sandbox/evidence/screenshots" \
		npx playwright test tests/music-home-candidate.spec.ts \
		--project=chromium --workers=1 ${playwright_args[@]+"${playwright_args[@]}"}
	test_status=$?
	set -e
	exit "$test_status"
fi

printf '\nMusic Production Home local demo is ready.\n'
printf 'Open: %s\n' "$base_url"
printf 'Use Create Group to review the staged package and its separate role setup.\n'
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
