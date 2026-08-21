#!/usr/bin/env bash

# Owns only the build/server/capture process lifecycle. Versioned Node helpers
# own metadata, image processing, reports, and eventual acceptance logic.
set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
readonly STAGING_PARENT="${REPO_ROOT}/test-results/readme-refresh"
readonly METADATA_HELPER="${REPO_ROOT}/scripts/readme/run-metadata.mjs"
readonly PORT_HELPER="${REPO_ROOT}/scripts/readme/free-port.mjs"
readonly REPORT_HELPER="${REPO_ROOT}/scripts/readme/report.mjs"

RUN_ID=""
RUN_DIR=""
SANDBOX=""
SERVER_PID=""
FINAL_STATUS="failed"
METADATA_FINALIZED=0
GO_ROOT=""
GO_MODULE_CACHE=""

print_safe_retry() {
  if [[ -n "${RUN_ID}" ]]; then
    printf 'Safe retry: bash scripts/readme-refresh.sh cleanup --run-id %q && bash scripts/readme-refresh.sh capture --run-id %q\n' "${RUN_ID}" "${RUN_ID}" >&2
  else
    printf 'Safe retry: make readme-capture\n' >&2
  fi
}

fail() {
  printf 'README capture error: %s\n' "$*" >&2
  print_safe_retry
  exit 2
}

usage() {
  cat <<'EOF'
Usage:
  scripts/readme-refresh.sh capture [--run-id ID] [--port PORT] [--driver REPO_RELATIVE_PATH]
  scripts/readme-refresh.sh cleanup --run-id ID

Capture builds the current worktree, starts one isolated local server, and writes
only staged artifacts under test-results/readme-refresh/<run-id>/. It never
modifies README.md or docs/images/.
EOF
}

assert_repo_root() {
  [[ -f "${REPO_ROOT}/README.md" && -e "${REPO_ROOT}/.git" ]] || fail "repository root was not resolved safely"
  [[ -f "${METADATA_HELPER}" && -f "${PORT_HELPER}" && -f "${REPORT_HELPER}" ]] || fail "required README helpers are missing"
}

