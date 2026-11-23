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

# Two checkout-shaped working directories, one cache. The writer exits while
# the reader is between two reads of the same seed. The fake Go cleaner lets
# this reproduce the old failure without ever clearing an actual build cache.
test_shared_cache() (
	local overlap="$fixture_root/overlap" reader_pid="" writer_pid="" status=0
	mkdir -p "$overlap/reader" "$overlap/writer" "$overlap/bin" "$overlap/build-cache" \
		"$overlap/temp/ori-test-old" "$overlap/temp/ori-test-recent"
	trap 'touch "$overlap/release"; [[ -z "$writer_pid" ]] || wait "$writer_pid" || true;
		[[ -z "$reader_pid" ]] || wait "$reader_pid" || true' EXIT
	trap 'exit 130' INT
	trap 'exit 143' TERM

	export TMPDIR="$overlap/temp" GOCACHE="$overlap/build-cache" FIXTURE_CACHE="$overlap/build-cache"
	export ORI_GO_BIN="$overlap/bin/go" CACHE_OPERATIONS="$overlap/cache-operations"
	export ORI_KEEP_TEST_SANDBOX=0 ORI_SKIP_CACHE_PRUNE=0 ORI_GO_CACHE_MAX_KIB=1 ORI_STALE_ARTIFACT_HOURS=24
	export PATH="$overlap/bin:$PATH"
	touch -t 202001010000 "$TMPDIR/ori-test-old"
	printf 'cache seed\n%8192s\n' '' > "$GOCACHE/seed"

	cat > "$ORI_GO_BIN" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >> "$CACHE_OPERATIONS"
if [[ $# != 2 || "$1" != clean || "$2" != -cache || "${GOCACHE:-}" != "$FIXTURE_CACHE" || -L "$GOCACHE" ]]; then
	printf 'Refusing unexpected fake Go call or cache path\n' >&2
	exit 2
fi
rm -f -- "$GOCACHE/seed"
SH
	cat > "$overlap/bin/du" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf 'du %s\n' "$*" >> "$CACHE_OPERATIONS"
[[ $# == 2 && "$1" == -sk && "$2" == "$FIXTURE_CACHE" && ! -L "$2" ]] || exit 2
exec /usr/bin/du "$@"
SH
	chmod +x "$ORI_GO_BIN" "$overlap/bin/du"

	(
		cd "$overlap/reader"
		exec "$runner" bash -c '
			set -euo pipefail
			IFS= read -r seed < "$GOCACHE/seed"
			[[ "$seed" == "cache seed" ]]
			: > "$1/ready"
			deadline=$((SECONDS + 15))
			until [[ -f "$1/release" ]]; do
				if ((SECONDS >= deadline)); then
					printf "Timed out waiting to release the cache reader.\n" >&2
					exit 2
				fi
				sleep 0.05
			done
			if [[ ! -r "$GOCACHE/seed" ]]; then
				printf "Shared cache seed disappeared after peer wrapper exited.\n" >&2
				exit 23
			fi
			IFS= read -r seed < "$GOCACHE/seed"
			[[ "$seed" == "cache seed" ]]
		' bash "$overlap"
	) &
	reader_pid=$!
	local deadline=$((SECONDS + 15))
	until [[ -f "$overlap/ready" ]]; do
		if ((SECONDS >= deadline)) || ! kill -0 "$reader_pid" 2>/dev/null; then
			printf 'Cache reader failed to signal readiness.\n' >&2
			exit 1
		fi
		sleep 0.05
	done
	(cd "$overlap/writer" && exec "$runner" /bin/sh -c ':') &
	writer_pid=$!
	wait "$writer_pid"
	writer_pid=""
	: > "$overlap/release"
	wait "$reader_pid" || status=$?
	reader_pid=""
	[[ "$status" == 0 ]] || exit "$status"
	[[ -f "$GOCACHE/seed" ]]
	[[ ! -e "$TMPDIR/ori-test-old" && -d "$TMPDIR/ori-test-recent" ]]

	mkdir "$TMPDIR/ori-test-failed-run"
	touch -t 202001010000 "$TMPDIR/ori-test-failed-run"
	status=0
	"$runner" /bin/sh -c 'exit 17' || status=$?
	[[ "$status" == 17 && -f "$GOCACHE/seed" && ! -e "$TMPDIR/ori-test-failed-run" ]]

	mkdir "$TMPDIR/ori-test-opt-out"
	touch -t 202001010000 "$TMPDIR/ori-test-opt-out"
	ORI_SKIP_CACHE_PRUNE=1 "$runner" /bin/sh -c ':'
	[[ -d "$TMPDIR/ori-test-opt-out" && -f "$GOCACHE/seed" ]]
	if [[ -e "$CACHE_OPERATIONS" ]]; then
		printf 'Test wrapper unexpectedly accessed Go cache tooling:\n' >&2
		cat "$CACHE_OPERATIONS" >&2
		exit 1
	fi
)
test_shared_cache

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
