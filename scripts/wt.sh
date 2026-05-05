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

function wt_get_dev_worktree {
  local line wt_path=""
  while IFS= read -r line; do
    if [[ "$line" == worktree\ * ]]; then
      wt_path="${line#worktree }"
    elif [[ "$line" == "branch refs/heads/$BASE_BRANCH" ]]; then
      print -r -- "$wt_path"
      return 0
    fi
  done < <(git worktree list --porcelain)
  return 1
}

function wt_load_worktrees {
  # Populates globals WT_PATHS and WT_BRANCHES (parallel arrays).
  typeset -ga WT_PATHS WT_BRANCHES
  WT_PATHS=()
  WT_BRANCHES=()
  local line wt_path="" branch=""
  while IFS= read -r line; do
    if [[ "$line" == worktree\ * ]]; then
      if [[ -n "$wt_path" ]]; then
        WT_PATHS+=("$wt_path")
        WT_BRANCHES+=("$branch")
      fi
      wt_path="${line#worktree }"
      branch=""
    elif [[ "$line" == branch\ refs/heads/* ]]; then
      branch="${line#branch refs/heads/}"
    fi
  done < <(git worktree list --porcelain)
  if [[ -n "$wt_path" ]]; then
    WT_PATHS+=("$wt_path")
    WT_BRANCHES+=("$branch")
  fi
}

function wt_branch_status {
  # Echoes "ahead behind label" for branch vs BASE_BRANCH.
  local branch="$1"
  local ahead behind label
  ahead=$(git rev-list --count "${BASE_BRANCH}..${branch}" 2>/dev/null || print -r -- "?")
  behind=$(git rev-list --count "${branch}..${BASE_BRANCH}" 2>/dev/null || print -r -- "?")
  if [[ "$branch" == "$BASE_BRANCH" ]]; then
    label="(base)"
  elif git merge-base --is-ancestor "$branch" "$BASE_BRANCH" 2>/dev/null; then
    label="merged"
  elif [[ "$ahead" == "0" ]]; then
    label="empty"
  else
    label="active"
  fi
  print -r -- "$ahead $behind $label"
}

function wt_color_init {
  typeset -g WT_C_RESET WT_C_DIM WT_C_BOLD WT_C_RED WT_C_GREEN WT_C_YELLOW WT_C_CYAN
  if [[ -t 1 ]]; then
    WT_C_RESET=$'\e[0m'
    WT_C_DIM=$'\e[2m'
    WT_C_BOLD=$'\e[1m'
    WT_C_RED=$'\e[31m'
    WT_C_GREEN=$'\e[32m'
    WT_C_YELLOW=$'\e[33m'
    WT_C_CYAN=$'\e[36m'
  else
    WT_C_RESET="" WT_C_DIM="" WT_C_BOLD="" WT_C_RED="" WT_C_GREEN="" WT_C_YELLOW="" WT_C_CYAN=""
  fi
}

function wt_compute_widths {
  # Sets WT_NAME_W and WT_BRANCH_W from the named arrays (default WT_PATHS/WT_BRANCHES).
  local paths_var="${1:-WT_PATHS}"
  local branches_var="${2:-WT_BRANCHES}"
  typeset -g WT_NAME_W=8 WT_BRANCH_W=6
  local -a paths_arr branches_arr
  paths_arr=("${(@P)paths_var}")
  branches_arr=("${(@P)branches_var}")
  local i n b
  for i in {1..${#paths_arr[@]}}; do
    n="${paths_arr[$i]:t}"
    b="${branches_arr[$i]:-detached}"
    (( ${#n} > WT_NAME_W )) && WT_NAME_W=${#n}
    (( ${#b} > WT_BRANCH_W )) && WT_BRANCH_W=${#b}
  done
}

function wt_render_row {
  # Args: idx_prefix name branch ahead behind label
  # idx_prefix is printed verbatim (may contain ANSI). Pads name/branch to
  # WT_NAME_W / WT_BRANCH_W+2 (brackets) before wrapping with color codes.
  local idx_prefix="$1" name="$2" branch="$3" ahead="$4" behind="$5" label="$6"
  local name_pad branch_text branch_pad ahead_pad behind_pad
  printf -v name_pad "%-${WT_NAME_W}s" "$name"
  if [[ -n "$branch" ]]; then
    branch_text="[$branch]"
  else
    branch_text="[detached]"
  fi
  printf -v branch_pad "%-$((WT_BRANCH_W + 2))s" "$branch_text"
  printf -v ahead_pad "%4s" "+$ahead"
  printf -v behind_pad "%4s" "-$behind"

  local ahead_color="$WT_C_DIM" behind_color="$WT_C_DIM"
  [[ "$ahead" != "0" && "$ahead" != "?" ]] && ahead_color="$WT_C_GREEN"
  [[ "$behind" != "0" && "$behind" != "?" ]] && behind_color="$WT_C_RED"

  local label_color
  case "$label" in
    merged)        label_color="$WT_C_GREEN" ;;
    active)        label_color="$WT_C_YELLOW" ;;
    "(base)")      label_color="$WT_C_CYAN" ;;
    "[detached]")  label_color="$WT_C_RED" ;;
    *)             label_color="$WT_C_DIM" ;;
  esac

  print -r -- "${idx_prefix}${name_pad}  ${WT_C_CYAN}${branch_pad}${WT_C_RESET}  ${ahead_color}${ahead_pad}${WT_C_RESET} ${behind_color}${behind_pad}${WT_C_RESET}  ${label_color}${label}${WT_C_RESET}"
}

function wt_render_header {
  # Prints a dimmed header row matching wt_render_row layout.
  local prefix="$1"
  local name_pad branch_pad
  printf -v name_pad "%-${WT_NAME_W}s" "WORKTREE"
  printf -v branch_pad "%-$((WT_BRANCH_W + 2))s" "BRANCH"
  print -r -- "${WT_C_DIM}${prefix}${name_pad}  ${branch_pad}  AHEAD BEHIND  STATUS${WT_C_RESET}"
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
    # Build folder picker in the new worktree
    local picker_script="$WORKTREE_DIR$name/scripts/build-folder-picker.sh"
    if [[ -f "$picker_script" ]]; then
      echo "Building folder picker in new worktree..."
      (cd "$WORKTREE_DIR$name" && bash scripts/build-folder-picker.sh)
    fi
    ;;
  rm)
    local name="$2"
    local target_path

    if [[ -z "$name" ]]; then
      # Interactive selection
      local -a rm_names rm_paths rm_branches
      local line
      while IFS= read -r line; do
        local parts=("${(@s/ /)line}")
        local wt_path="${parts[1]}"
        local branch="${parts[3]}"
        branch="${branch#\[}"
        branch="${branch%\]}"
        local wt_name="${wt_path:t}"
        if wt_is_protected_worktree "$wt_path"; then
          continue
        fi
        rm_names+=("$wt_name")
        rm_paths+=("$wt_path")
        rm_branches+=("$branch")
      done < <(git worktree list)

      if [[ ${#rm_names[@]} -eq 0 ]]; then
        echo "No removable worktrees found"
        return 1
      fi

      echo "Select worktree to remove:"
      local i
      for i in {1..${#rm_names[@]}}; do
        echo "  $i) ${rm_names[$i]} (${rm_branches[$i]})"
      done
      echo "  q) Quit"
      echo

      read "choice?Choice: "

      if [[ "$choice" == "q" || -z "$choice" ]]; then
        return 0
      fi

      if ! [[ "$choice" =~ ^[0-9]+$ ]] || (( choice < 1 || choice > ${#rm_names[@]} )); then
        echo "Invalid choice"
        return 1
      fi

      name="${rm_names[$choice]}"
      target_path="${rm_paths[$choice]}"
    else
      target_path="$(wt_resolve_worktree_path "$name")"
    fi

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
    # Interactive mode - show menu of worktrees with status vs $BASE_BRANCH.
    wt_color_init
    wt_load_worktrees
    if [[ ${#WT_PATHS[@]} -eq 0 ]]; then
      echo "No worktrees found"
      return 1
    fi
    wt_compute_widths

    echo "Select worktree (compared against ${WT_C_CYAN}$BASE_BRANCH${WT_C_RESET}):"
    wt_render_header "      "
    local i wt_path branch ahead behind label idx_prefix
    local -a info
    for i in {1..${#WT_PATHS[@]}}; do
      wt_path="${WT_PATHS[$i]}"
      branch="${WT_BRANCHES[$i]}"
      ahead="0"; behind="0"; label="[detached]"
      if [[ -n "$branch" ]]; then
        info=("${(@s/ /)$(wt_branch_status "$branch")}")
        ahead="${info[1]}"; behind="${info[2]}"; label="${info[3]}"
      fi
      printf -v idx_prefix "  ${WT_C_BOLD}%2d)${WT_C_RESET} " "$i"
      wt_render_row "$idx_prefix" "${wt_path:t}" "$branch" "$ahead" "$behind" "$label"
    done
    print -r -- "  ${WT_C_BOLD} q)${WT_C_RESET} Quit"
    echo

    read "choice?Choice: "

    if [[ "$choice" == "q" || -z "$choice" ]]; then
      return 0
    fi

    if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#WT_PATHS[@]} )); then
      local target="${WT_PATHS[$choice]}"
      cd "$target"
      echo "Changed to: $target"
    else
      echo "Invalid choice"
      return 1
    fi
    ;;
  status)
    wt_color_init
    wt_load_worktrees
    if [[ ${#WT_PATHS[@]} -eq 0 ]]; then
      echo "No worktrees found"
      return 1
    fi
    wt_compute_widths
    wt_render_header "  "
    local i wt_path branch ahead behind label
    local -a info
    for i in {1..${#WT_PATHS[@]}}; do
      wt_path="${WT_PATHS[$i]}"
      branch="${WT_BRANCHES[$i]}"
      ahead="0"; behind="0"; label="[detached]"
      if [[ -n "$branch" ]]; then
        info=("${(@s/ /)$(wt_branch_status "$branch")}")
        ahead="${info[1]}"; behind="${info[2]}"; label="${info[3]}"
      fi
      wt_render_row "  " "${wt_path:t}" "$branch" "$ahead" "$behind" "$label"
    done
    echo
    echo "Compared against: ${WT_C_CYAN}$BASE_BRANCH${WT_C_RESET}"
    ;;
  merge)
    local name="$2"
    local target_path target_branch

    local dev_path
    dev_path="$(wt_get_dev_worktree)"
    if [[ -z "$dev_path" ]]; then
      echo "Could not find $BASE_BRANCH worktree"
      return 1
    fi

    wt_load_worktrees

    # Build candidate list: skip detached, skip dev, skip main.
    local -a m_names m_paths m_branches
    local i
    for i in {1..${#WT_PATHS[@]}}; do
      local wt_path="${WT_PATHS[$i]}"
      local branch="${WT_BRANCHES[$i]}"
      [[ -z "$branch" ]] && continue
      [[ "$branch" == "$BASE_BRANCH" || "$branch" == "main" ]] && continue
      m_names+=("${wt_path:t}")
      m_paths+=("$wt_path")
      m_branches+=("$branch")
    done

    if [[ -z "$name" ]]; then
      if [[ ${#m_names[@]} -eq 0 ]]; then
        echo "No worktrees available to merge into $BASE_BRANCH"
        return 1
      fi

      wt_color_init
      wt_compute_widths m_paths m_branches

      echo "Select worktree to merge into ${WT_C_CYAN}$BASE_BRANCH${WT_C_RESET}:"
      wt_render_header "      "
      local idx_prefix
      local -a info
      for i in {1..${#m_names[@]}}; do
        info=("${(@s/ /)$(wt_branch_status "${m_branches[$i]}")}")
        printf -v idx_prefix "  ${WT_C_BOLD}%2d)${WT_C_RESET} " "$i"
        wt_render_row "$idx_prefix" "${m_names[$i]}" "${m_branches[$i]}" \
          "${info[1]}" "${info[2]}" "${info[3]}"
      done
      print -r -- "  ${WT_C_BOLD} q)${WT_C_RESET} Quit"
      echo

      read "choice?Choice: "

      if [[ "$choice" == "q" || -z "$choice" ]]; then
        return 0
      fi

      if ! [[ "$choice" =~ ^[0-9]+$ ]] || (( choice < 1 || choice > ${#m_names[@]} )); then
        echo "Invalid choice"
        return 1
      fi

      name="${m_names[$choice]}"
      target_path="${m_paths[$choice]}"
      target_branch="${m_branches[$choice]}"
    else
      target_path="$(wt_resolve_worktree_path "$name")"
      target_branch=""
      for i in {1..${#WT_PATHS[@]}}; do
        if [[ "${WT_PATHS[$i]:t}" == "$name" || "${WT_PATHS[$i]}" == "$target_path" ]]; then
          target_branch="${WT_BRANCHES[$i]}"
          break
        fi
      done
      [[ -z "$target_branch" ]] && target_branch="feature/$name"
    fi

    if ! git rev-parse --verify "$target_branch" >/dev/null 2>&1; then
      echo "Branch not found: $target_branch"
      return 1
    fi

    if [[ -n "$(git -C "$dev_path" status --porcelain)" ]]; then
      echo "Dev worktree has uncommitted changes: $dev_path"
      git -C "$dev_path" status --short
      return 1
    fi

    if git merge-base --is-ancestor "$target_branch" "$BASE_BRANCH" 2>/dev/null; then
      echo "$target_branch is already merged into $BASE_BRANCH (nothing to do)"
      read "remove?Remove worktree '$name'? [y/N]: "
      if [[ "$remove" == "y" || "$remove" == "Y" ]]; then
        if wt_is_protected_worktree "$target_path"; then
          echo "Refusing to remove protected worktree: ${target_path:t}"
          return 0
        fi
        git worktree remove "$target_path" --force
        git branch -D "$target_branch" 2>/dev/null
        echo "Removed worktree and branch: $name"
      fi
      return 0
    fi

    local ahead
    ahead=$(git rev-list --count "${BASE_BRANCH}..${target_branch}" 2>/dev/null || echo "?")
    echo
    echo "Will merge '$target_branch' into '$BASE_BRANCH' ($ahead commits ahead)"
    echo "  Dev worktree: $dev_path"
    echo
    git --no-pager log --oneline "${BASE_BRANCH}..${target_branch}" | head -20
    echo
    read "confirm?Proceed with merge? [y/N]: "
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
      echo "Aborted"
      return 0
    fi

    if ! git -C "$dev_path" merge --no-ff "$target_branch"; then
      echo "Merge failed. Resolve conflicts in $dev_path then commit."
      return 1
    fi

    echo "Merged $target_branch into $BASE_BRANCH"
    echo
    read "remove?Remove worktree '$name'? [y/N]: "
    if [[ "$remove" == "y" || "$remove" == "Y" ]]; then
      if wt_is_protected_worktree "$target_path"; then
        echo "Refusing to remove protected worktree: ${target_path:t}"
        return 0
      fi
      git worktree remove "$target_path" --force
      git branch -D "$target_branch" 2>/dev/null
      echo "Removed worktree and branch: $name"
    fi
    ;;
  *)
    echo "Usage: wt [command] [args]"
    echo "  wt               - Interactive mode (select worktree to navigate)"
    echo "  wt new <name>    - Create worktree (always based on $BASE_BRANCH)"
    echo "  wt rm [name]     - Remove worktree and branch (interactive if no name)"
    echo "  wt ls            - List worktrees"
    echo "  wt cd <name>     - Navigate to worktree"
    echo "  wt status        - Show ahead/behind/merged vs $BASE_BRANCH for all worktrees"
    echo "  wt merge [name]  - Merge worktree branch into $BASE_BRANCH (interactive if no name)"
    ;;
  esac
}

# If executed directly (not sourced), show help
if [[ "$ZSH_EVAL_CONTEXT" == "toplevel" ]]; then
  wt help
fi
