#!/usr/bin/env bash
# Isolated real-host planning with a loopback scripted model; not live-model evidence.
# Default: PA. 'home' uses a synthetic package; 'music --source DIR' exports
# an exact clean Music candidate into the test sandbox (never the live store).
set -euo pipefail
cd "$(dirname "$0")/.."
suite="${1:-pa}"
extra_env=()
case "$suite" in
  pa|home)
    [[ $# -le 1 ]] || { echo 'Unexpected arguments' >&2; exit 2; }
    spec=tests/personal-assistant-arrangement.spec.ts
    [[ "$suite" != home ]] || spec=tests/home-arrangement.spec.ts
    ;;
  music)
    [[ $# -eq 3 && "$2" == --source ]] || { echo "Usage: $0 music --source DIR" >&2; exit 2; }
    source_root="$(cd -- "$3" && pwd -P)"
    [[ "$(git -C "$source_root" rev-parse --show-toplevel)" == "$source_root" ]] || { echo 'Source must be a Git worktree root' >&2; exit 2; }
    [[ -z "$(git -C "$source_root" status --porcelain --untracked-files=all)" ]] || { echo 'Music source must be clean' >&2; exit 2; }
    revision="$(git -C "$source_root" rev-parse HEAD)"
    tree="$(git -C "$source_root" rev-parse 'HEAD^{tree}')"
    printf 'Music candidate: %s\nTree: %s\n' "$revision" "$tree"
    extra_env=(--env "ORI_ARRANGEMENT_MUSIC_SOURCE=$source_root" --env "ORI_ARRANGEMENT_MUSIC_REVISION=$revision" --env "ORI_ARRANGEMENT_MUSIC_TREE=$tree")
    spec=tests/home-arrangement.spec.ts
    ;;
  *) echo "Usage: $0 [pa|home|music --source DIR]" >&2; exit 2 ;;
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
  ${extra_env[@]+"${extra_env[@]}"} "$spec" -- --workers=1 --reporter=list \
  --output="tasks/evidence-agentic-workspace-arrangement/$suite-arrangement-browser"
