#!/usr/bin/env bash
# Run one feature spec per browser process. Long-lived macOS WebKit runs can
# stall before navigation at their 64th context; do not hide that with retries.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
utilities=0
server_args=()
playwright_args=()
while (($#)); do
  case "$1" in
    --utilities) utilities=1; shift ;;
    --help|-h)
      printf '%s\n' \
        'Usage: scripts/test-ui-consistency.sh [--utilities] [e2e options] -- [Playwright options]' \
        'Starts no server. Pass --port and --env ORI_UI_SANDBOX=<owned sandbox>.' \
        'Use --env ORI_UI_FONTS=real for the separate intended-font pass.' \
        'All specs run sequentially; each invocation retains the scoped config and its one worker.'
      exit 0 ;;
    --) shift; playwright_args=("$@"); break ;;
    *) server_args+=("$1"); shift ;;
  esac
done

shopt -s nullglob
if ((utilities)); then
  specs=(tests/ui-consistency-utilities*.spec.ts)
else
  specs=(tests/ui-consistency-*.spec.ts)
fi
((${#specs[@]})) || { printf 'No matching UI consistency specs.\n' >&2; exit 2; }
for spec in "${specs[@]}"; do
  printf '\n=== UI consistency: %s ===\n' "$spec"
  "$repo_root/scripts/e2e.sh" ${server_args[@]+"${server_args[@]}"} "$spec" -- \
    --config=playwright.ui-consistency.config.ts ${playwright_args[@]+"${playwright_args[@]}"}
done
