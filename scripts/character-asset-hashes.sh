#!/usr/bin/env bash
#
# Records the SHA-256 of every shipped character asset so the reviewed file and
# the shipped file can be compared exactly (PRD FR-112).
#
#   ./scripts/character-asset-hashes.sh            # rewrite the committed record
#   ./scripts/character-asset-hashes.sh --check    # fail if the tree has drifted
#
# A mismatch means an asset changed without its provenance record being
# revisited; re-review before release rather than regenerating blindly.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
asset_dir="$repo_root/internal/web/static/characters"
record="$repo_root/docs/character-asset-hashes.txt"

if [[ ! -d "$asset_dir" ]]; then
  echo "no character assets at $asset_dir" >&2
  exit 1
fi

hash_all() {
  # Sorted, repo-relative paths so the record is stable across machines.
  find "$asset_dir" -name '*.svg' -type f | sort | while read -r file; do
    printf '%s  %s\n' "$(shasum -a 256 "$file" | cut -d' ' -f1)" "${file#"$repo_root"/}"
  done
}

if [[ "${1:-}" == "--check" ]]; then
  if [[ ! -f "$record" ]]; then
    echo "no committed hash record at $record; run without --check first" >&2
    exit 1
  fi
  if diff -u "$record" <(hash_all); then
    echo "character assets match their reviewed hashes"
  else
    echo >&2
    echo "ERROR: shipped character assets differ from docs/character-asset-hashes.txt." >&2
    echo "Re-review the changed asset, update docs/CHARACTER_ASSET_PROVENANCE.md, then rerun this script." >&2
    exit 1
  fi
else
  hash_all >"$record"
  echo "wrote $(grep -c . "$record") hashes to ${record#"$repo_root"/}"
fi
