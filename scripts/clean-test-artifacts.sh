#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/clean-test-artifacts.sh [options]

Find Ori test artifacts in the system temporary directory.

Options:
  --delete                 Delete matching artifacts. The default is a dry run.
  --dry-run                Print matching artifacts without deleting them.
  --older-than-hours HOURS Only match artifacts older than HOURS (default: 0).
  --quiet                  Suppress one line per matching artifact.
  --temp-dir DIR           Scan DIR instead of TMPDIR (primarily for testing).
  -h, --help               Show this help text.

Matched names:
  ori-test-*
  ori-vault-files-*
  ori-agent-test-*
  ori-db-test-*
  ori-db-migration-*
  ori-wt-herd.*
  ori-wt-backlog.*
  ori-e2e.*
  ori-e2e-demo.*
  ori-readme-capture.*
  ori-clean-test-artifacts.*
EOF
}

fail() {
	printf 'Error: %s\n' "$*" >&2
	exit 2
}

dry_run=true
quiet=false
older_than_hours=0
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
	--older-than-hours)
		[[ $# -ge 2 ]] || fail "--older-than-hours requires a number"
		older_than_hours="$2"
		shift 2
		;;
	--older-than-hours=*)
		older_than_hours="${1#*=}"
		shift
		;;
	--quiet)
		quiet=true
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

[[ "$older_than_hours" =~ ^[0-9]+$ ]] || fail "--older-than-hours must be a non-negative integer"

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
	"$temp_root"/ori-wt-herd.*
	"$temp_root"/ori-wt-backlog.*
	"$temp_root"/ori-e2e.*
	"$temp_root"/ori-e2e-demo.*
	"$temp_root"/ori-readme-capture.*
	"$temp_root"/ori-clean-test-artifacts.*
)
shopt -u nullglob

matched=0
removed=0
skipped_recent=0
minimum_age_minutes=$((older_than_hours * 60))

for candidate in "${candidates[@]}"; do
	[[ -e "$candidate" || -L "$candidate" ]] || continue

	candidate_name="${candidate##*/}"
	case "$candidate_name" in
	ori-test-* | ori-vault-files-* | ori-agent-test-* | ori-db-test-* | ori-db-migration-* | \
		ori-wt-herd.* | ori-wt-backlog.* | ori-e2e.* | ori-e2e-demo.* | \
		ori-readme-capture.* | ori-clean-test-artifacts.*)
		;;
	*)
		fail "refusing unexpected artifact name: $candidate_name"
		;;
	esac

	[[ "${candidate%/*}" == "$temp_root" ]] ||
		fail "refusing artifact outside temporary directory: $candidate"

	if ((minimum_age_minutes > 0)); then
		if [[ -z "$(find "$candidate" -prune -mmin "+$minimum_age_minutes" -print 2>/dev/null)" ]]; then
			skipped_recent=$((skipped_recent + 1))
			continue
		fi
	fi

	matched=$((matched + 1))
	if [[ "$dry_run" == "true" ]]; then
		[[ "$quiet" == "true" ]] || printf 'Would delete: %s\n' "$candidate"
		continue
	fi

	if rm -rf -- "$candidate"; then
		[[ "$quiet" == "true" ]] || printf 'Deleted: %s\n' "$candidate"
		removed=$((removed + 1))
	else
		printf 'Failed to delete: %s\n' "$candidate" >&2
	fi
done

if [[ "$dry_run" == "true" ]]; then
	printf '\nDry run: found %d Ori test artifact(s) in %s.\n' "$matched" "$temp_root"
	if [[ "$skipped_recent" -gt 0 ]]; then
		printf 'Skipped %d recent artifact(s) younger than %d hour(s).\n' "$skipped_recent" "$older_than_hours"
	fi
	if [[ "$matched" -gt 0 ]]; then
		printf 'Run again with --delete to remove them.\n'
	fi
	exit 0
fi

printf '\nRemoved %d of %d Ori test artifact(s) from %s.\n' "$removed" "$matched" "$temp_root"
if [[ "$skipped_recent" -gt 0 ]]; then
	printf 'Skipped %d recent artifact(s) younger than %d hour(s).\n' "$skipped_recent" "$older_than_hours"
fi
[[ "$removed" -eq "$matched" ]]
