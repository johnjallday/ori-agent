#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "$0")" && pwd -P)"
root="$(mktemp -d "${TMPDIR:-/tmp}/ori-unit-wrapper-test.XXXXXX")"
trap 'rm -rf -- "$root"' EXIT
mkdir -p "$root/repo/scripts" "$root/bin" "$root/artifacts" "$root/tmp"
cp "$script_dir/run-unit-tests.sh" "$script_dir/run-test-command.sh" "$script_dir/prune-test-cache.sh" "$script_dir/list-unit-packages.sh" "$script_dir/unit-shards.py" "$script_dir/unit-shard-weights.json" "$root/repo/scripts/"
# Do not inherit the enclosing CI shard or the local gate's package cap and
# cache setting.
unset ORI_UNIT_SHARD ORI_UNIT_SHARD_COUNT ORI_UNIT_TEST_PARALLEL ORI_UNIT_TEST_CACHE
# The fixture weights make internal/two the heavier package, so the run order
# (heaviest first) differs from the alphabetical selection.
printf '{"github.com/johnjallday/ori-agent/internal/two": 50}\n' > "$root/repo/scripts/unit-shard-weights.json"
cat > "$root/reporter" <<'STUB'
#!/usr/bin/env bash
cat >/dev/null
printf '## Fixture timing summary\n'
exit "${REPORT_STATUS:-0}"
STUB
cat > "$root/bin/go" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  build)
    [[ "${BUILD_STATUS:-0}" -eq 0 ]] || exit "$BUILD_STATUS"
    cp "$TEST_REPORTER" "$3"
    chmod +x "$3"
    ;;
  list)
    [[ "${LIST_STATUS:-0}" -eq 0 ]] || exit "$LIST_STATUS"
    if [[ -n "${GOOS:-}" ]]; then
      # The platform selector lists compiled files per GOOS and keeps the differences.
      printf 'github.com/johnjallday/ori-agent/internal/platform |trash_%s.go||||\n' "$GOOS"
      printf 'github.com/johnjallday/ori-agent/internal/one |same.go||||\n'
      printf 'github.com/johnjallday/ori-agent/internal/two |same.go||||\n'
    else
      printf 'github.com/johnjallday/ori-agent/internal/one\ngithub.com/johnjallday/ori-agent/internal/two\n'
    fi
    ;;
  version) printf 'go version go1.26.0 fixture/fixture\n' ;;
  test)
    printf '%s\n' "$@" > "$TEST_ARGUMENTS"
    printf '%s\n' "$ORI_TEST_RUN_DIR" > "$TEST_SANDBOX"
    umask > "$TEST_UMASK"
    printf '%s\n' '{"Action":"start","Package":"fixture"}'
    printf 'synthetic compiler diagnostic\n' >&2
    if [[ "${WAIT_FOR_SIGNAL:-0}" -eq 1 ]]; then
      printf '%s\n' "$$" > "$TEST_CHILD_PID"
      trap 'exit 143' TERM
      sleep 60 &
      wait "$!"
    fi
    printf '%s\n' '{"Action":"pass","Package":"fixture","Elapsed":0.01}'
    exit "${TEST_STATUS:-0}"
    ;;
  *) exit 99 ;;
esac
STUB
cat > "$root/bin/git" <<'STUB'
#!/usr/bin/env bash
printf 'fixture-revision\n'
STUB
chmod +x "$root/reporter" "$root/bin/go" "$root/bin/git" "$root/repo/scripts/"*.sh
export PATH="$root/bin:$PATH" TEST_REPORTER="$root/reporter"
export TMPDIR="$root/tmp" ORI_SKIP_CACHE_PRUNE=1 ORI_UNIT_ARTIFACT_PARENT="$root/artifacts"
export TEST_ARGUMENTS="$root/arguments" TEST_SANDBOX="$root/sandbox" TEST_CHILD_PID="$root/child-pid" TEST_UMASK="$root/umask"
export GITHUB_OUTPUT="$root/outputs" GITHUB_STEP_SUMMARY="$root/summary"
runner="$root/repo/scripts/run-unit-tests.sh"

