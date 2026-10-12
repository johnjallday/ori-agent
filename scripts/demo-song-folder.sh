#!/usr/bin/env bash
#
# demo-song-folder.sh — lay out a small folder of song projects inside a demo
# sandbox, so a folder path pasted into the assistant has something real to
# explore: six songs, each with a project file and a few media files, with
# different last-edited dates.
#
# Usage:
#   ./scripts/demo-song-folder.sh <ori-demo-sandbox> [folder-under-home]
#
# The sandbox must be a demo sandbox (its path contains "/ori-demo."); nothing
# outside it is ever written. The folder defaults to "Music/REAPER Projects".
# Dates run from oldest to newest in the order below, so "Night Drive" is the
# most recently edited song.

set -euo pipefail

sandbox="${1:-}"
folder="${2:-Music/REAPER Projects}"
if [[ -z "$sandbox" || ! -d "$sandbox" || "$sandbox" != *"/ori-demo."* ]]; then
	echo "usage: $0 <ori-demo-sandbox> [folder-under-home]" >&2
	exit 2
fi

root="$sandbox/$folder"
songs=("Harbor Lights" "Low Tide" "Paper Kites" "Glass Hour" "Slow Motion" "Night Drive")
# One date per song, oldest first (touch -t format: [[CC]YY]MMDDhhmm).
dates=(202603141030 202605021815 202606201200 202608051945 202609180910 202610091630)

for index in "${!songs[@]}"; do
	song="${songs[$index]}"
	stamp="${dates[$index]}"
	dir="$root/$song"
	mkdir -p "$dir/Media"
	printf '<REAPER_PROJECT 0.1 "7.27/macOS-arm64" 1760000000\n  TEMPO 120 4 4\n  <TRACK\n    NAME "Drums"\n  >\n  <TRACK\n    NAME "Bass"\n  >\n>\n' >"$dir/$song.rpp"
	for take in "drums" "bass" "vocal"; do
		printf 'RIFF' >"$dir/Media/$take.wav"
		touch -t "$stamp" "$dir/Media/$take.wav"
	done
	touch -t "$stamp" "$dir/$song.rpp" "$dir/Media" "$dir"
done
touch -t "${dates[${#dates[@]} - 1]}" "$root"

echo "FOLDER=$root"
echo "SONGS=${#songs[@]}"
echo "NEWEST=${songs[${#songs[@]} - 1]}"
