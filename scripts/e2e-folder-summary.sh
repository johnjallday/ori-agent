#!/usr/bin/env bash
# Real isolated host + reviewed packages + a loopback scripted provider.
# No paid/live model and no user's HOME, plugins or workspace state are used.
set -euo pipefail
cd "$(dirname "$0")/.."
evidence="$PWD/test-results/folder-summary-evidence"
upgrade=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --evidence) [[ -n "${2:-}" ]] || { echo 'Missing evidence directory' >&2; exit 2; }; evidence="$2"; shift 2 ;;
    --upgrade) upgrade=1; shift ;;
    *) echo "Usage: $0 [--upgrade] [--evidence DIR]" >&2; exit 2 ;;
  esac
done
model_port="${ORI_FOLDER_SUMMARY_MODEL_PORT:-18974}"
app_port="${ORI_FOLDER_SUMMARY_APP_PORT:-8955}"
for port in "$model_port" "$app_port"; do
  [[ "$port" =~ ^[0-9]+$ ]] && (( port > 0 && port <= 65535 )) || { echo 'Invalid fixture port' >&2; exit 2; }
  if lsof -nP -t -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "Fixture port $port is occupied; refusing to contact it." >&2
    exit 2
  fi
done
unset CODEX_HOME OPENAI_API_KEY ANTHROPIC_API_KEY GEMINI_API_KEY
export ORI_DEMO_NO_CODEX=1
export LM_STUDIO_BASE_URL="http://127.0.0.1:$model_port/v1"
exec ./scripts/e2e-fresh.sh --port "$app_port" --sandbox-env ORI_FOLDER_SUMMARY_SANDBOX \
  --env "ORI_FOLDER_SUMMARY_MODEL_PORT=$model_port" --env "ORI_FOLDER_SUMMARY_EVIDENCE=$evidence" \
  --env "ORI_FOLDER_SUMMARY_UPGRADE=$upgrade" \
  tests/folder-optional-summary.spec.ts -- --project=chromium --workers=1
