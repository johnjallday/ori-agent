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
# A run alone on the machine always gets slot 0: the same TMPDIR and GOTMPDIR
# every time, which is what lets Go's test cache serve unchanged packages.
[[ "$run_root" == "$host_temp/ori-test-run.0" ]]

run_slot() {
	local capture="$1"
	shift
	"$@" "$runner" /bin/sh -c 'printf "%s\n%s\n" "$ORI_TEST_RUN_DIR" "$GOTMPDIR" > "$1"' sh "$capture"
	sed -n '1p' "$capture"
}
[[ "$(run_slot "$fixture_root/again" env ORI_SKIP_CACHE_PRUNE=1 TMPDIR="$host_temp")" == "$host_temp/ori-test-run.0" ]]

# A slot another live run holds (its owner is this test) is left alone and the
# next free number is used; a slot whose owner has exited is reclaimed in place.
mkdir "$host_temp/ori-test-run.0"
printf '%s\n' "$$" > "$host_temp/ori-test-run.0/.owner"
[[ "$(run_slot "$fixture_root/held" env ORI_SKIP_CACHE_PRUNE=1 TMPDIR="$host_temp")" == "$host_temp/ori-test-run.1" ]]
[[ -f "$host_temp/ori-test-run.0/.owner" ]]
[[ ! -e "$host_temp/ori-test-run.1" ]]
printf '%s\n' 2147483647 > "$host_temp/ori-test-run.0/.owner"
[[ "$(run_slot "$fixture_root/reclaimed" env ORI_SKIP_CACHE_PRUNE=1 TMPDIR="$host_temp")" == "$host_temp/ori-test-run.0" ]]
[[ ! -e "$host_temp/ori-test-run.0" ]]

capture="$fixture_root/kept-env"
keep_output="$(ORI_SKIP_CACHE_PRUNE=1 ORI_KEEP_TEST_SANDBOX=1 TMPDIR="$host_temp" \
	"$runner" /bin/sh -c 'printf "%s\n" "$ORI_TEST_RUN_DIR" > "$1"' sh "$capture")"
kept_root="$(sed -n '1p' "$capture")"
[[ "$keep_output" == *"Test sandbox preserved: $kept_root"* ]]
[[ -d "$kept_root" ]]
# A kept sandbox records no owner, so no later run reclaims it: the next run
# takes the next slot and the kept one survives.
[[ "$kept_root" == "$host_temp/ori-test-run.0" ]]
[[ ! -e "$kept_root/.owner" ]]
[[ "$(run_slot "$fixture_root/beside-kept" env ORI_SKIP_CACHE_PRUNE=1 TMPDIR="$host_temp")" == "$host_temp/ori-test-run.1" ]]
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

# The wrapper nests TMPDIR under its own per-run directory
# (ori-test-run.N/tmp), which can push a Unix-domain socket fixture
# past macOS's short sockaddr_un.sun_path limit. Reproduce a deliberately
# long parent here and prove an Ori-owned short socket fixture passes through
# the wrapper, without depending on a package from the external toolbox.
if [[ "$(uname -s)" == "Darwin" ]]; then
	repo_root="$(cd "$script_dir/.." && pwd -P)"
	long_parent="$fixture_root/deliberately-long-parent-temp-directory-to-exceed-the-unix-domain-socket-sockaddr-un-path-limit-on-macos"
	mkdir -p "$long_parent"
	if ! (cd "$repo_root" && ORI_SKIP_CACHE_PRUNE=1 TMPDIR="$long_parent" \
		"$runner" go test ./scripts/testdata/short-socket -run TestShortSocketUnderLongTMPDIR); then
		printf 'Ori Unix-socket fixture failed under a long TMPDIR parent.\n' >&2
		exit 1
	fi
fi

printf 'run-test-command tests passed\n'
