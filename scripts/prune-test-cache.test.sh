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
# go clean -cache removes the hex shard directories, not arbitrary files at
# the cache root. Seed a representative entry without compiling anything.
seed="$go_cache/00/$(printf '%064d' 0)-a"
mkdir -p "$temp_root/ori-test-old" "$temp_root/ori-test-recent" "${seed%/*}"
touch -t 202001010000 "$temp_root/ori-test-old"
printf 'cache seed\n%8192s\n' '' >"$seed"

# Always pass the owned absolute cache to the real Go cleaner, regardless of
# the caller's cache, Go settings, or pruning overrides.
export GOCACHE="$go_cache" TMPDIR="$temp_root" GOENV=off GOWORK=off GOFLAGS= GOTOOLCHAIN=local
unset GOCACHEPROG
export ORI_GO_BIN=go ORI_SKIP_CACHE_PRUNE=0 ORI_GO_CACHE_MAX_KIB=1 ORI_STALE_ARTIFACT_HOURS=24

dry_output="$("$prune_script" --dry-run)"
[[ "$dry_output" == *"Dry run: found 1 Ori test artifact(s)"* ]]
[[ "$dry_output" == *"Would prune Go build cache"* ]]
[[ -d "$temp_root/ori-test-old" ]]
[[ -f "$seed" ]]

# A higher CLI limit overrides the 1 KiB environment limit, so explicit
# maintenance still removes stale artifacts while leaving the cache alone.
"$prune_script" --max-go-cache-kib 1048576
[[ ! -e "$temp_root/ori-test-old" ]]
[[ -d "$temp_root/ori-test-recent" ]]
[[ -f "$seed" ]]

"$prune_script"
[[ -d "$temp_root/ori-test-recent" ]]
[[ ! -e "$seed" ]]

mkdir -p "$temp_root/ori-test-skip"
touch -t 202001010000 "$temp_root/ori-test-skip"
mkdir -p "${seed%/*}"
printf 'keep cache\n%8192s\n' '' >"$seed"
skip_output="$(ORI_SKIP_CACHE_PRUNE=1 "$prune_script")"
[[ "$skip_output" == *"Cache pruning skipped"* ]]
[[ -d "$temp_root/ori-test-skip" ]]
[[ -f "$seed" ]]

printf 'prune-test-cache tests passed\n'
