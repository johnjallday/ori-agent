#!/usr/bin/env bash
# Lightweight shell coverage for the thin entrypoint. Deeper behavior lives in
# Go tests with an injected Herdr command runner.
set -euo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

bash -n "$repo_root/scripts/herdr-devflow.sh"
source_help="$(HERDR_DEVFLOW_USE_SOURCE=1 bash "$repo_root/scripts/herdr-devflow.sh" help)"
if [[ "$source_help" != *"Ori Herdr Devflow bridge"* ]]; then
  printf 'Herdr Devflow source fallback did not pass the helper command arguments correctly.\n' >&2
  exit 1
fi
if rg -n 'Worktree(Create|Remove)|"worktree", "(create|remove)"' "$repo_root/tools/herdr-devflow" --glob '*.go'; then
  printf 'Herdr Devflow must never create or remove Ori Git worktrees through Herdr.\n' >&2
  exit 1
fi
if rg -n -e '\beval\b' -e 'exec\.Command(Context)?\([^\n]*("|`)(sh|bash|zsh)("|`)' "$repo_root/tools/herdr-devflow" --glob '*.go'; then
  printf 'Herdr Devflow must pass arguments directly and must not evaluate shell command strings.\n' >&2
  exit 1
fi
go test ./tools/herdr-devflow/...
