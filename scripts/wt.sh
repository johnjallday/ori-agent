#!/bin/zsh
# wt - worktree manager for parallel agent workflows
#
# Usage:
#   source scripts/wt.sh    # Load the function (cd works directly)
#   wt                      # Interactive REPL (type: go, status, start, ...)
#   wt go                   # One-shot worktree picker (navigate + cd)
#   wt plan --issue <N> [--yes]  # Start Codex planning a Ready Issue in the dev worktree
#   wt start [prd] [--kind KIND] [--no-herdr] # Create a worktree from a PRD or task list in the dev tasks/ folder
#   wt new <name>           # Create a clean worktree (no PRD/tasks)
#   wt pr [name]            # Push branch and open a PR against dev
#   wt done [name] [--keep-issue-open] [--herdr-override] # Finish attached Issue, archive tasks, and clean up
#   wt rm [name]            # Remove worktree and its branch
#   wt ls                   # List worktrees
#   wt status               # Feature-first overview of every feature in the repo
#   wt status --worktrees   # Show ahead/behind/merged vs dev for all worktrees
#   wt cd <name>            # Navigate to a worktree
#   wt demo [port]          # Build current worktree + serve an ISOLATED demo sandbox (default port 8931)
#
# The backlog left this file for scripts/devops.sh, a direct GitHub Issue REPL
# with all, needs-decision, backlog, and feature-proposal label views.
#
# Planning docs live in the dev worktree's tasks/ folder (gitignored). Create
# each PRD + task list there, then `wt start` fans a single PRD out into its
# own worktree so one PRD = one branch = one PR.

