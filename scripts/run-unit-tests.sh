#!/usr/bin/env bash
# Capture once, report once. The existing runner owns/cleans test sandboxes;
# this wrapper owns a separate, newly allocated evidence directory.
set -euo pipefail
if [[ $# -ne 0 ]]; then
  printf 'Usage: ./scripts/run-unit-tests.sh (RUNNER_OS=macOS selects platform scope)\n' >&2
  exit 2
fi
script_dir="$(cd "$(dirname "$0")" && pwd -P)"
cd "$script_dir/.."
parent="${ORI_UNIT_ARTIFACT_PARENT:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}}"
parent="$(cd "$parent" && pwd -P)"
case "$parent" in
  /|*$'\n'*|*$'\r'*) printf 'Unsafe unit artifact parent\n' >&2; exit 2 ;;
esac
artifacts="$(mktemp -d "$parent/ori-unit.XXXXXX")"
chmod 700 "$artifacts"
printf 'Unit artifacts: %q\n' "$artifacts"
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  printf 'artifact-dir=%s\n' "$artifacts" >> "$GITHUB_OUTPUT"
fi
child=""
reporter="$artifacts/testtiming"
scope="not selected"
package_count=0
reporter_build_seconds="not completed"
test_command_seconds="not run"

finish() {
  local test_status="$1" report_status=1 output_status="${2:-0}"
  if [[ -x "$reporter" && -f "$artifacts/events.json" ]]; then
    if "$reporter" < "$artifacts/events.json" > "$artifacts/summary.md" 2> "$artifacts/diagnostics.txt"; then
      report_status=0
    else
      report_status=$?
    fi
    # Reporter output is escaped; raw test/compile output is never echoed.
    cat "$artifacts/diagnostics.txt" || output_status=1
    if [[ -s "$artifacts/stderr.txt" ]]; then
      "$reporter" -stderr < "$artifacts/stderr.txt" || output_status=1
    fi
  else
    printf '## Unit tests did not complete\n\nSetup/capture exited %s. See raw artifacts.\n' "$test_status" > "$artifacts/summary.md" || output_status=1
  fi
  printf '\nSelection: %s (%s packages). Full-suite coverage comes from Ubuntu; macOS runs only the platform subset.\n' "$scope" "$package_count" >> "$artifacts/summary.md" || output_status=1
  printf '\nWall clocks: reporter build %s s; test command %s s (includes compilation). Setup/cache/save and whole job are timed by Actions step/job timers; package durations overlap.\n' "$reporter_build_seconds" "$test_command_seconds" >> "$artifacts/summary.md" || output_status=1
  printf '\nTest command exit status: %s. Reporter exit status: %s. Diagnostic/output status: %s.\n' "$test_status" "$report_status" "$output_status" >> "$artifacts/summary.md" || output_status=1
  cat "$artifacts/summary.md" || output_status=1
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    cat "$artifacts/summary.md" >> "$GITHUB_STEP_SUMMARY" || output_status=1
  fi
  if [[ "$test_status" -ne 0 ]]; then return "$test_status"; fi
  if [[ "$report_status" -ne 0 ]]; then return "$report_status"; fi
  return "$output_status"
}

cancel() {
  local status="$1"
  trap '' INT TERM
  if [[ -n "$child" ]]; then
    # Monitor mode below creates this exact owned process group; terminate
    # the runner, go command and test descendants together, never by name.
    kill -TERM -- "-$child" 2>/dev/null || true
    wait "$child" 2>/dev/null || true
    child=""
  fi
  finish "$status" || true
  exit "$status"
}
trap 'cancel 130' INT
trap 'cancel 143' TERM

run_owned() {
  local status=0
  set -m
  "$script_dir/run-test-command.sh" "$@" &
  child=$!
  wait "$child" || status=$?
  child=""
  set +m
  return "$status"
}

start=$SECONDS
if run_owned go build -o "$reporter" ./scripts/testtiming > "$artifacts/reporter-build.log" 2>&1; then
  reporter_build_seconds="$((SECONDS-start))"
  printf 'reporter_build_seconds=%s\n' "$reporter_build_seconds" > "$artifacts/metadata.txt"
else
  status=$?
  finish "$status"
  exit "$status"
fi
scope=full
selection=""
if [[ "${RUNNER_OS:-}" == macOS ]]; then scope=platform; selection=--platform; fi
if "$script_dir/list-unit-packages.sh" ${selection:+"$selection"} > "$artifacts/packages.txt" 2> "$artifacts/stderr.txt"; then
  packages=()
  while IFS= read -r package; do
    [[ -n "$package" ]] && packages+=("$package")
  done < "$artifacts/packages.txt"
else
  status=$?
  finish "$status"
  exit "$status"
fi
package_count=${#packages[@]}
if [[ "$package_count" -eq 0 ]]; then
  printf 'Unit package selection was empty\n' >> "$artifacts/stderr.txt"
  finish 2
  exit 2
fi
{
  printf 'scope=%s\nrevision=%s\n' "$scope" "$(git rev-parse HEAD)"
  go version
  printf 'flags=-json -short -race -timeout 30m -count=1 -covermode=atomic\n'
} >> "$artifacts/metadata.txt"
start=$SECONDS
test_status=0
# Capture inside the owned runner so its cleanup messages cannot corrupt JSON.
# No pipeline, tee, report failure or shell exit can replace the test status.
run_owned bash -c '
  artifacts=$1
  shift
  exec go test -json -short -race -timeout 30m -count=1 \
    -covermode=atomic -coverprofile="$artifacts/coverage.txt" "$@" \
    > "$artifacts/events.json" 2> "$artifacts/stderr.txt"
' unit-capture "$artifacts" "${packages[@]}" || test_status=$?
metadata_status=0
test_command_seconds="$((SECONDS-start))"
printf 'test_command_seconds=%s\ntest_exit=%s\n' "$test_command_seconds" "$test_status" >> "$artifacts/metadata.txt" || metadata_status=1
finish "$test_status" "$metadata_status"
