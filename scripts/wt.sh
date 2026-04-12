#!/bin/zsh
# wt - worktree manager for parallel agent workflows
#
# Usage:
#   source scripts/wt.sh   # Load the function (cd works directly)
#   wt                     # Interactive mode - select worktree to navigate
#   wt new <name>          # Create new worktree
#   wt rm <name>           # Remove worktree
#   wt ls                  # List worktrees
#   wt cd <name>           # Navigate to worktree

WORKTREE_DIR="../"
BASE_BRANCH="dev"
PROTECTED_WORKTREES=("ori-agent" "ori-agent-dev")

unalias wt 2>/dev/null

function wt_is_protected_worktree {
  local candidate="$1"
  local candidate_name="${candidate:t}"
  local protected_name

  for protected_name in "${PROTECTED_WORKTREES[@]}"; do
    if [[ "$candidate_name" == "$protected_name" ]]; then
      return 0
    fi
  done

  return 1
}

function wt_resolve_worktree_path {
  local name="$1"
  local absolute_name="${name:A}"
  local line
  local worktree_path

  while IFS= read -r line; do
    [[ "$line" == worktree\ * ]] || continue
    worktree_path="${line#worktree }"
    if [[ "${worktree_path:t}" == "$name" || "$worktree_path" == "$name" || "$worktree_path" == "$absolute_name" ]]; then
      print -r -- "$worktree_path"
      return 0
    fi
  done < <(git worktree list --porcelain)

  print -r -- "${WORKTREE_DIR}${name}"
}

function wt {
  local script_dir="${0:A:h}"

  case "$1" in
  new)
    local name="$2"
    if [[ -z "$name" ]]; then
      echo "Usage: wt new <name>"
      return 1
    fi
    git worktree add -b "feature/$name" "$WORKTREE_DIR$name" "$BASE_BRANCH"
    echo "Created worktree: $WORKTREE_DIR$name (branch: feature/$name, based on $BASE_BRANCH)"
    ;;
  rm)
    local name="$2"
    local target_path
    if [[ -z "$name" ]]; then
      echo "Usage: wt rm <name>"
      return 1
    fi
    target_path="$(wt_resolve_worktree_path "$name")"
    if wt_is_protected_worktree "$target_path"; then
      echo "Refusing to remove protected worktree: ${target_path:t}"
      return 1
    fi
    git worktree remove "$target_path" --force
    git branch -D "feature/$name" 2>/dev/null
    echo "Removed worktree and branch: $name"
    ;;
  ls)
    git worktree list
    ;;
  cd)
    local name="$2"
    if [[ -z "$name" ]]; then
      # No name provided, show interactive menu
      wt go
      return $?
    fi
    local target="${script_dir}/../../$name"
    if [[ -d "$target" ]]; then
      cd "$target"
      echo "Changed to worktree: $target"
    else
      echo "Worktree not found: $target"
      return 1
    fi
    ;;
  ""|go)
    # Interactive mode - show menu of worktrees
    local -a worktrees
    local -a paths
    local i=1

    while IFS= read -r line; do
      # Parse: /path/to/worktree  abc1234 [branch-name]
      local parts=("${(@s/ /)line}")
      local wt_path="${parts[1]}"
      local branch="${parts[3]}"
      # Remove brackets from branch
      branch="${branch#\[}"
      branch="${branch%\]}"
      # Get basename
      local name="${wt_path:t}"
      worktrees+=("$name ($branch)")
      paths+=("$wt_path")
    done < <(git worktree list)

    if [[ ${#worktrees[@]} -eq 0 ]]; then
      echo "No worktrees found"
      return 1
    fi

    echo "Select worktree:"
    for i in {1..${#worktrees[@]}}; do
      echo "  $i) ${worktrees[$i]}"
    done
    echo "  q) Quit"
    echo

    read "choice?Choice: "

    if [[ "$choice" == "q" || -z "$choice" ]]; then
      return 0
    fi

    if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#paths[@]} )); then
      local target="${paths[$choice]}"
      cd "$target"
      echo "Changed to: $target"
    else
      echo "Invalid choice"
      return 1
    fi
    ;;
  *)
    echo "Usage: wt [command] [args]"
    echo "  wt              - Interactive mode (select worktree to navigate)"
    echo "  wt new <name>   - Create worktree (always based on dev)"
    echo "  wt rm <name>    - Remove worktree and branch"
    echo "  wt ls           - List worktrees"
    echo "  wt cd <name>    - Navigate to worktree"
    ;;
  esac
}

# If executed directly (not sourced), show help
if [[ "$ZSH_EVAL_CONTEXT" == "toplevel" ]]; then
  wt help
fi
