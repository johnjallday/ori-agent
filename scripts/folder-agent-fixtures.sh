#!/usr/bin/env bash
#
# folder-agent-fixtures.sh — write the folder agent demo fixtures into DIR.
#
# Usage: ./scripts/folder-agent-fixtures.sh DIR
#
# DIR must be an existing directory under $TMPDIR (a demo or e2e sandbox), so
# a fixture never lands in real folders. It writes:
#
#   One Song/                 a single REAPER song at the fed folder (Tools stage)
#   Ori Music Demo/           ~/Desktop/Ori Music Demo's layout: 7 REAPER song
#                             folders (Album-5 holds two sessions), 1 Logic Pro
#                             bundle, an empty Filed/ (Home stage)
#   Label 2026/               six songs six folders deep (Home stage at depth 6)
#   Three Drafts/             three manuscripts under Drafts/ (Group stage)
#   Messy/                    many boxes, nothing recognized near the top
#   Loose Files/              36 loose files of six kinds (a dump: File Curator)
#
# Files are tiny placeholders; nothing is read by the walk.
set -euo pipefail

dir="${1:-}"
if [[ -z "$dir" || ! -d "$dir" ]]; then
  echo "usage: $0 DIR (an existing directory under \$TMPDIR)" >&2
  exit 2
fi
dir="$(cd "$dir" && pwd -P)"
tmp="$(cd "${TMPDIR:-/tmp}" && pwd -P)"
case "$dir" in
  "$tmp"/*) ;;
  *)
    echo "refusing: $dir is not under \$TMPDIR ($tmp)" >&2
    exit 2
    ;;
esac

put() {
  mkdir -p "$(dirname "$dir/$1")"
  printf '%s\n' "${2:-x}" >"$dir/$1"
}

put "One Song/Song.rpp" "<REAPER_PROJECT"
put "One Song/Audio/kick.wav"

demo="Ori Music Demo"
for album in 1 2 3 4 5; do put "$demo/Albums/Album-$album/Song.rpp" "<REAPER_PROJECT"; done
put "$demo/Albums/Album-3/Song.rpp-bak"
put "$demo/Albums/Album-5/Alternate.rpp" "<REAPER_PROJECT"
put "$demo/Albums/Logic Sketch/Sketch.logicx/ProjectData"
put "$demo/Picker Songs/Track-1/Song.rpp" "<REAPER_PROJECT"
put "$demo/Picker Songs/Track-2/Song.rpp" "<REAPER_PROJECT"
mkdir -p "$dir/$demo/Filed"

label="Label 2026"
put "$label/2025/Artist A/Album X/Disc 1/Song One/Song One.rpp" "<REAPER_PROJECT"
put "$label/2025/Artist A/Album X/Disc 1/Song Two/Song Two.rpp" "<REAPER_PROJECT"
put "$label/2026/Artist B/Album Y/Disc 2/Song Three/Song Three.rpp" "<REAPER_PROJECT"
put "$label/2026/Artist B/Album Y/Disc 2/Song Four/Song Four.rpp" "<REAPER_PROJECT"
put "$label/2026/Artist C/EP Z/Side A/Song Five/Song Five.rpp" "<REAPER_PROJECT"
put "$label/2026/Artist C/EP Z/Side A/Song Six/Song Six.rpp" "<REAPER_PROJECT"

for draft in "Night Train" "Salt Year" "The Orchard"; do
  put "Three Drafts/Drafts/$draft/outline.md" "# $draft"
  put "Three Drafts/Drafts/$draft/chapter-01.md" "Chapter one"
done

# About 2,400 entries: more than one walk's 2,000-entry budget, so the walk
# stops with folders unopened and nothing recognized yet.
for box in Clients Archive Photos Downloads; do
  for i in $(seq -w 1 40); do
    mkdir -p "$dir/Messy/$box/Folder $i"
    for j in $(seq -w 1 14); do : >"$dir/Messy/$box/Folder $i/note-$j.txt"; done
  done
done
put "Messy/Clients/Folder 40/Deep/repo/go.mod" "module example.com/repo"

# 36 loose files of six kinds sitting directly in the folder: a dump (Tools
# stage, File Janitor in place with File Curator).
for i in $(seq -w 1 6); do
  for kind in pdf jpg zip docx dmg csv; do put "Loose Files/file-$i.$kind"; done
done

echo "Fixtures written under $dir:"
for name in "One Song" "Ori Music Demo" "Label 2026" "Three Drafts" "Messy" "Loose Files"; do
  echo "  $dir/$name"
done