# Configuration.
#
# These live inside a function rather than at the top level on purpose. Claude
# Code — and anything else that restores a shell from a snapshot of its
# function definitions — captures `function` bodies but NOT top-level
# assignments. A restored shell therefore had `wt` defined while every value
# here was empty, which resolved the worktree root to `/`, the base branch to
# `origin/`, and emptied PROTECTED_WORKTREES so the removal guard below
# silently permitted everything. Initializing from inside `wt` keeps the config
# correct in a restored shell as well as a freshly-sourced one.
#
# Each value is only defaulted, never overwritten, so an operator who exports
# their own WORKTREE_DIR or BASE_BRANCH still wins.
function wt_config_init {
  typeset -g WORKTREE_DIR="${WORKTREE_DIR:-../}"
  typeset -g BASE_BRANCH="${BASE_BRANCH:-dev}"

  # Permission mode written into each new feature worktree's
  # .claude/settings.local.json. Feature worktrees are isolated and reviewed at
  # merge, so they run hotter than dev. Options:
  #   acceptEdits       - auto-apply file edits, still gate/deny bash (recommended)
  #   bypassPermissions - skip all prompts, fully unattended (use for headless runs)
  typeset -g FEATURE_WORKTREE_PERMISSION_MODE="${FEATURE_WORKTREE_PERMISSION_MODE:-acceptEdits}"

  # Declare first so the count below is safe under `set -u` when the parameter
  # is entirely unset (the snapshot case). On an array that already has values
  # this is a no-op and preserves them.
  typeset -ga PROTECTED_WORKTREES
  (( ${#PROTECTED_WORKTREES[@]} )) || PROTECTED_WORKTREES=("ori-agent" "ori-agent-dev")
}

# Initialize at source time as well, so non-`wt` helpers in this file behave the
# same when the script is sourced normally.
wt_config_init

unalias wt 2>/dev/null || true

function wt_is_protected_worktree {
  local candidate="$1"
  local candidate_name="${candidate:t}"
  local protected_name

  # Fail closed. An empty list means the config never initialized, and reading
  # that as "nothing is protected" is exactly what made this guard a silent
  # no-op: `wt rm ori-agent-dev` would have reached `git worktree remove
  # --force` plus `git branch -D dev`. Refusing every removal until the config
  # is sane is the recoverable direction to be wrong in. `${+NAME}` is tested
  # first so an entirely unset parameter does not trip `set -u` before the
  # count is reached.
  if (( ! ${+PROTECTED_WORKTREES} )) || (( ${#PROTECTED_WORKTREES[@]} == 0 )); then
    return 0
  fi

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

function wt_parse_create_flags {
  # The argument parser `wt start` and `wt new` share. Both accept the same five
  # things — --no-herdr, --yes/-y, --kind <value>, one positional name, and no
  # other option — and both used to say so in their own copy of this loop.
  #
  # What they do not share is wording. Each command names itself, prints its own
  # usage line, and calls its positional something different, so the caller
  # passes those three strings in: they are the user-visible surface, and
  # homogenizing them would be a behavior change wearing a refactor's clothes.
  #
  # Results come back in WT_PARSE_* globals rather than on stdout, because a
  # positional may contain anything and a command substitution would re-split it.
  typeset -g WT_PARSE_NAME WT_PARSE_NO_HERDR WT_PARSE_KIND WT_PARSE_ASSUME_YES
  local label="$1" usage="$2" positional_noun="$3"
  shift 3

  WT_PARSE_NAME=""
  WT_PARSE_NO_HERDR=0
  WT_PARSE_KIND=""
  WT_PARSE_ASSUME_YES=0

  local -a parse_args
  parse_args=("$@")
  local parse_arg parse_index=1
  while (( parse_index <= ${#parse_args[@]} )); do
    parse_arg="${parse_args[$parse_index]}"
    case "$parse_arg" in
      --no-herdr)
        WT_PARSE_NO_HERDR=1
        ;;
      --yes|-y)
        WT_PARSE_ASSUME_YES=1
        ;;
      --kind)
        parse_index=$(( parse_index + 1 ))
        if (( parse_index > ${#parse_args[@]} )) || [[ "${parse_args[$parse_index]}" == --* ]]; then
          echo "$label --kind requires a Herdr agent kind"
          echo "$usage"
          return 1
        fi
        if [[ -n "$WT_PARSE_KIND" ]]; then
          echo "$label accepts --kind only once"
          return 1
        fi
        WT_PARSE_KIND="${parse_args[$parse_index]}"
        ;;
      --*)
        echo "Unknown $label option: $parse_arg"
        echo "$usage"
        return 1
        ;;
      *)
        if [[ -n "$WT_PARSE_NAME" ]]; then
          echo "$label accepts one $positional_noun (got: $WT_PARSE_NAME and $parse_arg)"
          return 1
        fi
        WT_PARSE_NAME="$parse_arg"
        ;;
    esac
    parse_index=$(( parse_index + 1 ))
  done
  return 0
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
  typeset -g WT_C_RESET WT_C_DIM WT_C_BOLD WT_C_RED WT_C_GREEN WT_C_YELLOW WT_C_CYAN WT_C_MAGENTA
  if [[ -t 1 ]]; then
    WT_C_RESET=$'\e[0m'
    WT_C_DIM=$'\e[2m'
    WT_C_BOLD=$'\e[1m'
    WT_C_RED=$'\e[31m'
    WT_C_GREEN=$'\e[32m'
    WT_C_YELLOW=$'\e[33m'
    WT_C_CYAN=$'\e[36m'
    WT_C_MAGENTA=$'\e[35m'
  else
    WT_C_RESET="" WT_C_DIM="" WT_C_BOLD="" WT_C_RED="" WT_C_GREEN="" WT_C_YELLOW="" WT_C_CYAN="" WT_C_MAGENTA=""
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

  # Local secrets live in a gitignored .env, which the server auto-loads on
  # start. Being gitignored, it is per-worktree: a fresh one has none, and
  # anything needing a key fails in a way that looks like a config bug rather
  # than a missing file. Copy the dev worktree's, preserving its permissions,
  # and never overwrite one that is already there.
  local env_source
  env_source="$(wt_get_dev_worktree 2>/dev/null || true)"
  if [[ -n "$env_source" && -f "$env_source/.env" && ! -e "$target/.env" ]]; then
    if cp -p "$env_source/.env" "$target/.env"; then
      echo "Copied .env from $env_source (untracked local secrets)."
    else
      echo "Warning: could not copy .env from $env_source; set secrets in $target/.env by hand."
    fi
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
  echo "wt REPL - commands: go, status, plan, start, new, pr, done, cd, ls, rm, demo, herd, help  (q to quit)"
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
  # Re-assert config on every invocation. In a snapshot-restored shell the
  # source-time call above never ran, so this is the only thing standing
  # between `wt` and an empty WORKTREE_DIR/BASE_BRANCH.
  wt_config_init

  # No args -> interactive REPL. With args -> one-shot dispatch.
  if [[ $# -eq 0 ]]; then
    wt_repl
    return $?
  fi
  wt_dispatch "$@"
}

# wt status is a thin dispatcher. The feature-first overview is produced by the
# Go helper, which owns every collector; this shell only validates and forwards
# arguments. The pre-existing Git-only table stays available, unchanged, behind
# --worktrees for scripts and habits that depend on its exact columns.
function wt_status {
  local -a forward
  local arg feature=""
  local worktrees=0

  while (( $# > 0 )); do
    arg="$1"
    case "$arg" in
    --worktrees)
      worktrees=1
      ;;
    --json | --no-color | --watch)
      forward+=("$arg")
      ;;
    --feature)
      if (( $# < 2 )); then
        echo "wt status: --feature requires a feature slug"
        return 2
      fi
      shift
      feature="$1"
      forward+=(--feature "$feature")
      ;;
    --feature=*)
      feature="${arg#--feature=}"
      if [[ -z "$feature" ]]; then
        echo "wt status: --feature requires a feature slug"
        return 2
      fi
      forward+=(--feature "$feature")
      ;;
    -h | --help)
      wt_status_help
      return 0
      ;;
    *)
      echo "wt status: unknown option $arg"
      wt_status_help
      return 2
      ;;
    esac
    shift
  done

  if (( worktrees )); then
    if (( ${#forward[@]} > 0 )); then
      echo "wt status: --worktrees cannot be combined with ${forward[1]}"
      return 2
    fi
    wt_status_worktrees
    return $?
  fi

  # Arguments are passed as separate words, never through eval, so a slug
  # containing shell metacharacters cannot be executed.
  wt_herd feature-overview "${forward[@]}"
}

function wt_status_help {
  echo "Usage: wt status [--feature <slug>] [--json] [--no-color] [--watch]"
  echo "       wt status --worktrees   # the Git-only worktree table"
  echo
  echo "Feature-first overview of every feature in this repository, joining"
  echo "planning artifacts, worktrees, Git, GitHub, and Herdr."
  echo "Read-only: it never writes planning, Git, GitHub, bridge, or Herdr state."
  echo
  echo "Exit codes: 0 complete, 1 incomplete (a required source such as GitHub"
  echo "was unavailable; local facts are still printed), 2 invalid arguments."
  echo "Run 'wt herd doctor' if wt status keeps exiting 1."
}

# The legacy Git-only table, preserved byte for byte. It makes no network call
# beyond the merged-PR set it already loaded, and it reports Git facts only.
function wt_status_worktrees {
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
}

# --- Issue planning -----------------------------------------------------
#
# wt plan --issue <N> starts a Codex planning session for one Ready GitHub
# Issue in the dev worktree. It is a distinct lifecycle stage from wt start:
# planning happens in ori-agent-dev and never creates a branch, a worktree, or
# an implementation agent. Everything past argument parsing — the fresh
# GitHub read, eligibility, identity, artifact-state resolution, rendering,
# confirmation, and the actual writes/Herdr calls — happens in the Go helper,
# the same way wt status delegates its rendering. This function only
# validates arguments and resolves the exact dev worktree to plan in.
function wt_plan_issue_usage {
  echo "Usage: wt plan --issue <positive-integer> [--yes]"
}

function wt_plan_issue {
  local issue="" assume_yes=0
  local -a args
  args=("$@")
  local arg index=1
  while (( index <= ${#args[@]} )); do
    arg="${args[$index]}"
    case "$arg" in
      --issue)
        index=$(( index + 1 ))
        if (( index > ${#args[@]} )); then
          echo "wt plan --issue requires a positive Issue number"
          wt_plan_issue_usage
          return 1
        fi
        if [[ -n "$issue" ]]; then
          echo "wt plan accepts --issue only once"
          return 1
        fi
        if [[ ! "${args[$index]}" =~ ^[1-9][0-9]*$ ]]; then
          echo "wt plan --issue requires a positive Issue number"
          wt_plan_issue_usage
          return 1
        fi
        issue="${args[$index]}"
        ;;
      --yes|-y)
        assume_yes=1
        ;;
      *)
        echo "Unknown wt plan argument: $arg"
        wt_plan_issue_usage
        return 1
        ;;
    esac
    index=$(( index + 1 ))
  done

  if [[ -z "$issue" ]]; then
    echo "wt plan requires --issue <positive-integer>"
    wt_plan_issue_usage
    return 1
  fi

  local dev_path
  dev_path="$(wt_get_dev_worktree)"
  if [[ -z "$dev_path" ]]; then
    echo "Could not find $BASE_BRANCH worktree"
    return 1
  fi

  # Arguments are forwarded as separate words, never through eval, so the
  # Issue number (already validated above as digits-only) and the resolved
  # worktree path are the only values reaching the helper's argument vector.
  local -a plan_args
  plan_args=(issue-plan --issue "$issue" --worktree "$dev_path")
  if (( assume_yes )); then
    plan_args+=(--yes)
  fi
  wt_herd "${plan_args[@]}"
}

function wt_devflow {
  # Delegate structured work to the small repository-local Go helper rather than
  # growing the worktree manager into a terminal, GitHub, or session
  # implementation. The helper resolves repository-local configuration and keeps
  # mutable runtime state outside Git.
  #
  # It runs from whichever checkout the caller is standing in — source, dev, or
  # any linked feature worktree — because `git rev-parse` answers per worktree
  # while all of them share one repository. Arguments are forwarded as separate
  # words and never through eval, so a title, a flag, or a slug carrying shell
  # metacharacters is data the helper reads, not syntax this shell runs.
  local repo_root helper
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null)" || {
    echo "wt must run from an Ori Git worktree"
    return 1
  }
  helper="$repo_root/scripts/herdr-devflow.sh"
  if [[ ! -f "$helper" ]]; then
    echo "Ori devflow helper not found: $helper"
    return 1
  fi
  bash "$helper" "$@"
}

function wt_herd {
  # The Herdr bridge half of the helper. Kept as its own name because the
  # commands behind it need a running Herdr, while the Git/GitHub commands that
  # go through wt_devflow do not.
  wt_devflow "$@"
}

# --- Guided start flow -------------------------------------------------------
#
# wt start runs in four phases: resolve → plan → confirm → execute. The split is
# the point of the feature. Everything up to and including the confirmation
# summary is pure reading, so declining costs exactly nothing: no branch, no
# worktree, no npm install, no Herdr call happens until wt_start_execute runs.
# Previously all of it happened before you saw any of it.
#
# The plan is a record of decisions, deliberately separate from the code that
# applies it, so the summary cannot drift from what actually runs — and so the
# shell tests can assert individual fields.

function wt_plan_reset {
  typeset -g WT_PLAN_FEATURE="" WT_PLAN_BRANCH="" WT_PLAN_TARGET=""
  typeset -g WT_PLAN_DEV="" WT_PLAN_PRD="" WT_PLAN_TASKS="" WT_PLAN_TASKS_STATE="none"
  typeset -g WT_PLAN_ISSUE_SNAPSHOT=""
  typeset -g WT_PLAN_KIND="" WT_PLAN_KIND_DISPLAY="" WT_PLAN_START_AGENT=1 WT_PLAN_COPY_DOCS=1 WT_PLAN_PROMPT=1
  typeset -g WT_PLAN_WORKSPACE="" WT_PLAN_WORKSPACE_STATE=""
}

# The configured primary kind, so the summary names what will actually start.
# WT_PLAN_KIND stays empty unless the user picked one: passing no --kind lets the
# helper use its own recorded default, which matters on a retry where the kind is
# already saved in state.
function wt_plan_default_kind {
  local config root kind=""
  # `|| true` matters: wt.sh is sourced, and a caller running under `set -e`
  # (the shell test suite does) would otherwise abort here whenever this runs
  # outside a Git checkout.
  root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
  config="$root/.herdr/devflow.toml"
  if [[ -f "$config" ]]; then
    kind="$(sed -n '/^\[primary\]/,/^\[roles\]/{ s/^[[:space:]]*kind[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p; }' "$config" | head -1)"
  fi
  print -r -- "${kind:-claude}"
}

# Interactivity is decided by the terminal, not by whether a feature was named.
# Naming one only skips the PRD picker; it must not silently skip the gate that
# shows what is about to happen. Without a terminal every prompt is bypassed, so
# CI and headless runs can never block on stdin.
function wt_plan_is_interactive {
  [[ -t 0 && -t 1 ]]
}

# Ask Herdr where a tab would go. Read-only, best effort, and never fatal: the
# helper always exits 0 here, and a summary is not worth failing a command over.
# Note the answer is a hint — Herdr's focus is sampled when the handoff actually
# runs, and it can move between now and then.
function wt_plan_resolve_workspace {
  typeset -g WT_PLAN_WORKSPACE="" WT_PLAN_WORKSPACE_STATE="unavailable"
  local line
  if ! line="$(wt_herd target 2>/dev/null)"; then
    return 0
  fi
  local -a fields
  fields=("${(@s:	:)line}")
  WT_PLAN_WORKSPACE_STATE="${fields[1]:-unavailable}"
  WT_PLAN_WORKSPACE="${fields[3]:-}"
  return 0
}

function wt_plan_render {
  local marker="${WT_C_YELLOW}!${WT_C_RESET}"
  echo
  echo "${WT_C_BOLD}Plan${WT_C_RESET}"
  printf '  %-14s %s\n' "Feature" "$WT_PLAN_FEATURE"
  printf '  %-14s %s  %s\n' "Branch" "$WT_PLAN_BRANCH" "$marker ${WT_C_DIM}new branch${WT_C_RESET}"
  local setup_note="new worktree + npm install"
  if [[ -n "$WT_PLAN_DEV" && -f "$WT_PLAN_DEV/.env" ]]; then
    setup_note="$setup_note, .env copied"
  fi
  printf '  %-14s %s  %s\n' "Worktree" "$WT_PLAN_TARGET" "$marker ${WT_C_DIM}${setup_note}${WT_C_RESET}"

  if [[ -n "$WT_PLAN_PRD" ]]; then
    printf '  %-14s %s\n' "PRD" "$WT_PLAN_PRD"
  elif (( WT_PLAN_COPY_DOCS )); then
    # wt start selected a feature with a task list but no PRD (a
    # size:quick/size:planned Issue plan): honest, never "ad-hoc" and never a
    # PRD path that does not exist (AR28).
    printf '  %-14s %s\n' "PRD" "${WT_C_DIM}none (task-list-sized)${WT_C_RESET}"
  else
    printf '  %-14s %s\n' "PRD" "${WT_C_DIM}none (ad-hoc)${WT_C_RESET}"
  fi

  if [[ -n "$WT_PLAN_ISSUE_SNAPSHOT" ]]; then
    printf '  %-14s %s\n' "Issue snapshot" "$WT_PLAN_ISSUE_SNAPSHOT"
  fi

  case "$WT_PLAN_TASKS_STATE" in
    present)  printf '  %-14s %s\n' "Task list" "$WT_PLAN_TASKS" ;;
    generate) printf '  %-14s %s\n' "Task list" "${WT_C_CYAN}will be created as the agent's first task${WT_C_RESET}" ;;
    *)
      if [[ -n "$WT_PLAN_PRD" ]]; then
        printf '  %-14s %s\n' "Task list" "${WT_C_DIM}none — the agent works from the PRD alone${WT_C_RESET}"
      else
        printf '  %-14s %s\n' "Task list" "${WT_C_DIM}none (ad-hoc)${WT_C_RESET}"
      fi
      ;;
  esac

  if (( WT_PLAN_START_AGENT )); then
    if (( WT_PLAN_PROMPT )); then
      printf '  %-14s %s\n' "Agent" "$WT_PLAN_KIND_DISPLAY ${WT_C_DIM}(started in a new tab, given the bootstrap prompt)${WT_C_RESET}"
    else
      printf '  %-14s %s\n' "Agent" "$WT_PLAN_KIND_DISPLAY ${WT_C_DIM}(started in a new tab; no prompt — nothing to point it at)${WT_C_RESET}"
    fi
    case "$WT_PLAN_WORKSPACE_STATE" in
      ready)    printf '  %-14s %s\n' "Herdr tab" "in workspace ${WT_C_CYAN}${WT_PLAN_WORKSPACE}${WT_C_RESET} ${WT_C_DIM}(whichever is focused when this runs)${WT_C_RESET}" ;;
      disabled) printf '  %-14s %s\n' "Herdr tab" "${WT_C_DIM}bridge disabled — worktree only${WT_C_RESET}" ;;
      *)        printf '  %-14s %s\n' "Herdr tab" "${WT_C_DIM}Herdr unreachable — worktree only, retry later${WT_C_RESET}" ;;
    esac
  else
    printf '  %-14s %s\n' "Agent" "${WT_C_DIM}none (--no-herdr)${WT_C_RESET}"
  fi

  echo
  echo "  ${WT_C_YELLOW}!${WT_C_RESET} ${WT_C_DIM}marks steps that are not undone by declining later.${WT_C_RESET}"
}

# The gate. Returns non-zero to mean "do nothing", which is the safe direction:
# a caller that ignores the status still has not mutated anything yet.
function wt_plan_confirm {
  local assume_yes="${1:-0}"
  if (( assume_yes )); then
    return 0
  fi
  if ! wt_plan_is_interactive; then
    echo
    echo "${WT_C_DIM}Non-interactive: proceeding without confirmation.${WT_C_RESET}"
    return 0
  fi
  local reply
  echo
  if ! read -r "reply?Proceed? [y/N] "; then
    echo
    return 1
  fi
  case "$reply" in
    y|Y|yes|YES) return 0 ;;
    *) echo "Nothing was changed."; return 1 ;;
  esac
}

# Everything that mutates lives here, and nothing above it does. Ordered so the
# irreversible Git step happens first and the optional layers stack on top: a
# failure in the Herdr half leaves a worktree you can still work in.
function wt_start_execute {
  if ! wt_provision_worktree "$WT_PLAN_BRANCH" "$WT_PLAN_FEATURE"; then
    return 1
  fi
  WT_PLAN_TARGET="$WT_PROVISIONED_TARGET"

  # Each planning artifact is copied independently of the others (AR29): a
  # task-list-only feature has no PRD to gate the copy on, and an Issue
  # snapshot from `wt plan` is copied whenever one exists, regardless of
  # whether a PRD or task list accompanies it.
  if (( WT_PLAN_COPY_DOCS )) && [[ -n "$WT_PLAN_PRD" || -n "$WT_PLAN_ISSUE_SNAPSHOT" || "$WT_PLAN_TASKS_STATE" != "none" ]]; then
    echo "Copying planning documents into new worktree..."
    mkdir -p "$WT_PLAN_TARGET/tasks"
    [[ -n "$WT_PLAN_PRD" ]] && cp "$WT_PLAN_PRD" "$WT_PLAN_TARGET/tasks/"
    [[ -n "$WT_PLAN_ISSUE_SNAPSHOT" ]] && cp "$WT_PLAN_ISSUE_SNAPSHOT" "$WT_PLAN_TARGET/tasks/"
    if [[ "$WT_PLAN_TASKS_STATE" == "present" && -n "$WT_PLAN_TASKS" ]]; then
      cp "$WT_PLAN_TASKS" "$WT_PLAN_TARGET/tasks/"
    elif [[ "$WT_PLAN_TASKS_STATE" == "generate" ]]; then
      wt_write_starter_tasklist "$WT_PLAN_TARGET/tasks/tasks-$WT_PLAN_FEATURE.md" "$WT_PLAN_FEATURE"
    fi
  fi

  # Nothing is recorded anywhere else. Starting a feature used to also write and
  # push a backlog entry on dev, which moved dev and left the new branch behind
  # it the moment it was created. The Issue on GitHub is the record now, and
  # `wt start` does not touch it: it neither reads an Issue nor changes one.

  if (( WT_PLAN_START_AGENT )); then
    wt_herd_handoff "$WT_PLAN_FEATURE" "$WT_PLAN_TARGET" "$WT_PLAN_BRANCH" "$WT_PLAN_KIND" "$WT_PLAN_PROMPT"
  else
    echo "Skipping Herdr handoff (--no-herdr)."
  fi

  cd "$WT_PLAN_TARGET"
  echo "Changed to: $WT_PLAN_TARGET"
  return 0
}

# A placeholder checklist is more useful than none: the bootstrap prompt reads
# the first unchecked item, so this turns "there is no task list" into the
# agent's actual first instruction instead of a note that scrolls past.
function wt_write_starter_tasklist {
  # Not named `path`: in zsh that is the special array tied to PATH, so a
  # `local path=...` here silently empties PATH for the whole function and every
  # command in it fails with "command not found".
  local list_path="$1" feature="$2"
  mkdir -p "${list_path:h}"
  cat > "$list_path" <<TASKS
# Tasks: $feature

Source PRD: \`tasks/prd-$feature.md\`

This checklist is a placeholder created by \`wt start\`. It is not a real plan.

## Tasks

- [ ] 1.1 Read \`tasks/prd-$feature.md\` and replace this file with a real task
      list: parent tasks with sub-tasks, each parent group ending in a commit,
      the final group ending in a PR. Do not start implementing until it exists.
TASKS
  echo "Wrote a starter task list: $list_path"
}

# The single Herdr degradation contract, shared by every command that hands a
# worktree to Herdr.
#
# Herdr is a third-party binary on its own release channel, so it stays an
# optional session layer over a Git-and-GitHub core. Every Herdr-side failure
# lands here: binary missing, daemon down, version or API schema too old, no
# focused workspace to place the tab in, tab create refused, the tab's pane
# unusable, agent start or bootstrap prompt timed out, socket permission denied.
#
# All of them are non-fatal by construction. The worktree and its planning
# documents already exist before this runs and are never rolled back; the helper
# has already printed the stage, code, message, and its own recovery command on
# stderr; this adds the line that re-runs only the missing Herdr half.
#
# Always returns 0. A Herdr problem must never make wt start or wt new look like
# it failed, because the thing the user actually asked for — a worktree they can
# work in — is sitting there ready.
function wt_herd_handoff {
  local feature="$1" target="$2" branch="$3" primary_kind="${4:-}" prompt="${5:-1}"
  local -a handoff_args
  handoff_args=(handoff --feature "$feature" --worktree "$target" --branch "$branch")
  if [[ -n "$primary_kind" ]]; then
    handoff_args+=(--kind "$primary_kind")
  fi
  if (( ! prompt )); then
    handoff_args+=(--no-prompt)
  fi
  echo "Handing the existing worktree to Herdr..."
  if wt_herd "${handoff_args[@]}"; then
    return 0
  fi
  echo
  echo "Herdr handoff did not finish. The Git worktree and its planning documents are ready and unchanged."
  echo "  Retry the Herdr half: wt herd retry --feature '$feature' --worktree '$target' --branch '$branch'"
  echo "  Diagnose:             wt herd doctor"
  echo "  Continue without it:  cd '$target'"
  return 0
}

# Run the cleanup preflight from the target worktree itself. Older worktrees
# without the optional bridge remain fully compatible with wt done; only a
# worktree carrying both its checked-in bridge config and helper is guarded.
function wt_herd_cleanup_preflight {
  local target_path="$1" override="${2:-0}"
  local helper="$target_path/scripts/herdr-devflow.sh"
  if [[ ! -f "$target_path/.herdr/devflow.toml" || ! -f "$helper" ]]; then
    return 0
  fi

  local -a cleanup_args
  cleanup_args=(cleanup --worktree "$target_path")
  if [[ "$override" == "1" ]]; then
    cleanup_args+=(--override)
  fi
  # Cleanup is safety-critical, so it deliberately uses the checked-in source
  # from the target worktree rather than a possibly stale ignored dev binary.
  HERDR_DEVFLOW_USE_SOURCE=1 bash "$helper" "${cleanup_args[@]}"
}

# Guard the destructive half of wt done. A known active agent or unresolved
# schedule is never overridden. Unknown/unreachable Herdr state and a failed
# tab close may proceed only from an interactive terminal after a dedicated
# acknowledgement (or the explicit flag), separate from the later dirty-worktree
# prompt.
#
# Cleanup closes the feature's own tab and nothing else. A feature recorded
# before tab-scoped handoff has no tab, so its workspace is left open and named
# for the user to close by hand — closing it automatically is what cascaded on
# 2026-07-26.
function wt_done_herdr_guard {
  local target_path="$1" requested_override="${2:-0}" cleanup_status
  if wt_herd_cleanup_preflight "$target_path" 0; then
    return 0
  else
    cleanup_status=$?
  fi
  if (( cleanup_status == 20 )); then
    echo "Refusing wt done: Herdr work is still active in this worktree. Resolve the listed agents or schedules, then retry."
    return 1
  fi

  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "Refusing wt done: Herdr safety cannot be verified in a non-interactive shell."
    echo "Run wt done from an interactive terminal after inspecting wt herd status."
    return 1
  fi

  if [[ "$requested_override" == "1" ]]; then
    echo "WARNING: --herdr-override was supplied after an unverified Herdr cleanup check."
  else
    echo "WARNING: Herdr state could not be verified or the feature's tab could not be closed."
    local acknowledgement
    read "acknowledgement?Type HERDR-OVERRIDE to continue and accept orphan-risk: "
    if [[ "$acknowledgement" != "HERDR-OVERRIDE" ]]; then
      echo "Aborted; Git worktree preserved."
      return 1
    fi
  fi

  if wt_herd_cleanup_preflight "$target_path" 1; then
    echo "WARNING: continuing wt done with explicit Herdr-safety override; the removed Git worktree may remain open in a Herdr tab."
    return 0
  else
    cleanup_status=$?
    if (( cleanup_status == 20 )); then
      echo "Refusing wt done: managed Herdr work became active while cleanup was being confirmed."
    else
      echo "Herdr cleanup override did not complete; Git worktree preserved."
    fi
    return 1
  fi
}

# Resolve the one Issue explicitly attached by `wt plan`. The generated marker
# is always the third line of the exact issue-<feature>.md snapshot; reading only
# that header keeps the untrusted Issue body and comments from supplying a fake
# attachment. The number-first feature identity is a second, independent check
# before wt done is allowed to mutate GitHub.
function wt_done_resolve_attached_issue {
  typeset -g WT_DONE_ISSUE_NUMBER=""
  local target_path="$1" feature="$2"
  local snapshot="$target_path/tasks/issue-$feature.md"
  local marker marker_pattern='^<!-- ori-devflow: issue-snapshot; issue=([1-9][0-9]*) -->$'
  local -a match mbegin mend

  if [[ ! -f "$snapshot" ]]; then
    echo "Refusing wt done: attached Issue snapshot is not a regular file: $snapshot"
    echo "Fix the snapshot, or use --keep-issue-open to clean up without changing GitHub."
    return 1
  fi
  if ! marker="$(sed -n '3p' "$snapshot" 2>/dev/null)"; then
    echo "Refusing wt done: could not read attached Issue snapshot: $snapshot"
    echo "Fix the snapshot, or use --keep-issue-open to clean up without changing GitHub."
    return 1
  fi
  if [[ ! "$marker" =~ "$marker_pattern" ]]; then
    echo "Refusing wt done: attached Issue snapshot has no valid generated marker on line 3: $snapshot"
    echo "Fix the snapshot, or use --keep-issue-open to clean up without changing GitHub."
    return 1
  fi

  WT_DONE_ISSUE_NUMBER="${match[1]}"
  if [[ "$feature" != ${WT_DONE_ISSUE_NUMBER}-* ]]; then
    echo "Refusing wt done: Issue #$WT_DONE_ISSUE_NUMBER does not match feature '$feature'."
    echo "Fix the snapshot/feature identity, or use --keep-issue-open to clean up without changing GitHub."
    WT_DONE_ISSUE_NUMBER=""
    return 1
  fi
}

# Close only the primary Issue attached above, and only after this branch has a
# confirmed merged PR. Failure is fatal while the worktree still exists, making
# the operation safely retryable. An already-closed Issue is idempotent.
function wt_done_close_attached_issue {
  local issue_number="$1" merged_num="$2" issue_state
  if ! issue_state="$(gh issue view "$issue_number" --json state --jq '.state')"; then
    echo "Could not inspect attached Issue #$issue_number; worktree preserved."
    echo "Retry when GitHub is available, or use --keep-issue-open intentionally."
    return 1
  fi

  case "$issue_state" in
    OPEN)
      echo "Closing attached Issue #$issue_number as completed..."
      if ! gh issue close "$issue_number" --reason completed --comment "Delivered by PR #$merged_num."; then
        echo "Could not close attached Issue #$issue_number; worktree preserved."
        echo "Retry when GitHub is available, or use --keep-issue-open intentionally."
        return 1
      fi
      echo "Closed attached Issue #$issue_number."
      ;;
    CLOSED)
      echo "Attached Issue #$issue_number is already closed; leaving it unchanged."
      ;;
    *)
      echo "Could not safely interpret attached Issue #$issue_number state '$issue_state'; worktree preserved."
      return 1
      ;;
  esac
}

function wt_dispatch {
  case "$1" in
  repl)
    wt_repl
    ;;
  start)
    # PRD-and/or-task-list-driven creation, in four phases: resolve → plan →
    # confirm → execute. Only wt_start_execute mutates anything, so declining
    # below leaves Git and Herdr exactly as they were.
    #
    # A feature is startable with a PRD, a task list, or both: `wt plan`
    # produces task-list-only features for size:quick/size:planned Issues (no
    # PRD at all), and those are exactly as startable as the original
    # PRD-driven shape.
    wt_color_init
    wt_plan_reset

    local dev_path tasks_dir
    dev_path="$(wt_get_dev_worktree)"
    if [[ -z "$dev_path" ]]; then
      echo "Could not find $BASE_BRANCH worktree"
      return 1
    fi
    tasks_dir="$dev_path/tasks"
    WT_PLAN_DEV="$dev_path"

    # The (N) glob qualifier below needs BARE_GLOB_QUAL, which is a zsh default
    # but is off when this file is sourced from an `emulate sh`-style shell (e.g.
    # a non-interactive/CI/headless agent). Without it, `prd-*.md(N)` aborts with
    # "no matches found" before the loop runs. Force the option locally;
    # local_options reverts it on return so the caller's shell is untouched.
    setopt local_options bareglobqual

    # Candidate features are the union of prd-*.md and tasks-*.md stems, so a
    # task-list-only feature is listed exactly once alongside PRD-driven ones
    # (FR-26).
    local -a candidate_features
    local f feat
    for f in "$tasks_dir"/prd-*.md(N); do
      feat="${f:t}"; feat="${feat#prd-}"; feat="${feat%.md}"
      candidate_features+=("$feat")
    done
    for f in "$tasks_dir"/tasks-*.md(N); do
      feat="${f:t}"; feat="${feat#tasks-}"; feat="${feat%.md}"
      if (( ! ${candidate_features[(Ie)$feat]} )); then
        candidate_features+=("$feat")
      fi
    done

    local chosen no_herdr primary_kind assume_yes
    if ! wt_parse_create_flags "wt start" \
      "Usage: wt start [feature] [--kind KIND] [--no-herdr] [--yes]" \
      "PRD/task-list feature name" "${@:2}"; then
      return 1
    fi
    chosen="$WT_PARSE_NAME"
    no_herdr="$WT_PARSE_NO_HERDR"
    primary_kind="$WT_PARSE_KIND"
    assume_yes="$WT_PARSE_ASSUME_YES"

    if (( no_herdr )) && [[ -n "$primary_kind" ]]; then
      echo "wt start --kind cannot be combined with --no-herdr"
      return 1
    fi

    # FR-13: bare `wt start` is the stepwise guided flow. Naming a feature is the
    # quick path — it still shows the summary and still asks before mutating, but
    # it does not walk through choices the caller has already made.
    local guided=0
    if [[ -z "$chosen" ]] && wt_plan_is_interactive; then
      guided=1
    fi

    if [[ -z "$chosen" ]]; then
      if [[ ${#candidate_features[@]} -eq 0 ]]; then
        echo "No PRDs or task lists found in $tasks_dir (expected prd-*.md or tasks-*.md)."
        echo "Create one there first (or run 'wt plan --issue <N>'), then re-run 'wt start'."
        echo "For work that does not need one: wt new <name>"
        return 1
      fi
      if ! wt_plan_is_interactive; then
        echo "wt start needs a feature name when there is no terminal to choose from."
        echo "Usage: wt start <feature> [--kind KIND] [--no-herdr]"
        return 1
      fi
      echo "Select a feature to start (from ${WT_C_CYAN}$tasks_dir${WT_C_RESET}):"
      local i
      for i in {1..${#candidate_features[@]}}; do
        feat="${candidate_features[$i]}"
        local row_label starter_note=""
        if [[ -f "$tasks_dir/prd-$feat.md" ]]; then
          row_label="prd-$feat.md"
        else
          row_label="tasks-$feat.md  ${WT_C_DIM}(task-list-sized, no PRD)${WT_C_RESET}"
        fi
        if [[ -f "$tasks_dir/tasks-$feat.md" ]] && grep -qF 'ori-devflow: planning-starter' "$tasks_dir/tasks-$feat.md" 2>/dev/null; then
          starter_note="  ${WT_C_YELLOW}(planning incomplete)${WT_C_RESET}"
        elif [[ ! -f "$tasks_dir/tasks-$feat.md" && -f "$tasks_dir/prd-$feat.md" ]]; then
          starter_note="  ${WT_C_DIM}(no task list yet)${WT_C_RESET}"
        fi
        echo "  $i) ${row_label}${starter_note}"
      done
      echo "  q) Quit"
      echo
      local choice
      if ! read -r "choice?Choice: "; then
        echo
        return 0
      fi
      if [[ "$choice" == "q" || -z "$choice" ]]; then
        return 0
      fi
      if ! [[ "$choice" =~ ^[0-9]+$ ]] || (( choice < 1 || choice > ${#candidate_features[@]} )); then
        echo "Invalid choice"
        return 1
      fi
      chosen="${candidate_features[$choice]}"
    else
      # Accept "prd-foo.md", "tasks-foo.md", "foo.md", or plain "foo".
      chosen="${chosen%.md}"
      chosen="${chosen#prd-}"
      chosen="${chosen#tasks-}"
      if [[ ! -f "$tasks_dir/prd-$chosen.md" && ! -f "$tasks_dir/tasks-$chosen.md" ]]; then
        echo "No PRD or task list found for '$chosen' in $tasks_dir"
        echo "  expected prd-$chosen.md and/or tasks-$chosen.md"
        return 1
      fi
    fi

    WT_PLAN_FEATURE="$chosen"
    WT_PLAN_BRANCH="feature/$WT_PLAN_FEATURE"
    WT_PLAN_TARGET="$(wt_new_worktree_dir)/$WT_PLAN_FEATURE"
    WT_PLAN_COPY_DOCS=1

    if [[ -f "$tasks_dir/prd-$WT_PLAN_FEATURE.md" ]]; then
      WT_PLAN_PRD="$tasks_dir/prd-$WT_PLAN_FEATURE.md"
    fi
    if [[ -f "$tasks_dir/issue-$WT_PLAN_FEATURE.md" ]]; then
      WT_PLAN_ISSUE_SNAPSHOT="$tasks_dir/issue-$WT_PLAN_FEATURE.md"
    fi

    if [[ -f "$tasks_dir/tasks-$WT_PLAN_FEATURE.md" ]]; then
      # AR27: a task list still carrying the wt-plan starter marker is not a
      # real plan yet. Refuse to create an implementation worktree until
      # Codex has replaced it — implementing against the starter would mean
      # coding against instructions that say "read the Issue and write the
      # real plan", not a plan at all.
      if grep -qF 'ori-devflow: planning-starter' "$tasks_dir/tasks-$WT_PLAN_FEATURE.md" 2>/dev/null; then
        echo "${WT_C_YELLOW}$WT_PLAN_FEATURE's task list is still a planning starter.${WT_C_RESET}"
        echo "Codex has not finished planning tasks/tasks-$WT_PLAN_FEATURE.md yet."
        echo "Finish planning first, then re-run 'wt start $WT_PLAN_FEATURE'."
        return 1
      fi
      WT_PLAN_TASKS="$tasks_dir/tasks-$WT_PLAN_FEATURE.md"
      WT_PLAN_TASKS_STATE="present"
    elif [[ -n "$WT_PLAN_PRD" ]] && wt_plan_is_interactive; then
      # Previously this printed a note and carried on, so a feature could get
      # all the way to a running agent before anyone noticed it had no plan.
      echo
      echo "${WT_C_YELLOW}No task list for $WT_PLAN_FEATURE.${WT_C_RESET} tasks-$WT_PLAN_FEATURE.md does not exist in $tasks_dir."
      echo "  g) Create a starter checklist whose first task is to write the real one"
      echo "  c) Continue without one; the agent works from the PRD alone"
      echo "  q) Cancel so you can generate it properly first"
      local tasks_choice
      if ! read -r "tasks_choice?Choice [g/c/q]: "; then
        echo
        return 0
      fi
      case "$tasks_choice" in
        g|G|"") WT_PLAN_TASKS_STATE="generate" ;;
        c|C)    WT_PLAN_TASKS_STATE="none" ;;
        *)      echo "Nothing was changed."; return 0 ;;
      esac
    else
      WT_PLAN_TASKS_STATE="none"
    fi

    if (( no_herdr )); then
      WT_PLAN_START_AGENT=0
    else
      WT_PLAN_START_AGENT=1
      WT_PLAN_KIND="$primary_kind"
      WT_PLAN_KIND_DISPLAY="${primary_kind:-$(wt_plan_default_kind)}"
      wt_plan_resolve_workspace
      # The agent step defaults to the configured primary kind with
      # start-and-prompt pre-selected, so accepting every default reproduces
      # exactly what wt start did before this flow existed.
      if (( guided )) && (( ! assume_yes )) && [[ -z "$primary_kind" ]]; then
        local agent_choice
        echo
        echo "Agent: ${WT_C_CYAN}${WT_PLAN_KIND_DISPLAY}${WT_C_RESET} started in a new tab and given the bootstrap prompt."
        echo "  Enter) accept    n) no agent, worktree only    <kind>) use a different agent kind"
        if ! read -r "agent_choice?Choice: "; then
          echo
          return 0
        fi
        case "$agent_choice" in
          "")     ;;
          n|N)    WT_PLAN_START_AGENT=0 ;;
          *)      WT_PLAN_KIND="$agent_choice"; WT_PLAN_KIND_DISPLAY="$agent_choice" ;;
        esac
      fi
    fi

    wt_plan_render
    if ! wt_plan_confirm "$assume_yes"; then
      return 0
    fi
    wt_start_execute
    ;;
  herd)
    shift
    wt_herd "$@"
    ;;
  plan)
    shift
    wt_plan_issue "$@"
    ;;
  new)
    # Ad-hoc creation, through the same four phases as wt start. The only
    # differences are the ones FR-23 allows: no planning documents are copied,
    # and the agent is started without a bootstrap prompt because there is no
    # PRD or checklist to point it at.
    wt_color_init
    wt_plan_reset

    local name no_herdr primary_kind assume_yes
    if ! wt_parse_create_flags "wt new" \
      "Usage: wt new <name> [--kind KIND] [--no-herdr] [--yes]" \
      "name" "${@:2}"; then
      return 1
    fi
    name="$WT_PARSE_NAME"
    no_herdr="$WT_PARSE_NO_HERDR"
    primary_kind="$WT_PARSE_KIND"
    assume_yes="$WT_PARSE_ASSUME_YES"

    if [[ -z "$name" ]]; then
      echo "Usage: wt new <name>            (branch feature/<name>)"
      echo "       wt new <type>/<name>     (e.g. fix/foo -> branch fix/foo)"
      echo "For PRD-driven work, prefer: wt start"
      return 1
    fi
    if (( no_herdr )) && [[ -n "$primary_kind" ]]; then
      echo "wt new --kind cannot be combined with --no-herdr"
      return 1
    fi

    wt_parse_name "$name"
    # The bridge requires a feature name matching ^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$
    # (agents/service.go). Rejecting here means an unusable name costs nothing;
    # letting it through would create the branch and worktree first and only
    # then fail at handoff, leaving a worktree the bridge cannot ever adopt.
    if [[ ! "$WT_DIR_NAME" =~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$' ]]; then
      echo "Invalid name: ${WT_C_YELLOW}$WT_DIR_NAME${WT_C_RESET}"
      echo "Use letters, digits, dot, underscore, or dash, starting with a letter or digit (max 81 characters)."
      return 1
    fi
    # The branch is checked separately because a <type>/<name> form can produce a
    # usable feature slug from an unusable ref — `a//b` yields the slug `b`. Git
    # is the authority on what a branch may be called, so ask it rather than
    # reimplementing the rule.
    if ! git check-ref-format --branch "$WT_BRANCH_NAME" >/dev/null 2>&1; then
      echo "Invalid branch name: ${WT_C_YELLOW}$WT_BRANCH_NAME${WT_C_RESET}"
      echo "Git will not accept it as a ref. Try 'wt new <type>/<name>', e.g. fix/thing."
      return 1
    fi

    WT_PLAN_FEATURE="$WT_DIR_NAME"
    WT_PLAN_BRANCH="$WT_BRANCH_NAME"
    WT_PLAN_TARGET="$(wt_new_worktree_dir)/$WT_DIR_NAME"
    WT_PLAN_DEV="$(wt_get_dev_worktree)"
    WT_PLAN_PRD=""
    WT_PLAN_TASKS_STATE="none"
    WT_PLAN_COPY_DOCS=0
    # Resolved during group 5 (PRD open question 4): no bootstrap prompt. There
    # is no PRD and no checklist, so any prompt would either name documents that
    # do not exist or say nothing the agent cannot already see.
    WT_PLAN_PROMPT=0

    if (( no_herdr )); then
      WT_PLAN_START_AGENT=0
    else
      WT_PLAN_START_AGENT=1
      WT_PLAN_KIND="$primary_kind"
      WT_PLAN_KIND_DISPLAY="${primary_kind:-$(wt_plan_default_kind)}"
      wt_plan_resolve_workspace
    fi

    wt_plan_render
    if ! wt_plan_confirm "$assume_yes"; then
      return 0
    fi
    wt_start_execute
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
    # Post-merge completion: close an explicitly attached Issue, archive the
    # completed task list back to dev, remove the worktree + local/remote branch,
    # then rebase dev onto origin/dev. Run only after the PR is squash-merged.
    local name="" target_path branch merged_num="" herdr_override=0 keep_issue_open=0 done_arg
    local issue_snapshot="" issue_snapshot_present=0 issue_number="" merged_lookup_failed=0
    for done_arg in "${@:2}"; do
      case "$done_arg" in
        --herdr-override)
          herdr_override=1
          ;;
        --keep-issue-open)
          keep_issue_open=1
          ;;
        --*)
          echo "Unknown wt done option: $done_arg"
          echo "Usage: wt done [name] [--keep-issue-open] [--herdr-override]"
          return 1
          ;;
        *)
          if [[ -n "$name" ]]; then
            echo "wt done accepts one worktree name (got: $name and $done_arg)"
            return 1
          fi
          name="$done_arg"
          ;;
      esac
    done
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

    # Presence of this exact snapshot is the opt-in attachment contract. A
    # number-looking slug alone is never enough to close an Issue.
    issue_snapshot="$target_path/tasks/issue-$name.md"
    if [[ -e "$issue_snapshot" || -L "$issue_snapshot" ]]; then
      issue_snapshot_present=1
      if (( ! keep_issue_open )); then
        if ! wt_done_resolve_attached_issue "$target_path" "$name"; then
          return 1
        fi
        issue_number="$WT_DONE_ISSUE_NUMBER"
      fi
    fi

    # Best-effort for ad-hoc work remains backward compatible. Issue-backed
    # work is stricter: without --keep-issue-open, a failed GitHub lookup cannot
    # silently turn final delivery into local cleanup only.
    if command -v gh >/dev/null 2>&1; then
      if ! merged_num="$(gh pr list --head "$branch" --base "$BASE_BRANCH" --state merged --limit 1 \
                         --json number --jq '.[0].number' 2>/dev/null)"; then
        merged_lookup_failed=1
      fi
      if (( merged_lookup_failed )); then
        if (( issue_snapshot_present && ! keep_issue_open )); then
          echo "Could not verify a merged PR for Issue-backed feature '$name'; worktree preserved."
          echo "Retry when GitHub is available, or use --keep-issue-open intentionally."
          return 1
        fi
        echo "Warning: could not verify whether a PR for '$branch' merged."
        read "cont?Continue cleaning up anyway? [y/N]: "
        [[ "$cont" == y* ]] || { echo "Aborted"; return 0; }
      elif [[ -z "$merged_num" ]]; then
        echo "Warning: no merged PR found for '$branch'."
        read "cont?Continue cleaning up anyway? [y/N]: "
        [[ "$cont" == y* ]] || { echo "Aborted"; return 0; }
      fi
    elif (( issue_snapshot_present && ! keep_issue_open )); then
      echo "gh CLI is required to finish attached Issue #$issue_number; worktree preserved."
      echo "Install/authenticate gh, or use --keep-issue-open intentionally."
      return 1
    fi

    # Do this after merged-PR and Issue-attachment resolution but before every
    # mutation: task archival, dirty confirmation, Issue closure, and Git removal.
    if ! wt_done_herdr_guard "$target_path" "$herdr_override"; then
      return 1
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

    if (( issue_snapshot_present )); then
      if (( keep_issue_open )); then
        echo "Keeping the attached Issue open (--keep-issue-open)."
      elif [[ -z "$merged_num" ]]; then
        echo "Attached Issue #$issue_number was not changed because no merged PR was confirmed."
      elif ! wt_done_close_attached_issue "$issue_number" "$merged_num"; then
        return 1
      fi
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
  demo)
    # Build the CURRENT worktree and serve it from an isolated demo sandbox,
    # so a feature branch can be seen running (and manually tested) before its
    # PR ever opens. Isolation rules (see repo CLAUDE.md "Smoke Testing"):
    #   - HOME is overridden so "Ori Workspaces" never touches the real tree
    #   - ORI_DATA_DIR is overridden so DB/vaults/templates are sandboxed
    #   - the server is launched from INSIDE the sandbox because the plugin
    #     store resolves relative to the launch directory
    # Foreground process: Ctrl-C stops it. The sandbox is a throwaway temp dir.
    local demo_root
    demo_root="$(git rev-parse --show-toplevel 2>/dev/null)"
    if [[ -z "$demo_root" ]]; then
      echo "wt demo must run inside a git worktree"
      return 1
    fi
    local demo_port="${2:-8931}"
    echo "Building $demo_root ..."
    (cd "$demo_root" && go build -o bin/ori-agent ./cmd/server) || return 1
    local demo_parent
    demo_parent="$(cd "${TMPDIR:-/tmp}" 2>/dev/null && pwd -P)" || {
      echo "Could not resolve the demo temporary directory"
      return 1
    }
    local demo_dir
    demo_dir="$(mktemp -d "$demo_parent/ori-demo.XXXXXX")" || return 1
    echo "Demo sandbox: $demo_dir   (removed automatically on exit)"
    echo "Branch:       $(git -C "$demo_root" branch --show-current)"
    echo "URL:          http://localhost:$demo_port   (Ctrl-C to stop)"
    local demo_status=0
    {
      (cd "$demo_dir" && HOME="$demo_dir" ORI_DATA_DIR="$demo_dir" PORT="$demo_port" "$demo_root/bin/ori-agent") || demo_status=$?
    } always {
      if [[ "${ORI_KEEP_DEMO_SANDBOX:-0}" == "1" ]]; then
        echo "Demo sandbox preserved: $demo_dir"
      else
        case "$demo_dir" in
          "$demo_parent"/ori-demo.*)
            rm -rf -- "$demo_dir" || {
              echo "Failed to remove demo sandbox: $demo_dir"
              [[ "$demo_status" -ne 0 ]] || demo_status=1
            }
            ;;
          *)
            echo "Refusing to remove unexpected demo sandbox: $demo_dir"
            [[ "$demo_status" -ne 0 ]] || demo_status=1
            ;;
        esac
      fi
    }
    return "$demo_status"
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
    shift
    wt_status "$@"
    ;;
  merge)
    # Removed. It merged straight into the dev worktree with --no-ff, which
    # bypasses PR review and the checks $BASE_BRANCH now requires. There is no
    # single-command replacement, so this names the workflow that replaced it
    # rather than leaving the generic usage dump to be read as a typo.
    echo "wt merge was removed — use wt pr, then wt done after the PR merges"
    return 1
    ;;
  backlog)
    # A signpost, not an alias: forwarding would keep the old spelling alive.
    echo "wt backlog moved to ./scripts/devops.sh"
    echo "  ./scripts/devops.sh            - all open Issues and the interactive REPL"
    echo "  ./scripts/devops.sh decisions  - Issues labeled needs-decision"
    echo "  ./scripts/devops.sh backlog    - Issues labeled backlog"
    echo "  ./scripts/devops.sh proposals  - Issues labeled feature-proposal"
    return 1
    ;;
  *)
    echo "Usage: wt [command] [args]"
    echo "  wt               - Interactive REPL (bare 'wt'; type commands, q to quit)"
    echo "  wt go            - One-shot worktree picker (navigate + cd)"
    echo "  wt plan --issue <N> [--yes] - Start a Codex planning session for a Ready GitHub"
    echo "                     Issue in the dev worktree (PRD-first or tasks-first by size)."
    echo "                     Writes tasks/issue-<feature>.md and a starter checklist there;"
    echo "                     never touches the Issue on GitHub. Run 'wt start <feature>' once"
    echo "                     Codex has replaced the starter with a real plan."
    echo "  wt start [prd] [--kind KIND] [--no-herdr] - Create worktree from a PRD or task list in the dev tasks/ folder"
    echo "  wt new <name> [--kind KIND] [--no-herdr] [--yes] - Ad-hoc worktree (feature/<name>, or <type>/<name>)"
    echo "                     Same guided flow as wt start, minus the planning documents."
    echo "                     --no-herdr on either: bare Git worktree, no Herdr tab or agent."
    echo "                     Herdr is optional throughout; if it is missing or unhealthy the"
    echo "                     worktree is still created and 'wt herd retry' resumes the rest."
    echo "  wt pr [name]     - Push branch and open a PR against $BASE_BRANCH"
    echo "  wt done [name] [--keep-issue-open] [--herdr-override]"
    echo "                     Close an explicitly attached Issue after merge, archive tasks,"
    echo "                     then perform guarded remove/rebase cleanup. --keep-issue-open"
    echo "                     skips the GitHub mutation for an intentional exception."
    echo "                     Closes the feature's Herdr tab only; the workspace and its"
    echo "                     sibling tabs survive. Features created before tab-scoped"
    echo "                     cleanup have their workspace left open for you to close."
    echo "  wt rm [name]     - Remove worktree and branch (interactive if no name)"
    echo "  wt ls            - List worktrees"
    echo "  wt status        - Feature-first overview (--feature/--json/--no-color/--watch)"
    echo "  wt status --worktrees - Show ahead/behind/merged vs $BASE_BRANCH for all worktrees"
    echo "  wt cd <name>     - Navigate to worktree"
    echo "  wt demo [port]   - Build current worktree + serve an isolated demo sandbox (default 8931)"
    echo "  wt herd <sub>    - Manage the opt-in Ori-to-Herdr devflow bridge (setup, doctor, ...)"
    echo "  ./scripts/devops.sh - Open GitHub Issues and curated workflow-label filters"
    echo "                     A standalone executable; no shell or Herdr setup required."
    ;;
  esac
}

# If executed directly (not sourced), show help
if [[ "$ZSH_EVAL_CONTEXT" == "toplevel" ]]; then
  wt help
fi
