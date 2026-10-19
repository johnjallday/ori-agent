#!/usr/bin/env bash
#
# external-memory-demo.sh — serve the current worktree from an isolated demo
# sandbox whose Codex, Claude Code and Gemini CLI homes hold the anonymized
# external-memory fixtures, never the user's real memory.
#
# Usage:
#   ./scripts/external-memory-demo.sh [port] [sandbox_dir]
#
# It copies internal/externalmemory/testdata into <sandbox>/fixtures (Codex
# under fixtures/codex, Claude Code under fixtures/claude, the account paste
# text at fixtures/account-export.md), places a Gemini home at
# <sandbox>/.gemini with one listed project, and then runs
# scripts/demo-server.sh with CODEX_HOME and CLAUDE_CONFIG_DIR pointing at the
# fixtures. Every source still starts Off: switch them on in Profile >
# Connected memory.
#
# CODEX_HOME doubles as the Codex CLI's login home, so the Codex model
# provider is not signed in on this server. For a real assistant turn, set a
# system model such as openai/gpt-5-mini (needs OPENAI_API_KEY in the
# environment). A Codex-provider turn would read the user's real Codex memory
# natively, which these demos must never capture.
set -euo pipefail

port="${1:-8931}"
sandbox="${2:-${TMPDIR:-/tmp}/ori-demo.external-memory}"
repo="$(cd "$(dirname "$0")/.." && pwd)"
testdata="$repo/internal/externalmemory/testdata"

mkdir -p "$sandbox/fixtures" "$sandbox/.gemini" "$sandbox/Projects/chart-lab"
rm -rf "$sandbox/fixtures/codex" "$sandbox/fixtures/claude"
cp -R "$testdata/codex" "$sandbox/fixtures/codex"
cp -R "$testdata/claude_code" "$sandbox/fixtures/claude"
cp "$testdata/account/export.md" "$sandbox/fixtures/account-export.md"
cp "$testdata/gemini/GEMINI.md" "$sandbox/.gemini/GEMINI.md"
printf -- '- Run the study scripts with the frozen data hash.\n- 수수료는 0.6%%로 계산한다.\n' >"$sandbox/Projects/chart-lab/GEMINI.md"
printf '{"projects": {"%s": "chart-lab"}}\n' "$sandbox/Projects/chart-lab" >"$sandbox/.gemini/projects.json"

echo "FIXTURES=$sandbox/fixtures"
export CODEX_HOME="$sandbox/fixtures/codex"
export CLAUDE_CONFIG_DIR="$sandbox/fixtures/claude"
exec "$repo/scripts/demo-server.sh" "$port" "$sandbox"
