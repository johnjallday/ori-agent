#!/bin/zsh
# wt - worktree manager for parallel agent workflows
#
# Usage:
#   source scripts/wt.sh    # Load the function (cd works directly)
#   wt                      # Interactive REPL (type: go, status, start, ...)
#   wt go                   # One-shot worktree picker (navigate + cd)
#   wt start [prd]          # Create a worktree from a PRD in the dev tasks/ folder
#   wt new <name>           # Create a clean worktree (no PRD/tasks)
#   wt pr [name]            # Push branch and open a PR against dev
#   wt done [name]          # Archive tasks back to dev, then remove worktree+branch
#   wt rm [name]            # Remove worktree and its branch
#   wt ls                   # List worktrees
#   wt status               # Show ahead/behind/merged vs dev for all worktrees
#   wt cd <name>            # Navigate to a worktree
#   wt merge [name]         # Local merge into dev (legacy; prefer wt pr)
#
# Planning docs live in the dev worktree's tasks/ folder (gitignored). Create
# each PRD + task list there, then `wt start` fans a single PRD out into its
# own worktree so one PRD = one branch = one PR.

WORKTREE_DIR="../"
BASE_BRANCH="dev"
PROTECTED_WORKTREES=("ori-agent" "ori-agent-dev")

# Permission mode written into each new feature worktree's
# .claude/settings.local.json. Feature worktrees are isolated and reviewed at
# merge, so they run hotter than dev. Options:
#   acceptEdits       - auto-apply file edits, still gate/deny bash (recommended)
#   bypassPermissions - skip all prompts, fully unattended (use for headless runs)
FEATURE_WORKTREE_PERMISSION_MODE="acceptEdits"

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

function wt_new_worktree_dir {
  # Directory that new worktrees are created in: the dev worktree's parent, so
  # placement does not depend on the current working directory. Falls back to
  # the CWD-relative WORKTREE_DIR only when the dev worktree can't be found.
  local dev_path
  dev_path="$(wt_get_dev_worktree)"
  if [[ -n "$dev_path" ]]; then
    print -r -- "${dev_path:h}"
  else
    print -r -- "${WORKTREE_DIR%/}"
  fi
}

