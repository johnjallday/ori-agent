#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
prune_script="$script_dir/prune-test-cache.sh"
fixture_parent="$(cd "${TMPDIR:-/tmp}" && pwd -P)"
fixture_root="$(mktemp -d "$fixture_parent/ori-prune-test-cache.XXXXXX")"

cleanup_fixture() {
	case "$fixture_root" in
	"$fixture_parent"/ori-prune-test-cache.*) rm -rf -- "$fixture_root" ;;
	*) printf 'Refusing to remove unexpected fixture: %s\n' "$fixture_root" >&2 ;;
	esac
}
trap cleanup_fixture EXIT

temp_root="$fixture_root/temp"
go_cache="$fixture_root/go-cache"
mkdir -p "$temp_root/ori-test-old" "$temp_root/ori-test-recent" "$go_cache"
touch -t 202001010000 "$temp_root/ori-test-old"
printf 'cache seed\n' >"$go_cache/seed"

dry_output="$(GOCACHE="$go_cache" "$prune_script" --dry-run --temp-dir "$temp_root" --older-than-hours 24 --max-go-cache-kib 1)"
[[ "$dry_output" == *"Dry run: found 1 Ori test artifact(s)"* ]]
[[ "$dry_output" == *"Would prune Go build cache"* ]]
[[ -d "$temp_root/ori-test-old" ]]
[[ -f "$go_cache/seed" ]]

GOCACHE="$go_cache" "$prune_script" --temp-dir "$temp_root" --older-than-hours 24 --max-go-cache-kib 1
[[ ! -e "$temp_root/ori-test-old" ]]
[[ -d "$temp_root/ori-test-recent" ]]
[[ ! -e "$go_cache/seed" ]]

mkdir -p "$temp_root/ori-test-skip"
touch -t 202001010000 "$temp_root/ori-test-skip"
printf 'keep cache\n' >"$go_cache/keep"
skip_output="$(ORI_SKIP_CACHE_PRUNE=1 GOCACHE="$go_cache" "$prune_script" --temp-dir "$temp_root" --max-go-cache-kib 1)"
[[ "$skip_output" == *"Cache pruning skipped"* ]]
[[ -d "$temp_root/ori-test-skip" ]]
[[ -f "$go_cache/keep" ]]

printf 'prune-test-cache tests passed\n'
