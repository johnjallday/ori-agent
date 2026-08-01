#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/prune-test-cache.sh [options]

Prune stale Ori-owned temporary artifacts and conditionally clear the shared
Go build cache when it exceeds the configured size limit.

Options:
  --dry-run                 Report what would be pruned without deleting it.
  --temp-dir DIR            Scan DIR for stale Ori artifacts instead of TMPDIR.
  --older-than-hours HOURS  Stale-artifact minimum age (default: 24).
  --max-go-cache-kib KIB    Go build-cache limit (default: 20971520 / 20 GiB).
  -h, --help                Show this help text.

Environment:
  ORI_SKIP_CACHE_PRUNE=1       Skip all shared-cache and stale-artifact pruning.
  ORI_STALE_ARTIFACT_HOURS=N   Override the default artifact age.
  ORI_GO_CACHE_MAX_KIB=N       Override the default Go cache limit.
  ORI_GO_BIN=PATH              Use a specific Go executable.

This script never removes the Go module cache or Playwright browser cache.
EOF
}

fail() {
	printf 'Error: %s\n' "$*" >&2
	exit 2
}

is_true() {
	case "${1:-}" in
	1 | true | TRUE | yes | YES) return 0 ;;
	*) return 1 ;;
	esac
}

if is_true "${ORI_SKIP_CACHE_PRUNE:-}"; then
	printf 'Cache pruning skipped (ORI_SKIP_CACHE_PRUNE=1).\n'
	exit 0
fi

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
cleanup_script="$script_dir/clean-test-artifacts.sh"
requested_temp_dir="${TMPDIR:-/tmp}"
older_than_hours="${ORI_STALE_ARTIFACT_HOURS:-24}"
max_go_cache_kib="${ORI_GO_CACHE_MAX_KIB:-20971520}"
go_bin="${ORI_GO_BIN:-go}"
dry_run=false

while [[ $# -gt 0 ]]; do
	case "$1" in
	--dry-run)
		dry_run=true
		shift
		;;
	--temp-dir)
		[[ $# -ge 2 ]] || fail "--temp-dir requires a directory"
		requested_temp_dir="$2"
		shift 2
		;;
	--temp-dir=*)
		requested_temp_dir="${1#*=}"
		shift
		;;
	--older-than-hours)
		[[ $# -ge 2 ]] || fail "--older-than-hours requires a number"
		older_than_hours="$2"
		shift 2
		;;
	--older-than-hours=*)
		older_than_hours="${1#*=}"
		shift
		;;
	--max-go-cache-kib)
		[[ $# -ge 2 ]] || fail "--max-go-cache-kib requires a number"
		max_go_cache_kib="$2"
		shift 2
		;;
	--max-go-cache-kib=*)
		max_go_cache_kib="${1#*=}"
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		fail "unknown option: $1"
		;;
	esac
done

[[ "$older_than_hours" =~ ^[0-9]+$ ]] || fail "artifact age must be a non-negative integer"
[[ "$max_go_cache_kib" =~ ^[0-9]+$ ]] || fail "Go cache limit must be a non-negative integer"
[[ -x "$cleanup_script" ]] || fail "cleanup helper is not executable: $cleanup_script"

cleanup_args=(--quiet --older-than-hours "$older_than_hours" --temp-dir "$requested_temp_dir")
if [[ "$dry_run" == "true" ]]; then
	cleanup_args=(--dry-run "${cleanup_args[@]}")
else
	cleanup_args=(--delete "${cleanup_args[@]}")
fi

cleanup_output="$("$cleanup_script" "${cleanup_args[@]}")"
if [[ "$dry_run" == "true" || "$cleanup_output" != *"Removed 0 of 0 Ori test artifact(s)"* ]]; then
	printf '%s\n' "$cleanup_output"
fi

if ! command -v "$go_bin" >/dev/null 2>&1; then
	printf 'Skipping Go build-cache pruning: %s was not found.\n' "$go_bin" >&2
	exit 0
fi

go_cache_dir="${GOCACHE:-}"
if [[ -z "$go_cache_dir" ]]; then
	go_cache_dir="$("$go_bin" env GOCACHE)"
fi
if [[ -z "$go_cache_dir" || "$go_cache_dir" == "off" || ! -d "$go_cache_dir" ]]; then
	exit 0
fi

if ! cache_size_line="$(du -sk "$go_cache_dir" 2>/dev/null)"; then
	printf 'Skipping Go build-cache pruning: cannot measure %s.\n' "$go_cache_dir" >&2
	exit 0
fi
cache_size_kib="${cache_size_line%%[[:space:]]*}"
[[ "$cache_size_kib" =~ ^[0-9]+$ ]] || fail "could not parse Go cache size: $cache_size_line"

if ((cache_size_kib <= max_go_cache_kib)); then
	if [[ "$dry_run" == "true" ]]; then
		printf 'Go build cache: %d KiB (limit: %d KiB); no prune needed.\n' "$cache_size_kib" "$max_go_cache_kib"
	fi
	exit 0
fi

if [[ "$dry_run" == "true" ]]; then
	printf 'Would prune Go build cache: %d KiB exceeds the %d KiB limit.\n' "$cache_size_kib" "$max_go_cache_kib"
	exit 0
fi

printf 'Pruning Go build cache: %d KiB exceeds the %d KiB limit...\n' "$cache_size_kib" "$max_go_cache_kib"
GOCACHE="$go_cache_dir" "$go_bin" clean -cache
printf 'Go build cache pruned. Module and Playwright browser caches were preserved.\n'
