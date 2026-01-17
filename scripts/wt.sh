#!/bin/bash
# wt - worktree manager for parallel agent workflows

WORKTREE_DIR="../"  # Parent directory for worktrees
BASE_BRANCH="dev"   # Default base branch

unalias wt 2>/dev/null
function wt {
  case "$1" in
    new)
      local name="$2"
      local base="${3:-$BASE_BRANCH}"
      if [[ -z "$name" ]]; then
        echo "Usage: wt new <name> [base-branch]"
        return 1
      fi
      git worktree add -b "feature/$name" "$WORKTREE_DIR$name" "$base"
      echo "Created worktree: $WORKTREE_DIR$name (branch: feature/$name)"
      ;;
    rm)
      local name="$2"
      if [[ -z "$name" ]]; then
        echo "Usage: wt rm <name>"
        return 1
      fi
      git worktree remove "$WORKTREE_DIR$name" --force
      git branch -D "feature/$name" 2>/dev/null
      echo "Removed worktree and branch: $name"
      ;;
    ls)
      git worktree list
      ;;
    *)
      echo "Usage: wt <new|rm|ls> [args]"
      echo "  wt new <name> [base]  - Create worktree"
      echo "  wt rm <name>          - Remove worktree and branch"
      echo "  wt ls                 - List worktrees"
      ;;
  esac
}
