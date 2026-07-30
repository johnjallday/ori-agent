#!/bin/zsh
# wt - worktree manager for parallel agent workflows
#
# Usage:
#   source scripts/wt.sh    # Load the function (cd works directly)
#   wt                      # Interactive REPL (type: go, status, start, ...)
#   wt go                   # One-shot worktree picker (navigate + cd)
#   wt start [prd] [--kind KIND] [--no-herdr] # Create a worktree from a PRD in the dev tasks/ folder
#   wt new <name>           # Create a clean worktree (no PRD/tasks)
#   wt pr [name]            # Push branch and open a PR against dev
#   wt done [name] [--herdr-override] # Archive tasks back to dev, then remove worktree+branch
#   wt rm [name]            # Remove worktree and its branch
#   wt ls                   # List worktrees
#   wt status               # Feature-first overview of every feature in the repo
#   wt status --worktrees   # Show ahead/behind/merged vs dev for all worktrees
#   wt cd <name>            # Navigate to a worktree
#   wt demo [port]          # Build current worktree + serve an ISOLATED demo sandbox (default port 8931)
#   wt backlog [sub]        # BACKLOG.md: list | add | sync | prune [days] (scoped commit+push to dev)
#   wt merge [name]         # Local merge into dev (legacy; prefer wt pr)
#
# Backlog is kept in sync with git automatically: `wt start` promotes the PRD's
# feature to ## Doing, `wt done` retires it to ## Shipped with the merged PR
# number, and both push a scoped docs(backlog) commit to dev. Every mutation
# also prunes date-prefixed Shipped / dropped history older than seven days.
#
# Planning docs live in the dev worktree's tasks/ folder (gitignored). Create
# each PRD + task list there, then `wt start` fans a single PRD out into its
# own worktree so one PRD = one branch = one PR.

WORKTREE_DIR="../"
BASE_BRANCH="dev"
PROTECTED_WORKTREES=("ori-agent" "ori-agent-dev")
WT_BACKLOG_RETENTION_DAYS="${WT_BACKLOG_RETENTION_DAYS:-7}"

# Permission mode written into each new feature worktree's
# .claude/settings.local.json. Feature worktrees are isolated and reviewed at
# merge, so they run hotter than dev. Options:
#   acceptEdits       - auto-apply file edits, still gate/deny bash (recommended)
#   bypassPermissions - skip all prompts, fully unattended (use for headless runs)
FEATURE_WORKTREE_PERMISSION_MODE="acceptEdits"

unalias wt 2>/dev/null || true

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

function wt_backlog_file {
  # Echoes the path to the tracked BACKLOG.md, which lives in the dev worktree
  # (where its docs(backlog) commits ride, same as they do by hand today).
  # Returns non-zero if the dev worktree can't be found.
  local dev_path
  dev_path="$(wt_get_dev_worktree)" || return 1
  [[ -z "$dev_path" ]] && return 1
  print -r -- "$dev_path/BACKLOG.md"
}

function wt_backlog_cutoff_date {
  # Prints the oldest date retained in ## Shipped / dropped. macOS and GNU date
  # use different relative-date syntax, so support both without adding another
  # runtime dependency.
  local days="$1" cutoff=""
  if [[ "$days" != <-> ]] || (( days < 1 || days > 3650 )); then
    echo "Backlog: retention days must be a whole number from 1 to 3650." >&2
    return 1
  fi
  cutoff="$(date -v-"${days}"d +%F 2>/dev/null)" \
    || cutoff="$(date -d "$days days ago" +%F 2>/dev/null)" \
    || {
      echo "Backlog: could not calculate the retention cutoff date." >&2
      return 1
    }
  print -r -- "$cutoff"
}

function wt_backlog_prune {
  # Removes only date-prefixed entries older than the retention window from
  # ## Shipped / dropped. Ideas, Doing, and undated history are preserved.
  # Git remains the durable archive for every removed line.
  # Args: $1 = BACKLOG.md path, $2 = retention days (optional).
  local file="$1" days="${2:-$WT_BACKLOG_RETENTION_DAYS}" cutoff tmp
  [[ -f "$file" ]] || { echo "Backlog: no BACKLOG.md at $file" >&2; return 1; }
  cutoff="$(wt_backlog_cutoff_date "$days")" || return 1
  tmp="${file}.wt.$$"
  awk -v cutoff="$cutoff" '
    /^## / {
      shipped = ($0 ~ /^## Shipped \/ dropped *$/)
      print
      next
    }
    shipped && /^- [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9] / {
      entry_date = substr($0, 3, 10)
      if (entry_date < cutoff) next
    }
    { print }
  ' "$file" > "$tmp" || { rm -f "$tmp"; return 1; }
  if cmp -s "$file" "$tmp"; then
    rm -f "$tmp"
    echo "Backlog: no Shipped / dropped entries older than $days days."
    return 0
  fi
  mv "$tmp" "$file" || { rm -f "$tmp"; return 1; }
  echo "Backlog: pruned Shipped / dropped entries older than $days days (before $cutoff)."
}

