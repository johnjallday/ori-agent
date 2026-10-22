#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./scripts/run-test-command.sh COMMAND [ARG ...]

Run one test command inside an owned temporary sandbox: the lowest free
ori-test-run.N slot under TMPDIR. A run that is alone on the machine always
gets slot 0, so tests see the same TMPDIR and GOTMPDIR from run to run and Go's
test cache can serve packages whose inputs did not change. The sandbox is
removed on success, failure, or interruption, then normal cache maintenance
runs.

Environment:
  ORI_KEEP_TEST_SANDBOX=1  Preserve the sandbox and print its path. It keeps
                           its slot until removed (clean-test-artifacts.sh
                           --delete), so later runs use the next slot.
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

# One slot per concurrent run, the lowest free number first; mkdir is the
# atomic claim. Go keys a cached test result on the environment the test read,
# and t.TempDir reads GOTMPDIR, so a random mktemp name here missed the cache
# for every package using it. A numbered slot keeps the path stable for the
# common case of one run at a time, and distinct for runs that overlap.
run_root=""
slot=0
while [[ -z "$run_root" ]]; do
	candidate="$temp_parent/ori-test-run.$slot"
	if mkdir -m 700 "$candidate" 2>/dev/null; then
		run_root="$candidate"
		break
	fi
	# A slot whose recorded owner has exited was left by a killed run. Move it
	# aside before deleting, so two reclaimers can never delete each other's
	# fresh sandbox; the loser's mv fails and it simply retries the claim. A
	# kept sandbox records no owner and is never reclaimed.
	owner=""
	if [[ -f "$candidate/.owner" ]]; then
		IFS= read -r owner < "$candidate/.owner" || true
	fi
	if [[ "$owner" =~ ^[0-9]+$ ]] && ! kill -0 "$owner" 2>/dev/null; then
		stale="$candidate.stale.$$"
		if mv "$candidate" "$stale" 2>/dev/null; then
			rm -rf -- "$stale"
			continue
		fi
	fi
	slot=$((slot + 1))
	if ((slot >= 64)); then
		printf 'Error: no free test sandbox slot under %s\n' "$temp_parent" >&2
		exit 2
	fi
done
case "$run_root" in
"$temp_parent"/ori-test-run.*) ;;
*)
	printf 'Error: unexpected test sandbox path: %s\n' "$run_root" >&2
	exit 2
	;;
esac

mkdir -p "$run_root/tmp" "$run_root/go-tmp"
printf '%s\n' "$$" > "$run_root/.owner"

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
		# Without an owner the slot is never reclaimed by a later run.
		rm -f -- "$run_root/.owner"
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
