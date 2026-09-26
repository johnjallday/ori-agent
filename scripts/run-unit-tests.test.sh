#!/usr/bin/env bash
set -euo pipefail
umask 077
script_dir="$(cd "$(dirname "$0")" && pwd -P)"
root="$(mktemp -d "${TMPDIR:-/tmp}/ori-unit-wrapper-test.XXXXXX")"
trap 'rm -rf -- "$root"' EXIT
mkdir -p "$root/repo/scripts" "$root/bin" "$root/artifacts" "$root/tmp"
cp "$script_dir/run-unit-tests.sh" "$script_dir/run-test-command.sh" "$script_dir/prune-test-cache.sh" "$script_dir/list-unit-packages.sh" "$root/repo/scripts/"
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
    if [[ "$*" == *IgnoredGoFiles* ]]; then
      printf 'github.com/johnjallday/ori-agent/internal/platform\n'
    else
      printf 'github.com/johnjallday/ori-agent/internal/one\ngithub.com/johnjallday/ori-agent/internal/two\n'
    fi
    ;;
  version) printf 'go version go1.26.0 fixture/fixture\n' ;;
  test)
    printf '%s\n' "$@" > "$TEST_ARGUMENTS"
    printf '%s\n' "$ORI_TEST_RUN_DIR" > "$TEST_SANDBOX"
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
export TEST_ARGUMENTS="$root/arguments" TEST_SANDBOX="$root/sandbox" TEST_CHILD_PID="$root/child-pid"
export GITHUB_OUTPUT="$root/outputs" GITHUB_STEP_SUMMARY="$root/summary"
runner="$root/repo/scripts/run-unit-tests.sh"

check_case() {
  local name="$1" test_status="$2" report_status="$3" expected="$4" status=0
  TEST_STATUS="$test_status" REPORT_STATUS="$report_status" "$runner" > "$root/$name.log" 2>&1 || status=$?
  [[ "$status" -eq "$expected" ]] || { printf '%s: status %s, expected %s\n' "$name" "$status" "$expected" >&2; exit 1; }
  grep -q 'Fixture timing summary' "$root/$name.log"
  local sandbox
  IFS= read -r sandbox < "$TEST_SANDBOX"
  [[ ! -e "$sandbox" ]] || { printf 'Leaked test sandbox\n' >&2; exit 1; }
}
check_case success 0 0 0
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
grep -qx 'github.com/johnjallday/ori-agent/internal/one' "$TEST_ARGUMENTS"
RUNNER_OS=macOS check_case platform 0 0 0
grep -qx 'github.com/johnjallday/ori-agent/internal/platform' "$TEST_ARGUMENTS"
grep -qx 'github.com/johnjallday/ori-agent/cmd/server' "$TEST_ARGUMENTS"
if grep -q 'internal/one' "$TEST_ARGUMENTS"; then exit 1; fi
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
printf 'unit wrapper: scope, flags, owned artifacts, failures and cancellation passed\n'
