#!/usr/bin/env bash
# Shared, filesystem-and-Git-only devflow helpers.
#
# This file is sourced by scripts/devops.sh (macOS bash 3.2) and the away
# dispatcher. Keep it free of associative arrays, namerefs, mapfile/readarray,
# and source-time mutations beyond initializing its result arrays.

if [[ "${DEVFLOW_COMMON_LOADED:-0}" == "1" ]]; then
  return 0
fi
DEVFLOW_COMMON_LOADED=1

tasks_dir="${tasks_dir:-}"

# Trusted attachment identity lives only in the generated snapshot header on
# line 3. Issue-authored bodies and comments may contain marker-looking text;
# these readers never scan beyond the header.
snapshot_members=()
read_snapshot_members() {
  local path="$1" heading blank marker csv member previous=0 old_ifs
  local single_re='^<!-- ori-devflow: issue-snapshot; issue=([1-9][0-9]*) -->$'
  local bundle_re='^<!-- ori-devflow: issue-bundle-snapshot; issues=([1-9][0-9]*(,[1-9][0-9]*)+) -->$'

  snapshot_members=()
  {
    IFS= read -r heading || return 1
    IFS= read -r blank || return 1
    IFS= read -r marker || return 1
  } < "$path"
  heading="${heading%$'\r'}"
  blank="${blank%$'\r'}"
  marker="${marker%$'\r'}"
  [[ "$heading" == '# '* && -z "$blank" ]] || return 1

  if [[ "$marker" =~ $single_re ]]; then
    snapshot_members=("${BASH_REMATCH[1]}")
    return 0
  fi
  if [[ ! "$marker" =~ $bundle_re ]]; then
    return 1
  fi
  csv="${BASH_REMATCH[1]}"
  old_ifs="$IFS"
  IFS=','
  # Intentional splitting of the generated numeric CSV marker.
  # shellcheck disable=SC2086
  set -- $csv
  IFS="$old_ifs"
  [[ "$#" -ge 2 ]] || return 1
  for member in "$@"; do
    [[ "$member" =~ ^[1-9][0-9]*$ ]] || return 1
    if [[ "$previous" -gt 0 && "$member" -le "$previous" ]]; then
      snapshot_members=()
      return 1
    fi
    snapshot_members+=("$member")
    previous="$member"
  done
}

feature_members=()
load_feature_members() {
  local slug="$1" snapshot
  snapshot="$tasks_dir/issue-$slug.md"
  feature_members=()
  if [[ -f "$snapshot" ]]; then
    if ! read_snapshot_members "$snapshot"; then
      return 2
    fi
    feature_members=("${snapshot_members[@]}")
    return 0
  fi
  # Preserve number-first plans created before trusted snapshots existed.
  if [[ "$slug" =~ ^([1-9][0-9]*)- ]]; then
    feature_members=("${BASH_REMATCH[1]}")
    return 0
  fi
  return 1
}

feature_members_include() {
  local wanted="$1" member
  for member in ${feature_members[@]+"${feature_members[@]}"}; do
    [[ "$member" == "$wanted" ]] && return 0
  done
  return 1
}

resolve_tasks_dir() {
  local line path

  tasks_dir=""
  # The dev worktree owns tasks/. Find it rather than assuming a path, so this
  # works from any worktree and from a plain clone.
  path=""
  while IFS= read -r line; do
    case "$line" in
      "worktree "*) path="${line#worktree }" ;;
      "branch refs/heads/dev")
        if [[ -d "$path/tasks" ]]; then
          tasks_dir="$path/tasks"
          return 0
        fi
        ;;
    esac
  done < <(git worktree list --porcelain 2>/dev/null)

  if [[ -d "$repo_root/tasks" ]]; then
    tasks_dir="$repo_root/tasks"
  fi
}

task_is_planning_starter() {
  local file="$1"
  grep -Fq '<!-- ori-devflow: planning-starter;' "$file" 2>/dev/null
}

# Parallel arrays rather than an associative array: this has to run under the
# bash 3.2 that ships with macOS, which has no declare -A.
flight_numbers=()
flight_states=()
flight_refs=()
flight_branch_names=()
flight_branch_states=()
flight_branch_refs=()

remember_flight() {
  local number="$1" state="$2" ref="${3:-}" index
  for index in ${flight_numbers[@]+"${!flight_numbers[@]}"}; do
    if [[ "${flight_numbers[$index]}" == "$number" ]]; then
      # worktree beats branch; never downgrade a stronger signal.
      if [[ "${flight_states[$index]}" != "worktree" ]]; then
        flight_states[$index]="$state"
        [[ -n "$ref" ]] && flight_refs[$index]="$ref"
      elif [[ -z "${flight_refs[$index]:-}" && -n "$ref" ]]; then
        flight_refs[$index]="$ref"
      fi
      return 0
    fi
  done
  flight_numbers+=("$number")
  flight_states+=("$state")
  flight_refs+=("$ref")
}

