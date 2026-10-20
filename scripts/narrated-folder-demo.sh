#!/usr/bin/env bash
#
# narrated-folder-demo.sh — the narrated-folder-setup demo sandbox in one
# command (tasks/tasks-narrated-folder-setup.md): an isolated Ori built from
# this worktree, the REAPER plugin installed from a clean local candidate, a
# hired assistant with its Personal HQ, a system model, and one REAPER song
# folder ("Ori Music Demo") under the sandbox's Documents.
#
# Usage:
#   ./scripts/narrated-folder-demo.sh <mode> --reaper-source DIR [--music-source DIR] [--port PORT]
#
#   installed            both candidates are installed, and Ori treats those
#                        exact local copies as the reviewed releases.
#   integration-missing  both are staged, then the REAPER plugin is uninstalled:
#                        the plan installs it first, from the staged copy,
#                        without the network.
#   provider-missing     Music Project Management is never installed: there is
#                        no Home template to plan a new Home from, so the plan
#                        is unavailable and says which plugin it waits on.
#
# installed and integration-missing need --music-source; it must be at least
# the reviewed Music Project Management floor (internal/reviewedintegration).
#
# Sources default to ORI_REAPER_PLUGIN_SOURCE and ORI_MUSIC_PLUGIN_SOURCE, as
# for scripts/reaper-demo.sh, which this wraps; both must be clean Git
# worktrees (only `git archive` reads them). The model is openai/gpt-5-mini
# unless NARRATED_DEMO_MODEL=provider/model says otherwise; the server inherits
# OPENAI_API_KEY from this shell.
#
# Everything is written under a new $TMPDIR/ori-demo.narrated-* sandbox, which
# is kept. `READY SANDBOX=… ORI_URL=…` is printed once the sandbox can be
# demoed; the script then waits on the server (stop it with
# ./scripts/stop-demo-server.sh PORT).

set -euo pipefail

fail() {
	printf 'narrated-folder-demo: %s\n' "$*" >&2
	exit 2
}

mode="${1:-}"
case "$mode" in
installed | integration-missing | provider-missing) ;;
*) fail "usage: $0 <installed|integration-missing|provider-missing> --reaper-source DIR [--music-source DIR] [--port PORT]" ;;
esac
shift
reaper_source="${ORI_REAPER_PLUGIN_SOURCE:-}"
music_source="${ORI_MUSIC_PLUGIN_SOURCE:-}"
port=8952
while [[ $# -gt 0 ]]; do
	case "$1" in
	--reaper-source)
		reaper_source="${2:-}"
		shift 2
		;;
	--music-source)
		music_source="${2:-}"
		shift 2
		;;
	--port)
		port="${2:-}"
		shift 2
		;;
	*) fail "unknown argument '$1'" ;;
	esac
done
[[ -n "$reaper_source" ]] || fail "--reaper-source is required"
[[ "$mode" == "provider-missing" || -n "$music_source" ]] || fail "$mode needs --music-source"
[[ "$port" =~ ^[0-9]+$ ]] || fail "port must be numeric"
model="${NARRATED_DEMO_MODEL:-openai/gpt-5-mini}"
[[ "$model" == */* ]] || fail "NARRATED_DEMO_MODEL must be provider/model"

root="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
base_url="http://localhost:$port"
if curl -fsS -o /dev/null --max-time 1 "$base_url/health" 2>/dev/null; then
	fail "port $port already has an Ori server; choose another with --port"
fi
sandbox="${TMPDIR:-/tmp}"
sandbox="${sandbox%/}/ori-demo.narrated-$mode-$(date +%s)"
mkdir -p "$sandbox"
log="$sandbox/narrated-demo.log"

args=(serve --reaper-source "$reaper_source" --port "$port" --sandbox "$sandbox")
if [[ "$mode" != "provider-missing" ]]; then
	args+=(--music-source "$music_source" --install-order music-first)
fi
"$root/scripts/reaper-demo.sh" "${args[@]}" >"$log" 2>&1 &
demo_pid=$!
trap 'kill "$demo_pid" 2>/dev/null || true' HUP INT TERM

# reaper-demo prints its prompt only after the candidates are installed.
for _ in $(seq 1 600); do
	if grep -q 'Press Ctrl-C to stop the server' "$log" 2>/dev/null; then
		break
	fi
	if ! kill -0 "$demo_pid" 2>/dev/null; then
		cat "$log" >&2
		fail "reaper-demo.sh exited before the sandbox was ready"
	fi
	sleep 1
done
grep -q 'Press Ctrl-C to stop the server' "$log" || fail "the sandbox was not ready after 10 minutes (log: $log)"
if [[ "$mode" == "integration-missing" ]]; then
	# The staged copy stays the server's reviewed source, so the plan's install
	# line installs exactly it again.
	curl -fsS -o /dev/null -X DELETE "$base_url/api/plugins/reaper-plugin" -H 'X-Requested-With: XMLHttpRequest' ||
		fail "the REAPER plugin could not be uninstalled"
	echo "ok   uninstalled reaper-plugin (the plan installs it first)"
fi

"$root/scripts/smoke.sh" showfolder "$base_url" hq
"$root/scripts/smoke.sh" showfolder "$base_url" model "${model%%/*}" "${model#*/}"
"$root/scripts/smoke.sh" showfolder "$base_url" seed-song "$sandbox" Documents "Ori Music Demo"

printf 'READY SANDBOX=%s ORI_URL=%s SONG=%s MODE=%s\n' "$sandbox" "$base_url" "$sandbox/Documents/Ori Music Demo" "$mode"
wait "$demo_pid"
