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

# The wrapper nests TMPDIR under its own per-run directory
# (ori-test-run.XXXXXX/tmp), which can push a Unix-domain socket fixture
# past macOS's short sockaddr_un.sun_path limit. Reproduce a deliberately
# long parent here and prove the Herdr socket fixture still passes through
# the wrapper (tools/herdr-devflow/internal/herdr uses an owned short
# socket directory instead of TMPDIR-derived paths for its socket files).
if [[ "$(uname -s)" == "Darwin" ]]; then
	repo_root="$(cd "$script_dir/.." && pwd -P)"
	long_parent="$fixture_root/deliberately-long-parent-temp-directory-to-exceed-the-unix-domain-socket-sockaddr-un-path-limit-on-macos"
	mkdir -p "$long_parent"
	if ! (cd "$repo_root" && ORI_SKIP_CACHE_PRUNE=1 TMPDIR="$long_parent" \
		"$runner" go test ./tools/herdr-devflow/internal/herdr/... \
		-run 'TestCallSocketUsesJSONLines|TestAgentListAndWorkspaceClosePreferTheStructuredSocket'); then
		printf 'Herdr Unix-socket fixture failed under a long TMPDIR parent.\n' >&2
		exit 1
	fi
fi

printf 'run-test-command tests passed\n'
