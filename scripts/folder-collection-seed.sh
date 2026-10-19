#!/usr/bin/env bash
#
# folder-collection-seed.sh — lay out a folder that holds several projects
# inside a demo sandbox, so attaching it proposes a group with one workspace per
# project: five album folders, each with a session file and its media, and one
# sketch folder that no project marker names.
#
# Usage:
#   ./scripts/folder-collection-seed.sh <sandbox> [folder-under-home]
#   ./scripts/folder-collection-seed.sh --tombstone <sandbox> [folder-under-home]
#
# The sandbox must be an existing directory under $TMPDIR; nothing outside it is
# ever written. The folder defaults to "Downloads", which the assistant's folder
# chips offer. Every file is dated two weeks back, so no folder passes for
# current work on dates alone.
#
# --tombstone reproduces an old "No thanks" on that folder, the way one sat in a
# real HQ: a tombstone for the folder and a decline of music_production, both
# dated 2026-10-06. It writes them into the sandbox HQ's folder digest, so the
# HQ must already exist (hire the assistant and build its HQ first), and it
# changes nothing else.

set -euo pipefail

tombstone=0
if [[ "${1:-}" == "--tombstone" ]]; then
	tombstone=1
	shift
fi
sandbox="${1:-}"
folder="${2:-Downloads}"
if [[ -z "$sandbox" || ! -d "$sandbox" || -z "${TMPDIR:-}" ]]; then
	echo "usage: $0 [--tombstone] <sandbox-under-TMPDIR> [folder-under-home]" >&2
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

if ((tombstone == 1)); then
	root="$sandbox/$folder"
	[[ -d "$root" ]] || {
		echo "seed $root first" >&2
		exit 2
	}
	shopt -s nullglob
	digests=("$sandbox/Ori Workspaces"/*/.ori/folder-digest-v1.json)
	shopt -u nullglob
	if ((${#digests[@]} != 1)); then
		echo "expected one HQ folder digest under $sandbox/Ori Workspaces, found ${#digests[@]}; hire the assistant and build its HQ first" >&2
		exit 2
	fi
	# The key is the folder's own: sha256 of its canonical path, as the server's.
	python3 -I - "${digests[0]}" "$(cd "$root" && pwd -P)" "$folder" <<'PY'
import hashlib, json, os, sys

path, root, name = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, encoding="utf-8") as handle:
    doc = json.load(handle)
at = "2026-10-06T08:02:00Z"
key = hashlib.sha256(os.path.normpath(root).encode()).hexdigest()
doc["tombstones"] = [t for t in doc.get("tombstones") or [] if t.get("key") != key]
doc["tombstones"].append({"key": key, "name": name, "created_at": at})
doc["domain_declines"] = [d for d in doc.get("domain_declines") or [] if d.get("domain") != "music_production"]
doc["domain_declines"].append({"domain": "music_production", "first_evidence": "portfolio", "count": 1, "at": at})
tmp = path + ".seed-tmp"
# The server reads its sidecars only when no one else can (mode 0600).
with os.fdopen(os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w", encoding="utf-8") as handle:
    json.dump(doc, handle, indent=2)
    handle.write("\n")
os.replace(tmp, path)
print(f"TOMBSTONE={key}")
PY
	exit 0
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
