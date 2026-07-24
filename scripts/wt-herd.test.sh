#!/bin/zsh
# Verify wt start's Herdr handoff boundary without creating a real shared Git
# worktree or contacting a running Herdr server.
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-wt-herd.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

dev_root="$fixture_root/dev"
target_root="$fixture_root/feature"
mkdir -p "$dev_root/tasks" "$target_root"
print -r -- "# bridge" > "$dev_root/tasks/prd-bridge.md"
print -r -- "## Tasks" > "$dev_root/tasks/tasks-bridge.md"

source "$repo_root/scripts/wt.sh"

function wt_get_dev_worktree {
  print -r -- "$dev_root"
}

function wt_provision_worktree {
  typeset -g WT_PROVISIONED_TARGET="$target_root"
  return 0
}

function wt_herd {
  print -r -- "$*" >> "$fixture_root/herd-calls"
  return 1
}

# Opting out must copy planning artifacts and never invoke the helper.
wt start bridge --no-herdr >/dev/null
[[ -f "$target_root/tasks/prd-bridge.md" ]]
[[ -f "$target_root/tasks/tasks-bridge.md" ]]
[[ ! -f "$fixture_root/herd-calls" ]]

# A failed handoff is non-blocking: Git provisioning remains successful and
# the handoff command receives the exact feature/path/branch identity.
wt start bridge >/dev/null
[[ "$(<"$fixture_root/herd-calls")" == "handoff --feature bridge --worktree $target_root --branch feature/bridge" ]]

# A blocked Herdr cleanup guard must stop wt done before it mutates the
# backlog, archives tasks, checks dirty state, or asks Git to remove anything.
print -r -- "## Doing" > "$dev_root/BACKLOG.md"
print -r -- "- [bridge](tasks/prd-bridge.md)" >> "$dev_root/BACKLOG.md"
print -r -- "# completed bridge tasks" > "$target_root/tasks/tasks-bridge.md"
before_backlog="$(<"$dev_root/BACKLOG.md")"

function wt_resolve_worktree_path {
  print -r -- "$target_root"
}

function wt_resolve_worktree_branch {
  print -r -- "feature/bridge"
}

function wt_is_protected_worktree {
  return 1
}

function wt_herd_cleanup_preflight {
  print -r -- "$*" >> "$fixture_root/cleanup-calls"
  return 20
}

function gh {
  print -r -- "42"
}

function git {
  print -r -- "$*" >> "$fixture_root/git-calls"
  return 0
}

if wt done bridge > "$fixture_root/done-output" 2>&1; then
  print -r -- "Expected blocked Herdr cleanup to stop wt done." >&2
  exit 1
fi
[[ "$(<"$fixture_root/cleanup-calls")" == "$target_root 0" ]]
[[ "$(<"$dev_root/BACKLOG.md")" == "$before_backlog" ]]
[[ -f "$target_root/tasks/tasks-bridge.md" ]]
[[ ! -e "$fixture_root/git-calls" ]]
rg -q "managed Herdr work is still active" "$fixture_root/done-output"

# A disconnected/unknown Herdr result also fails closed in this non-interactive
# fixture, even when a caller supplied the explicit override flag.
function wt_herd_cleanup_preflight {
  print -r -- "$*" >> "$fixture_root/cleanup-calls"
  return 21
}

if wt done bridge --herdr-override > "$fixture_root/done-unavailable-output" 2>&1; then
  print -r -- "Expected unverified Herdr cleanup to fail closed without a terminal." >&2
  exit 1
fi
[[ "$(<"$dev_root/BACKLOG.md")" == "$before_backlog" ]]
[[ ! -e "$fixture_root/git-calls" ]]
rg -q "cannot be verified in a non-interactive shell" "$fixture_root/done-unavailable-output"

# The cleanup addition must not perturb the existing read-only worktree views.
function wt_load_merged_set {
  return 0
}

function wt_load_worktrees {
  typeset -ga WT_PATHS WT_BRANCHES
  WT_PATHS=("$dev_root" "$target_root")
  WT_BRANCHES=("dev" "feature/bridge")
}

function wt_branch_status {
  print -r -- "0 0 [clean]"
}

wt ls > "$fixture_root/list-output"
wt status > "$fixture_root/status-output"
rg -q "worktree list" "$fixture_root/git-calls"
rg -q "bridge" "$fixture_root/status-output"
