#!/usr/bin/env bash
# Explicit offline pairing. Stage only candidate wrappers/project adapters into
# disposable Git repos; all outward effects are fakes, never installed tools.
set -euo pipefail
ori=""
devtools=""
fixtures_only=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ori|--devtools)
      [[ $# -ge 2 && "$2" == /* ]] || { echo "$1 requires an absolute candidate path" >&2; exit 2; }
      if [[ "$1" == --ori ]]; then ori="$2"; else devtools="$2"; fi
      shift 2 ;;
    --fixtures-only) fixtures_only=1; shift ;;
    *) echo 'Usage: bash scripts/devtools-integration.test.sh --ori PATH --devtools PATH [--fixtures-only]' >&2; exit 2 ;;
  esac
done
[[ -n "$ori" && -n "$devtools" ]] || { echo 'Both candidate paths are required' >&2; exit 2; }
ori="$(CDPATH= cd -- "$ori" && pwd -P)"
devtools="$(CDPATH= cd -- "$devtools" && pwd -P)"
[[ "$ori" != "$devtools" ]] || { echo 'Ori and tool roots must differ' >&2; exit 2; }
[[ "$(< "$devtools/devtools-contract")" == ori-devtools-v1 ]] || { echo 'Incompatible companion contract' >&2; exit 2; }
cmp "$ori/internal/workspaceplan/testdata/devtools-approved-tasks.md" \
    "$devtools/tools/herdr-devflow/internal/agents/testdata/approved-tasks.md"
(cd "$ori" && GOWORK=off GOFLAGS=-mod=readonly go test ./internal/workspaceplan -run '^TestApprovedTaskListMatchesDevtoolsFixture$' -count=1)
(cd "$devtools" && GOWORK=off GOFLAGS=-mod=readonly go test ./tools/herdr-devflow/internal/agents -run '^TestApprovedOriTaskListIsImmediatelyActionableByWtHandoff$' -count=1)
printf 'PASS: paired artifact bytes, producer, and next-actionable-task consumer\n'
if [[ "$fixtures_only" == 0 ]]; then
  (cd "$devtools" && python3 scripts/lifecycle.test.py --ori "$ori")
  printf 'PASS: exact Ori wrappers/adapters + selected companion lifecycle (fake outward effects)\n'
else
  printf 'Fixture-only: lifecycle not requested\n'
fi
