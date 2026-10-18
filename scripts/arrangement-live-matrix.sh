#!/usr/bin/env bash
# Live-model behavior matrix for workspace arrangement (checklist 5.2).
#
# Runs TestArrangementLiveMatrix: the real planner, planning tools and Home
# catalog store against a REAL provider on synthetic catalog metadata, with the
# Music candidate's guidance composed the way the host does. Not a browser test
# and not scripted evidence. Every request/response lands in the evidence dir.
#
#   ./scripts/arrangement-live-matrix.sh --music-source DIR [--provider ollama] \
#       [--model gemma4:26b] [--evidence DIR] [--context-tokens 32768]
#
# ollama needs no key and spends nothing. openai / anthropic / gemini read their
# key from the environment inside the test; this script never prints one.
set -euo pipefail
cd "$(dirname "$0")/.."
provider="${ORI_ARRANGEMENT_LIVE_PROVIDER:-ollama}"
model="${ORI_ARRANGEMENT_LIVE_MODEL:-gemma4:26b}"
context_tokens="${ORI_ARRANGEMENT_LIVE_CONTEXT_TOKENS:-32768}"
source_root=""
evidence=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider) provider="${2:?}"; shift 2 ;;
    --model) model="${2:?}"; shift 2 ;;
    --music-source) source_root="${2:?}"; shift 2 ;;
    --evidence) evidence="${2:?}"; shift 2 ;;
    --context-tokens) context_tokens="${2:?}"; shift 2 ;;
    *) echo "Usage: $0 --music-source DIR [--provider ollama] [--model NAME] [--evidence DIR] [--context-tokens N]" >&2; exit 2 ;;
  esac
done
[[ -n "$source_root" ]] || { echo '--music-source DIR is required (a clean Music worktree)' >&2; exit 2; }
source_root="$(cd -- "$source_root" && pwd -P)"
[[ "$(git -C "$source_root" rev-parse --show-toplevel)" == "$source_root" ]] || { echo 'Music source must be a Git worktree root' >&2; exit 2; }
[[ -z "$(git -C "$source_root" status --porcelain --untracked-files=all)" ]] || { echo 'Music source must be clean' >&2; exit 2; }
[[ "$context_tokens" =~ ^[0-9]+$ ]] || { echo '--context-tokens must be numeric' >&2; exit 2; }
music_revision="$(git -C "$source_root" rev-parse HEAD)"
ori_revision="$(git rev-parse HEAD)"
[[ -n "$evidence" ]] || evidence="$PWD/tasks/evidence-agentic-workspace-arrangement/live-matrix-$provider"
mkdir -p "$evidence"
evidence="$(cd -- "$evidence" && pwd -P)"
if [[ "$provider" == ollama ]]; then
  base_url="${ORI_ARRANGEMENT_LIVE_BASE_URL:-http://127.0.0.1:11434}"
  tags="$(curl -fsS --max-time 5 "$base_url/api/tags")" || { echo "Ollama is not answering at $base_url" >&2; exit 2; }
  grep -Fq "\"name\":\"$model\"" <<<"$tags" || { echo "model $model is not installed in Ollama at $base_url" >&2; exit 2; }
fi
printf 'provider=%s model=%s context_tokens=%s\nmusic=%s (%s)\nori=%s\nevidence=%s\n' \
  "$provider" "$model" "$context_tokens" "$source_root" "$music_revision" "$ori_revision" "$evidence"
ORI_ARRANGEMENT_LIVE=1 ORI_ARRANGEMENT_LIVE_PROVIDER="$provider" ORI_ARRANGEMENT_LIVE_MODEL="$model" \
  ORI_ARRANGEMENT_LIVE_CONTEXT_TOKENS="$context_tokens" \
  ORI_ARRANGEMENT_LIVE_MUSIC_SOURCE="$source_root" ORI_ARRANGEMENT_LIVE_MUSIC_REVISION="$music_revision" \
  ORI_ARRANGEMENT_LIVE_ORI_REVISION="$ori_revision" ORI_ARRANGEMENT_LIVE_EVIDENCE="$evidence" \
  go test ./internal/projectlibrary/ -run '^TestArrangementLiveMatrix$' -count=1 -v -timeout 90m 2>&1 | tee "$evidence/go-test.log"
