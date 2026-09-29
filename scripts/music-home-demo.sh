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

Export an exact clean Music Project Management commit, build this Ori worktree,
and start it with disposable HOME/ORI_DATA_DIR state. By default, install and
enable that local export through the real plugin API. With --provider reviewed,
leave the plugin uninstalled so the portfolio UI can review and install the
published release from Ori's built-in registry instead.

Commands:
  serve       Launch the prepared manual demo (default); Ctrl-C stops it.
  test        Run the real-candidate Music Production Home browser acceptance.

Options:
  --source DIR      Clean Music Project Management candidate worktree.
  --reaper-source DIR  Optional clean REAPER candidate, exported and built only in the
                       disposable sandbox for the paired portfolio-reviewed test.
  --port PORT       Server port (default: 8931).
  --sandbox DIR     Use and preserve this sandbox instead of a temporary one.
  --keep            Preserve the generated temporary sandbox after exit.
  --restart-check   In the paired reviewed test only, restart this sandbox's
                    server once when the browser requests it (never user state).
  --suite NAME      Browser test suite: home (default), portfolio (local refusal),
                    or portfolio-reviewed (published release; test only).
  --provider MODE   local (default) or reviewed (portfolio-reviewed only).
  --open            Open the manual demo in the default browser (macOS).
  -h, --help        Show this help.

Environment:
  ORI_MUSIC_PLUGIN_SOURCE=DIR     Same as --source.
  ORI_MUSIC_HOME_DEMO_PORT=PORT  Change the default port.
  ORI_KEEP_MUSIC_SANDBOX=1       Preserve generated state after exit.

The script prints and records the exact local candidate commit, Git tree,
archive SHA-256, and validator output. In reviewed mode the export is NOT
installed: Ori must resolve and install its published reviewed release after
browser confirmation. The script creates no branch, commit, tag, release,
registry entry, or real user installation.
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
reaper_source=""
port="${ORI_MUSIC_HOME_DEMO_PORT:-8931}"
sandbox=""
keep_sandbox="${ORI_KEEP_MUSIC_SANDBOX:-0}"
test_suite="home"
provider_mode="local"
restart_check=0
open_browser=0
playwright_args=()

