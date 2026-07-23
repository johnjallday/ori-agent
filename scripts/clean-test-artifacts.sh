#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/clean-test-artifacts.sh [options]

Find Ori test artifacts in the system temporary directory.

Options:
  --delete          Delete matching artifacts. The default is a dry run.
  --dry-run         Print matching artifacts without deleting them.
  --temp-dir DIR    Scan DIR instead of TMPDIR (primarily for testing).
  -h, --help        Show this help text.

Matched names:
  ori-test-*
  ori-vault-files-*
  ori-agent-test-*
  ori-db-test-*
  ori-db-migration-*
EOF
}

fail() {
	printf 'Error: %s\n' "$*" >&2
	exit 2
}

dry_run=true
requested_temp_dir="${TMPDIR:-/tmp}"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--delete)
		dry_run=false
		shift
		;;
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
	-h | --help)
		usage
		exit 0
		;;
	*)
		fail "unknown option: $1"
		;;
	esac
done

[[ -n "$requested_temp_dir" ]] || fail "temporary directory cannot be empty"
[[ -d "$requested_temp_dir" ]] || fail "temporary directory does not exist: $requested_temp_dir"

if ! temp_root="$(cd "$requested_temp_dir" 2>/dev/null && pwd -P)"; then
	fail "cannot resolve temporary directory: $requested_temp_dir"
fi

case "$temp_root" in
"" | "/")
	fail "refusing to scan unsafe temporary directory: $temp_root"
	;;
esac

shopt -s nullglob
candidates=(
	"$temp_root"/ori-test-*
	"$temp_root"/ori-vault-files-*
	"$temp_root"/ori-agent-test-*
	"$temp_root"/ori-db-test-*
	"$temp_root"/ori-db-migration-*
)
shopt -u nullglob

matched=0
removed=0

for candidate in "${candidates[@]}"; do
	[[ -e "$candidate" || -L "$candidate" ]] || continue

	candidate_name="${candidate##*/}"
	case "$candidate_name" in
	ori-test-* | ori-vault-files-* | ori-agent-test-* | ori-db-test-* | ori-db-migration-*)
		;;
	*)
		fail "refusing unexpected artifact name: $candidate_name"
		;;
	esac

	[[ "${candidate%/*}" == "$temp_root" ]] ||
		fail "refusing artifact outside temporary directory: $candidate"

	matched=$((matched + 1))
	if [[ "$dry_run" == "true" ]]; then
		printf 'Would delete: %s\n' "$candidate"
		continue
	fi

	if rm -rf "$candidate"; then
		printf 'Deleted: %s\n' "$candidate"
		removed=$((removed + 1))
	else
		printf 'Failed to delete: %s\n' "$candidate" >&2
	fi
done

if [[ "$dry_run" == "true" ]]; then
	printf '\nDry run: found %d Ori test artifact(s) in %s.\n' "$matched" "$temp_root"
	if [[ "$matched" -gt 0 ]]; then
		printf 'Run again with --delete to remove them.\n'
	fi
	exit 0
fi

printf '\nRemoved %d of %d Ori test artifact(s) from %s.\n' "$removed" "$matched" "$temp_root"
[[ "$removed" -eq "$matched" ]]
