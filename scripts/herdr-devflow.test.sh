#!/usr/bin/env bash
# Lightweight shell coverage for the thin entrypoint. Deeper behavior lives in
# Go tests with an injected Herdr command runner.
set -euo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

bash -n "$repo_root/scripts/herdr-devflow.sh"
if rg -n 'Worktree(Create|Remove)|"worktree", "(create|remove)"' "$repo_root/tools/herdr-devflow" --glob '*.go'; then
  printf 'Herdr Devflow must never create or remove Ori Git worktrees through Herdr.\n' >&2
  exit 1
fi
go test ./tools/herdr-devflow/...