function wt_backlog_commit_push {
  # Scoped commit of BACKLOG.md ONLY + push to origin/$BASE_BRANCH. The
  # path-limited commit never sweeps up unrelated WIP in the shared dev
  # worktree. On a non-fast-forward push (a concurrent backlog edit landed) it
  # rebases onto origin/$BASE_BRANCH - but only when nothing else is dirty,
  # since rebase can't run over unstaged edits. Non-fatal by contract: returns
  # non-zero on failure, but callers proceed with their real work.
  # Args: $1 = dev worktree path, $2 = commit message, $3 = retention days (optional).
  local dev_path="$1" msg="$2" retention_days="${3:-$WT_BACKLOG_RETENTION_DAYS}"
  wt_backlog_prune "$dev_path/BACKLOG.md" "$retention_days" || return 1
  if [[ -z "$(git -C "$dev_path" status --porcelain -- BACKLOG.md 2>/dev/null)" ]]; then
    echo "Backlog: no BACKLOG.md changes to commit."
    return 0
  fi
  git -C "$dev_path" commit -q -m "$msg" -- BACKLOG.md || return 1
  echo "Backlog: committed - $msg"
  git -C "$dev_path" fetch origin "$BASE_BRANCH" --quiet 2>/dev/null
  if git -C "$dev_path" push -q origin "$BASE_BRANCH" 2>/dev/null; then
    echo "Backlog: pushed to origin/$BASE_BRANCH"
    return 0
  fi
  echo "Backlog: push rejected (origin/$BASE_BRANCH moved); integrating..."
  if [[ -n "$(git -C "$dev_path" status --porcelain --untracked-files=no)" ]]; then
    echo "Backlog: dev worktree has other uncommitted changes; can't auto-rebase."
    echo "  Sync manually: git -C '$dev_path' pull --rebase origin $BASE_BRANCH && git -C '$dev_path' push origin $BASE_BRANCH"
    return 1
  fi
  if git -C "$dev_path" rebase "origin/$BASE_BRANCH" >/dev/null 2>&1; then
    git -C "$dev_path" push -q origin "$BASE_BRANCH" \
      && echo "Backlog: pushed to origin/$BASE_BRANCH" \
      || { echo "Backlog: push still failing; push manually from $dev_path"; return 1; }
  else
    git -C "$dev_path" rebase --abort >/dev/null 2>&1
    echo "Backlog: rebase onto origin/$BASE_BRANCH hit conflicts; resolve in $dev_path and push."
    return 1
  fi
}