flight_branch_index_of() {
  local branch="$1" index
  for index in ${flight_branch_names[@]+"${!flight_branch_names[@]}"}; do
    if [[ "${flight_branch_names[$index]}" == "$branch" ]]; then
      printf '%s' "$index"
      return 0
    fi
  done
  return 1
}

remember_flight_branch_record() {
  local branch="$1" state="$2" ref="$3" index
  if index="$(flight_branch_index_of "$branch")"; then
    if [[ "${flight_branch_states[$index]}" != "worktree" ]]; then
      flight_branch_states[$index]="$state"
      flight_branch_refs[$index]="$ref"
    fi
    return 0
  fi
  flight_branch_names+=("$branch")
  flight_branch_states+=("$state")
  flight_branch_refs+=("$ref")
}

# Register a branch under BOTH keys it can be looked up by: its Issue number
# (fix/339-slug -> 339) and its full slug. The optional third argument is the
# exact Git ref used for footprint diffs (for example origin/feature/foo).
remember_branch_flight() {
  local branch="$1" state="$2" ref="${3:-$1}" slug

  case "$branch" in
    */*) slug="${branch#*/}" ;;
    *) return 0 ;;
  esac
  [[ -n "$slug" ]] || return 0

  remember_flight_branch_record "$branch" "$state" "$ref"
  remember_flight "$slug" "$state" "$ref"
  if [[ "$slug" =~ ^([0-9]+)- ]]; then
    remember_flight "${BASH_REMATCH[1]}" "$state" "$ref"
  fi
}

flight_ref_of() {
  local number="$1" index
  for index in ${flight_numbers[@]+"${!flight_numbers[@]}"}; do
    if [[ "${flight_numbers[$index]}" == "$number" ]]; then
      printf '%s' "${flight_refs[$index]:-}"
      return 0
    fi
  done
  printf '%s' ""
}

index_attached_plan_flights() {
  local file slug state ref member
  [[ -n "$tasks_dir" ]] || return 0
  for file in "$tasks_dir"/tasks-*.md; do
    [[ -f "$file" ]] || continue
    slug="${file##*/}"
    slug="${slug#tasks-}"
    slug="${slug%.md}"
    load_feature_members "$slug" || continue
    state="$(flight_state_of "$slug")"
    ref="$(flight_ref_of "$slug")"
    [[ -n "$state" ]] || continue
    for member in "${feature_members[@]}"; do
      remember_flight "$member" "$state" "$ref"
    done
  done
}

load_flight_index() {
  local line branch normalized

  flight_numbers=()
  flight_states=()
  flight_refs=()
  flight_branch_names=()
  flight_branch_states=()
  flight_branch_refs=()

  while IFS= read -r line; do
    case "$line" in
      "branch refs/heads/"*)
        branch="${line#branch refs/heads/}"
        remember_branch_flight "$branch" worktree "$branch"
        ;;
    esac
  done < <(git worktree list --porcelain 2>/dev/null)

  while IFS= read -r branch; do
    normalized="${branch#origin/}"
    remember_branch_flight "$normalized" branch "$branch"
  done < <(git branch --all --format='%(refname:short)' 2>/dev/null)

  # A bundle branch's numeric slug prefix identifies only its first member by
  # itself. The trusted local snapshot attaches the same branch/worktree state
  # to every remaining member without consulting GitHub or Herdr.
  index_attached_plan_flights
}

flight_state_of() {
  local number="$1" index
  for index in ${flight_numbers[@]+"${!flight_numbers[@]}"}; do
    if [[ "${flight_numbers[$index]}" == "$number" ]]; then
      printf '%s' "${flight_states[$index]}"
      return 0
    fi
  done
  printf '%s' ""
}

# done/total over the task list's parent groups: top-level checkbox lines only.
task_groups_of_file() {
  local file="$1" done total

  done="$(grep -c '^- \[[xX]\]' "$file" 2>/dev/null)" || done=0
  total="$(grep -c '^- \[[ xX]\]' "$file" 2>/dev/null)" || total=0
  if [[ "${total:-0}" -le 0 ]]; then
    printf '%s' ""
    return 0
  fi
  printf '%s/%s' "${done:-0}" "$total"
}