check_case() {
  local name="$1" test_status="$2" report_status="$3" expected="$4" status=0
  TEST_STATUS="$test_status" REPORT_STATUS="$report_status" "$runner" > "$root/$name.log" 2>&1 || status=$?
  [[ "$status" -eq "$expected" ]] || { printf '%s: status %s, expected %s\n' "$name" "$status" "$expected" >&2; exit 1; }
  grep -q 'Fixture timing summary' "$root/$name.log"
  grep -q 'Wall clocks: reporter build .* test command .* (includes compilation)' "$root/$name.log"
  local sandbox
  IFS= read -r sandbox < "$TEST_SANDBOX"
  [[ ! -e "$sandbox" ]] || { printf 'Leaked test sandbox\n' >&2; exit 1; }
}
check_case success 0 0 0
expected_umask="$(umask)"
IFS= read -r actual_umask < "$TEST_UMASK"
[[ "$actual_umask" == "$expected_umask" ]]
check_case assertion 17 1 17
check_case build-test 1 1 1
check_case timeout 124 1 124
check_case report-failure 0 9 9
check_case both-fail 42 9 42
GITHUB_STEP_SUMMARY="$root" check_case summary-io-failure 0 0 1
GITHUB_STEP_SUMMARY="$root" check_case test-and-summary-io-failure 42 0 42
for flag in -json -short -race -timeout 30m -count=1 -covermode=atomic; do
  grep -qx -- "$flag" "$TEST_ARGUMENTS"
done
grep -q -- '-coverprofile=.*/ori-unit\..*/coverage.txt' "$TEST_ARGUMENTS"
# Unset, Go keeps its own package parallelism (CI's runs).
if grep -qx -- '-p' "$TEST_ARGUMENTS"; then printf 'Unrequested -p\n' >&2; exit 1; fi
grep -qx 'github.com/johnjallday/ori-agent/internal/one' "$TEST_ARGUMENTS"
# Heaviest first: internal/two reaches go test ahead of internal/one although
# the selection lists them alphabetically, and the selection itself is unchanged.
two_line="$(grep -n 'internal/two$' "$TEST_ARGUMENTS" | cut -d: -f1)"
one_line="$(grep -n 'internal/one$' "$TEST_ARGUMENTS" | cut -d: -f1)"
[[ "$two_line" -lt "$one_line" ]] || { printf 'Packages did not run heaviest first\n' >&2; exit 1; }
for directory in "$root/artifacts"/ori-unit.*; do
  [[ "$(head -1 "$directory/packages.txt")" == 'github.com/johnjallday/ori-agent/internal/one' ]]
  [[ "$(head -1 "$directory/run-order.txt")" == 'github.com/johnjallday/ori-agent/internal/two' ]]
done
RUNNER_OS=macOS check_case platform 0 0 0
grep -qx 'github.com/johnjallday/ori-agent/internal/platform' "$TEST_ARGUMENTS"
grep -qx 'github.com/johnjallday/ori-agent/cmd/server' "$TEST_ARGUMENTS"
if grep -q 'internal/one' "$TEST_ARGUMENTS"; then exit 1; fi
# The platform leg keeps the race detector but never produces coverage.
for flag in -json -short -race -timeout 30m -count=1; do
  grep -qx -- "$flag" "$TEST_ARGUMENTS"
done
if grep -q -- '-cover' "$TEST_ARGUMENTS"; then printf 'Platform leg requested coverage\n' >&2; exit 1; fi
grep -qx 'flags=-json -short -race -timeout 30m -count=1' "$root/artifacts"/ori-unit.*/metadata.txt
status=0
BUILD_STATUS=23 "$runner" > "$root/build-failure.log" 2>&1 || status=$?
[[ "$status" -eq 23 ]]
status=0
LIST_STATUS=27 "$runner" > "$root/list-failure.log" 2>&1 || status=$?
[[ "$status" -eq 27 ]]
# Each run allocates a new root; sibling evidence must not be overwritten/deleted.
count=0
for directory in "$root/artifacts"/ori-unit.*; do
  [[ -s "$directory/summary.md" ]]
  count=$((count+1))
done
[[ "$count" -eq 11 ]]

# Each shard executes only its assigned packages, with exactly the original
# flags. The planner places the heavier fixture package (internal/two) first.
ORI_UNIT_SHARD=1 ORI_UNIT_SHARD_COUNT=2 check_case shard-one 0 0 0
grep -qx 'github.com/johnjallday/ori-agent/internal/two' "$TEST_ARGUMENTS"
if grep -qx 'github.com/johnjallday/ori-agent/internal/one' "$TEST_ARGUMENTS"; then exit 1; fi
ORI_UNIT_SHARD=2 ORI_UNIT_SHARD_COUNT=2 check_case shard-two 0 0 0
grep -qx 'github.com/johnjallday/ori-agent/internal/one' "$TEST_ARGUMENTS"
if grep -qx 'github.com/johnjallday/ori-agent/internal/two' "$TEST_ARGUMENTS"; then exit 1; fi
for flag in -json -short -race -timeout 30m -count=1 -covermode=atomic; do
  grep -qx -- "$flag" "$TEST_ARGUMENTS"