function wt_parse_name {
  # Splits a user-supplied name into a branch and a worktree directory name.
  #   foo                -> branch feature/foo, dir foo
  #   fix/foo            -> branch fix/foo,     dir foo   (honors intent prefix)
  # Sets globals WT_BRANCH_NAME and WT_DIR_NAME.
  typeset -g WT_BRANCH_NAME WT_DIR_NAME
  local raw="$1"
  if [[ "$raw" == */* ]]; then
    WT_BRANCH_NAME="$raw"
    WT_DIR_NAME="${raw:t}"
  else
    WT_BRANCH_NAME="feature/$raw"
    WT_DIR_NAME="$raw"
  fi
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

  print -r -- "$(wt_new_worktree_dir)/${name}"
}

function wt_resolve_worktree_branch {
  # Echoes the branch checked out in the worktree named/pathed by $1, or nothing.
  local name="$1"
  local target_path
  target_path="$(wt_resolve_worktree_path "$name")"
  local line wt_path=""
  while IFS= read -r line; do
    if [[ "$line" == worktree\ * ]]; then
      wt_path="${line#worktree }"
    elif [[ "$line" == branch\ refs/heads/* ]]; then
      if [[ "${wt_path:t}" == "$name" || "$wt_path" == "$target_path" ]]; then
        print -r -- "${line#branch refs/heads/}"
        return 0
      fi
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

function wt_load_merged_set {
  # Populates WT_MERGED_SET with branches that were merged via a (squash) PR on
  # GitHub. Best-effort: needs gh and only runs once per shell session. Because
  # we squash-merge, merged branch commits are never ancestors of dev, so
  # merge-base alone can't detect them - this closes that gap.
  typeset -gA WT_MERGED_SET
  [[ -n "${WT_MERGED_LOADED:-}" ]] && return 0
  typeset -g WT_MERGED_LOADED=1
  command -v gh >/dev/null 2>&1 || return 0
  local ref
  while IFS= read -r ref; do
    [[ -n "$ref" ]] && WT_MERGED_SET[$ref]=1
  done < <(gh pr list --state merged --base "$BASE_BRANCH" --limit 100 \
             --json headRefName --jq '.[].headRefName' 2>/dev/null)
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
  elif [[ -n "${WT_MERGED_SET[$branch]:-}" ]]; then
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

function wt_provision_worktree {
  # Creates a worktree for $1 (branch) at dir $2 (name), based on an up-to-date
  # origin/dev, and sets it up: Claude permission profile, folder picker, npm.
  # Sets global WT_PROVISIONED_TARGET to the created path. Returns non-zero on
  # failure to create the worktree.
  local branch="$1" dir_name="$2"
  typeset -g WT_PROVISIONED_TARGET=""
  local base_dir target
  base_dir="$(wt_new_worktree_dir)"
  target="${base_dir}/${dir_name}"

  # Base on origin/dev so parallel work landing on dev is picked up. Fall back
  # to the local base branch if the remote ref isn't available (offline).
  git fetch origin "$BASE_BRANCH" --quiet 2>/dev/null
  local start_point="origin/$BASE_BRANCH"
  if ! git rev-parse --verify "$start_point" >/dev/null 2>&1; then
    echo "Warning: origin/$BASE_BRANCH unavailable; basing on local $BASE_BRANCH"
    start_point="$BASE_BRANCH"
  fi

  if ! git worktree add -b "$branch" "$target" "$start_point"; then
    return 1
  fi
  echo "Created worktree: $target (branch: $branch, based on $start_point)"
  WT_PROVISIONED_TARGET="$target"

  # Give the feature worktree its own Claude Code permission profile. It runs
  # hotter than dev (see FEATURE_WORKTREE_PERMISSION_MODE): edits auto-apply
  # because the worktree is isolated and reviewed at merge, while a deny floor
  # still blocks destructive commands. settings.local.json is gitignored, so it
  # stays in this worktree and never touches dev/main; the shared allow/deny in
  # the checked-in .claude/settings.json still applies on top.
  local claude_local="$target/.claude/settings.local.json"
  if [[ ! -f "$claude_local" ]]; then
    echo "Writing feature-worktree Claude profile ($FEATURE_WORKTREE_PERMISSION_MODE)..."
    mkdir -p "$target/.claude"
    cat > "$claude_local" <<JSON
{
  "permissions": {
    "defaultMode": "$FEATURE_WORKTREE_PERMISSION_MODE",
    "deny": [
      "Bash(rm -rf:*)",
      "Bash(git push --force:*)",
      "Bash(git push -f:*)",
      "Bash(./scripts/release.sh:*)",
      "Read(**/.env)",
      "Read(**/*secret*)"
    ]
  }
}
JSON
  fi

  # Build folder picker in the new worktree
  local picker_script="$target/scripts/build-folder-picker.sh"
  if [[ -f "$picker_script" ]]; then
    echo "Building folder picker in new worktree..."
    (cd "$target" && bash scripts/build-folder-picker.sh)
  fi

  # Install npm dependencies. node_modules is gitignored and not shared
  # between worktrees, so a fresh worktree starts without it and tooling
  # like eslint/prettier/playwright won't run until this completes.
  if [[ -f "$target/package.json" ]] && command -v npm >/dev/null 2>&1; then
    echo "Installing npm dependencies in new worktree..."
    (cd "$target" && npm install)
  fi

  return 0
}

