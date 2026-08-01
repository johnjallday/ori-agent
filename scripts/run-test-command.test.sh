#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
runner="$script_dir/run-test-command.sh"
fixture_parent="$(cd "${TMPDIR:-/tmp}" && pwd -P)"
fixture_root="$(mktemp -d "$fixture_parent/ori-run-test-command.XXXXXX")"

cleanup_fixture() {
	case "$fixture_root" in
	"$fixture_parent"/ori-run-test-command.*) rm -rf -- "$fixture_root" ;;
	*) printf 'Refusing to remove unexpected fixture: %s\n' "$fixture_root" >&2 ;;
	esac
}
trap cleanup_fixture EXIT

host_temp="$fixture_root/host-temp"
mkdir -p "$host_temp"

capture="$fixture_root/default-env"
ORI_SKIP_CACHE_PRUNE=1 TMPDIR="$host_temp" "$runner" /bin/sh -c \
	'printf "%s\n%s\n%s\n" "$TMPDIR" "$GOTMPDIR" "$PWTEST_CACHE_DIR" > "$1"' sh "$capture"
run_tmp="$(sed -n '1p' "$capture")"
run_go_tmp="$(sed -n '2p' "$capture")"
playwright_cache="$(sed -n '3p' "$capture")"
run_root="${run_tmp%/tmp}"
[[ "$run_go_tmp" == "$run_root/go-tmp" ]]
[[ "$playwright_cache" == "$host_temp"/playwright-transform-cache-* ]]
[[ ! -e "$run_root" ]]

capture="$fixture_root/kept-env"
keep_output="$(ORI_SKIP_CACHE_PRUNE=1 ORI_KEEP_TEST_SANDBOX=1 TMPDIR="$host_temp" \
	"$runner" /bin/sh -c 'printf "%s\n" "$ORI_TEST_RUN_DIR" > "$1"' sh "$capture")"
kept_root="$(sed -n '1p' "$capture")"
[[ "$keep_output" == *"Test sandbox preserved: $kept_root"* ]]
[[ -d "$kept_root" ]]

if ORI_SKIP_CACHE_PRUNE=1 TMPDIR="$host_temp" "$runner" /bin/sh -c 'exit 17'; then
	printf 'Expected the wrapped command failure to propagate.\n' >&2
	exit 1
else
	status=$?
	[[ "$status" -eq 17 ]]
fi

remaining=("$host_temp"/ori-test-run.*)
for candidate in "${remaining[@]}"; do
	[[ -e "$candidate" ]] || continue
	[[ "$candidate" == "$kept_root" ]] || {
		printf 'Unexpected leaked sandbox: %s\n' "$candidate" >&2
		exit 1
	}
done

printf 'run-test-command tests passed\n'