function wt_backlog_add_idea {
  # Inserts "- YYYY-MM-DD <text>" after the last bullet of the ## Ideas section
  # (or right after the header if the section is empty). Args: $1 = file,
  # $2 = idea text (verbatim; user supplies any !, #small/#large, tags).
  local file="$1" text="$2"
  local bullet="- $(date +%F) $text"
  local tmp="${file}.wt.$$"
  awk -v nl="$bullet" '
    { L[NR] = $0 }
    END {
      ideas = 0
      for (i = 1; i <= NR; i++) if (L[i] ~ /^## Ideas *$/) { ideas = i; break }
      if (ideas == 0) { for (i = 1; i <= NR; i++) print L[i]; print nl; exit }
      nx = NR + 1
      for (i = ideas + 1; i <= NR; i++) if (L[i] ~ /^## /) { nx = i; break }
      at = ideas
      for (i = ideas + 1; i < nx; i++) if (L[i] ~ /^- /) at = i
      for (i = 1; i <= NR; i++) { print L[i]; if (i == at) print nl }
    }
  ' "$file" > "$tmp" && mv "$tmp" "$file" || { rm -f "$tmp"; return 1; }
}

function wt_backlog_ensure_doing {
  # Appends a Doing entry keyed by the PRD slug, so wt done can retire it on
  # merge. Idempotent: skips if a Doing line already references prd-<feature>.md.
  # Deliberately does NOT touch ## Ideas (rule E forbids rewording/deleting
  # ideas; matching free text to a slug is unsafe). Args: $1 = file, $2 = slug.
  local file="$1" feature="$2"
  local entry="- $feature -> PRD at tasks/prd-$feature.md (started $(date +%F))"
  local tmp="${file}.wt.$$"
  awk -v feature="$feature" -v entry="$entry" '
    { L[NR] = $0 }
    END {
      doing = 0
      for (i = 1; i <= NR; i++) if (L[i] ~ /^## Doing *$/) { doing = i; break }
      if (doing == 0) { for (i = 1; i <= NR; i++) print L[i]; exit }
      nx = NR + 1
      for (i = doing + 1; i <= NR; i++) if (L[i] ~ /^## /) { nx = i; break }
      for (i = doing + 1; i < nx; i++)
        if (index(L[i], "prd-" feature ".md") > 0) { for (j = 1; j <= NR; j++) print L[j]; exit }
      at = doing
      for (i = doing + 1; i < nx; i++) if (L[i] ~ /^- /) at = i
      for (i = 1; i <= NR; i++) { print L[i]; if (i == at) print entry }
    }
  ' "$file" > "$tmp" && mv "$tmp" "$file" || { rm -f "$tmp"; return 1; }
}

function wt_backlog_retire {
  # Moves the ## Doing line for this feature to the top of ## Shipped / dropped,
  # annotated with the merged PR number and date. Falls back to appending a
  # fresh Shipped line if no Doing line matches (shipment still recorded).
  # Matches on prd-<feature>.md first, then a bare-slug substring for
  # hand-written Doing lines. Args: $1 = file, $2 = slug, $3 = PR number (opt).
  local file="$1" feature="$2" prnum="$3"
  local d="$(date +%F)" suffix
  if [[ -n "$prnum" ]]; then suffix="PR #$prnum merged to dev ($d)"; else suffix="merged to dev ($d)"; fi
  local tmp="${file}.wt.$$"
  awk -v feature="$feature" -v suffix="$suffix" -v d="$d" '
    { L[NR] = $0 }
    END {
      doing = 0; shipped = 0
      for (i = 1; i <= NR; i++) {
        if (L[i] ~ /^## Doing *$/) doing = i
        if (L[i] ~ /^## Shipped/) shipped = i
      }
      moved = 0
      if (doing > 0) {
        dn = NR + 1
        for (i = doing + 1; i <= NR; i++) if (L[i] ~ /^## /) { dn = i; break }
        for (i = doing + 1; i < dn; i++)
          if (L[i] ~ /^- / && (index(L[i], "prd-" feature ".md") > 0 || index(L[i], feature) > 0)) { moved = i; break }
      }
      if (moved > 0) { base = L[moved]; sub(/^- /, "", base) } else { base = feature }
      shipline = "- " d " " base " - " suffix
      for (i = 1; i <= NR; i++) {
        if (i == moved) continue
        print L[i]
        if (i == shipped) print shipline
      }
      if (shipped == 0) { print ""; print "## Shipped / dropped"; print shipline }
    }
  ' "$file" > "$tmp" && mv "$tmp" "$file" || { rm -f "$tmp"; return 1; }
}

function wt_backlog_render {
  # Pretty, colorized view of BACKLOG.md for reading in a terminal: a one-line
  # summary, section headers tinted by kind (Ideas cyan / Doing yellow /
  # Shipped green) with a bullet count, and per-bullet emphasis - dimmed date
  # prefix, red "!" priority flag, magenta #tags. Intro prose is folded away.
  # Colours come from wt_color_init and vanish when stdout isn't a tty, but
  # callers should prefer raw `cat` when piped (see the dispatch).
  local file="$1"
  wt_color_init
  awk -v R="$WT_C_RESET" -v B="$WT_C_BOLD" -v D="$WT_C_DIM" \
      -v RED="$WT_C_RED" -v GRN="$WT_C_GREEN" -v YEL="$WT_C_YELLOW" \
      -v CYN="$WT_C_CYAN" -v MAG="$WT_C_MAGENTA" '
    { L[NR] = $0 }
    END {
      ideas = 0; doing = 0; shipped = 0
      for (i = 1; i <= NR; i++) {
        if (L[i] ~ /^## /) {
          c = 0
          for (j = i + 1; j <= NR && L[j] !~ /^## /; j++) if (L[j] ~ /^- /) c++
          cnt[i] = c
          if (L[i] ~ /Ideas/)   ideas = c
          if (L[i] ~ /Doing/)   doing = c
          if (L[i] ~ /Shipped/) shipped = c
        }
      }
      cur = CYN
      for (i = 1; i <= NR; i++) {
        line = L[i]
        if (line ~ /^# /) {
          print B line R
          print D ideas " ideas  •  " doing " doing  •  " shipped " shipped" R
          for (j = i + 1; j <= NR && L[j] !~ /^## /; j++) ;   # fold intro prose
          i = j - 1
          continue
        }
        if (line ~ /^## /) {
          hc = CYN
          if (line ~ /Doing/)   hc = YEL
          if (line ~ /Shipped/) hc = GRN
          cur = hc
          print ""
          print hc B line R "  " D "(" cnt[i] ")" R
          continue
        }
        if (line ~ /^- /) {
          t = substr(line, 3)
          sub(/^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]/, D "&" R, t)
          sub(/^! /, RED B "!" R " ", t)
          gsub(/ ! /, " " RED B "!" R " ", t)
          gsub(/#[a-zA-Z0-9_-]+/, MAG "&" R, t)
          print "  " cur "•" R " " t
          continue
        }
        if (line == "") continue   # spacing is emitted per-header, not per file blank
        print D line R
      }
    }
  ' "$file"
}

function wt_repl {
  # Persistent prompt: read a line and dispatch it as `wt <words>`. Runs in the
  # current shell (wt is sourced), so cd/start still change the shell's dir; the
  # prompt shows the current directory's basename so you can see where you are.
  wt_color_init
  echo "wt REPL - commands: go, status, start, new, pr, done, cd, ls, rm, demo, backlog, herd, merge, help  (q to quit)"
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
  echo "planning artifacts, BACKLOG.md, worktrees, Git, GitHub, and Herdr."
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

function wt_herd {
  # Delegate bridge behavior to a small Go helper rather than growing the
  # worktree manager into a terminal/session implementation. The helper finds
  # repository-local configuration and keeps mutable runtime state outside Git.
  local repo_root helper
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null)" || {
    echo "wt herd must run from an Ori Git worktree"
    return 1
  }
  helper="$repo_root/scripts/herdr-devflow.sh"
  if [[ ! -f "$helper" ]]; then
    echo "Herdr bridge helper not found: $helper"
    return 1
  fi
  bash "$helper" "$@"
}

# --- Guided start flow -------------------------------------------------------
#
# wt start runs in four phases: resolve → plan → confirm → execute. The split is
# the point of the feature. Everything up to and including the confirmation
# summary is pure reading, so declining costs exactly nothing: no branch, no
# worktree, no npm install, no BACKLOG.md commit, no Herdr call happens until
# wt_start_execute runs. Previously all of it happened before you saw any of it.
#
# The plan is a record of decisions, deliberately separate from the code that
# applies it, so the summary cannot drift from what actually runs — and so the
# shell tests can assert individual fields.

function wt_plan_reset {
  typeset -g WT_PLAN_FEATURE="" WT_PLAN_BRANCH="" WT_PLAN_TARGET=""
  typeset -g WT_PLAN_DEV="" WT_PLAN_PRD="" WT_PLAN_TASKS="" WT_PLAN_TASKS_STATE="none"
  typeset -g WT_PLAN_KIND="" WT_PLAN_KIND_DISPLAY="" WT_PLAN_START_AGENT=1 WT_PLAN_COPY_DOCS=1 WT_PLAN_PROMPT=1
  typeset -g WT_PLAN_BACKLOG=0 WT_PLAN_WORKSPACE="" WT_PLAN_WORKSPACE_STATE=""
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
  else
    printf '  %-14s %s\n' "PRD" "${WT_C_DIM}none (ad-hoc)${WT_C_RESET}"
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

  if (( WT_PLAN_BACKLOG )); then
    printf '  %-14s %s  %s\n' "Backlog" "promote to ## Doing in $WT_PLAN_DEV/BACKLOG.md" "$marker ${WT_C_DIM}commits and pushes${WT_C_RESET}"
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
# failure in the backlog or Herdr half leaves a worktree you can still work in.
function wt_start_execute {
  if ! wt_provision_worktree "$WT_PLAN_BRANCH" "$WT_PLAN_FEATURE"; then
    return 1
  fi
  WT_PLAN_TARGET="$WT_PROVISIONED_TARGET"

  if (( WT_PLAN_COPY_DOCS )) && [[ -n "$WT_PLAN_PRD" ]]; then
    echo "Copying PRD and task list into new worktree..."
    mkdir -p "$WT_PLAN_TARGET/tasks"
    cp "$WT_PLAN_PRD" "$WT_PLAN_TARGET/tasks/"
    if [[ "$WT_PLAN_TASKS_STATE" == "present" && -n "$WT_PLAN_TASKS" ]]; then
      cp "$WT_PLAN_TASKS" "$WT_PLAN_TARGET/tasks/"
    elif [[ "$WT_PLAN_TASKS_STATE" == "generate" ]]; then
      wt_write_starter_tasklist "$WT_PLAN_TARGET/tasks/tasks-$WT_PLAN_FEATURE.md" "$WT_PLAN_FEATURE"
    fi
  fi

  # Backlog bookkeeping: record this feature under ## Doing (keyed by the PRD
  # slug) and push, so wt done can retire it on merge. Non-fatal - never blocks
  # starting the feature. Commits land on the dev worktree's tracked
  # BACKLOG.md, not this new feature worktree.
  if (( WT_PLAN_BACKLOG )) && [[ -f "$WT_PLAN_DEV/BACKLOG.md" ]]; then
    wt_backlog_ensure_doing "$WT_PLAN_DEV/BACKLOG.md" "$WT_PLAN_FEATURE" \
      && wt_backlog_commit_push "$WT_PLAN_DEV" "docs(backlog): promote $WT_PLAN_FEATURE to Doing"
  fi

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

function wt_dispatch {
  case "$1" in
  repl)
    wt_repl
    ;;
  start)
    # PRD-driven creation, in four phases: resolve → plan → confirm → execute.
    # Only wt_start_execute mutates anything, so declining below leaves Git,
    # BACKLOG.md, and Herdr exactly as they were.
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

    local -a prd_files
    local f
    for f in "$tasks_dir"/prd-*.md(N); do
      prd_files+=("${f:t}")
    done

    local chosen="" no_herdr=0 primary_kind="" assume_yes=0 start_arg start_index
    local -a start_args
    start_args=("${@:2}")
    start_index=1
    while (( start_index <= ${#start_args[@]} )); do
      start_arg="${start_args[$start_index]}"
      case "$start_arg" in
        --no-herdr)
          no_herdr=1
          ;;
        --yes|-y)
          assume_yes=1
          ;;
        --kind)
          start_index=$(( start_index + 1 ))
          if (( start_index > ${#start_args[@]} )) || [[ "${start_args[$start_index]}" == --* ]]; then
            echo "wt start --kind requires a Herdr agent kind"
            echo "Usage: wt start [feature] [--kind KIND] [--no-herdr] [--yes]"
            return 1
          fi
          if [[ -n "$primary_kind" ]]; then
            echo "wt start accepts --kind only once"
            return 1
          fi
          primary_kind="${start_args[$start_index]}"
          ;;
        --*)
          echo "Unknown wt start option: $start_arg"
          echo "Usage: wt start [feature] [--kind KIND] [--no-herdr] [--yes]"
          return 1
          ;;
        *)
          if [[ -n "$chosen" ]]; then
            echo "wt start accepts one PRD/feature name (got: $chosen and $start_arg)"
            return 1
          fi
          chosen="$start_arg"
          ;;
      esac
      start_index=$(( start_index + 1 ))
    done
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
      if [[ ${#prd_files[@]} -eq 0 ]]; then
        echo "No PRDs found in $tasks_dir (expected prd-*.md)."
        echo "Create a PRD there first, then re-run 'wt start'."
        echo "For work that does not need one: wt new <name>"
        return 1
      fi
      if ! wt_plan_is_interactive; then
        echo "wt start needs a PRD name when there is no terminal to choose from."
        echo "Usage: wt start <feature> [--kind KIND] [--no-herdr]"
        return 1
      fi
      echo "Select a PRD to start (from ${WT_C_CYAN}$tasks_dir${WT_C_RESET}):"
      local i
      for i in {1..${#prd_files[@]}}; do
        local feat="${prd_files[$i]#prd-}"; feat="${feat%.md}"
        local has_tasks="  ${WT_C_DIM}(no task list yet)${WT_C_RESET}"
        [[ -f "$tasks_dir/tasks-$feat.md" ]] && has_tasks=""
        echo "  $i) ${prd_files[$i]}${has_tasks}"
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

    WT_PLAN_FEATURE="${chosen#prd-}"; WT_PLAN_FEATURE="${WT_PLAN_FEATURE%.md}"
    WT_PLAN_BRANCH="feature/$WT_PLAN_FEATURE"
    WT_PLAN_TARGET="$(wt_new_worktree_dir)/$WT_PLAN_FEATURE"
    WT_PLAN_PRD="$tasks_dir/$chosen"
    WT_PLAN_BACKLOG=1
    WT_PLAN_COPY_DOCS=1

    if [[ -f "$tasks_dir/tasks-$WT_PLAN_FEATURE.md" ]]; then
      WT_PLAN_TASKS="$tasks_dir/tasks-$WT_PLAN_FEATURE.md"
      WT_PLAN_TASKS_STATE="present"
    elif wt_plan_is_interactive; then
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
  new)
    # Ad-hoc creation, through the same four phases as wt start. The only
    # differences are the ones FR-23 allows: no planning documents are copied,
    # no BACKLOG.md entry is made, and the agent is started without a bootstrap
    # prompt because there is no PRD or checklist to point it at.
    wt_color_init
    wt_plan_reset

    local name="" no_herdr=0 primary_kind="" assume_yes=0 new_arg new_index
    local -a new_args
    new_args=("${@:2}")
    new_index=1
    while (( new_index <= ${#new_args[@]} )); do
      new_arg="${new_args[$new_index]}"
      case "$new_arg" in
        --no-herdr)
          no_herdr=1
          ;;
        --yes|-y)
          assume_yes=1
          ;;
        --kind)
          new_index=$(( new_index + 1 ))
          if (( new_index > ${#new_args[@]} )) || [[ "${new_args[$new_index]}" == --* ]]; then
            echo "wt new --kind requires a Herdr agent kind"
            echo "Usage: wt new <name> [--kind KIND] [--no-herdr] [--yes]"
            return 1
          fi
          if [[ -n "$primary_kind" ]]; then
            echo "wt new accepts --kind only once"
            return 1
          fi
          primary_kind="${new_args[$new_index]}"
          ;;
        --*)
          echo "Unknown wt new option: $new_arg"
          echo "Usage: wt new <name> [--kind KIND] [--no-herdr] [--yes]"
          return 1
          ;;
        *)
          if [[ -n "$name" ]]; then
            echo "wt new accepts one name (got: $name and $new_arg)"
            return 1
          fi
          name="$new_arg"
          ;;
      esac
      new_index=$(( new_index + 1 ))
    done

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
    WT_PLAN_BACKLOG=0
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
    # Post-merge cleanup: archive the (completed) task list back to dev, remove
    # the worktree + local/remote branch, then rebase the dev worktree onto
    # origin/dev. Meant to run after the feature's PR has been squash-merged.
    local name="" target_path branch merged_num="" herdr_override=0 done_arg
    for done_arg in "${@:2}"; do
      case "$done_arg" in
        --herdr-override)
          herdr_override=1
          ;;
        --*)
          echo "Unknown wt done option: $done_arg"
          echo "Usage: wt done [name] [--herdr-override]"
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

    # Best-effort merged check so we don't discard unmerged work by accident.
    if command -v gh >/dev/null 2>&1; then
      merged_num="$(gh pr list --head "$branch" --state merged --limit 1 \
                 --json number --jq '.[0].number' 2>/dev/null)"
      if [[ -z "$merged_num" ]]; then
        echo "Warning: no merged PR found for '$branch'."
        read "cont?Continue cleaning up anyway? [y/N]: "
        [[ "$cont" == y* ]] || { echo "Aborted"; return 0; }
      fi
    fi

    # Do this after merged-PR resolution but before every existing mutation:
    # backlog retirement, task archival, dirty confirmation, and Git removal.
    if ! wt_done_herdr_guard "$target_path" "$herdr_override"; then
      return 1
    fi

    # Backlog bookkeeping: move this feature from ## Doing to ## Shipped/dropped
    # with the merged PR number, then commit+push on dev. Non-fatal - a failed
    # push here never blocks the worktree cleanup below. $name is the worktree
    # dir = feature slug (matches prd-<slug>.md created by wt start).
    if [[ -f "$dev_path/BACKLOG.md" ]]; then
      wt_backlog_retire "$dev_path/BACKLOG.md" "$name" "$merged_num" \
        && wt_backlog_commit_push "$dev_path" "docs(backlog): ship $name${merged_num:+ (PR #$merged_num)}"
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
    # Purge sandboxes left behind by prior demo runs before creating a new one,
    # so these don't accumulate indefinitely in $TMPDIR across sessions.
    rm -rf "${TMPDIR:-/tmp}"/ori-demo.* 2>/dev/null
    local demo_dir
    demo_dir="$(mktemp -d "${TMPDIR:-/tmp}/ori-demo.XXXXXX")" || return 1
    echo "Demo sandbox: $demo_dir   (safe to rm -rf when done)"
    echo "Branch:       $(git -C "$demo_root" branch --show-current)"
    echo "URL:          http://localhost:$demo_port   (Ctrl-C to stop)"
    (cd "$demo_dir" && HOME="$demo_dir" ORI_DATA_DIR="$demo_dir" PORT="$demo_port" "$demo_root/bin/ori-agent")
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
  backlog)
    # BACKLOG.md capture + sync. Operates on the tracked copy in the dev
    # worktree and commits/pushes scoped changes so the file never drifts.
    wt_color_init
    local bl_file
    bl_file="$(wt_backlog_file)" || { echo "Could not find $BASE_BRANCH worktree"; return 1; }
    if [[ ! -f "$bl_file" ]]; then
      echo "No BACKLOG.md at $bl_file"
      return 1
    fi
    local bl_dev="${bl_file:h}"
    case "$2" in
      ""|list|ls)
        # Pretty colorized view for a terminal; raw file when piped/redirected
        # so downstream tools (grep, less -F, editors) still see plain markdown.
        if [[ -t 1 ]]; then
          wt_backlog_render "$bl_file"
        else
          cat "$bl_file"
        fi
        ;;
      add)
        shift 2
        local idea="$*"
        idea="${idea## }"; idea="${idea%% }"
        if [[ -z "$idea" ]]; then
          echo "Usage: wt backlog add <idea text>"
          return 1
        fi
        wt_backlog_add_idea "$bl_file" "$idea" || { echo "Failed to edit BACKLOG.md"; return 1; }
        wt_backlog_commit_push "$bl_dev" "docs(backlog): add idea - $idea"
        ;;
      sync)
        wt_backlog_commit_push "$bl_dev" "docs(backlog): update"
        ;;
      prune)
        local retention_days="${3:-$WT_BACKLOG_RETENTION_DAYS}"
        wt_backlog_commit_push "$bl_dev" "docs(backlog): prune shipped and dropped history" "$retention_days"
        ;;
      *)
        echo "Usage: wt backlog [list | add <idea> | sync | prune [days]]"
        return 1
        ;;
    esac
    ;;
  *)
    echo "Usage: wt [command] [args]"
    echo "  wt               - Interactive REPL (bare 'wt'; type commands, q to quit)"
    echo "  wt go            - One-shot worktree picker (navigate + cd)"
    echo "  wt start [prd] [--kind KIND] [--no-herdr] - Create worktree from a PRD in the dev tasks/ folder"
    echo "  wt new <name> [--kind KIND] [--no-herdr] [--yes] - Ad-hoc worktree (feature/<name>, or <type>/<name>)"
    echo "                     Same guided flow as wt start, minus planning docs and backlog."
    echo "                     --no-herdr on either: bare Git worktree, no Herdr tab or agent."
    echo "                     Herdr is optional throughout; if it is missing or unhealthy the"
    echo "                     worktree is still created and 'wt herd retry' resumes the rest."
    echo "  wt pr [name]     - Push branch and open a PR against $BASE_BRANCH"
    echo "  wt done [name] [--herdr-override] - Guarded archive/remove/rebase cleanup"
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
    echo "  wt backlog [sub] - BACKLOG.md: list | add <idea> | sync | prune [days] (7-day shipped-history retention)"
    echo "  wt merge [name]  - Local merge into $BASE_BRANCH (legacy; prefer wt pr)"
    ;;
  esac
}

# If executed directly (not sourced), show help
if [[ "$ZSH_EVAL_CONTEXT" == "toplevel" ]]; then
  wt help
fi