done
ORI_UNIT_SHARD=1 ORI_UNIT_SHARD_COUNT=2 check_case shard-failure 17 1 17
for settings in 'ORI_UNIT_SHARD=0 ORI_UNIT_SHARD_COUNT=2' \
  'ORI_UNIT_SHARD=3 ORI_UNIT_SHARD_COUNT=2' \
  'ORI_UNIT_SHARD=1' 'ORI_UNIT_SHARD_COUNT=2' \
  'ORI_UNIT_SHARD=1 ORI_UNIT_SHARD_COUNT=3' \
  'RUNNER_OS=macOS ORI_UNIT_SHARD=1 ORI_UNIT_SHARD_COUNT=2'; do
  # Settings are fixed literals above, not a shell program or caller input.
  read -r -a settings_args <<< "$settings"
  rm -f "$TEST_ARGUMENTS"
  if env "${settings_args[@]}" "$runner" > "$root/invalid-shard.log" 2>&1; then
    printf 'Invalid shard accepted: %s\n' "$settings" >&2
    exit 1
  fi
  [[ ! -e "$TEST_ARGUMENTS" ]] || { printf 'Invalid shard ran tests\n' >&2; exit 1; }
done

# A local cap on packages run at once reaches go test and the recorded flags.
ORI_UNIT_TEST_PARALLEL=3 check_case parallel 0 0 0
grep -A1 -x -- '-p' "$TEST_ARGUMENTS" | grep -qx '3' || { printf 'ORI_UNIT_TEST_PARALLEL did not reach go test\n' >&2; exit 1; }
for flag in -json -short -race -timeout 30m -count=1 -covermode=atomic; do
  grep -qx -- "$flag" "$TEST_ARGUMENTS"
done
grep -qx 'flags=-json -short -race -timeout 30m -count=1 -covermode=atomic -p 3' "$root/artifacts"/ori-unit.*/metadata.txt
for parallel in 0 -2 two 1.5; do
  rm -f "$TEST_ARGUMENTS"
  if ORI_UNIT_TEST_PARALLEL="$parallel" "$runner" > "$root/invalid-parallel.log" 2>&1; then
    printf 'Invalid ORI_UNIT_TEST_PARALLEL accepted: %s\n' "$parallel" >&2
    exit 1
  fi
  [[ ! -e "$TEST_ARGUMENTS" ]] || { printf 'Invalid ORI_UNIT_TEST_PARALLEL ran tests\n' >&2; exit 1; }
done

# Reusing Go's test cache drops exactly -count=1, from the recorded flags too;
# 0 is the fresh run CI makes, and anything else is refused before any test.
ORI_UNIT_TEST_CACHE=1 check_case cache 0 0 0
if grep -qx -- '-count=1' "$TEST_ARGUMENTS"; then printf 'ORI_UNIT_TEST_CACHE=1 still forced a fresh run\n' >&2; exit 1; fi
for flag in -json -short -race -timeout 30m -covermode=atomic; do
  grep -qx -- "$flag" "$TEST_ARGUMENTS"
done
grep -qx 'flags=-json -short -race -timeout 30m -covermode=atomic' "$root/artifacts"/ori-unit.*/metadata.txt
ORI_UNIT_TEST_CACHE=0 check_case fresh 0 0 0
grep -qx -- '-count=1' "$TEST_ARGUMENTS"
for cache in 2 yes true; do
  rm -f "$TEST_ARGUMENTS"
  if ORI_UNIT_TEST_CACHE="$cache" "$runner" > "$root/invalid-cache.log" 2>&1; then
    printf 'Invalid ORI_UNIT_TEST_CACHE accepted: %s\n' "$cache" >&2
    exit 1
  fi
  [[ ! -e "$TEST_ARGUMENTS" ]] || { printf 'Invalid ORI_UNIT_TEST_CACHE ran tests\n' >&2; exit 1; }
done

WAIT_FOR_SIGNAL=1 "$runner" > "$root/cancel.log" 2>&1 &
parent=$!
for ((i=0; i<100; i++)); do
  [[ ! -s "$TEST_CHILD_PID" ]] || break
  sleep 0.05
done
[[ -s "$TEST_CHILD_PID" ]] || { kill -TERM "$parent"; wait "$parent" || true; exit 1; }
IFS= read -r child < "$TEST_CHILD_PID"
kill -TERM "$parent"
status=0
wait "$parent" || status=$?
[[ "$status" -eq 143 ]]
if kill -0 "$child" 2>/dev/null; then printf 'Leaked test command after cancellation\n' >&2; exit 1; fi
grep -q 'Test command exit status: 143' "$root/cancel.log"
# JSON files contain only child events, never sandbox cleanup/pruning chatter.
for file in "$root/artifacts"/ori-unit.*/events.json; do
  if grep -q 'Cache pruning\|Test sandbox' "$file"; then exit 1; fi
done
printf 'unit wrapper: scope, run order, flags, cache switch, owned artifacts, failures and cancellation passed\n'
