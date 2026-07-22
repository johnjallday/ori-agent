#!/usr/bin/env bash

# Creates the routine monthly README refresh worktree through Ori's standard
# `wt new` lifecycle. This script never removes a branch or worktree.
set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

fail() {
  printf 'README refresh worktree error: %s\n' "$*" >&2
  exit 2
}

usage() {
  cat <<'EOF'
Usage: scripts/readme-new-refresh-worktree.sh YYYY-MM

Run only from the clean dev worktree. It creates docs/readme-refresh-YYYY-MM
through `wt new`, then prints the verified target worktree path.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

month="${1:-}"
[[ "${month}" =~ ^[0-9]{4}-(0[1-9]|1[0-2])$ ]] || { usage; fail "month must use YYYY-MM"; }
[[ $# -eq 1 ]] || { usage; fail "expected one YYYY-MM argument"; }
[[ "$(git -C "${REPO_ROOT}" rev-parse --show-toplevel)" == "${REPO_ROOT}" ]] || fail "repository root was not resolved safely"
[[ "$(git -C "${REPO_ROOT}" branch --show-current)" == "dev" ]] || fail "routine refresh must start from the dev worktree"
[[ -z "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=no)" ]] || fail "dev has tracked changes; commit or revert them before creating a refresh worktree"

readonly WORKTREE_NAME="docs/readme-refresh-${month}"
readonly BRANCH_NAME="docs/readme-refresh-${month}"
if git -C "${REPO_ROOT}" show-ref --verify --quiet "refs/heads/${BRANCH_NAME}"; then
  fail "branch ${BRANCH_NAME} already exists; choose a different month or inspect the existing refresh"
fi
if git -C "${REPO_ROOT}" worktree list --porcelain | rg -q "^branch refs/heads/${BRANCH_NAME}$"; then
  fail "a worktree already uses ${BRANCH_NAME}; refusing to overwrite it"
fi

# `wt.sh` is intentionally Zsh-native. Execute the lifecycle command in Zsh
# instead of sourcing it into this Bash wrapper (where `set -e` and Zsh
# parameter expansions make the helper exit before creating the worktree).
zsh -c 'source "$1"; wt new "$2"' _ "${REPO_ROOT}/scripts/wt.sh" "${WORKTREE_NAME}"

target="$(git -C "${REPO_ROOT}" worktree list --porcelain | awk -v branch="refs/heads/${BRANCH_NAME}" '
  /^worktree / { path = substr($0, 10) }
  /^branch / && substr($0, 8) == branch { print path; exit }
')"
[[ -n "${target}" && -d "${target}" ]] || fail "wt new completed without a verified ${BRANCH_NAME} worktree"
printf 'README refresh worktree ready: %s\n' "${target}"
printf 'Next action: cd %q and run make readme-capture.\n' "${target}"
