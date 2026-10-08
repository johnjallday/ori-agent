#!/usr/bin/env bash
#
# assistant-workspace-acceptance.sh — run every browser acceptance run for the
# workspace-aware Personal Assistant, one after another, and report each.
#
#   ./scripts/assistant-workspace-acceptance.sh [--reaper-source DIR --music-source DIR]
#
# Each run builds this working tree and uses its own isolated sandbox (wt demo,
# e2e-fresh.sh or the exact-candidate runner), never real app data. Every reply
# comes from the runner's local deterministic stand-in: no vendor model is
# called, nothing is installed outside a sandbox, and nothing is pushed.
#
# Without both exact companion sources the three candidate runs are reported as
# SKIPPED, which is not a pass. A port already in use is reported as BLOCKED and
# the run is not started: an ori-agent started on a busy port stops whatever was
# there. The companion sources must be clean before and after.
#
# Logs go to a new directory under $TMPDIR, printed at the end. Exit status is 0
# only when nothing failed or was blocked.

set -u
cd "$(dirname "$0")/.." || exit 2

reaper=""
music=""
while [ $# -gt 0 ]; do
	case "$1" in
	--reaper-source)
		reaper="${2:-}"
		shift 2
		;;
	--music-source)
		music="${2:-}"
		shift 2
		;;
	-h | --help)
		grep '^#' "$0" | grep -v '^#!' | cut -c3-
		exit 0
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
done
if { [ -n "$reaper" ] && [ -z "$music" ]; } || { [ -z "$reaper" ] && [ -n "$music" ]; }; then
	echo "candidate runs need both --reaper-source and --music-source" >&2
	exit 2
fi

out="${TMPDIR:-/tmp}/assistant-workspace-acceptance.$$"
mkdir -p "$out" || exit 2
failed=0
summary=""

note() { summary="${summary}$1"$'\n'; }

# run NAME PORT COMMAND... — one run, refused when its port is busy.
run() {
	local name="$1" port="$2"
	shift 2
	if lsof -ti ":$port" >/dev/null 2>&1; then
		note "BLOCKED  $name (port $port is in use)"
		failed=1
		return
	fi
	echo "== $name"
	if "$@" >"$out/$name.log" 2>&1; then
		# A skipped test is not a pass: say so beside the result.
		if grep -Eq '[0-9]+ skipped' "$out/$name.log"; then
			note "PASS     $name (with skipped tests: $out/$name.log)"
		else
			note "PASS     $name"
		fi
	else
		note "FAIL     $name ($out/$name.log)"
		failed=1
	fi
}

demo=(python3 scripts/assistant-workspace-demo.py)
run history 8954 "${demo[@]}"
run placement 8954 "${demo[@]}" --placement
run sources 8954 "${demo[@]}" --sources
run files 8954 "${demo[@]}" --files
run accessibility 8954 "${demo[@]}" --accessibility
run generic-awareness 8952 ./scripts/e2e-fresh.sh --port 8952 \
	--sandbox-env ORI_WORKSPACE_AWARENESS_SANDBOX \
	tests/personal-assistant-workspace-awareness.spec.ts -- --workers=1
run folder-chat 8952 ./scripts/e2e-fresh.sh --port 8952 \
	--sandbox-env ORI_FOLDER_CHAT_SANDBOX \
	tests/personal-assistant-folder-chat.spec.ts -- --workers=1

if [ -z "$reaper" ]; then
	note "SKIPPED  candidate-existing-home (no companion sources given)"
	note "SKIPPED  candidate-new-home (no companion sources given)"
	note "SKIPPED  candidate-portfolio (no companion sources given)"
else
	clean() { [ -z "$(git -C "$1" status --short 2>/dev/null)" ] && git -C "$1" rev-parse HEAD >/dev/null 2>&1; }
	if clean "$reaper" && clean "$music"; then
		candidate=("${demo[@]}" --port 8950 --reaper-source "$reaper" --music-source "$music")
		run candidate-existing-home 8950 "${candidate[@]}"
		run candidate-new-home 8950 "${candidate[@]}" --new-home
		run candidate-portfolio 8950 "${candidate[@]}" --portfolio
		if clean "$reaper" && clean "$music"; then
			note "CLEAN    companion sources unchanged ($(git -C "$reaper" rev-parse --short HEAD), $(git -C "$music" rev-parse --short HEAD))"
		else
			note "FAIL     a companion source changed during the run"
			failed=1
		fi
	else
		note "BLOCKED  candidate runs (a companion source is not a clean git checkout)"
		failed=1
	fi
fi

echo
printf '%s' "$summary"
echo "logs: $out"
exit "$failed"
