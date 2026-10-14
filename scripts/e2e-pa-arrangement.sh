#!/usr/bin/env bash
# Isolated real-host planning with a loopback scripted model; not live-model evidence.
# Default: PA. Pass 'home' for the synthetic installed Home/Manager path.
set -euo pipefail
cd "$(dirname "$0")/.."
suite="${1:-pa}"
case "$suite" in
  pa) spec=tests/personal-assistant-arrangement.spec.ts ;;
  home) spec=tests/home-arrangement.spec.ts ;;
  *) echo "Usage: $0 [pa|home]" >&2; exit 2 ;;
esac
model_port="${ORI_ARRANGEMENT_MODEL_PORT:-18963}"
[[ "$model_port" =~ ^[0-9]+$ ]] && (( model_port > 0 && model_port <= 65535 )) || { echo 'Invalid model fixture port' >&2; exit 2; }
if lsof -nP -t -iTCP:"$model_port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Model fixture port $model_port is occupied; refusing to contact it." >&2
  exit 2
fi
export LM_STUDIO_BASE_URL="http://127.0.0.1:$model_port/v1"
exec ./scripts/e2e-fresh.sh --port "${ORI_ARRANGEMENT_APP_PORT:-8949}" \
  --sandbox-env ORI_ARRANGEMENT_SANDBOX --env "ORI_ARRANGEMENT_MODEL_PORT=$model_port" \
  "$spec" -- --workers=1 --reporter=list \
  --output="tasks/evidence-agentic-workspace-arrangement/$suite-arrangement-browser"
