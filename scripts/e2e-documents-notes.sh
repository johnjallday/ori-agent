#!/usr/bin/env bash
# Each case gets a fresh HOME/data and a freshly seeded HQ. Never point these
# cases at an existing server or use a native folder picker.
set -euo pipefail
cd "$(dirname "$0")/.."
port=${ORI_DOCUMENTS_E2E_PORT:-19214}
for mode in writable setup changed vault empty; do
  args=(--env "ORI_DOCUMENTS_MODE=$mode")
  if [[ $mode == empty ]]; then args+=(--env ORI_DOCUMENTS_EMPTY=1); fi
  ./scripts/e2e-fresh.sh --port "$port" --sandbox-env ORI_HQ_FIRST_VISIT_SANDBOX "${args[@]}" \
    tests/hq-documents-to-notes.spec.ts -- --workers=1 "$@"
done
