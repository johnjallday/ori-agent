#!/usr/bin/env bash
#
# folder-collection-seed.sh — lay out a folder that holds several projects
# inside a demo sandbox, so attaching it proposes a group with one workspace per
# project: five album folders, each with a session file and its media, and one
# sketch folder that no project marker names.
#
# Usage:
#   ./scripts/folder-collection-seed.sh <sandbox> [folder-under-home]
#
# The sandbox must be an existing directory under $TMPDIR; nothing outside it is
# ever written. The folder defaults to "Downloads", which the assistant's folder
# chips offer. Every file is dated two weeks back, so no folder passes for
# current work on dates alone.

set -euo pipefail

sandbox="${1:-}"
folder="${2:-Downloads}"
if [[ -z "$sandbox" || ! -d "$sandbox" || -z "${TMPDIR:-}" ]]; then
	echo "usage: $0 <sandbox-under-TMPDIR> [folder-under-home]" >&2
	exit 2
fi
# Compare real paths: on macOS $TMPDIR sits behind a symbolic link.
sandbox="$(cd "$sandbox" && pwd -P)"
tmp="$(cd "$TMPDIR" && pwd -P)"
if [[ "$sandbox" != "$tmp"/* ]]; then
	echo "refusing to seed $sandbox: it is not under \$TMPDIR" >&2
	exit 2
fi
if [[ "$folder" == /* || "$folder" == *..* ]]; then
	echo "the folder must be a plain name under the sandbox" >&2
	exit 2
fi

# touch -t format ([[CC]YY]MMDDhhmm), two weeks ago; BSD date first, then GNU.
stamp="$(date -v-14d +%Y%m%d%H%M 2>/dev/null || date -d '14 days ago' +%Y%m%d%H%M)"

root="$sandbox/$folder"
session='<REAPER_PROJECT 0.1 "7.27/macOS-arm64" 1760000000\n  TEMPO 120 4 4\n  <TRACK\n    NAME "Drums"\n  >\n>\n'

for number in 1 2 3 4 5; do
	album="$root/Album-$number"
	mkdir -p "$album/Media"
	# shellcheck disable=SC2059 # the session text is a fixed format string
	printf "$session" >"$album/Song.rpp"
	printf 'RIFF' >"$album/Media/drums.wav"
done
# shellcheck disable=SC2059
printf "$session" >"$root/Album-3/Song.rpp-bak"
# shellcheck disable=SC2059
printf "$session" >"$root/Album-5/Alternate.rpp"

sketch="$root/Logic Sketch"
mkdir -p "$sketch"
printf 'ideas\n' >"$sketch/ideas.txt"
printf 'MThd' >"$sketch/sketch.mid"
printf 'RIFF' >"$sketch/bounce.wav"

# Files first, then the folders that hold them, deepest first.
find "$root" -type f -exec touch -t "$stamp" {} +
find "$root" -depth -type d -exec touch -t "$stamp" {} +

echo "FOLDER=$root"
echo "MEMBERS=6"
