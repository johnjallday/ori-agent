#!/usr/bin/env bash
# Real reviewed installs and UI, no inference; every phase starts a new server.
set -euo pipefail
cd "$(dirname "$0")/.."
port="${1:-8947}"
scenario="${2:-all}"
[[ "$port" =~ ^[0-9]+$ && "$scenario" =~ ^(all|entry|continuity)$ ]] || {
  echo "usage: $0 [port] [all|entry|continuity]" >&2
  exit 2
}
# Never inherit paid-provider credentials or CLI homes into a configured demo.
unset ANTHROPIC_API_KEY GEMINI_API_KEY GOOGLE_API_KEY CODEX_HOME CLAUDE_CONFIG_DIR GOOGLE_APPLICATION_CREDENTIALS
export OPENAI_API_KEY=disposable-no-inference OPENAI_BASE_URL=http://127.0.0.1:1/v1
export OLLAMA_BASE_URL=http://127.0.0.1:9 ORI_DEMO_OPEN=0 NO_BROWSER=1
# Other worktrees may be building. Per-run cleanup remains enabled; shared Go
# cache pruning must not remove their compiler inputs between our phases.
export ORI_SKIP_CACHE_PRUNE=1
repo="$(pwd -P)"
for journey in entry continuity; do
  [[ "$scenario" == all || "$scenario" == "$journey" ]] || continue
  sandbox="$(mktemp -d "${TMPDIR:-/tmp}/ori-e2e.XXXXXX")"
  sandbox="$(cd "$sandbox" && pwd -P)"
  printf 'worktree=%s\n' "$repo" >"$sandbox/.ori-demo-owner"
  chmod 600 "$sandbox/.ori-demo-owner"
  mkdir -p "$sandbox/evidence"
  echo "Preserving $journey evidence: $sandbox"
  ./scripts/folder-collection-seed.sh --exact-albums "$sandbox" Albums
  phases=(entry-seed entry-choice entry-run entry-receipt)
  [[ "$journey" == entry ]] || phases=(entry-seed entry-choice entry-handoff repick home-seed home-reopen build-seed build-reopen)
  for phase in "${phases[@]}"; do
    ./scripts/e2e-fresh.sh --port "$port" --sandbox "$sandbox" \
      --sandbox-env ORI_FOLDER_REVIEW_CONTINUITY_SANDBOX \
      --env "ORI_FOLDER_REVIEW_CONTINUITY_PHASE=$phase" \
      --env ORI_FOLDER_REVIEW_CONTINUITY_OFFLINE_MODEL=1 \
      tests/folder-review-continuity.spec.ts -- --project=chromium --workers=1 --trace=retain-on-failure \
      --output="$sandbox/evidence/$phase-results" 2>&1 | tee "$sandbox/evidence/$phase.log"
  done
done
