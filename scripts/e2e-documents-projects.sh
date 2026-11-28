#!/usr/bin/env bash
# One disposable Documents Manager, always outside protected app data. --seed
# leaves it at the starting line for an independent tester; --live exercises
# the paid syllabus read and requires explicitly supplied Claude credentials.
set -euo pipefail
cd "$(dirname "$0")/.."
export ORI_DEMO_SPLIT_PROFILE=1
unset ORI_DISCOVERY_MODEL ORI_DISCOVERY_SEED_ONLY
case "${1:-}" in
  '') ;;
  --seed) : "${ANTHROPIC_API_KEY:?Supply Claude credentials with with-keys to configure the demo model}"; export ORI_DISCOVERY_SEED_ONLY=1 ORI_DISCOVERY_MODEL="${ORI_COURSE_MODEL:-claude-haiku-4-5}" ;;
  --live) : "${ANTHROPIC_API_KEY:?Supply Claude credentials with with-keys}"; export ORI_DISCOVERY_MODEL="${ORI_COURSE_MODEL:-claude-haiku-4-5}" ;;
  *) echo "usage: $0 [--seed|--live]" >&2; exit 2 ;;
esac
exec ./scripts/e2e-fresh.sh --port "${ORI_COURSE_PORT:-19367}" --sandbox-env ORI_DISCOVERY_SANDBOX tests/documents-project-discovery.spec.ts -- --workers=1
