#!/bin/zsh
# Verify wt's configuration survives a snapshot-restored shell, and that the
# protected-worktree guard fails closed when it does not.
#
# The bug this pins: Claude Code (and anything else that rebuilds a shell from a
# snapshot of its function definitions) restores `function` bodies but NOT
# top-level assignments. `wt` was therefore callable in an agent session while
# WORKTREE_DIR, BASE_BRANCH and PROTECTED_WORKTREES were all empty — which
# resolved new worktrees to `/<name>`, the base branch to `origin/`, and turned
# wt_is_protected_worktree into a no-op that would have let `wt rm ori-agent-dev`
# reach `git worktree remove --force` and `git branch -D dev`.
set -euo pipefail

exec < /dev/null

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
source "$repo_root/scripts/wt.sh"

# Reproduce a snapshot-restored shell: functions defined, config gone.
function simulate_snapshot_restore {
  unset WORKTREE_DIR BASE_BRANCH FEATURE_WORKTREE_PERMISSION_MODE PROTECTED_WORKTREES
}

# --- wt_config_init restores every value a snapshot drops ------------------

simulate_snapshot_restore
wt_config_init
[[ "$WORKTREE_DIR" == "../" ]]
[[ "$BASE_BRANCH" == "dev" ]]
[[ "$FEATURE_WORKTREE_PERMISSION_MODE" == "acceptEdits" ]]
(( ${#PROTECTED_WORKTREES[@]} == 2 ))

# --- calling `wt` repairs the config on its own ----------------------------
#
# The source-time call never runs in a restored shell, so `wt` itself has to be
# the one that re-asserts. Stub the dispatcher so this stays a pure config
# assertion and never shells out to git.

function wt_dispatch {
  print -r -- "dispatched:$*" >> "$TMPDIR_FIXTURE/dispatches"
  return 0
}

TMPDIR_FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/ori-wt-config.XXXXXX")"
trap 'rm -rf -- "$TMPDIR_FIXTURE"' EXIT

simulate_snapshot_restore
wt status --worktrees
[[ "$BASE_BRANCH" == "dev" ]]
[[ "$WORKTREE_DIR" == "../" ]]
(( ${#PROTECTED_WORKTREES[@]} == 2 ))
[[ -f "$TMPDIR_FIXTURE/dispatches" ]]

# --- an operator's own values are defaulted, never overwritten -------------

simulate_snapshot_restore
BASE_BRANCH="main"
WORKTREE_DIR="/tmp/elsewhere/"
wt_config_init
[[ "$BASE_BRANCH" == "main" ]]
[[ "$WORKTREE_DIR" == "/tmp/elsewhere/" ]]

# --- the removal guard fails closed on an empty list -----------------------

simulate_snapshot_restore
if ! wt_is_protected_worktree "/Users/someone/Projects/ori/worktrees/some-feature"; then
  print -r -- "FAIL: guard permitted removal while PROTECTED_WORKTREES was empty" >&2
  exit 1
fi

# --- and still discriminates correctly once initialized --------------------

wt_config_init
if ! wt_is_protected_worktree "/Users/someone/Projects/ori/worktrees/ori-agent-dev"; then
  print -r -- "FAIL: ori-agent-dev is not protected" >&2
  exit 1
fi
if ! wt_is_protected_worktree "/Users/someone/Projects/ori/ori-agent"; then
  print -r -- "FAIL: ori-agent is not protected" >&2
  exit 1
fi
if wt_is_protected_worktree "/Users/someone/Projects/ori/worktrees/some-feature"; then
  print -r -- "FAIL: an ordinary feature worktree was reported as protected" >&2
  exit 1
fi

print -r -- "wt-config.test.sh: OK"
