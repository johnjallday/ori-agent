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
if [[ -z "$sandbox" || ! -d "$sandbox" || "$sandbox" != *"/ori-demo."* ]]; then
	echo "usage: $0 <ori-demo-sandbox> [folder-under-home]" >&2
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
		printf '>\n'
	} >"$dir/Song.rpp"
	printf 'RIFF' >"$dir/Media/take.wav"
done

mkdir -p "$root/Logic Sketch/Sketch.logicx"
printf 'logic' >"$root/Logic Sketch/Sketch.logicx/ProjectData"

echo "FOLDER=$root"
echo "REAPER_SONGS=5"
echo "LOGIC_SONGS=1"
