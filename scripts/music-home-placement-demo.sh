#!/usr/bin/env bash
# Real-candidate Music Home placement acceptance, always in a fresh sandbox.
# Usage: ./scripts/music-home-placement-demo.sh --case CASE --port PORT \
#   --reaper-source DIR --music-source DIR --music-ref REF
# CASE: wizard, wizard-choice, wizard-quest, wizard-close, wizard-close-unknown, wizard-integration-missing, wizard-provider-missing, single, pointer, later, collection, collection-map, staffing, staffing-repair, staffing-recovery, accept, layout, ack, occupied, partial,
#       stale, two-tabs, competing-home, disabled-provider, deleted-home, missing-model,
#       a11y-light, a11y-dark, a11y-touch, a11y-tree, a11y-zoom, chat, chat-choice, chat-a11y
# chat: the Home is set up, then Go to lands on its map where the manager's
# offer takes a reply as the brief and the song is placed inside the Home.
# Uses an invented credential and a refused loopback endpoint: no paid inference.
set -euo pipefail
root="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$root"
# Each explicit case owns these flags; an old shell export must not add a fault.
unset MUSIC_HOME_PLACEMENT_FAULT MUSIC_HOME_PLACEMENT_CONFIRM MUSIC_HOME_PLACEMENT_COLLECTION \
	MUSIC_HOME_PLACEMENT_LIBRARY MUSIC_HOME_PLACEMENT_SECOND_SONG MUSIC_HOME_PLACEMENT_A11Y MUSIC_HOME_PLACEMENT_COLLECTION_MAP MUSIC_HOME_PLACEMENT_TWO_FILES MUSIC_HOME_WIZARD_PREREQUISITE MUSIC_HOME_WIZARD_RESPONSE
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
wizard | wizard-choice | wizard-quest | wizard-close | wizard-close-unknown | wizard-integration-missing | wizard-provider-missing | single | chat | chat-choice | chat-a11y | pointer | later | collection | collection-map | staffing | staffing-repair | staffing-recovery | accept | layout | ack | occupied | partial | stale | two-tabs | competing-home | disabled-provider | deleted-home | missing-model | a11y-light | a11y-dark | a11y-touch | a11y-tree | a11y-zoom) ;;
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
demo_mode=installed
if [[ "$case_name" == wizard-integration-missing ]]; then demo_mode=integration-missing; fi
# Missing-package recovery uses the host's published reviewed floor, not the
# local development-source acceptance override (which only covers installed
# copies). Refuse release discovery to make that reviewed version deterministic.
env OPENAI_API_KEY=disposable-no-inference OPENAI_BASE_URL=http://127.0.0.1:1/v1 \
	ORI_INTEGRATION_RELEASES_API=http://127.0.0.1:1 \
	NARRATED_DEMO_MODEL=openai/gpt-5-mini \
	"$root/scripts/narrated-folder-demo.sh" "$demo_mode" "${sources[@]}" --port "$port" >"$log" 2>&1 &
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
[[ "$sandbox" == "$tmp_root"/ori-demo.narrated-"$demo_mode"-* ]] || { echo 'sandbox is outside the disposable staging root' >&2; exit 1; }
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
wizard | wizard-choice | wizard-close | wizard-close-unknown)
	if [[ "$case_name" == wizard-close* ]]; then
		args+=(--env "MUSIC_HOME_WIZARD_RESPONSE=$case_name")
	fi
	if [[ "$case_name" == wizard-choice ]]; then
		cp "$sandbox/Documents/Ori Music Demo/Ori Music Demo.rpp" "$sandbox/Documents/Ori Music Demo/Ori Music Demo (mix).rpp"
		args+=(--env MUSIC_HOME_PLACEMENT_TWO_FILES=1)
	fi
	spec=tests/reaper-folder-setup-wizard.spec.ts
	pattern='music wizard' ;;
wizard-quest)
	spec=tests/reaper-folder-setup-wizard.spec.ts
	pattern='music guided' ;;