assert_run_id() {
  local candidate="$1"
  [[ "${candidate}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$ && "${candidate}" != *..* ]] || fail "unsafe run ID: ${candidate}"
}

assert_safe_port() {
  local candidate="$1"
  [[ "${candidate}" =~ ^[0-9]+$ ]] || fail "port must be a number"
  (( candidate >= 1024 && candidate <= 65535 )) || fail "port must be between 1024 and 65535"
}

resolve_repo_file() {
  local candidate="$1"
  local absolute
  [[ "${candidate}" != /* ]] || fail "driver path must be repository-relative"
  absolute="$(cd "$(dirname "${REPO_ROOT}/${candidate}")" 2>/dev/null && pwd)/$(basename "${candidate}")" || fail "driver path does not exist: ${candidate}"
  [[ "${absolute}" == "${REPO_ROOT}/"* && -f "${absolute}" ]] || fail "driver must be a file inside this repository"
  printf '%s\n' "${absolute}"
}

stop_exact_server() {
  if [[ -z "${SERVER_PID}" ]]; then
    return
  fi
  if kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  SERVER_PID=""
}

finish_capture() {
  local exit_status=$?
  trap - EXIT INT TERM
  stop_exact_server
  if [[ "${METADATA_FINALIZED}" != "1" && -n "${RUN_DIR}" && -f "${RUN_DIR}/run.json" ]]; then
    node "${METADATA_HELPER}" finalize \
      --repo-root "${REPO_ROOT}" \
      --run-id "${RUN_ID}" \
      --run-dir "${RUN_DIR}" \
      --status "${FINAL_STATUS}" || true
  fi
  exit "${exit_status}"
}

capture() {
  local requested_port=""
  local driver="scripts/readme/capture-driver.mjs"
  local server_binary=""
  local playwright_browsers_path=""
  if printenv PLAYWRIGHT_BROWSERS_PATH >/dev/null 2>&1; then
    playwright_browsers_path="$(printenv PLAYWRIGHT_BROWSERS_PATH)"
  fi
  while (( $# > 0 )); do
    case "$1" in
      --run-id) RUN_ID="${2:-}"; shift 2 ;;
      --port) requested_port="${2:-}"; shift 2 ;;
      --driver) driver="${2:-}"; shift 2 ;;
      -h|--help) usage; return ;;
      *) fail "unknown capture option: $1" ;;
    esac
  done

  assert_repo_root
  if [[ -z "$playwright_browsers_path" ]]; then
    playwright_browsers_path="$(
      cd "$REPO_ROOT"
      node --input-type=module -e '
        import { chromium } from "playwright";
        import path from "node:path";
        let current = chromium.executablePath();
        while (path.basename(current) !== path.parse(current).root && !path.basename(current).startsWith("chromium-")) {
          current = path.dirname(current);
        }
        if (!path.basename(current).startsWith("chromium-")) {
          throw new Error("Could not locate the Chromium cache.");
        }
        process.stdout.write(path.dirname(current));
      '
    )" || fail "could not resolve the installed Playwright Chromium cache"
  fi
  [[ -d "$playwright_browsers_path" ]] || fail "the Playwright Chromium cache is unavailable: $playwright_browsers_path"
  if [[ -z "${RUN_ID}" ]]; then
    RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$(git -C "${REPO_ROOT}" rev-parse --short HEAD)-$$"
  fi
  assert_run_id "${RUN_ID}"
  if [[ -n "${requested_port}" ]]; then
    assert_safe_port "${requested_port}"
  else
    requested_port="$(node "${PORT_HELPER}")" || fail "could not choose a free local port"
  fi
  local driver_path
  driver_path="$(resolve_repo_file "${driver}")"

  RUN_DIR="${STAGING_PARENT}/${RUN_ID}"
  [[ ! -e "${RUN_DIR}" ]] || fail "staged run already exists: ${RUN_DIR}"
  mkdir -p "${RUN_DIR}"/{logs,raw,proposed,sidecars,comparison}
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/ori-readme-capture.XXXXXX")" || fail "could not create isolated sandbox"
  [[ -d "${SANDBOX}" && "${SANDBOX}" != / && "${SANDBOX}" != "${HOME:-}" ]] || fail "unsafe sandbox path"

  node "${METADATA_HELPER}" init \
    --repo-root "${REPO_ROOT}" \
    --run-id "${RUN_ID}" \
    --run-dir "${RUN_DIR}" \
    --sandbox "${SANDBOX}" \
    --port "${requested_port}" \
    --log-path "${RUN_DIR}/logs/server.log" \
    --driver "${driver#${REPO_ROOT}/}"

  trap finish_capture EXIT
  trap 'exit 130' INT TERM

  printf 'Building README capture server for run %s...\n' "${RUN_ID}"
  if [[ -n "${README_CAPTURE_TEST_SERVER_BINARY:-}" ]]; then
    [[ "${README_CAPTURE_TEST_MODE:-}" == "1" ]] || fail "test server override requires README_CAPTURE_TEST_MODE=1"
    server_binary="${README_CAPTURE_TEST_SERVER_BINARY}"
    [[ -x "${server_binary}" ]] || fail "test server override is not executable"
    printf 'Test-only server override: %s\n' "${server_binary}" >"${RUN_DIR}/logs/build.log"
  else
    GO_ROOT="${README_CAPTURE_GOROOT:-$(go env GOROOT)}" || fail "could not resolve the active Go toolchain"
    GO_MODULE_CACHE="${README_CAPTURE_GOMODCACHE:-$(go env GOMODCACHE)}" || fail "could not resolve the Go module cache"
    [[ -x "${GO_ROOT}/bin/go" ]] || fail "the active Go toolchain is unavailable at ${GO_ROOT}/bin/go"
    mkdir -p "${GO_MODULE_CACHE}" || fail "could not create the Go module cache directory: ${GO_MODULE_CACHE}"
    server_binary="${RUN_DIR}/ori-agent"
    (
      cd "${REPO_ROOT}"
      GOROOT="${GO_ROOT}" \
        GOTOOLCHAIN=local \
        GOMODCACHE="${GO_MODULE_CACHE}" \
        GOCACHE="${RUN_DIR}/go-build-cache" \
        "${GO_ROOT}/bin/go" build -o "${server_binary}" ./cmd/server
    ) >"${RUN_DIR}/logs/build.log" 2>&1 || {
      tail -n 80 "${RUN_DIR}/logs/build.log" >&2 || true
      fail "server build failed; see ${RUN_DIR}/logs/build.log"
    }
  fi

  (
    cd "${SANDBOX}"
    exec env -i \
      PATH="${PATH}" \
      HOME="${SANDBOX}" \
      ORI_DATA_DIR="${SANDBOX}/ori-data" \
      PORT="${requested_port}" \
      NO_BROWSER=1 \
      LANG=C.UTF-8 \
      LC_ALL=C.UTF-8 \
      TZ=UTC \
      "${server_binary}" --no-browser
  ) >"${RUN_DIR}/logs/server.log" 2>&1 &
  SERVER_PID=$!
  node "${METADATA_HELPER}" record-pid --run-dir "${RUN_DIR}" --pid "${SERVER_PID}"

  local attempt health_url
  health_url="http://127.0.0.1:${requested_port}/health"
  for attempt in $(seq 1 40); do
    if curl --fail --silent --show-error --max-time 1 "${health_url}" >"${RUN_DIR}/logs/health.json" 2>/dev/null; then
      break
    fi
    if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
      tail -n 80 "${RUN_DIR}/logs/server.log" >&2 || true
      fail "server exited before it became healthy; see ${RUN_DIR}/logs/server.log"
    fi
    sleep 0.25
  done
  if [[ ! -s "${RUN_DIR}/logs/health.json" ]]; then
    tail -n 80 "${RUN_DIR}/logs/server.log" >&2 || true
    fail "server did not become healthy within 10 seconds; see ${RUN_DIR}/logs/server.log"
  fi

  # Seed the "Workspace Command" README scene's backing workspace once, here,
  # before the capture driver's two Playwright passes. The page route resolves
  # `/workspaces/<slug>` against the real workspace store (folder-slug
  # cutover), so that scene needs one real workspace to exist -- see
  # tests/readme-capture.spec.ts. Seeding it eagerly (rather than letting the
  # first Playwright pass create it lazily) keeps the several-second blank-
  # workspace creation cost off of the "first" run only: both passes then find
  # the same already-seeded workspace at the same (fast) speed, so the
  # determinism check (assertRepeatable) isn't skewed by one pass paying that
  # cost and the other not. Skipped under the lifecycle-test server override
  # (scripts/readme/refresh-runtime.test.mjs): that fixture server has no
  # /api/workspaces route -- it stands in for orchestration tests only, never
  # runs the real Playwright capture, and doesn't need this workspace to exist.
  if [[ -z "${README_CAPTURE_TEST_SERVER_BINARY:-}" ]]; then
    local seed_url="http://127.0.0.1:${requested_port}/api/workspaces"
    if ! curl --fail --silent --show-error --max-time 15 \
      -H 'Content-Type: application/json' \
      -d '{"name":"Ws Product Launch","blank":true}' \
      "${seed_url}" >"${RUN_DIR}/logs/seed-workspace.json" 2>"${RUN_DIR}/logs/seed-workspace.log"; then
      cat "${RUN_DIR}/logs/seed-workspace.log" >&2 || true
      fail "could not seed the Workspace Command scene's workspace; see ${RUN_DIR}/logs/seed-workspace.log"
    fi
  fi

  if ! env -i \
    PATH="${PATH}" \
    HOME="${SANDBOX}" \
    ORI_DATA_DIR="${SANDBOX}/ori-data" \
    PLAYWRIGHT_BROWSERS_PATH="${playwright_browsers_path}" \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    TZ=UTC \
    node "${driver_path}" \
      --base-url "http://127.0.0.1:${requested_port}" \
      --run-dir "${RUN_DIR}" \
      --manifest "${REPO_ROOT}/docs/readme-screenshots.json"; then
    fail "capture driver failed; inspect the named scene or rule above and the preserved logs in ${RUN_DIR}/logs/"
  fi

  FINAL_STATUS="succeeded"
  if [[ "${driver}" == "scripts/readme/capture-driver.mjs" ]]; then
    node "${METADATA_HELPER}" finalize \
      --repo-root "${REPO_ROOT}" \
      --run-id "${RUN_ID}" \
      --run-dir "${RUN_DIR}" \
      --status "${FINAL_STATUS}"
    METADATA_FINALIZED=1
    if ! node "${REPORT_HELPER}" --run-dir "${RUN_DIR}" --manifest "${REPO_ROOT}/docs/readme-screenshots.json" >"${RUN_DIR}/comparison/report-path.txt"; then
      FINAL_STATUS="failed"
      METADATA_FINALIZED=0
      fail "comparison report failed; see ${RUN_DIR}/comparison/"
    fi
  fi
  printf '\nStaged README capture: %s\n' "${RUN_DIR}"
  if [[ "${driver}" == "scripts/readme/capture-driver.mjs" ]]; then
    printf 'Comparison report: %s\n' "$(cat "${RUN_DIR}/comparison/report-path.txt")"
  fi
  printf 'Tracked README files were checked before and after capture.\n'
  printf 'Next action: inspect the staged report; no tracked files were changed.\n'
}

cleanup() {
  local cleanup_id=""
  while (( $# > 0 )); do
    case "$1" in
      --run-id) cleanup_id="${2:-}"; shift 2 ;;
      -h|--help) usage; return ;;
      *) fail "unknown cleanup option: $1" ;;
    esac
  done
  assert_repo_root
  [[ -n "${cleanup_id}" ]] || fail "cleanup requires --run-id"
  assert_run_id "${cleanup_id}"
  node "${METADATA_HELPER}" cleanup --repo-root "${REPO_ROOT}" --run-id "${cleanup_id}"
  printf 'Removed staged README run %s and its validated temporary sandbox.\n' "${cleanup_id}"
}

command="${1:-}"
case "${command}" in
  capture) shift; capture "$@" ;;
  cleanup) shift; cleanup "$@" ;;
  -h|--help|'') usage ;;
  *) fail "unknown command: ${command}" ;;
esac
