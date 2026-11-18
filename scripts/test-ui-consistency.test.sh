#!/usr/bin/env bash
# Fake-only batching/argument contracts; no server, browser or credentials.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/ori-ui-batches.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/scripts" "$tmp/tests"
cp "$root/scripts/test-ui-consistency.sh" "$tmp/scripts/test-ui-consistency.sh"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'for arg in "$@"; do printf "<%s>" "$arg"; done >> "$CALL_LOG"' \
  'printf "\n" >> "$CALL_LOG"' \
  'for arg in "$@"; do if [[ -n "${FAIL_SPEC:-}" && "$arg" == "${FAIL_SPEC:-}" ]]; then exit 17; fi; done' \
  > "$tmp/scripts/e2e.sh"
chmod 750 "$tmp/scripts/e2e.sh"
touch "$tmp/tests/ui-consistency-components.spec.ts" \
  "$tmp/tests/ui-consistency-utilities-alpha.spec.ts" \
  "$tmp/tests/ui-consistency-utilities-beta.spec.ts"
export CALL_LOG="$tmp/calls"

bash "$tmp/scripts/test-ui-consistency.sh" --utilities --port 8943 --env 'ORI_UI_SANDBOX=/owned sandbox' -- --project=webkit >/dev/null
printf '%s\n' \
  '<--port><8943><--env><ORI_UI_SANDBOX=/owned sandbox><tests/ui-consistency-utilities-alpha.spec.ts><--><--config=playwright.ui-consistency.config.ts><--project=webkit>' \
  '<--port><8943><--env><ORI_UI_SANDBOX=/owned sandbox><tests/ui-consistency-utilities-beta.spec.ts><--><--config=playwright.ui-consistency.config.ts><--project=webkit>' \
  > "$tmp/expected"
cmp "$tmp/expected" "$CALL_LOG"

: > "$CALL_LOG"
bash "$tmp/scripts/test-ui-consistency.sh" >/dev/null
printf '%s\n' \
  '<tests/ui-consistency-components.spec.ts><--><--config=playwright.ui-consistency.config.ts>' \
  '<tests/ui-consistency-utilities-alpha.spec.ts><--><--config=playwright.ui-consistency.config.ts>' \
  '<tests/ui-consistency-utilities-beta.spec.ts><--><--config=playwright.ui-consistency.config.ts>' \
  > "$tmp/expected"
cmp "$tmp/expected" "$CALL_LOG"

: > "$CALL_LOG"
status=0
FAIL_SPEC=tests/ui-consistency-utilities-alpha.spec.ts bash "$tmp/scripts/test-ui-consistency.sh" --utilities >/dev/null || status=$?
[[ "$status" -eq 17 ]]
printf '%s\n' '<tests/ui-consistency-utilities-alpha.spec.ts><--><--config=playwright.ui-consistency.config.ts>' > "$tmp/expected"
cmp "$tmp/expected" "$CALL_LOG"
printf 'UI batching contracts: selection, quoted arguments, empty options and failure propagation passed.\n'
