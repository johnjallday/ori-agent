#!/usr/bin/env bash
# One-card no-model/provider/Adjust phases on fresh hosts, reviewed packages and a loopback
# scripted provider. No signed-in/paid model or real user folder is used.
set -euo pipefail
cd "$(dirname "$0")/.."
model_return_only=0
if [[ "${1:-}" == --model-return-only && $# -eq 1 ]]; then
  model_return_only=1
elif [[ $# -ne 0 ]]; then
  echo "Usage: $0 [--model-return-only]" >&2; exit 2
fi
app_port="${ORI_ONE_CARD_APP_PORT:-8972}"
model_port="${ORI_ONE_CARD_SCRIPTED_PORT:-18978}"
for port in "$app_port" "$model_port"; do
  [[ "$port" =~ ^[0-9]+$ ]] && (( port > 0 && port <= 65535 )) || exit 2
  if lsof -nP -t -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "Port $port is occupied; refusing to contact it." >&2
    exit 2
  fi
done
unset CODEX_HOME OPENAI_API_KEY ANTHROPIC_API_KEY GEMINI_API_KEY
export ORI_DEMO_NO_CODEX=1
export LM_STUDIO_BASE_URL="http://127.0.0.1:$model_port/v1"
if [[ "$model_return_only" == 0 ]]; then
for phase in no-model provider adjust; do
  ./scripts/e2e-fresh.sh --port "$app_port" --sandbox-env ORI_ONE_CARD_SANDBOX \
    --env "ORI_ONE_CARD_PHASE=$phase" --env "ORI_ONE_CARD_SCRIPTED_PORT=$model_port" \
    --env ORI_ONE_CARD_PROVIDER=lmstudio --env ORI_ONE_CARD_MODEL=one-card-fixture \
    tests/one-card-folder-setup.spec.ts -- --project=chromium --workers=1
done
fi
./scripts/e2e-fresh.sh --port "$app_port" --sandbox-env ORI_READINESS_MODEL_SANDBOX \
  --env "ORI_READINESS_MODEL_PORT=$model_port" --env ORI_READINESS_MODEL_INSTALL=1 \
  tests/folder-readiness-model-return.spec.ts -- --project=chromium --workers=1