wizard-integration-missing | wizard-provider-missing)
	kind=integration
	if [[ "$case_name" == wizard-provider-missing ]]; then
		# Remove only this disposable installation before any Home exists.
		# Explicit recovery installs the published reviewed floor into the sandbox.
		curl -fsS -o /dev/null -X DELETE "http://localhost:$port/api/plugins/music-project-management" -H 'X-Requested-With: XMLHttpRequest'
		kind=provider
	fi
	args+=(--env "MUSIC_HOME_WIZARD_PREREQUISITE=$kind")
	spec=tests/reaper-folder-setup-wizard.spec.ts
	pattern='music prerequisites' ;;
a11y-*)
	args+=(--env "MUSIC_HOME_PLACEMENT_A11Y=${case_name#a11y-}")
	spec=tests/music-home-map-placement.a11y.spec.ts
	pattern='source Home' ;;
chat) pattern='single-song' ;;
chat-choice)
	# A song folder with two project files: the manager asks which one to open.
	song_dir="$sandbox/Documents/Ori Music Demo"
	cp "$song_dir/Ori Music Demo.rpp" "$song_dir/Ori Music Demo (mix).rpp"
	args+=(--env MUSIC_HOME_PLACEMENT_TWO_FILES=1)
	pattern='single-song' ;;
chat-a11y)
	# The Home is set up by the plain journey first; the a11y spec then drives
	# the manager's hand-off on it.
	"$root/scripts/e2e.sh" "${args[@]}" "$spec" -- --workers=1 -g 'single-song reviewed' --output "$sandbox/evidence/placement-setup"
	args+=(--env MUSIC_HOME_PLACEMENT_A11Y=chat)
	spec=tests/music-home-map-placement.a11y.spec.ts
	pattern='manager hand-off' ;;
pointer) args+=(--env MUSIC_HOME_PLACEMENT_CONFIRM=pointer) ;;
later)
	"$root/scripts/smoke.sh" showfolder "http://localhost:$port" seed-song "$sandbox" Documents 'Second Song'
	args+=(--env "MUSIC_HOME_PLACEMENT_SECOND_SONG=$sandbox/Documents/Second Song")
	pattern='single-song reviewed|existing Home' ;;
accept | layout | ack | occupied | partial | stale | two-tabs | competing-home | disabled-provider | deleted-home | missing-model)
	args+=(--env "MUSIC_HOME_PLACEMENT_FAULT=$case_name")
	pattern='single-song reviewed' ;;
staffing | staffing-repair | staffing-recovery)
	# library-setup-staffing: a catalog-only Home with standing approval for its
	# shared assistant (the collection setup records it), then the map sheet's
	# confirmed setup with the assistant.
	for name in Album-1 Album-2 Album-3 Album-4 Album-5; do
		"$root/scripts/smoke.sh" showfolder "http://localhost:$port" seed-song "$sandbox" Desktop "$name"
	done
	"$root/scripts/e2e.sh" --port "$port" --env "MUSIC_HOME_PLACEMENT_COLLECTION=$sandbox/Desktop" \
		tests/music-home-map-placement.spec.ts -- --workers=1 -g 'collection automatic setup' --output "$sandbox/evidence/placement-setup"
	args=(--port "$port" --env LIBRARY_SETUP_STAFFING=1)
	spec=tests/library-setup-staffing.spec.ts
	pattern='staffing'
	if [[ "$case_name" == staffing-repair ]]; then
		# Projects connected before the fix, then repaired.
		args=(--port "$port" --env LIBRARY_SETUP_STAFFING=repair)
		pattern='repair existing'
	fi
	if [[ "$case_name" == staffing-recovery ]]; then
		# Lost answers, sharing switched off mid-batch, Close during a request.
		args=(--port "$port" --env LIBRARY_SETUP_STAFFING=recovery)
		pattern='recovery:'
	fi ;;
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
