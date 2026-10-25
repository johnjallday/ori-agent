#!/usr/bin/env bash
# The in-place plugin update's real-server browser acceptance: the Plugins
# journey, the Home journey, the recovery cases and the ordinary-update
# regressions, each against its own
# freshly started isolated server (scripts/e2e-fresh.sh), with the Ori-owned
# disposable packages in tests/fixtures/in-place-upgrade.mjs. Nothing touches
# a real plugin store, a network release or the owner's own Ori.
#
# Usage: ./scripts/e2e-in-place-updates.sh [spec ...] [-- playwright args]
#   ORI_IN_PLACE_UPGRADE_PORT  port for the servers (default 8952; must be free)
set -euo pipefail
cd "$(dirname "$0")/.."
specs=()
while [[ $# -gt 0 && "$1" != -- ]]; do
  specs+=("$1")
  shift
done
if [[ ${#specs[@]} -eq 0 ]]; then
  specs=(
    tests/plugin-in-place-updates.spec.ts
    tests/plugin-in-place-updates-home.spec.ts
    tests/plugin-in-place-updates-recovery.spec.ts
    tests/plugin-in-place-updates-ordinary.spec.ts
  )
fi
[[ $# -eq 0 ]] || shift
exec ./scripts/e2e-fresh.sh --port "${ORI_IN_PLACE_UPGRADE_PORT:-8952}" \
  --sandbox-env ORI_IN_PLACE_UPGRADE_SANDBOX "${specs[@]}" -- --workers=1 "$@"
