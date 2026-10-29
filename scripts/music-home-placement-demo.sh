#!/usr/bin/env bash
# Real-candidate Music Home placement acceptance, always in a fresh sandbox.
# Usage: ./scripts/music-home-placement-demo.sh --case CASE --port PORT \
#   --reaper-source DIR --music-source DIR --music-ref REF
# CASE: single, pointer, later, collection, collection-map, accept, layout, ack, occupied, partial,
#       stale, two-tabs, competing-home, disabled-provider, deleted-home, missing-model,
#       a11y-light, a11y-dark, a11y-touch, a11y-tree, a11y-zoom
# Uses an invented credential and a refused loopback endpoint: no paid inference.
set -euo pipefail
root="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$root"
# Each explicit case owns these flags; an old shell export must not add a fault.
unset MUSIC_HOME_PLACEMENT_FAULT MUSIC_HOME_PLACEMENT_CONFIRM MUSIC_HOME_PLACEMENT_COLLECTION \
	MUSIC_HOME_PLACEMENT_LIBRARY MUSIC_HOME_PLACEMENT_SECOND_SONG MUSIC_HOME_PLACEMENT_A11Y MUSIC_HOME_PLACEMENT_COLLECTION_MAP
case_name=single
port=8956
sources=()
while [[ $# -gt 0 ]]; do
	case "$1" in
	--case) case_name="${2:?--case needs a value}"; shift 2 ;;
	--port) port="${2:?--port needs a value}"; shift 2 ;;
	--reaper-source | --music-source | --music-ref)
		sources+=("$1" "${2:?source option needs a value}"); shift 2 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
case "$case_name" in
single | pointer | later | collection | collection-map | accept | layout | ack | occupied | partial | stale | two-tabs | competing-home | disabled-provider | deleted-home | missing-model | a11y-light | a11y-dark | a11y-touch | a11y-tree | a11y-zoom) ;;
*) echo "unknown case: $case_name" >&2; exit 2 ;;
esac
[[ "$port" =~ ^[0-9]+$ ]] || { echo 'port must be numeric' >&2; exit 2; }
log_dir="$root/.tmp/music-home-map-placement"
mkdir -p "$log_dir"
log="$log_dir/candidate-$case_name.log"
ready=0
demo_pid=''
cleanup() {
	if [[ "$ready" == 1 ]]; then "$root/scripts/stop-demo-server.sh" "$port" || true; fi
	if [[ -n "$demo_pid" ]]; then kill "$demo_pid" 2>/dev/null || true; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
env OPENAI_API_KEY=disposable-no-inference OPENAI_BASE_URL=http://127.0.0.1:1/v1 \
	NARRATED_DEMO_MODEL=openai/gpt-5-mini \
	"$root/scripts/narrated-folder-demo.sh" installed "${sources[@]}" --port "$port" >"$log" 2>&1 &
demo_pid=$!
for _ in $(seq 1 600); do
	if grep -q '^READY SANDBOX=' "$log"; then ready=1; break; fi
	if ! kill -0 "$demo_pid" 2>/dev/null; then echo "staging failed; see $log" >&2; exit 1; fi
	sleep 1
done
[[ "$ready" == 1 ]] || { echo "staging timed out; see $log" >&2; exit 1; }
sandbox=''
while IFS= read -r line; do
	if [[ "$line" == 'READY SANDBOX='* ]]; then
		sandbox="${line#READY SANDBOX=}"
		sandbox="${sandbox%% ORI_URL=*}"
	fi
done <"$log"
[[ -n "$sandbox" && -d "$sandbox" ]] || { echo 'sandbox missing' >&2; exit 1; }
tmp_root="$(CDPATH= cd -- "${TMPDIR:-/tmp}" && pwd -P)"
sandbox="$(CDPATH= cd -- "$sandbox" && pwd -P)"
[[ "$sandbox" == "$tmp_root"/ori-demo.narrated-installed-* ]] || { echo 'sandbox is outside the disposable staging root' >&2; exit 1; }
# The narrated wrapper owns this newly staged directory, but older reaper-demo
# versions do not write the demo-server ownership marker. Record it here only.
if [[ ! -e "$sandbox/.ori-demo-owner" && ! -L "$sandbox/.ori-demo-owner" ]]; then
	(umask 077; printf 'worktree=%s\n' "$root" > "$sandbox/.ori-demo-owner")
fi
grep -Fxq "worktree=$root" "$sandbox/.ori-demo-owner" || { echo 'sandbox owner differs' >&2; exit 1; }
printf 'CASE=%s SANDBOX=%s\n' "$case_name" "$sandbox"
args=(--port "$port" --env "MUSIC_HOME_PLACEMENT_SONG=$sandbox/Documents/Ori Music Demo")
pattern=single-song
spec=tests/music-home-map-placement.spec.ts
case "$case_name" in
a11y-*)
	args+=(--env "MUSIC_HOME_PLACEMENT_A11Y=${case_name#a11y-}")
	spec=tests/music-home-map-placement.a11y.spec.ts
	pattern='source Home' ;;
pointer) args+=(--env MUSIC_HOME_PLACEMENT_CONFIRM=pointer) ;;
later)
	"$root/scripts/smoke.sh" showfolder "http://localhost:$port" seed-song "$sandbox" Documents 'Second Song'
	args+=(--env "MUSIC_HOME_PLACEMENT_SECOND_SONG=$sandbox/Documents/Second Song")
	pattern='single-song|existing Home' ;;
accept | layout | ack | occupied | partial | stale | two-tabs | competing-home | disabled-provider | deleted-home | missing-model) args+=(--env "MUSIC_HOME_PLACEMENT_FAULT=$case_name") ;;
collection | collection-map)
	for name in 'Song One' 'Song Two' 'Song Three' 'Song Four' 'Song Five'; do
		"$root/scripts/smoke.sh" showfolder "http://localhost:$port" seed-song "$sandbox" Desktop "$name"
	done
	args=(--port "$port" --env "MUSIC_HOME_PLACEMENT_COLLECTION=$sandbox/Desktop")
	pattern='collection automatic setup'
	if [[ "$case_name" == collection-map ]]; then
		args+=(--env MUSIC_HOME_PLACEMENT_COLLECTION_MAP=1)
		pattern='collection map setup'
	fi ;;
esac
# Playwright clears its output directory on startup. Keep it in this owned
# sandbox so it cannot erase another run (notably ci-local's README capture).
output="$sandbox/evidence/placement-results"
"$root/scripts/e2e.sh" "${args[@]}" "$spec" -- --workers=1 -g "$pattern" --output "$output"
if [[ "$case_name" == collection || "$case_name" == collection-map ]]; then
	"$root/scripts/e2e.sh" --port "$port" --env MUSIC_HOME_PLACEMENT_LIBRARY=1 \
		tests/music-home-map-placement.spec.ts -- --workers=1 -g 'collection library' --output "$output"
fi
