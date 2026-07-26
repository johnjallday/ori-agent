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

# An optional per-feature kind override is forwarded to the initial handoff;
# omitting it above leaves the configured Claude default unchanged.
> "$fixture_root/herd-calls"
wt start bridge --kind codex >/dev/null
[[ "$(<"$fixture_root/herd-calls")" == "handoff --feature bridge --worktree $target_root --branch feature/bridge --kind codex" ]]

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
wt status --worktrees > "$fixture_root/status-output"
rg -q "worktree list" "$fixture_root/git-calls"
rg -q "bridge" "$fixture_root/status-output"

# The Git-only table must survive the dispatcher change unchanged, including
# its header columns, so existing habits and scripts keep working.
rg -q "WORKTREE" "$fixture_root/status-output"
rg -q "AHEAD" "$fixture_root/status-output"
rg -q "Compared against" "$fixture_root/status-output"

# Bare `wt status` is now the feature overview and must delegate to the helper
# rather than rendering the worktree table itself.
function wt_herd {
  print -r -- "$*" >> "$fixture_root/overview-calls"
  return 0
}

wt status > "$fixture_root/overview-output"
[[ "$(<"$fixture_root/overview-calls")" == "overview" ]]
[[ ! -s "$fixture_root/overview-output" ]]

# Every supported option is forwarded as separate words, and a feature slug is
# never concatenated into a single argument or passed through eval.
> "$fixture_root/overview-calls"
wt status --feature downloads-janitor --json --no-color > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "overview --feature downloads-janitor --json --no-color" ]]

> "$fixture_root/overview-calls"
wt status --feature=downloads-janitor > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "overview --feature downloads-janitor" ]]

# The helper's exit status is the command's exit status: an incomplete
# snapshot must not be reported to a script as success.
function wt_herd {
  return 1
}
if wt status > /dev/null 2>&1; then
  print -r -- "wt status swallowed a nonzero helper exit status" >&2
  exit 1
fi

# Invalid combinations are rejected in the shell, before the helper runs.
function wt_herd {
  print -r -- "helper must not run for invalid arguments" >> "$fixture_root/invalid-calls"
  return 0
}

for invalid_args in "--bogus" "--feature" "--worktrees --json"; do
  if wt status ${=invalid_args} > /dev/null 2>&1; then
    print -r -- "wt status accepted invalid arguments: $invalid_args" >&2
    exit 1
  fi
done
[[ ! -f "$fixture_root/invalid-calls" ]]

print -r -- "wt-herd.test.sh: ok"
