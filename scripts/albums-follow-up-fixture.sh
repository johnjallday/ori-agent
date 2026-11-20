#!/usr/bin/env bash
#
# albums-follow-up-fixture.sh — lay out the "Albums" folder the Albums Manager
# follow-up cards are demonstrated on, inside a demo sandbox: five REAPER song
# projects the reader can parse (each with its own tempo, tracks and markers)
# and one Logic Pro bundle, a format with no offline reader.
#
# Usage:
#   ./scripts/albums-follow-up-fixture.sh <ori-demo-sandbox> [folder-under-home]
#
# The sandbox must be a demo sandbox (its path contains "/ori-demo."); nothing
# outside it is ever written. The folder defaults to "Albums". Run
# ./scripts/folder-agent-demo-setup.sh against the same server first (with the
# folder name Albums) so the saved agent "Albums Manager" exists when the
# folder is started.
#
# Unlike the placeholder files folder-agent-fixtures.sh writes (enough to be
# recognised, never read), these projects are valid, so "Learn 5 songs" has
# something real to read. Printed on stdout: FOLDER=<path>, REAPER_SONGS=5,
# LOGIC_SONGS=1.

set -euo pipefail

sandbox="${1:-}"
folder="${2:-Albums}"
pad_mib="${3:-0}"
if [[ -z "$sandbox" || ! -d "$sandbox" || "$sandbox" != *"/ori-demo."* ]]; then
	echo "usage: $0 <ori-demo-sandbox> [folder-under-home [pad-mib]]" >&2
	exit 2
fi
# pad-mib makes each REAPER project about that many MiB (one extra track holding
# many empty items), still valid and under the reader's 32 MiB cap, so reading the
# five songs takes seconds and a reload in the middle of it can be caught. 0 (the
# default) keeps the projects tiny.
case "$pad_mib" in
	"" | *[!0-9]*)
		echo "pad-mib must be a whole number of MiB" >&2
		exit 2
		;;
esac
if ((pad_mib > 28)); then
	echo "pad-mib is capped at 28 so a project stays under the 32 MiB read cap" >&2
	exit 2
fi
case "$folder" in
	"" | /* | *..*)
		echo "refusing: the folder must be a plain path under the sandbox" >&2
		exit 2
		;;
esac

root="$sandbox/$folder"
tempos=(96 110 124 132 140)

for index in 0 1 2 3 4; do
	n=$((index + 1))
	dir="$root/Album-$n"
	mkdir -p "$dir/Media"
	{
		printf '<REAPER_PROJECT 0.1 "7.27/macOS-arm64" 1760000000\n'
		printf '  TEMPO %s 4 4\n' "${tempos[$index]}"
		printf '  MARKER 1 0 "Intro" 0 0 1\n'
		printf '  MARKER 2 30 "Verse %s" 0 0 1\n' "$n"
		printf '  <TRACK\n    NAME "Drums"\n  >\n'
		printf '  <TRACK\n    NAME "Bass"\n  >\n'
		printf '  <TRACK\n    NAME "Vocal %s"\n  >\n' "$n"
		if ((pad_mib > 0)); then
			# 50 bytes per item; 20000 items is a little under 1 MiB.
			awk -v mib="$pad_mib" 'BEGIN {
				printf "  <TRACK\n    NAME \"Pad\"\n"
				for (i = 0; i < mib * 20000; i++) printf "    <ITEM\n      POSITION 0\n      LENGTH 4\n    >\n"
				printf "  >\n"
			}'
		fi
		printf '>\n'
	} >"$dir/Song.rpp"
	printf 'RIFF' >"$dir/Media/take.wav"
done

mkdir -p "$root/Logic Sketch/Sketch.logicx"
printf 'logic' >"$root/Logic Sketch/Sketch.logicx/ProjectData"

echo "FOLDER=$root"
echo "REAPER_SONGS=5"
echo "LOGIC_SONGS=1"
