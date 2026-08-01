#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/run-test-command.sh COMMAND [ARG ...]

Run one test command inside an owned temporary sandbox. The sandbox is removed
on success, failure, or interruption, then normal cache maintenance runs.

Environment:
  ORI_KEEP_TEST_SANDBOX=1  Preserve the sandbox and print its path.
  ORI_SKIP_CACHE_PRUNE=1   Skip stale-artifact and shared Go-cache pruning.
  PWTEST_CACHE_DIR=DIR     Override Playwright's reusable transform cache.
EOF
}

is_true() {
	case "${1:-}" in
	1 | true | TRUE | yes | YES) return 0 ;;
	*) return 1 ;;
	esac
}

if [[ $# -eq 0 ]]; then
	usage >&2
	exit 2
fi

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
prune_script="$script_dir/prune-test-cache.sh"
requested_temp_parent="${TMPDIR:-/tmp}"
if ! temp_parent="$(cd "$requested_temp_parent" 2>/dev/null && pwd -P)"; then
	printf 'Error: cannot resolve temporary directory: %s\n' "$requested_temp_parent" >&2
	exit 2
fi
case "$temp_parent" in
"" | "/")
	printf 'Error: refusing unsafe temporary directory: %s\n' "$temp_parent" >&2
	exit 2
	;;
esac

run_root="$(mktemp -d "$temp_parent/ori-test-run.XXXXXX")"
case "$run_root" in
"$temp_parent"/ori-test-run.*) ;;
*)
	printf 'Error: unexpected test sandbox path: %s\n' "$run_root" >&2
	exit 2
	;;
esac

mkdir -p "$run_root/tmp" "$run_root/go-tmp"

# Playwright's compiled-transform cache is intentionally reusable and tiny.
# Keep it beside the run sandboxes rather than deleting and rebuilding it on
# every invocation. Browser binaries remain in Playwright's normal user cache.
if [[ -z "${PWTEST_CACHE_DIR:-}" ]]; then
	export PWTEST_CACHE_DIR="$temp_parent/playwright-transform-cache-${UID:-user}"
fi
export ORI_TEST_RUN_DIR="$run_root"
export TMPDIR="$run_root/tmp"
export GOTMPDIR="$run_root/go-tmp"

finish_test_run() {
	local status=$?
	local cleanup_failed=0
	trap - EXIT INT TERM

	if is_true "${ORI_KEEP_TEST_SANDBOX:-}"; then
		printf 'Test sandbox preserved: %s\n' "$run_root"
	else
		case "$run_root" in
		"$temp_parent"/ori-test-run.*)
			if ! rm -rf -- "$run_root"; then
				printf 'Failed to remove test sandbox: %s\n' "$run_root" >&2
				cleanup_failed=1
			fi
			;;
		*)
			printf 'Refusing to remove unexpected test sandbox: %s\n' "$run_root" >&2
			cleanup_failed=1
			;;
		esac
	fi

	# The run-owned TMPDIR and GOTMPDIR may have just been removed. Restore a
	# valid temporary parent before invoking shared-cache maintenance.
	export TMPDIR="$temp_parent"
	unset GOTMPDIR

	if ! "$prune_script" --temp-dir "$temp_parent"; then
		printf 'Warning: automatic cache pruning failed.\n' >&2
	fi

	if [[ "$status" -eq 0 && "$cleanup_failed" -ne 0 ]]; then
		status=1
	fi
	exit "$status"
}

trap finish_test_run EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

"$@"