while [[ $# -gt 0 ]]; do
	case "$1" in
	--source)
		[[ $# -ge 2 ]] || fail "--source needs a directory"
		plugin_source="$2"
		shift 2
		;;
	--reaper-source)
		[[ $# -ge 2 ]] || fail "--reaper-source needs a directory"
		reaper_source="$2"
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
	--restart-check)
		restart_check=1
		shift
		;;
	--suite)
		[[ $# -ge 2 ]] || fail "--suite needs a name"
		test_suite="$2"
		shift 2
		;;
	--provider)
		[[ $# -ge 2 ]] || fail "--provider needs local or reviewed"
		provider_mode="$2"
		shift 2
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
if [[ -n "$reaper_source" && ( "$mode" != "test" || "$test_suite" != "portfolio-reviewed" || "$provider_mode" != "reviewed" ) ]]; then
	fail "--reaper-source needs test --suite portfolio-reviewed --provider reviewed"
fi
[[ "$port" =~ ^[0-9]+$ ]] || fail "port must be numeric"
((port >= 1 && port <= 65535)) || fail "port must be between 1 and 65535"
[[ "$keep_sandbox" == "0" || "$keep_sandbox" == "1" ]] || \
	fail "ORI_KEEP_MUSIC_SANDBOX must be 0 or 1"
if [[ "$mode" != "test" && ${#playwright_args[@]} -gt 0 ]]; then
	fail "Playwright arguments are only valid with the test command"
fi
[[ "$test_suite" == "home" || "$test_suite" == "portfolio" || "$test_suite" == "portfolio-reviewed" ]] || \
	fail "--suite needs home, portfolio or portfolio-reviewed"
[[ "$provider_mode" == "local" || "$provider_mode" == "reviewed" ]] || fail "--provider needs local or reviewed"
if [[ "$mode" != "test" && "$test_suite" != "home" ]]; then
	fail "--suite is only valid with the test command"
fi
if [[ "$test_suite" == "portfolio-reviewed" && "$provider_mode" != "reviewed" ]] || \
	[[ "$provider_mode" == "reviewed" && ( "$mode" != "test" || "$test_suite" != "portfolio-reviewed" ) ]]; then
	fail "portfolio-reviewed requires test --provider reviewed; other suites require local"
fi
if [[ "$mode" != "serve" && "$open_browser" -eq 1 ]]; then
	fail "--open is only valid with the serve command"
fi
if ((restart_check == 1)) && [[ "$mode" != "test" || "$test_suite" != "portfolio-reviewed" || "$provider_mode" != "reviewed" || -z "$reaper_source" ]]; then
	fail "--restart-check requires test --suite portfolio-reviewed --provider reviewed --reaper-source"
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
reaper_root=""
reaper_revision=""
reaper_tree=""
if [[ -n "$reaper_source" ]]; then
	reaper_root="$(CDPATH= cd -- "$reaper_source" 2>/dev/null && pwd)" || fail "REAPER source is unavailable"
	[[ -z "$(git -C "$reaper_root" status --porcelain --untracked-files=all)" ]] || fail "REAPER source must be clean"
	[[ -x "$reaper_root/scripts/build-local-artifact.sh" ]] || fail "REAPER source cannot build its sandbox artifact"
	reaper_revision="$(git -C "$reaper_root" rev-parse HEAD)"
	reaper_tree="$(git -C "$reaper_root" rev-parse HEAD^{tree})"
fi

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
if ((restart_check == 1)) && { [[ -e "$sandbox/evidence/restart.request" ]] ||
	[[ -e "$sandbox/evidence/restart.inprogress" ]] || [[ -e "$sandbox/evidence/restart.done" ]] ||
	[[ -e "$sandbox/evidence/restart.failed" ]]; }; then
	fail "--restart-check needs a fresh sandbox without prior restart markers"
fi
server_log="$sandbox/ori.log"
server_pid=""
test_pid=""

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	if [[ -n "$test_pid" ]] && kill -0 "$test_pid" 2>/dev/null; then
		kill "$test_pid" 2>/dev/null || true
		wait "$test_pid" 2>/dev/null || true
	fi
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
candidate_version="$(python3 - "$bundled_plugin/.ori-plugin/plugin.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding='utf-8') as handle:
    print(json.load(handle)['version'])
PY
)"
printf '%s\n' "$candidate_revision" >"$sandbox/evidence/candidate-revision.txt"
printf '%s\n' "$candidate_tree" >"$sandbox/evidence/candidate-tree.txt"
printf '%s\n' "$archive_sha256" >"$sandbox/evidence/candidate-archive-sha256.txt"

bundled_reaper=""
if [[ -n "$reaper_root" ]]; then
	reaper_archive="$sandbox/evidence/reaper-plugin.tar"
	bundled_reaper="$sandbox/plugin-source/reaper-plugin"
	git -C "$reaper_root" archive --format=tar --output="$reaper_archive" "$reaper_revision"
	mkdir -p "$bundled_reaper"
	tar -xf "$reaper_archive" -C "$bundled_reaper"
	(cd "$bundled_reaper" && ./scripts/build-local-artifact.sh) >"$sandbox/evidence/reaper-artifact-build.txt"
	python3 - "$bundled_reaper/.ori-plugin/plugin.json" <<'PY'
import json
import sys
from pathlib import Path
path = Path(sys.argv[1])
data = json.loads(path.read_text())
data['services'][0]['artifacts'][0]['source'] = {'kind': 'bundled', 'path': 'artifacts/reaper-plugin-darwin-arm64'}
path.write_text(json.dumps(data, indent=2) + '\n')
PY
	printf '%s\n' "$reaper_revision" >"$sandbox/evidence/reaper-candidate-revision.txt"
	printf '%s\n' "$reaper_tree" >"$sandbox/evidence/reaper-candidate-tree.txt"
fi

cd "$repo_root"
printf 'Building Ori server...\n'
go build -o bin/ori-agent ./cmd/server

(
	cd "$sandbox"
	exec env HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$port" ORI_NO_DESKTOP_OPEN=1 \
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

# A reviewed-run export is validated as evidence but is never installed.
skill_root="$bundled_plugin/skills/music-project-management"
[[ -f "$skill_root/SKILL.md" ]] || fail "packaged skill is missing from the disposable plugin export"
if [[ "$provider_mode" == "local" ]]; then
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

# Installed plugin skills are read in place from the package folder. Ori no
# longer copies them into ~/.agents/skills or writes legacy ownership receipts.
# Verify that the real install API recorded exactly this disposable export.
python3 - "$sandbox/evidence/plugin-install.json" "$sandbox/evidence/plugin-enable.json" "$bundled_plugin" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    installed = json.load(handle)
with open(sys.argv[2], encoding="utf-8") as handle:
    enabled = json.load(handle)
plugin = installed.get("plugin") or {}
assert installed.get("installed") is True, "plugin install did not succeed"
assert os.path.realpath(plugin.get("install_dir", "")) == os.path.realpath(sys.argv[3]), "install escaped disposable export"
assert plugin.get("skill_paths", {}).get("music-project-management") == "skills/music-project-management", "plugin did not register the packaged skill"
assert enabled.get("enabled") is True and enabled.get("name") == plugin.get("name"), "plugin enable failed"
PY
fi
source_skill_sha256="$(shasum -a 256 "$plugin_root/skills/music-project-management/SKILL.md" | awk '{print $1}')"
staged_skill_sha256="$(shasum -a 256 "$skill_root/SKILL.md" | awk '{print $1}')"
[[ "$source_skill_sha256" == "$staged_skill_sha256" ]] || fail "disposable exported skill differs from clean candidate"

printf 'SANDBOX=%s\n' "$sandbox"
printf 'ORI_URL=%s\n' "$base_url"
printf 'ORI_LOG=%s\n' "$server_log"
printf 'MUSIC_PLUGIN_SOURCE=%s\n' "$bundled_plugin"
printf 'MUSIC_PLUGIN_REVISION=%s\n' "$candidate_revision"
printf 'MUSIC_PLUGIN_VERSION=%s\n' "$candidate_version"
printf 'MUSIC_PLUGIN_TREE=%s\n' "$candidate_tree"
printf 'MUSIC_PLUGIN_ARCHIVE_SHA256=%s\n' "$archive_sha256"
printf 'MUSIC_SKILL_SHA256=%s\n' "$staged_skill_sha256"
printf 'MUSIC_PROVIDER_MODE=%s\n' "$provider_mode"
if [[ -n "$bundled_reaper" ]]; then
	printf 'REAPER_PLUGIN_SOURCE=%s\nREAPER_PLUGIN_REVISION=%s\nREAPER_PLUGIN_TREE=%s\n' "$bundled_reaper" "$reaper_revision" "$reaper_tree"
fi
if [[ "$provider_mode" == "reviewed" ]]; then
	printf 'Local export was validated but NOT installed; the browser must review the published release.\n'
fi

if [[ "$mode" == "test" ]]; then
	printf 'Running Music Production Home %s acceptance (provider: %s)...\n' "$test_suite" "$provider_mode"
	playwright_file="tests/music-home-candidate.spec.ts"
	if [[ "$test_suite" == "portfolio" ]]; then
		playwright_file="tests/music-home-portfolio.spec.ts"
	elif [[ "$test_suite" == "portfolio-reviewed" ]]; then
		playwright_file="tests/music-home-portfolio-reviewed.spec.ts"
	fi
	run_music_acceptance() {
		env PLAYWRIGHT_BASE_URL="$base_url" \
			ORI_MUSIC_HOME_SANDBOX="$sandbox" \
			ORI_MUSIC_HOME_ACCEPTANCE=1 \
			ORI_MUSIC_PROVIDER_MODE="$provider_mode" \
			ORI_MUSIC_RESTART_TEST="$restart_check" \
			ORI_REAPER_PLUGIN_PATH="$bundled_reaper" \
			ORI_MUSIC_PLUGIN_PATH="$bundled_plugin" \
			ORI_MUSIC_PLUGIN_REVISION="$candidate_revision" \
			ORI_MUSIC_PLUGIN_VERSION="$candidate_version" \
			ORI_MUSIC_PLUGIN_TREE="$candidate_tree" \
			ORI_MUSIC_PLUGIN_ARCHIVE_SHA256="$archive_sha256" \
			ORI_MUSIC_HOME_EVIDENCE_DIR="$sandbox/evidence/screenshots" \
			npx playwright test "$playwright_file" \
			--project=chromium --workers=1 ${playwright_args[@]+"${playwright_args[@]}"}
	}
	set +e
	if ((restart_check == 0)); then
		run_music_acceptance
		test_status=$?
	else
		# The browser writes only a request marker in this disposable sandbox.
		# The parent shell owns the child server PID and restarts exactly once.
		run_music_acceptance &
		test_pid=$!
		restarts=0
		restart_failed=0
		while kill -0 "$test_pid" 2>/dev/null; do
			if [[ -f "$sandbox/evidence/restart.request" ]]; then
				mv "$sandbox/evidence/restart.request" "$sandbox/evidence/restart.inprogress"
				if ((restarts != 0)); then
					printf 'duplicate restart request\n' >"$sandbox/evidence/restart.failed"
					restart_failed=1
					break
				fi
				restarts=1
				if ! kill -0 "$server_pid" 2>/dev/null; then
					printf 'sandbox server exited before restart request\n' >"$sandbox/evidence/restart.failed"
					restart_failed=1
					break
				fi
				old_server_pid=$server_pid
				kill "$server_pid" 2>/dev/null
				wait "$server_pid" 2>/dev/null
				server_pid=""
				(
					cd "$sandbox" || exit 1
					exec env HOME="$sandbox" ORI_DATA_DIR="$sandbox" PORT="$port" ORI_NO_DESKTOP_OPEN=1 \
						"$repo_root/bin/ori-agent"
				) >>"$server_log" 2>&1 &
				server_pid=$!
				restarted=0
				for _ in {1..120}; do
					if curl -fsS -o /dev/null --max-time 1 "$base_url/health" 2>/dev/null; then
						restarted=1
						break
					fi
					if ! kill -0 "$server_pid" 2>/dev/null; then break; fi
					sleep 0.25
				done
				if ((restarted == 0)); then
					printf 'sandbox server failed to restart; see ori.log\n' >"$sandbox/evidence/restart.failed"
					restart_failed=1
					break
				fi
				printf 'server restarted once with the same HOME and ORI_DATA_DIR: %s -> %s\n' \
					"$old_server_pid" "$server_pid" >"$sandbox/evidence/restart.done"
			fi
			sleep 0.1
		done
		wait "$test_pid"
		test_status=$?
		test_pid=""
		if ((restarts != 1 || restart_failed != 0)); then
			printf 'music-home-demo: restart check did not complete once (requests: %d)\n' "$restarts" >&2
			test_status=1
		fi
	fi
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