function wt_repl {
  # Persistent prompt: read a line and dispatch it as `wt <words>`. Runs in the
  # current shell (wt is sourced), so cd/start still change the shell's dir; the
  # prompt shows the current directory's basename so you can see where you are.
  wt_color_init
  echo "wt REPL - commands: go, status, start, new, pr, done, cd, ls, rm, merge, help  (q to quit)"
  local line
  local -a words
  while true; do
    if ! read -r "line?${WT_C_BOLD}wt${WT_C_RESET} ${WT_C_CYAN}${PWD:t}${WT_C_RESET}> "; then
      echo
      break
    fi
    line="${line## }"; line="${line%% }"
    [[ -z "$line" ]] && continue
    case "$line" in
      q|quit|exit) break ;;
      h|help|'?')  wt_dispatch help; continue ;;
    esac
    words=(${(z)line})
    wt_dispatch "${words[@]}"
  done
}

function wt {
  # No args -> interactive REPL. With args -> one-shot dispatch.
  if [[ $# -eq 0 ]]; then
    wt_repl
    return $?
  fi
  wt_dispatch "$@"
}

function wt_dispatch {
  case "$1" in
  repl)
    wt_repl
    ;;
  start)
    # PRD-driven creation: pick a prd-*.md from the dev worktree's tasks/ folder
    # and fan it (plus its matching tasks- list) out into a dedicated worktree.
    wt_color_init
    local dev_path tasks_dir
    dev_path="$(wt_get_dev_worktree)"
    if [[ -z "$dev_path" ]]; then
      echo "Could not find $BASE_BRANCH worktree"
      return 1
    fi
    tasks_dir="$dev_path/tasks"

    local -a prd_files
    local f
    for f in "$tasks_dir"/prd-*.md(N); do
      prd_files+=("${f:t}")
    done
    if [[ ${#prd_files[@]} -eq 0 ]]; then
      echo "No PRDs found in $tasks_dir (expected prd-*.md)."
      echo "Create a PRD there first, then re-run 'wt start'."
      return 1
    fi

    local chosen="$2"
    if [[ -z "$chosen" ]]; then
      echo "Select a PRD to start (from ${WT_C_CYAN}$tasks_dir${WT_C_RESET}):"
      local i
      for i in {1..${#prd_files[@]}}; do
        local feat="${prd_files[$i]#prd-}"; feat="${feat%.md}"
        local has_tasks="  (no task list yet)"
        [[ -f "$tasks_dir/tasks-$feat.md" ]] && has_tasks=""
        echo "  $i) ${prd_files[$i]}${has_tasks}"
      done
      echo "  q) Quit"
      echo
      read "choice?Choice: "
      if [[ "$choice" == "q" || -z "$choice" ]]; then
        return 0
      fi
      if ! [[ "$choice" =~ ^[0-9]+$ ]] || (( choice < 1 || choice > ${#prd_files[@]} )); then
        echo "Invalid choice"
        return 1
      fi
      chosen="${prd_files[$choice]}"
    else
      # Accept "prd-foo.md", "prd-foo", or "foo".
      [[ "$chosen" == *.md ]] || chosen="$chosen.md"
      [[ "$chosen" == prd-* ]] || chosen="prd-$chosen"
      if [[ ! -f "$tasks_dir/$chosen" ]]; then
        echo "PRD not found: $tasks_dir/$chosen"
        return 1
      fi
    fi

    local feature="${chosen#prd-}"; feature="${feature%.md}"
    local branch="feature/$feature"

    if ! wt_provision_worktree "$branch" "$feature"; then
      return 1
    fi
    local target="$WT_PROVISIONED_TARGET"

    echo "Copying PRD and task list into new worktree..."
    mkdir -p "$target/tasks"
    cp "$tasks_dir/$chosen" "$target/tasks/"
    if [[ -f "$tasks_dir/tasks-$feature.md" ]]; then
      cp "$tasks_dir/tasks-$feature.md" "$target/tasks/"
    else
      echo "Note: no tasks-$feature.md yet - generate the task list in this worktree."
    fi

    cd "$target"
    echo "Changed to: $target"
    ;;
  new)
    local name="$2"
    if [[ -z "$name" ]]; then
      echo "Usage: wt new <name>            (branch feature/<name>)"
      echo "       wt new <type>/<name>     (e.g. fix/foo -> branch fix/foo)"
      echo "For PRD-driven work, prefer: wt start"
      return 1
    fi
    wt_parse_name "$name"
    if ! wt_provision_worktree "$WT_BRANCH_NAME" "$WT_DIR_NAME"; then
      return 1
    fi
    echo "Clean worktree ready (no tasks copied). For PRD-driven work use 'wt start'."
    ;;
  pr)
    # Push the current (or named) worktree's branch and open a PR against dev.
    local name="$2" branch target_path
    if [[ -n "$name" ]]; then
      target_path="$(wt_resolve_worktree_path "$name")"
      branch="$(wt_resolve_worktree_branch "$name")"
    else
      target_path="$(git rev-parse --show-toplevel 2>/dev/null)"
      branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
    fi
    if [[ -z "$branch" || "$branch" == "HEAD" ]]; then
      echo "Could not determine a branch to open a PR for"
      return 1
    fi
    if [[ "$branch" == "$BASE_BRANCH" || "$branch" == "main" ]]; then
      echo "Refusing to open a PR from protected branch: $branch"
      return 1
    fi
    if ! command -v gh >/dev/null 2>&1; then
      echo "gh CLI not found; install it or push and open the PR manually."
      return 1
    fi
    echo "Pushing $branch and opening a PR against $BASE_BRANCH..."
    git -C "$target_path" push -u origin "$branch" || return 1
    (cd "$target_path" && gh pr create --base "$BASE_BRANCH" --head "$branch" --fill)
    ;;
  done)
    # Post-merge cleanup: archive the (completed) task list back to dev, remove
    # the worktree + local/remote branch, then rebase the dev worktree onto
    # origin/dev. Meant to run after the feature's PR has been squash-merged.
    local name="$2" target_path branch
    if [[ -z "$name" ]]; then
      target_path="$(git rev-parse --show-toplevel 2>/dev/null)"
      name="${target_path:t}"
      branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
    else
      target_path="$(wt_resolve_worktree_path "$name")"
      branch="$(wt_resolve_worktree_branch "$name")"
      [[ -z "$branch" ]] && branch="feature/$name"
    fi

    if wt_is_protected_worktree "$target_path"; then
      echo "Refusing to clean up protected worktree: ${target_path:t}"
      return 1
    fi

    local dev_path
    dev_path="$(wt_get_dev_worktree)"
    if [[ -z "$dev_path" ]]; then
      echo "Could not find $BASE_BRANCH worktree"
      return 1
    fi

    # Best-effort merged check so we don't discard unmerged work by accident.
    if command -v gh >/dev/null 2>&1; then
      local merged_num
      merged_num="$(gh pr list --head "$branch" --state merged --limit 1 \
                 --json number --jq '.[0].number' 2>/dev/null)"
      if [[ -z "$merged_num" ]]; then
        echo "Warning: no merged PR found for '$branch'."
        read "cont?Continue cleaning up anyway? [y/N]: "
        [[ "$cont" == y* ]] || { echo "Aborted"; return 0; }
      fi
    fi

    # Archive tasks/ (with ticked checkboxes) back into the dev worktree.
    if [[ -d "$target_path/tasks" ]]; then
      echo "Archiving tasks/ back into $dev_path/tasks/ ..."
      mkdir -p "$dev_path/tasks"
      cp -R "$target_path/tasks/." "$dev_path/tasks/"
    fi

    # Warn on any uncommitted tracked changes before a forced removal.
    local dirty
    dirty="$(git -C "$target_path" status --porcelain 2>/dev/null)"
    if [[ -n "$dirty" ]]; then
      echo "Worktree has uncommitted changes:"
      echo "$dirty"
      read "force?Remove anyway (discards these changes)? [y/N]: "
      [[ "$force" == y* ]] || { echo "Aborted"; return 1; }
    fi

    git worktree remove "$target_path" --force || return 1
    git branch -D "$branch" 2>/dev/null
    echo "Removed worktree and local branch: $name ($branch)"

    read "delremote?Delete remote branch origin/$branch too? [y/N]: "
    if [[ "$delremote" == y* ]]; then
      git push origin --delete "$branch" 2>/dev/null \
        && echo "Deleted remote branch: origin/$branch" \
        || echo "Could not delete origin/$branch (already gone?)"
    fi

    # Rebase the dev worktree onto origin/dev to pick up the squashed commit.
    # Never hard-reset - preserve any unpushed dev commits.
    if [[ -n "$(git -C "$dev_path" status --porcelain)" ]]; then
      echo "Dev worktree has uncommitted changes; skipping rebase. Sync it manually:"
      echo "  git -C '$dev_path' fetch origin $BASE_BRANCH && git -C '$dev_path' rebase origin/$BASE_BRANCH"
    else
      echo "Rebasing dev worktree onto origin/$BASE_BRANCH ..."
      git -C "$dev_path" fetch origin "$BASE_BRANCH" --quiet
      git -C "$dev_path" rebase "origin/$BASE_BRANCH" \
        || echo "Rebase hit conflicts; resolve them in $dev_path"
    fi
    ;;
  rm)
    local name="$2"
    local target_path branch

    if [[ -z "$name" ]]; then
      # Interactive selection (porcelain-parsed, protected worktrees skipped).
      wt_load_worktrees
      local -a rm_names rm_paths rm_branches
      local i
      for i in {1..${#WT_PATHS[@]}}; do
        wt_is_protected_worktree "${WT_PATHS[$i]}" && continue
        rm_names+=("${WT_PATHS[$i]:t}")
        rm_paths+=("${WT_PATHS[$i]}")
        rm_branches+=("${WT_BRANCHES[$i]:-detached}")
      done

      if [[ ${#rm_names[@]} -eq 0 ]]; then
        echo "No removable worktrees found"
        return 1
      fi

      echo "Select worktree to remove:"
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
      branch="${rm_branches[$choice]}"
    else
      target_path="$(wt_resolve_worktree_path "$name")"
      branch="$(wt_resolve_worktree_branch "$name")"
      [[ -z "$branch" ]] && branch="feature/$name"
    fi

    if wt_is_protected_worktree "$target_path"; then
      echo "Refusing to remove protected worktree: ${target_path:t}"
      return 1
    fi
    git worktree remove "$target_path" --force
    if [[ -n "$branch" && "$branch" != "detached" ]]; then
      git branch -D "$branch" 2>/dev/null
    fi
    echo "Removed worktree and branch: $name ($branch)"
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
    local target
    target="$(wt_resolve_worktree_path "$name")"
    if [[ -d "$target" ]]; then
      cd "$target"
      echo "Changed to worktree: $target"
    else
      echo "Worktree not found: $target"
      return 1
    fi
    ;;
  go)
    # Interactive mode - show menu of worktrees with status vs $BASE_BRANCH.
    # No gh lookup here so navigation stays network-free; use `wt status` for
    # the squash-merge-aware view.
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
    wt_load_merged_set
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
    echo "  wt               - Interactive REPL (bare 'wt'; type commands, q to quit)"
    echo "  wt go            - One-shot worktree picker (navigate + cd)"
    echo "  wt start [prd]   - Create worktree from a PRD in the dev tasks/ folder"
    echo "  wt new <name>    - Create a clean worktree (feature/<name>, or <type>/<name>)"
    echo "  wt pr [name]     - Push branch and open a PR against $BASE_BRANCH"
    echo "  wt done [name]   - Archive tasks to dev, remove worktree+branch, rebase dev"
    echo "  wt rm [name]     - Remove worktree and branch (interactive if no name)"
    echo "  wt ls            - List worktrees"
    echo "  wt status        - Show ahead/behind/merged vs $BASE_BRANCH for all worktrees"
    echo "  wt cd <name>     - Navigate to worktree"
    echo "  wt merge [name]  - Local merge into $BASE_BRANCH (legacy; prefer wt pr)"
    ;;
  esac
}

# If executed directly (not sourced), show help
if [[ "$ZSH_EVAL_CONTEXT" == "toplevel" ]]; then
  wt help
fi
