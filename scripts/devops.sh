#!/usr/bin/env bash
# A small REPL for this repository's open GitHub Issues.
#
# The list views are deliberately the GitHub CLI's own issue table. Filters
# issue a fresh `gh issue list` call instead of maintaining a second cache,
# parser, JSON contract, or Project board model inside Ori.
#
# The keyboard picker is the one place that parses JSON, because it needs an
# in-memory index to move between views without re-querying. Its filtering must
# therefore agree with the label filters `gh` applies server-side; the tests
# cover both paths against the same fixtures.
#
# GitHub writes are deliberately limited and confirm-gated: capturing an Issue,
# recording answers to its open questions, adding the `answered` receipt, and
# toggling the `approved` label. `approved` is the pipeline's only human gate, so
# it is never written by the grooming agent - only from here, or by hand on
# github.com.
set -uo pipefail

readonly issue_limit=1000
# A practical cap on how many merged-into-dev PRs `release` will scan looking
# for ones after the latest release's publish instant. GitHub returns merged
# PRs newest-first, so the count this Issue actually cares about - PRs merged
# since the last release - sits well inside this limit for any sane release
# cadence; it exists to keep one query bounded, not to model an unbounded
# repository history.
readonly release_pr_limit=500

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if ! repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel 2>/dev/null)"; then
  printf '%s\n' "devops.sh must live inside a Git checkout" >&2
  exit 2
fi
if ! command -v gh >/dev/null 2>&1; then
  printf '%s\n' "devops.sh requires the GitHub CLI (gh): https://cli.github.com" >&2
  exit 1
fi
cd "$repo_root" || exit 1

print_usage() {
  cat <<'EOF'
Usage:
  ./scripts/devops.sh
  ./scripts/devops.sh all
  ./scripts/devops.sh ready
  ./scripts/devops.sh decisions
  ./scripts/devops.sh backlog
  ./scripts/devops.sh proposals
  ./scripts/devops.sh status
  ./scripts/devops.sh release
  ./scripts/devops.sh view <issue-number>
  ./scripts/devops.sh new <title...> [--body <text> | --body-file <path|->] [--yes]
  ./scripts/devops.sh decide <issue-number> <answers...> [--rationale <text>] [--yes]
  ./scripts/devops.sh answer <issue-number> <answers...> [--rationale <text>] [--yes]
  ./scripts/devops.sh approve <issue-number> [--yes]
  ./scripts/devops.sh unapprove <issue-number> [--yes]

With no arguments in a terminal, the script opens a keyboard-driven Issue picker.
In a pipe or redirected shell, it lists every open Issue and starts the line REPL.
One-shot commands print their result and exit.

`ready` is what you can actually pick up now: feature proposals plus backlog
Issues that are neither already covered by a proposal (`bundled`) nor already
chosen (`approved`).

`status` answers "what am I part-way through": every task list on disk with its
done/total parent groups, and whether its branch has a worktree checked out. It
is entirely local - no network, no Herdr - and reads the dev worktree's
gitignored tasks/, so ticked checkboxes show up before you commit them.

`release` answers "what have I not shipped yet": the latest GitHub Release's
tag and publish time, plus how many PRs have merged into `dev` strictly after
that instant (an exact-timestamp comparison, so a PR merged earlier the same
day as the release is correctly excluded). The keyboard picker shows the same
count at the top and updates it on `r`. Both are read-only; the one-shot command
preserves a failed release or PR read as a non-zero exit with `gh`'s own error
instead of reporting a misleading zero count.

In the picker, Enter opens an Issue with its own action bar; press `c` there to
answer its open questions, or use the list's `c` shortcut to open directly at
those answers. `n` accepts an optional one-line body; enter `:edit` to use
VISUAL or EDITOR for multiline Markdown. For one-shot capture, `--body-file -`
reads the body from stdin.

In the picker's Ready view, pressing `s` on a selected row starts
`wt plan --issue <N>`, which shows its normal confirmation summary and launches
a Herdr-managed Pi planner in the dev worktree. Space marks ordinary backlog
rows and `b` plans at least two marks as one bundle after showing all evidence
and asking for the compatibility affirmation. Feature proposals remain on the
single-Issue path. The same `s` is available from the opened-Issue action bar
(Enter on any row) once that Issue's live labels satisfy `labels_are_ready`.
Any other label state, or a failed label read, is a clear refusal instead.

Planning is asynchronous. Return later and press `i` on the selected or opened
Issue to resolve its completed local task list, choose Claude, Codex, Pi, or a
worktree without Herdr, and delegate the confirmed start to `wt start`.

new/decide/approve/unapprove write to GitHub. They prompt for confirmation on
a terminal, and require --yes when stdin is not a terminal. `answer` remains a
backwards-compatible alias for `decide`. A new Issue is created with no labels -
capture takes ten seconds and the grooming routine specs it. A recorded decision
adds `answered` as a receipt and keeps `needs-decision` until that routine
processes the answer.
EOF
}

print_menu() {
  printf '\n%s\n' \
    "[1/a] All  [2/d] Needs my decision  [3/b] Backlog  [4/f] Proposals  [5/y] Ready" \
    "[v #] View  [n title] New  [c # choices] Decide  [ok #] Approve  [q] Quit"
}

# Labels arrive from `gh` as a ", "-joined string. Split on commas and trim so a
# label matches wherever it sits in the list - substring-matching the joined
# string only ever matched the FIRST label, which silently hid every Issue whose
# target label was not listed first (e.g. `type:fix, backlog, size:quick`).
# A label containing a literal comma would still split wrongly; GitHub allows it
# but this repository has none, and the alternative is a second machine-readable
# field in every template.
labels_contain() {
  local haystack="$1" needle="$2" label
  local old_ifs="$IFS"
  IFS=','
  # Intentional word splitting on commas.
  # shellcheck disable=SC2086
  set -- $haystack
  IFS="$old_ifs"
  for label in "$@"; do
    label="${label#"${label%%[![:space:]]*}"}"
    label="${label%"${label##*[![:space:]]}"}"
    if [[ "$label" == "$needle" ]]; then
      return 0
    fi
  done
  return 1
}

# The one place that knows what "ready to build" means, shared by the picker and
# the one-shot search so the two can never drift.
labels_are_ready() {
  local labels="$1"
  if labels_contain "$labels" "approved"; then
    return 1
  fi
  if labels_contain "$labels" "feature-proposal"; then
    return 0
  fi
  if labels_contain "$labels" "backlog" && ! labels_contain "$labels" "bundled"; then
    return 0
  fi
  return 1
}

size_label_of() {
  local labels="$1"
  for candidate in size:quick size:planned size:prd; do
    if labels_contain "$labels" "$candidate"; then
      printf '%s' "${candidate#size:}"
      return 0
    fi
  done
  printf '%s' ""
}

# Labels worth showing next to a title once size has its own column and the
# filter itself is implied by the view.
other_labels_of() {
  local haystack="$1" label first=1
  local old_ifs="$IFS"
  IFS=','
  # shellcheck disable=SC2086
  set -- $haystack
  IFS="$old_ifs"
  for label in "$@"; do
    label="${label#"${label%%[![:space:]]*}"}"
    label="${label%"${label##*[![:space:]]}"}"
    case "$label" in
      ""|size:*) continue ;;
    esac
    if [[ "$first" -eq 1 ]]; then
      printf '%s' "$label"
      first=0
    else
      printf ', %s' "$label"
    fi
  done
}

# ---------------------------------------------------------------------------
# In-flight status, from plain git and the filesystem.
#
# Deliberately NOT a Herdr integration: scripts/wt-herd.test.sh asserts this
# file never reaches for the devflow bridge, which is the whole point of the
# REPL replacing that helper. The Issue-number-first convention already encodes
# the link in a branch name (`fix/339-slug`) and a task file
# (`tasks/tasks-339-slug.md`), so local git answers "am I implementing this?"
# with no network and no second contract.
#
# Task files are gitignored and live in ONE place - the dev worktree's tasks/ -
# so progress is read from disk. That is deliberately fresher than anything
# pushed: checkboxes get ticked while you work, a pushed copy only at commit.
# ---------------------------------------------------------------------------

tasks_dir=""

# Trusted attachment identity lives only in the generated snapshot header on
# line 3. Issue-authored bodies and comments may contain marker-looking text;
# these readers never scan beyond the header.
snapshot_members=()
read_snapshot_members() {
  local path="$1" heading blank marker match csv member previous=0 old_ifs
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
  local slug="$1" snapshot="$tasks_dir/issue-$slug.md"
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

# Parallel arrays rather than an associative array: this has to run under the
# bash 3.2 that ships with macOS, which has no `declare -A`.
flight_numbers=()
flight_states=()

remember_flight() {
  local number="$1" state="$2" index
  for index in ${flight_numbers[@]+"${!flight_numbers[@]}"}; do
    if [[ "${flight_numbers[$index]}" == "$number" ]]; then
      # worktree beats branch; never downgrade a stronger signal.
      if [[ "${flight_states[$index]}" != "worktree" ]]; then
        flight_states[$index]="$state"
      fi
      return 0
    fi
  done
  flight_numbers+=("$number")
  flight_states+=("$state")
}

# Register a branch under BOTH keys it can be looked up by: its Issue number
# (`fix/339-slug` -> `339`) and its full slug (`339-slug`, or plain
# `workspace-ticket-management` for branches that predate the number-first
# convention). The picker looks up by number; `status` looks up by task-file
# slug, and those older features have no number to match on.
remember_branch_flight() {
  local branch="$1" state="$2" slug

  case "$branch" in
    */*) slug="${branch#*/}" ;;
    *) return 0 ;;
  esac
  [[ -n "$slug" ]] || return 0

  remember_flight "$slug" "$state"
  if [[ "$slug" =~ ^([0-9]+)- ]]; then
    remember_flight "${BASH_REMATCH[1]}" "$state"
  fi
}

index_attached_plan_flights() {
  local file slug state member
  [[ -n "$tasks_dir" ]] || return 0
  for file in "$tasks_dir"/tasks-*.md; do
    [[ -f "$file" ]] || continue
    slug="${file##*/}"
    slug="${slug#tasks-}"
    slug="${slug%.md}"
    load_feature_members "$slug" || continue
    state="$(flight_state_of "$slug")"
    [[ -n "$state" ]] || continue
    for member in "${feature_members[@]}"; do
      remember_flight "$member" "$state"
    done
  done
}

load_flight_index() {
  local line branch

  flight_numbers=()
  flight_states=()

  while IFS= read -r line; do
    case "$line" in
      "branch refs/heads/"*)
        remember_branch_flight "${line#branch refs/heads/}" worktree
        ;;
    esac
  done < <(git worktree list --porcelain 2>/dev/null)

  while IFS= read -r branch; do
    remember_branch_flight "${branch#origin/}" branch
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

# Resolve an Issue to the one exact local task list attached to it. This stays
# offline: the later `i` action reads only generated snapshot headers, tasks/,
# and Git, never GitHub or Herdr.
implementation_feature=""
resolve_implementation_feature() {
  local issue_number="$1" file slug state member_status
  local -a matches=()

  implementation_feature=""
  if [[ ! "$issue_number" =~ ^[1-9][0-9]*$ ]]; then
    printf 'Start implementation requires a positive Issue number.\n' >&2
    return 2
  fi

  resolve_tasks_dir
  if [[ -n "$tasks_dir" ]]; then
    for file in "$tasks_dir"/tasks-*.md; do
      [[ -f "$file" ]] || continue
      slug="${file##*/}"
      slug="${slug#tasks-}"
      slug="${slug%.md}"
      member_status=0
      load_feature_members "$slug" || member_status=$?
      if [[ "$member_status" -eq 2 ]]; then
        # A malformed snapshot whose filename starts with this Issue is an
        # explicit conflict, not permission to fall back to filename matching.
        if [[ "$slug" == "$issue_number"-* ]]; then
          printf 'Cannot resolve #%s: %s has a malformed generated attachment marker on line 3.\n' \
            "$issue_number" "$tasks_dir/issue-$slug.md" >&2
          return 1
        fi
        continue
      fi
      [[ "$member_status" -eq 0 ]] || continue
      if feature_members_include "$issue_number"; then
        matches+=("$file")
      fi
    done
  fi

  if [[ "${#matches[@]}" -eq 0 ]]; then
    printf 'No completed local plan found for #%s. Press s to start Pi planning, then return after the planner writes the real task list.\n' "$issue_number" >&2
    return 1
  fi
  if [[ "${#matches[@]}" -gt 1 ]]; then
    printf 'Multiple task lists match #%s; keep exactly one before starting implementation:\n' "$issue_number" >&2
    for file in "${matches[@]}"; do
      printf '  %s\n' "$file" >&2
    done
    return 1
  fi

  file="${matches[0]}"
  if grep -Fq '<!-- ori-devflow: planning-starter;' "$file"; then
    printf 'Planning for #%s is not complete: %s is still a planning starter. Return after Pi replaces it with the real task list.\n' "$issue_number" "$file" >&2
    return 1
  fi

  slug="${file##*/}"
  slug="${slug#tasks-}"
  slug="${slug%.md}"
  load_flight_index
  state="$(flight_state_of "$slug")"
  if [[ -z "$state" ]]; then
    state="$(flight_state_of "$issue_number")"
  fi
  case "$state" in
    worktree)
      printf 'Implementation for #%s already has a checked-out worktree (%s). Use that worktree instead of starting another.\n' "$issue_number" "$slug" >&2
      return 1
      ;;
    branch)
      printf 'Implementation for #%s already has a branch (%s). Resume it instead of starting duplicate work.\n' "$issue_number" "$slug" >&2
      return 1
      ;;
  esac

  implementation_feature="$slug"
}

# "<done>/<total>" over the task list's PARENT groups - the top-level `- [ ]`
# lines. Sub-tasks are indented, so an anchored match counts groups only.
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

task_progress_of_issue() {
  local number="$1" file slug

  [[ -n "$tasks_dir" ]] || { printf '%s' ""; return 0; }
  for file in "$tasks_dir"/tasks-*.md; do
    [[ -f "$file" ]] || continue
    slug="${file##*/}"
    slug="${slug#tasks-}"
    slug="${slug%.md}"
    load_feature_members "$slug" || continue
    if feature_members_include "$number"; then
      task_groups_of_file "$file"
      return 0
    fi
  done
  printf '%s' ""
}

# One short cell. Progress leads, because "which group am I on" is the question
# this column exists to answer; where the branch lives is the qualifier.
#   2/7 wt  - task list 2 of 7 groups done, worktree checked out
#   2/7 br  - same, but only a branch exists
#   wt / br - work started with no task list
flight_cell_of() {
  local number="$1" state progress short=""

  state="$(flight_state_of "$number")"
  progress="$(task_progress_of_issue "$number")"
  case "$state" in
    worktree) short="wt" ;;
    branch) short="br" ;;
  esac

  if [[ -n "$progress" && -n "$short" ]]; then
    printf '%s %s' "$progress" "$short"
  elif [[ -n "$progress" ]]; then
    printf '%s' "$progress"
  else
    printf '%s' "$short"
  fi
}

list_status() {
  local file slug progress state number member members_cell rows=0

  resolve_tasks_dir
  load_flight_index

  if [[ -z "$tasks_dir" ]]; then
    printf '\nNo tasks/ directory found in any worktree.\n'
    return 0
  fi

  printf '\nIn flight  %s\n\n' "$tasks_dir"
  for file in "$tasks_dir"/tasks-*.md; do
    [[ -f "$file" ]] || continue
    slug="${file##*/}"
    slug="${slug#tasks-}"
    slug="${slug%.md}"
    progress="$(task_groups_of_file "$file")"
    number=""
    members_cell=""
    if load_feature_members "$slug"; then
      for member in "${feature_members[@]}"; do
        if [[ -z "$members_cell" ]]; then
          members_cell="#$member"
        else
          members_cell="$members_cell,#$member"
        fi
      done
      number="${feature_members[0]:-}"
    elif [[ -f "$tasks_dir/issue-$slug.md" ]]; then
      members_cell="invalid"
    fi
    # Slug first: it also covers features whose branch carries no Issue number.
    state="$(flight_state_of "$slug")"
    if [[ -z "$state" && -n "$number" ]]; then
      state="$(flight_state_of "$number")"
    fi
    printf '  %-7s %-9s %-8s %s\n' \
      "${progress:--}" \
      "${state:--}" \
      "$members_cell" \
      "$slug"
    rows=$((rows + 1))
  done

  if [[ "$rows" -eq 0 ]]; then
    printf '  no task lists yet\n'
  fi
  printf '\ngroups done/total  •  where the branch is  •  Issue  •  feature\n'
}

list_issues() {
  local filter="$1"
  local heading label search output status
  local -a args

  label=""
  search=""
  case "$filter" in
    all)
      heading="Open issues"
      ;;
    ready)
      heading="Ready to build"
      search="label:backlog,feature-proposal -label:bundled -label:approved"
      ;;
    decisions)
      heading="Issues needing my decision"
      label="needs-decision"
      ;;
    backlog)
      heading="Backlog issues"
      label="backlog"
      ;;
    proposals)
      heading="Feature proposals"
      label="feature-proposal"
      ;;
    *)
      printf 'Unknown issue filter: %s\n' "$filter" >&2
      return 2
      ;;
  esac

  args=(issue list --state open --limit "$issue_limit")
  if [[ -n "$label" ]]; then
    args+=(--label "$label")
  fi
  if [[ -n "$search" ]]; then
    args+=(--search "$search")
  fi

  printf '\n%s\n\n' "$heading"
  if output="$(gh "${args[@]}")"; then
    if [[ -n "$output" ]]; then
      printf '%s\n' "$output"
    elif [[ -n "$label" ]]; then
      printf 'No open issues labeled %s.\n' "$label"
    elif [[ "$filter" == ready ]]; then
      printf 'Nothing ready to build.\n'
    else
      printf 'No open issues.\n'
    fi
    return 0
  else
    status=$?
    return "$status"
  fi
}

view_issue() {
  if [[ $# -ne 1 || ! "$1" =~ ^[1-9][0-9]*$ ]]; then
    printf '%s\n' "view requires one positive Issue number" >&2
    return 2
  fi
  gh issue view "$1" --comments
}

# Shared release state for the one-shot report and the picker's compact banner.
# load_release_status is read-only and always resets these fields first so a
# failed refresh can never leave stale release data on screen.
release_tag=""
release_published=""
release_url=""
release_merged_count=0

load_release_status() {
  local release_line pr_output
  local number merged title

  release_tag=""
  release_published=""
  release_url=""
  release_merged_count=0

  # `gh release view` with no tag defaults to the latest release. Command
  # substitution does not capture stderr, so callers choose whether failures
  # stay visible (the one-shot report) or become a compact unavailable banner
  # (the picker).
  release_line="$(gh release view --json tagName,publishedAt,url \
    --template '{{printf "%s\t%s\t%s" .tagName .publishedAt .url}}')" || return $?
  IFS=$'\t' read -r release_tag release_published release_url <<< "$release_line"
  if [[ -z "$release_tag" || -z "$release_published" ]]; then
    printf 'could not parse the latest release.\n' >&2
    return 1
  fi

  # Newest-first by default, and release_pr_limit is a practical cap on that
  # scan - see its declaration for why the count this Issue cares about always
  # sits well inside it.
  pr_output="$(gh pr list --state merged --base dev --limit "$release_pr_limit" \
    --json number,mergedAt,title \
    --template '{{range .}}{{printf "%v\t%s\t%s\n" .number .mergedAt .title}}{{end}}')" || return $?

  while IFS=$'\t' read -r number merged title; do
    [[ -z "$number" ]] && continue
    # Exact-timestamp comparison, not a date match: a PR merged earlier the
    # SAME DAY as the release must not count as unreleased just because the
    # two dates are equal. ISO 8601 UTC timestamps sort lexicographically, so
    # a plain string compare is exact.
    if [[ "$merged" > "$release_published" ]]; then
      release_merged_count=$((release_merged_count + 1))
    fi
  done <<< "$pr_output"
}

# release_report answers "what have I not shipped yet" as a one-shot command.
# Every GitHub failure stays non-zero with gh's own stderr rather than being
# swallowed into an empty-looking report.
release_report() {
  load_release_status || return $?

  printf 'Latest release: %s (published %s)\n' "$release_tag" "$release_published"
  printf '%s\n\n' "$release_url"
  if [[ "$release_merged_count" -eq 0 ]]; then
    printf 'No PRs merged into dev since %s.\n' "$release_tag"
  else
    printf '%s PR(s) merged into dev since %s.\n' "$release_merged_count" "$release_tag"
  fi
}

# The one place that reads a single Issue's current labels, in the ", "-joined
# shape labels_contain expects. Shared by the decision guard and the opened-Issue
# action bar so the action offered and the action allowed can never disagree.
issue_labels_of() {
  gh issue view "$1" --json labels \
    --template '{{range $index, $label := .labels}}{{if $index}}, {{end}}{{$label.name}}{{end}}'
}

# Confirmation is required for every write. On a terminal we ask; without one we
# insist on --yes, so a pipe can never post to GitHub by accident.
confirm_write() {
  local prompt="$1" assume_yes="$2" reply

  if [[ "$assume_yes" -eq 1 ]]; then
    return 0
  fi
  if [[ ! -t 0 ]]; then
    printf '%s\n' "refusing to write without a terminal; pass --yes to confirm" >&2
    return 2
  fi
  printf '%s [y/N] ' "$prompt"
  IFS= read -r reply || return 1
  [[ "$reply" == y || "$reply" == Y ]]
}

print_indented() {
  local value="$1" line
  while IFS= read -r line; do
    printf '  %s\n' "$line"
  done <<< "$value"
}

# A stable marker lets the grooming routine distinguish a deliberate decision
# from an unrelated follow-up comment without trying to infer intent from prose.
format_decision_comment() {
  local answers="$1" rationale="${2:-}"

  printf '<!-- ori-decision -->\n\n**Answers:** %s' "$answers"
  if [[ "$rationale" =~ [^[:space:]] ]]; then
    printf '\n\n**Rationale:** %s' "$rationale"
  fi
}

decision_recorded=0
decide_issue() {
  local assume_yes=0 expecting_rationale=0 rationale=""

  decision_recorded=0
  local -a rest=()
  local argument number answers body labels label_status=0 answered_status=0

  for argument in "$@"; do
    if [[ "$expecting_rationale" -eq 1 ]]; then
      rationale="$argument"
      expecting_rationale=0
      continue
    fi
    case "$argument" in
      --yes) assume_yes=1 ;;
      --rationale) expecting_rationale=1 ;;
      *) rest+=("$argument") ;;
    esac
  done

  if [[ "$expecting_rationale" -eq 1 ]]; then
    printf '%s\n' "--rationale requires text" >&2
    return 2
  fi
  if [[ "${#rest[@]}" -lt 2 || ! "${rest[0]}" =~ ^[1-9][0-9]*$ ]]; then
    printf '%s\n' "decide requires an Issue number and answers" >&2
    return 2
  fi
  number="${rest[0]}"
  answers="${rest[*]:1}"
  if [[ ! "$answers" =~ [^[:space:]] ]]; then
    printf '%s\n' "decide requires non-empty answers" >&2
    return 2
  fi
  if [[ -n "$rationale" && ! "$rationale" =~ [^[:space:]] ]]; then
    printf '%s\n' "--rationale requires non-empty text" >&2
    return 2
  fi

  # Only an Issue that is actually waiting for a decision can receive one. Every
  # entry point - the picker's c key, the opened-Issue action bar, the line
  # REPL, and the one-shot command - funnels through here, so this single gate
  # covers all four and none of them needs its own copy.
  #
  # The labels are read live rather than taken from the picker's cached row: the
  # row can be minutes old by the time c is pressed, and the grooming routine
  # relabels Issues while the picker is open. A read that fails must fail closed
  # and keep its own status, so a network error is never mistaken for an Issue
  # that simply is not needs-decision.
  labels="$(issue_labels_of "$number")" || label_status=$?
  if [[ "$label_status" -ne 0 ]]; then
    printf 'could not read labels for #%s; not deciding.\n' "$number" >&2
    return "$label_status"
  fi
  if ! labels_contain "$labels" "needs-decision"; then
    printf '#%s is not needs-decision; nothing to decide.\n' "$number" >&2
    return 1
  fi

  body="$(format_decision_comment "$answers" "$rationale")"
  printf '\nWill record this decision on #%s:\n' "$number"
  print_indented "$body"
  printf 'After the comment is posted, the answered label is added; needs-decision stays until the grooming routine processes it.\n'
  confirm_write "Post this decision and mark it answered?" "$assume_yes" || return $?
  gh issue comment "$number" --body "$body" || return $?
  decision_recorded=1

  # The marked comment is the answer of record. `answered` is an additive
  # receipt for humans and automation, so a failure to apply it must be reported
  # but must not turn the successfully posted decision into a failed operation.
  gh issue edit "$number" --add-label answered || answered_status=$?
  if [[ "$answered_status" -ne 0 ]]; then
    printf 'Decision recorded, but the answered label could not be added (GitHub exited %s). It will remain in Needs my decision until grooming triages it.\n' "$answered_status" >&2
    return 0
  fi
  printf 'Decision recorded and marked answered. It will remain in Needs my decision until grooming triages it.\n'
}

# Kept for callers that learned the original command name.
answer_issue() {
  decide_issue "$@"
}

# Capture is meant to take ten seconds: a title is enough, and the grooming
# routine researches the rest on its next run. The new Issue therefore carries
# NO labels - applying `backlog` here would skip the spec step the whole
# pipeline is built around, and `needs-decision` would assert a spec exists.
create_issue() {
  local assume_yes=0 expecting="" body_set=0 body_file_set=0
  local -a rest=()
  local argument title body="" body_file="" status

  for argument in "$@"; do
    if [[ -n "$expecting" ]]; then
      if [[ "$expecting" == body ]]; then
        body="$argument"
      else
        body_file="$argument"
      fi
      expecting=""
      continue
    fi
    case "$argument" in
      --yes) assume_yes=1 ;;
      --body)
        if [[ "$body_set" -eq 1 ]]; then
          printf '%s\n' "--body may only be provided once" >&2
          return 2
        fi
        body_set=1
        expecting="body"
        ;;
      --body-file)
        if [[ "$body_file_set" -eq 1 ]]; then
          printf '%s\n' "--body-file may only be provided once" >&2
          return 2
        fi
        body_file_set=1
        expecting="body-file"
        ;;
      *) rest+=("$argument") ;;
    esac
  done

  if [[ -n "$expecting" ]]; then
    printf '%s requires text\n' "--${expecting}" >&2
    return 2
  fi
  if [[ "$body_set" -eq 1 && "$body_file_set" -eq 1 ]]; then
    printf '%s\n' "use either --body or --body-file, not both" >&2
    return 2
  fi
  if [[ "${#rest[@]}" -eq 0 ]]; then
    printf '%s\n' "new requires a title" >&2
    return 2
  fi
  title="${rest[*]}"
  if [[ ! "$title" =~ [^[:space:]] ]]; then
    printf '%s\n' "new requires a non-empty title" >&2
    return 2
  fi

  if [[ "$body_file_set" -eq 1 ]]; then
    if [[ "$body_file" == - ]]; then
      body="$(cat || exit $?; printf x)"
    elif [[ ! -f "$body_file" || ! -r "$body_file" ]]; then
      printf 'cannot read Issue body file: %s\n' "$body_file" >&2
      return 2
    else
      body="$(cat < "$body_file" || exit $?; printf x)"
    fi
    status=$?
    [[ "$status" -eq 0 ]] || return "$status"
    # The sentinel prevents command substitution from stripping trailing
    # newlines; remove only the byte we appended after the read.
    body="${body%x}"
  fi

  printf '\nWill create an Issue titled:\n'
  print_indented "$title"
  if [[ "$body" =~ [^[:space:]] ]]; then
    printf 'with body:\n'
    print_indented "$body"
  fi
  printf 'It gets no labels; the grooming routine specs and triages it next run.\n'
  confirm_write "Create this Issue?" "$assume_yes" || return $?
  # Pass the title and body through verbatim. GitHub stores them as given, so
  # escaping an ampersand here would leave a literal `&amp;` in the Issue.
  gh issue create --title "$title" --body "$body"
}

set_approved() {
  local action="$1"
  shift
  local assume_yes=0
  local -a rest=()
  local argument number flag

  for argument in "$@"; do
    if [[ "$argument" == "--yes" ]]; then
      assume_yes=1
    else
      rest+=("$argument")
    fi
  done

  if [[ "${#rest[@]}" -ne 1 || ! "${rest[0]}" =~ ^[1-9][0-9]*$ ]]; then
    printf '%s requires one positive Issue number\n' "$action" >&2
    return 2
  fi
  number="${rest[0]}"

  if [[ "$action" == approve ]]; then
    flag="--add-label"
    printf '\nWill add the approved label to #%s.\n' "$number"
  else
    flag="--remove-label"
    printf '\nWill remove the approved label from #%s.\n' "$number"
  fi
  confirm_write "Apply this label change?" "$assume_yes" || return $?
  # --add-label/--remove-label are additive; unlike a labels array write they
  # cannot drop the Issue's other labels.
  gh issue edit "$number" "$flag" approved
}

run_one_shot() {
  local command="$1"
  shift

  case "$command" in
    all)
      [[ $# -eq 0 ]] || return 2
      list_issues all
      ;;
    ready)
      [[ $# -eq 0 ]] || return 2
      list_issues ready
      ;;
    status)
      [[ $# -eq 0 ]] || return 2
      list_status
      ;;
    release)
      [[ $# -eq 0 ]] || return 2
      release_report
      ;;
    decisions|decision)
      [[ $# -eq 0 ]] || return 2
      list_issues decisions
      ;;
    backlog)
      [[ $# -eq 0 ]] || return 2
      list_issues backlog
      ;;
    proposals|proposal)
      [[ $# -eq 0 ]] || return 2
      list_issues proposals
      ;;
    view)
      view_issue "$@"
      ;;
    decide|answer)
      decide_issue "$@"
      ;;
    new|create)
      create_issue "$@"
      ;;
    approve)
      set_approved approve "$@"
      ;;
    unapprove)
      set_approved unapprove "$@"
      ;;
    help|-h|--help)
      [[ $# -eq 0 ]] || return 2
      print_usage
      ;;
    *)
      printf 'Unknown devops command: %s\n\n' "$command" >&2
      print_usage >&2
      return 2
      ;;
  esac
}

filter_title() {
  case "$1" in
    all) printf '%s' "All open issues" ;;
    ready) printf '%s' "Ready to build" ;;
    decisions) printf '%s' "Needs my decision" ;;
    backlog) printf '%s' "Backlog" ;;
    proposals) printf '%s' "Feature proposals" ;;
    *) return 2 ;;
  esac
}

row_matches_filter() {
  local filter="$1" labels="$2"

  case "$filter" in
    all) return 0 ;;
    ready) labels_are_ready "$labels" ;;
    decisions) labels_contain "$labels" "needs-decision" ;;
    backlog) labels_contain "$labels" "backlog" ;;
    proposals) labels_contain "$labels" "feature-proposal" ;;
    *) return 2 ;;
  esac
}

color_enabled=0
if [[ -t 1 && "${TERM:-dumb}" != "dumb" && -z "${NO_COLOR:-}" ]]; then
  color_enabled=1
fi

style() {
  local code="$1"
  shift
  if [[ "$color_enabled" -eq 1 ]]; then
    printf '\033[%sm%s\033[0m' "$code" "$*"
  else
    printf '%s' "$*"
  fi
}

truncate() {
  local value="$1" limit="$2"
  if [[ "${#value}" -le "$limit" ]]; then
    printf '%s' "$value"
  else
    printf '%s...' "${value:0:$((limit - 3))}"
  fi
}

declare -a picker_filters=(ready all decisions backlog proposals)
declare -a all_issue_numbers=()
declare -a all_issue_titles=()
declare -a all_issue_labels=()
declare -a all_issue_updates=()
declare -a issue_numbers=()
declare -a issue_titles=()
declare -a issue_labels=()
declare -a issue_updates=()
# Bundle marks are immutable Issue numbers, never row indexes. Parallel-array
# helpers keep this compatible with the Bash 3.2 shipped by macOS.
declare -a bundle_mark_numbers=()
bundle_mark_notice=""
picker_error=""
picker_release_summary=""
picker_release_error=""
picker_release_count=0

bundle_labels_are_eligible() {
  local labels="$1"
  labels_are_ready "$labels" &&
    labels_contain "$labels" "backlog" &&
    ! labels_contain "$labels" "feature-proposal"
}

bundle_mark_index_of() {
  local number="$1" index
  for index in ${bundle_mark_numbers[@]+"${!bundle_mark_numbers[@]}"}; do
    if [[ "${bundle_mark_numbers[$index]}" == "$number" ]]; then
      printf '%s' "$index"
      return 0
    fi
  done
  return 1
}

bundle_mark_contains() {
  bundle_mark_index_of "$1" >/dev/null
}

bundle_mark_remove() {
  local number="$1" marked
  local -a kept=()
  for marked in ${bundle_mark_numbers[@]+"${bundle_mark_numbers[@]}"}; do
    [[ "$marked" == "$number" ]] || kept+=("$marked")
  done
  bundle_mark_numbers=("${kept[@]+"${kept[@]}"}")
}

bundle_mark_toggle() {
  local view="$1" number="$2" labels="$3"
  if [[ "$view" != "ready" ]]; then
    bundle_mark_notice="Bundle marks are available only in the Ready view."
    return 1
  fi
  if [[ ! "$number" =~ ^[1-9][0-9]*$ ]] || ! bundle_labels_are_eligible "$labels"; then
    bundle_mark_notice="Only ordinary Ready backlog Issues can be marked; feature proposals stay on the single-Issue plan path."
    return 1
  fi
  if bundle_mark_contains "$number"; then
    bundle_mark_remove "$number"
    bundle_mark_notice="Unmarked #$number."
  else
    bundle_mark_numbers+=("$number")
    bundle_mark_notice="Marked #$number for bundle planning."
  fi
}

bundle_mark_is_live_eligible() {
  local number="$1" index
  for index in ${all_issue_numbers[@]+"${!all_issue_numbers[@]}"}; do
    if [[ "${all_issue_numbers[$index]}" == "$number" ]]; then
      bundle_labels_are_eligible "${all_issue_labels[$index]}"
      return $?
    fi
  done
  return 1
}

prune_bundle_marks() {
  local number removed_text="" removed_count=0
  local -a kept=()
  for number in ${bundle_mark_numbers[@]+"${bundle_mark_numbers[@]}"}; do
    if bundle_mark_is_live_eligible "$number"; then
      kept+=("$number")
    else
      removed_count=$((removed_count + 1))
      if [[ -z "$removed_text" ]]; then
        removed_text="#$number"
      else
        removed_text="$removed_text, #$number"
      fi
    fi
  done
  bundle_mark_numbers=("${kept[@]+"${kept[@]}"}")
  if [[ "$removed_count" -gt 0 ]]; then
    bundle_mark_notice="Refresh removed $removed_count stale or ineligible bundle mark(s): $removed_text."
  fi
}

load_picker_release_status() {
  picker_release_summary=""
  picker_release_error=""
  picker_release_count=0

  if ! load_release_status 2>/dev/null; then
    picker_release_error="Release status unavailable — press r to retry."
    return 0
  fi

  picker_release_count="$release_merged_count"
  case "$release_merged_count" in
    0) picker_release_summary="No PRs merged into dev since $release_tag." ;;
    1) picker_release_summary="1 PR merged into dev since $release_tag." ;;
    *) picker_release_summary="$release_merged_count PRs merged into dev since $release_tag." ;;
  esac
}

load_picker_index() {
  local output number title labels updated
  local -a args

  all_issue_numbers=()
  all_issue_titles=()
  all_issue_labels=()
  all_issue_updates=()
  issue_numbers=()
  issue_titles=()
  issue_labels=()
  issue_updates=()
  picker_error=""

  # Local state and release status are refreshed on initial load and explicit
  # refresh rather than on every render or view change. That keeps the banner
  # current without turning arrow-key navigation into network traffic.
  resolve_tasks_dir
  load_flight_index
  load_picker_release_status

  args=(issue list --state open --limit "$issue_limit")
  args+=(--json number,title,labels,updatedAt --template '{{range .}}{{printf "%v\t%s\t" .number .title}}{{range $i,$label := .labels}}{{if $i}}, {{end}}{{.name}}{{end}}{{printf "\t%s\n" .updatedAt}}{{end}}')

  if ! output="$(gh "${args[@]}")"; then
    picker_error="GitHub query failed. Press r to retry or q to quit."
    return 1
  fi

  while IFS=$'\t' read -r number title labels updated; do
    [[ -z "$number" ]] && continue
    all_issue_numbers+=("$number")
    all_issue_titles+=("$title")
    all_issue_labels+=("$labels")
    all_issue_updates+=("$updated")
  done <<< "$output"
}

apply_picker_filter() {
  local filter="$1" index

  issue_numbers=()
  issue_titles=()
  issue_labels=()
  issue_updates=()
  for index in "${!all_issue_numbers[@]}"; do
    if row_matches_filter "$filter" "${all_issue_labels[$index]}"; then
      issue_numbers+=("${all_issue_numbers[$index]}")
      issue_titles+=("${all_issue_titles[$index]}")
      issue_labels+=("${all_issue_labels[$index]}")
      issue_updates+=("${all_issue_updates[$index]}")
    fi
  done
}

render_picker() {
  local filter_index="$1" selected_index="$2" count="$3"
  local current_filter="${picker_filters[$filter_index]}" index cursor mark title labels size updated
  local id id_width row labels_cell flight marked_count="${#bundle_mark_numbers[@]}"
  local -r title_width=52 size_width=7 flight_width=8 labels_width=26

  printf '\033[H\033[2J'
  style '1;36' 'Ori DevOps'
  printf '  '
  style '2' 'open GitHub Issues'
  printf '\n'
  style '2' 'Release'
  printf '  '
  if [[ -n "$picker_release_summary" ]]; then
    if [[ "$picker_release_count" -eq 0 ]]; then
      style '1;32' "$picker_release_summary"
    else
      style '1;33' "$picker_release_summary"
    fi
  else
    style '1;31' "${picker_release_error:-Release status loading...}"
  fi
  printf '\n\n'
  for index in "${!picker_filters[@]}"; do
    if [[ "$index" -eq "$filter_index" ]]; then
      style '1;30;46' " ${picker_filters[$index]} "
    else
      style '2' " ${picker_filters[$index]} "
    fi
    printf ' '
  done
  printf '\n\n'
  style '1' "$(filter_title "$current_filter")"
  printf '  '
  style '2' "$count issue(s)"
  printf '  '
  if [[ "$marked_count" -gt 0 ]]; then
    style '1;36' "$marked_count marked for bundle"
  else
    style '2' '0 marked for bundle'
  fi
  printf '\n'
  if [[ -n "$bundle_mark_notice" ]]; then
    style '1;33' "$bundle_mark_notice"
    printf '\n'
  fi
  printf '\n'

  if [[ -n "$picker_error" ]]; then
    style '1;31' "$picker_error"
    printf '\n'
  elif [[ "$count" -eq 0 ]]; then
    style '2' 'No matching open issues.'
    printf '\n'
  else
    # Every column is padded to a fixed width so the rows line up. Padding is
    # applied to the plain text BEFORE style() wraps it, because ANSI colour
    # codes are bytes printf would otherwise count toward the field width.
    id_width=0
    for ((index = 0; index < count; index++)); do
      id="#${issue_numbers[$index]}"
      if [[ "${#id}" -gt "$id_width" ]]; then
        id_width="${#id}"
      fi
    done

    for ((index = 0; index < count; index++)); do
      cursor=' '
      mark=' '
      id="#${issue_numbers[$index]}"
      title="$(truncate "${issue_titles[$index]}" "$title_width")"
      size="$(size_label_of "${issue_labels[$index]}")"
      labels="$(other_labels_of "${issue_labels[$index]}")"
      updated="${issue_updates[$index]:0:10}"

      # The selection highlight covers the whole id+title block, so it has to be
      # one already-padded string rather than several styled fragments.
      if [[ "$index" -eq "$selected_index" ]]; then
        cursor='›'
      fi
      if bundle_mark_contains "${issue_numbers[$index]}"; then
        mark='●'
      fi
      row="$(printf '%s%s %-*s %-*s' "$cursor" "$mark" "$id_width" "$id" "$title_width" "$title")"
      if [[ "$index" -eq "$selected_index" ]]; then
        style '1;30;47' "$row"
      else
        printf '%s' "$row"
      fi

      # Size gets its own fixed column so it can never be truncated away by a
      # long label list - it is the signal that says whether to write a PRD.
      printf '  '
      if [[ -n "$size" ]]; then
        case "$size" in
          quick) style '1;32' "$(printf '%-*s' "$size_width" "$size")" ;;
          planned) style '1;33' "$(printf '%-*s' "$size_width" "$size")" ;;
          *) style '1;31' "$(printf '%-*s' "$size_width" "$size")" ;;
        esac
      else
        style '2' "$(printf '%-*s' "$size_width" '-')"
      fi

      # Where the work already is: a checked-out worktree or an existing branch,
      # plus how far through its task list. Entirely local; see the block above
      # list_issues for why this is not a Herdr call.
      flight="$(flight_cell_of "${issue_numbers[$index]}")"
      printf '  '
      if [[ -n "$flight" ]]; then
        style '1;36' "$(printf '%-*s' "$flight_width" "$flight")"
      else
        style '2' "$(printf '%-*s' "$flight_width" '')"
      fi

      labels_cell=""
      if [[ -n "$labels" ]]; then
        labels_cell="[$(truncate "$labels" $((labels_width - 2)))]"
      fi
      printf '  '
      style '35' "$(printf '%-*s' "$labels_width" "$labels_cell")"

      printf '  '
      style '2' "$updated"
      printf '\n'
    done
  fi

  printf '\n'
  style '2' '↑/↓ or j/k select  •  ←/→ or h/l change view  •  Space mark/unmark  •  b plan marked bundle'
  printf '\n'
  style '2' 'Enter open  •  c decide  •  n new  •  o approve  •  s plan one  •  i start implementation  •  r refresh  •  ? help  •  q quit'
  printf '\n'
}

read_picker_key() {
  local first rest
  IFS= read -rsn1 first
  if [[ "$first" == $'\e' ]]; then
    IFS= read -rsn2 rest
    printf '%s' "$first$rest"
  else
    printf '%s' "$first"
  fi
}

restore_terminal() {
  if [[ -n "${picker_stty:-}" ]]; then
    stty "$picker_stty"
  fi
  printf '\033[?25h\033[?1049l'
}

enter_picker_screen() {
  picker_stty="$(stty -g)"
  stty -echo -icanon min 1 time 0
  printf '\033[?1049h\033[?25l'
}

# Every full-screen escape hatch shares this shape: drop out of the alternate
# screen and canonical-mode-off, run the interaction, then go back.
with_normal_terminal() {
  restore_terminal
  trap - EXIT INT TERM
  printf '\n'
  "$@"
  printf '\nPress Enter to return to the Issue picker.'
  IFS= read -r _
  enter_picker_screen
  trap restore_terminal EXIT INT TERM
}

# Issue detail owns its own action prompt, so unlike the simple output helper
# above it returns to the picker as soon as that prompt chooses Back.
with_normal_terminal_session() {
  local status

  restore_terminal
  trap - EXIT INT TERM
  printf '\n'
  "$@"
  status=$?
  enter_picker_screen
  trap restore_terminal EXIT INT TERM
  return "$status"
}

print_picker_help() {
  cat <<'EOF'
Picker keys

  ↑/↓, j/k      Select an Issue
  ←/→, h/l      Change view
  1..5          All, Decisions, Backlog, Proposals, Ready
  Enter         Open the selected Issue; its action bar offers Decide and Plan
                when eligible, plus Start implementation
  c             Open the selected Issue directly at its decision prompt
  n             Capture a new Issue with an optional body
  o             Add the approved label
  Space         Mark/unmark an ordinary backlog Issue in the Ready view
  b             Plan at least two marked Issues as one affirmed bundle
  s             Start asynchronous Pi planning for the selected Ready Issue
  i             Start implementation from a completed local plan; choose Claude,
                Codex, Pi, worktree-only, or cancel
  r             Refresh from GitHub
  ?             Show this help
  q             Quit

Bundle marks survive view changes. Refresh removes marks that disappeared or
stopped being ordinary Ready backlog Issues; feature proposals remain on the
single-Issue s path. Bundle planning shows every selected Issue's evidence and
asks you to affirm a shared root cause, shared files, or the same UI surface.

Writes always show a preview and ask for confirmation. Recorded decisions add
the answered label as a receipt and leave needs-decision in place until the
grooming routine processes them.
EOF
}

prompt_decision_answers() {
  local issue_number="$1" answers rationale

  decision_recorded=0
  printf '\nRecord a decision for #%s (e.g. "1B, 2A"); empty answers cancel.\n' "$issue_number"
  printf 'answers> '
  IFS= read -r answers || return 0
  if [[ ! "$answers" =~ [^[:space:]] ]]; then
    printf 'Cancelled.\n'
    return 0
  fi
  printf 'rationale (optional)> '
  IFS= read -r rationale || return 0
  if [[ "$rationale" =~ [^[:space:]] ]]; then
    decide_issue "$issue_number" "$answers" --rationale "$rationale"
  else
    decide_issue "$issue_number" "$answers"
  fi
}

# The opened Issue is an interaction, not a dead-end view. Enter reaches this
# action bar; the picker's c key passes an initial c to skip straight to answers.
#
# Unlike the picker's footer - one line shared by every row - this bar is drawn
# for one known Issue, so it can offer Decide only where a decision is actually
# pending. An Issue whose spec says "Open questions: None" has nothing to decide,
# and showing the action there just invites a rejected write.
prompt_open_issue() {
  local issue_number="$1" action="${2:-}" labels can_decide=1 can_plan=0 bar

  view_issue "$issue_number" || return $?
  # A failed lookup deliberately leaves Decide on offer: decide_issue re-reads
  # and fails closed, so the worst case is a clear refusal, whereas hiding the
  # action on a transient network error would look like the Issue changed.
  # Plan is the opposite: labels_are_ready is the first gate on launching a
  # planner, so a failed read must leave can_plan at its unready default rather
  # than fail open. wt plan performs the final fresh eligibility check.
  if labels="$(issue_labels_of "$issue_number")"; then
    labels_contain "$labels" "needs-decision" || can_decide=0
    labels_are_ready "$labels" && can_plan=1
  fi

  while true; do
    if [[ -z "$action" ]]; then
      bar="[i] Start implementation  [r] Refresh  [Enter] Back"
      [[ "$can_plan" -eq 1 ]] && bar="[s] Plan  $bar"
      [[ "$can_decide" -eq 1 ]] && bar="[c] Decide  $bar"
      printf '\n%s\n' "$bar"
      printf 'issue> '
      IFS= read -r action || return 0
    fi

    case "$action" in
      c|decide)
        if [[ "$can_decide" -eq 0 ]]; then
          # Reached from the picker's c key, which cannot know the row is
          # ineligible until the Issue is opened. Say so instead of collecting
          # answers that decide_issue would only refuse.
          printf '#%s is not needs-decision; nothing to decide.\n' "$issue_number" >&2
          action=""
          continue
        fi
        prompt_decision_answers "$issue_number" || true
        if [[ "$decision_recorded" -eq 1 ]]; then
          printf '\nUpdated Issue:\n\n'
          view_issue "$issue_number" || return $?
          decision_recorded=0
        fi
        ;;
      s|plan)
        start_issue_plan "$issue_number" "$can_plan" || true
        ;;
      i|implement|implementation)
        start_issue_implementation "$issue_number" || true
        ;;
      r|refresh)
        view_issue "$issue_number" || return $?
        ;;
      ""|b|back|q|quit)
        return 0
        ;;
      *)
        printf 'Unknown Issue action: %s\n' "$action" >&2
        ;;
    esac
    action=""
  done
}

prompt_approve_issue() {
  local issue_number="$1"
  set_approved approve "$issue_number"
}

# wt is a sourced zsh function, while this picker is a standalone bash
# process. Start it in a short-lived zsh child so the existing wt plan flow
# remains the single owner of eligibility revalidation, confirmation, planning
# artifacts, Herdr placement, and the Pi bootstrap prompt. The Issue number is
# validated before it reaches this argument vector and is never evaluated as
# shell syntax.
launch_pi_plan() {
  local issue_number="$1"

  if ! command -v zsh >/dev/null 2>&1; then
    printf 'Planning requires zsh to run scripts/wt.sh.\n' >&2
    return 1
  fi
  if [[ ! -f "$script_dir/wt.sh" ]]; then
    printf 'Planning entrypoint not found: %s\n' "$script_dir/wt.sh" >&2
    return 1
  fi
  zsh -c 'source "$1" && wt plan --issue "$2"' devops-plan "$script_dir/wt.sh" "$issue_number"
}

# Each selected number crosses the bash-to-zsh boundary as its own positional
# argument. The fixed child script builds a zsh array and never evaluates Issue
# data as code.
launch_pi_bundle_plan() {
  local number seen
  local -a numbers=("$@") validated=()

  if [[ "${#numbers[@]}" -lt 2 ]]; then
    printf 'Bundle planning requires at least two distinct marked Issues; use s for one Issue.\n' >&2
    return 1
  fi
  for number in "${numbers[@]}"; do
    if [[ ! "$number" =~ ^[1-9][0-9]*$ ]]; then
      printf 'Bundle planning received an invalid Issue number: %s\n' "$number" >&2
      return 1
    fi
    for seen in ${validated[@]+"${validated[@]}"}; do
      if [[ "$seen" == "$number" ]]; then
        printf 'Bundle planning received duplicate Issue #%s.\n' "$number" >&2
        return 1
      fi
    done
    validated+=("$number")
  done
  if ! command -v zsh >/dev/null 2>&1; then
    printf 'Planning requires zsh to run scripts/wt.sh.\n' >&2
    return 1
  fi
  if [[ ! -f "$script_dir/wt.sh" ]]; then
    printf 'Planning entrypoint not found: %s\n' "$script_dir/wt.sh" >&2
    return 1
  fi
  zsh -c 'source "$1" || exit; shift; typeset -a plan_args; plan_args=(); for issue in "$@"; do plan_args+=(--issue "$issue"); done; wt plan "${plan_args[@]}"' \
    devops-bundle-plan "$script_dir/wt.sh" "${validated[@]}"
}

start_bundle_plan() {
  local view="$1" number seen
  shift
  local -a numbers=("$@") validated=()

  if [[ "$view" != "ready" ]]; then
    printf 'Switch to the Ready view to plan the marked bundle.\n' >&2
    return 1
  fi
  if [[ "${#numbers[@]}" -lt 2 ]]; then
    printf 'Mark at least two ordinary Ready backlog Issues; use s to plan one Issue.\n' >&2
    return 1
  fi
  for number in "${numbers[@]}"; do
    if [[ ! "$number" =~ ^[1-9][0-9]*$ ]] || ! bundle_mark_is_live_eligible "$number"; then
      printf 'Marked Issue #%s is missing or no longer an ordinary Ready backlog Issue; press r to refresh marks.\n' "$number" >&2
      return 1
    fi
    for seen in ${validated[@]+"${validated[@]}"}; do
      if [[ "$seen" == "$number" ]]; then
        printf 'Marked Issue #%s appears more than once; unmark it and retry.\n' "$number" >&2
        return 1
      fi
    done
    validated+=("$number")
  done
  launch_pi_bundle_plan "${validated[@]}"
}

# start_plan is the Ready-list `s` key. It refuses stale or malformed picker
# state locally, then delegates the real operation to wt plan, which performs
# its own fresh GitHub eligibility check before writing or launching anything.
start_plan() {
  local view="$1" count="$2" issue_number="$3"

  if [[ "$view" != "ready" ]]; then
    printf 'Switch to the Ready view to start planning.\n' >&2
    return 1
  fi
  if [[ "$count" -le 0 || -z "$issue_number" || ! "$issue_number" =~ ^[1-9][0-9]*$ ]]; then
    printf 'No Ready row is selected.\n' >&2
    return 1
  fi
  launch_pi_plan "$issue_number"
}

# start_issue_plan is the opened-Issue action bar equivalent. can_plan was
# computed from that Issue's live labels, while wt plan still re-reads GitHub
# so eligibility cannot go stale between opening the Issue and confirming.
start_issue_plan() {
  local issue_number="$1" can_plan="$2"

  if [[ "$can_plan" -ne 1 ]]; then
    printf '#%s is not Ready; refusing to start planning.\n' "$issue_number" >&2
    return 1
  fi
  if [[ ! "$issue_number" =~ ^[1-9][0-9]*$ ]]; then
    printf 'No Ready Issue is selected.\n' >&2
    return 1
  fi
  launch_pi_plan "$issue_number"
}

implementation_mode=""
prompt_implementation_agent() {
  local issue_number="$1" feature="$2" choice

  implementation_mode=""
  printf '\nStart implementation for #%s (%s).\n' "$issue_number" "$feature"
  printf '  [1/c] Claude\n'
  printf '  [2/x] Codex\n'
  printf '  [3/p] Pi\n'
  printf '  [4/w] Worktree only (no Herdr handoff)\n'
  printf '  [Enter/q] Cancel\n'
  printf 'agent> '
  IFS= read -r choice || return 0
  case "$choice" in
    1|c|C|claude|Claude) implementation_mode="claude" ;;
    2|x|X|codex|Codex) implementation_mode="codex" ;;
    3|p|P|pi|Pi) implementation_mode="pi" ;;
    4|w|W|worktree|worktree-only) implementation_mode="no-herdr" ;;
    ""|q|Q|cancel|Cancel)
      printf 'Cancelled.\n'
      ;;
    *)
      printf 'Unknown agent choice; cancelled.\n' >&2
      ;;
  esac
}

# wt remains the owner of plan display, confirmation, worktree creation, and
# handoff. The child validates the already constrained mode again and receives
# every value as a separate argument; no Issue-derived text becomes shell code.
launch_implementation() {
  local feature="$1" mode="$2"

  case "$mode" in
    claude|codex|pi|no-herdr) ;;
    *)
      printf 'Unsupported implementation mode: %s\n' "$mode" >&2
      return 2
      ;;
  esac
  if ! command -v zsh >/dev/null 2>&1; then
    printf 'Starting implementation requires zsh to run scripts/wt.sh.\n' >&2
    return 1
  fi
  if [[ ! -f "$script_dir/wt.sh" ]]; then
    printf 'Implementation entrypoint not found: %s\n' "$script_dir/wt.sh" >&2
    return 1
  fi
  zsh -c 'source "$1" && if [[ "$3" == no-herdr ]]; then wt start "$2" --no-herdr; else wt start "$2" --kind "$3"; fi' devops-start "$script_dir/wt.sh" "$feature" "$mode"
}

start_issue_implementation() {
  local issue_number="$1" feature

  resolve_implementation_feature "$issue_number" || return $?
  feature="$implementation_feature"
  prompt_implementation_agent "$issue_number" "$feature"
  [[ -n "$implementation_mode" ]] || return 0
  launch_implementation "$feature" "$implementation_mode"
}

edited_issue_body=""
edit_issue_body() {
  local editor="${VISUAL:-${EDITOR:-}}" file status
  local -a editor_args=()

  edited_issue_body=""
  if [[ -z "$editor" ]]; then
    printf 'Set VISUAL or EDITOR to use :edit.\n' >&2
    return 2
  fi
  file="$(mktemp "${TMPDIR:-/tmp}/ori-issue-body.XXXXXX")" || return $?
  chmod 600 "$file" || { rm -f -- "$file"; return 1; }

  # Splitting supports common values such as "code --wait" while avoiding eval.
  # Editor paths containing spaces should be supplied through a wrapper script.
  read -r -a editor_args <<< "$editor"
  "${editor_args[@]}" "$file"
  status=$?
  if [[ "$status" -ne 0 ]]; then
    rm -f -- "$file"
    printf 'Editor exited with status %s.\n' "$status" >&2
    return "$status"
  fi
  edited_issue_body="$(cat < "$file"; printf x)"
  edited_issue_body="${edited_issue_body%x}"
  rm -f -- "$file"
}

prompt_create_issue() {
  local title body

  printf 'Capture a new Issue. Empty title cancels.\n'
  printf 'title> '
  IFS= read -r title || return 0
  if [[ ! "$title" =~ [^[:space:]] ]]; then
    printf 'Cancelled.\n'
    return 0
  fi

  printf 'Optional one-line body; enter :edit for multiline Markdown; blank skips.\n'
  printf 'body> '
  IFS= read -r body || return 0
  if [[ "$body" == :edit ]]; then
    edit_issue_body || return $?
    body="$edited_issue_body"
  fi

  if [[ "$body" =~ [^[:space:]] ]]; then
    create_issue "$title" --body "$body"
  else
    create_issue "$title"
  fi
}

run_picker() {
  local filter_index=0 selected_index=0 count key reload
  enter_picker_screen
  trap restore_terminal EXIT INT TERM
  if load_picker_index; then
    prune_bundle_marks
  fi
  apply_picker_filter "${picker_filters[$filter_index]}"

  while true; do
    count="${#issue_numbers[@]}"
    if [[ "$selected_index" -ge "$count" ]]; then
      selected_index=$((count > 0 ? count - 1 : 0))
    fi
    render_picker "$filter_index" "$selected_index" "$count"
    key="$(read_picker_key)" || break
    reload=0

    case "$key" in
      q) break ;;
      r) reload=1 ;;
      $'\e[A'|k) ((selected_index > 0)) && selected_index=$((selected_index - 1)) ;;
      $'\e[B'|j) ((selected_index + 1 < count)) && selected_index=$((selected_index + 1)) ;;
      $'\e[D'|h)
        filter_index=$(((filter_index + ${#picker_filters[@]} - 1) % ${#picker_filters[@]}))
        selected_index=0
        reload=1
        ;;
      $'\e[C'|l)
        filter_index=$(((filter_index + 1) % ${#picker_filters[@]}))
        selected_index=0
        reload=1
        ;;
      # Match the line REPL: 1 All, 2 Decisions, 3 Backlog, 4 Proposals,
      # 5 Ready. The picker's array starts with Ready because it is the default.
      1) filter_index=1; selected_index=0; reload=1 ;;
      2) filter_index=2; selected_index=0; reload=1 ;;
      3) filter_index=3; selected_index=0; reload=1 ;;
      4) filter_index=4; selected_index=0; reload=1 ;;
      5) filter_index=0; selected_index=0; reload=1 ;;
      c)
        if [[ "$count" -gt 0 ]]; then
          # c is a shortcut into the opened Issue's own Decide action.
          with_normal_terminal_session prompt_open_issue "${issue_numbers[$selected_index]}" c
        fi
        ;;
      o)
        if [[ "$count" -gt 0 ]]; then
          with_normal_terminal prompt_approve_issue "${issue_numbers[$selected_index]}"
          if load_picker_index; then
            prune_bundle_marks
          fi
          apply_picker_filter "${picker_filters[$filter_index]}"
        fi
        ;;
      n)
        # Capture does not need a selected row, so it works on an empty list.
        with_normal_terminal prompt_create_issue
        if load_picker_index; then
          prune_bundle_marks
        fi
        apply_picker_filter "${picker_filters[$filter_index]}"
        ;;
      ' ')
        if [[ "$count" -gt 0 ]]; then
          bundle_mark_toggle "${picker_filters[$filter_index]}" \
            "${issue_numbers[$selected_index]}" "${issue_labels[$selected_index]}" || true
        fi
        ;;
      b)
        # Keep the cached index and marks after a decline, success, or helper
        # refusal; only an explicit refresh is allowed to prune selection.
        with_normal_terminal start_bundle_plan "${picker_filters[$filter_index]}" \
          "${bundle_mark_numbers[@]+"${bundle_mark_numbers[@]}"}"
        ;;
      s)
        # Drop out of the alternate screen while wt shows its confirmation and
        # launches the Pi planner. Safe on an empty or non-Ready view.
        with_normal_terminal start_plan "${picker_filters[$filter_index]}" "$count" "${issue_numbers[$selected_index]:-}"
        ;;
      i)
        # Planning is asynchronous. Re-read only local artifacts when the owner
        # returns, then let wt own the selected implementation handoff.
        if [[ "$count" -gt 0 ]]; then
          with_normal_terminal start_issue_implementation "${issue_numbers[$selected_index]}"
        fi
        ;;
      '?')
        with_normal_terminal print_picker_help
        ;;
      ''|$'\r'|$'\n')
        if [[ "$count" -gt 0 ]]; then
          with_normal_terminal_session prompt_open_issue "${issue_numbers[$selected_index]}"
        fi
        ;;
    esac
    if [[ "$reload" -eq 1 ]]; then
      if [[ "$key" == r ]]; then
        if load_picker_index; then
          prune_bundle_marks
        fi
      fi
      apply_picker_filter "${picker_filters[$filter_index]}"
    fi
  done
}

# Sourcing the script exposes its pure helpers for unit tests without running
# the REPL. Nothing above this line touches GitHub.
if [[ -n "${DEVOPS_SOURCE_ONLY:-}" ]]; then
  return 0 2>/dev/null || exit 0
fi

if [[ $# -gt 0 ]]; then
  run_one_shot "$@"
  status=$?
  if [[ "$status" -ne 0 ]]; then
    if [[ "$status" -eq 2 ]]; then
      printf '\n' >&2
      print_usage >&2
    fi
    exit "$status"
  fi
  exit 0
fi

if [[ -t 0 && -t 1 ]]; then
  run_picker
  exit 0
fi

current_filter="all"
list_issues "$current_filter"
status=$?
if [[ "$status" -ne 0 ]]; then
  exit "$status"
fi

while true; do
  print_menu
  printf 'devops> '
  if ! IFS= read -r input; then
    printf '\n'
    break
  fi

  command=""
  argument=""
  extra=""
  read -r command argument extra <<< "$input"

  case "$command" in
    ""|r|refresh)
      list_issues "$current_filter"
      ;;
    1|a|all)
      if [[ -n "$argument" || -n "$extra" ]]; then
        printf '%s\n' "all takes no arguments" >&2
        continue
      fi
      current_filter="all"
      list_issues "$current_filter"
      ;;
    2|d|decision|decisions)
      if [[ -n "$argument" || -n "$extra" ]]; then
        printf '%s\n' "decisions takes no arguments" >&2
        continue
      fi
      current_filter="decisions"
      list_issues "$current_filter"
      ;;
    3|b|backlog)
      if [[ -n "$argument" || -n "$extra" ]]; then
        printf '%s\n' "backlog takes no arguments" >&2
        continue
      fi
      current_filter="backlog"
      list_issues "$current_filter"
      ;;
    4|f|proposal|proposals)
      if [[ -n "$argument" || -n "$extra" ]]; then
        printf '%s\n' "proposals takes no arguments" >&2
        continue
      fi
      current_filter="proposals"
      list_issues "$current_filter"
      ;;
    5|y|ready)
      if [[ -n "$argument" || -n "$extra" ]]; then
        printf '%s\n' "ready takes no arguments" >&2
        continue
      fi
      current_filter="ready"
      list_issues "$current_filter"
      ;;
    v|view)
      if [[ -n "$extra" ]]; then
        printf '%s\n' "view requires one positive Issue number" >&2
        continue
      fi
      view_issue "$argument"
      ;;
    n|new)
      if [[ -z "$argument" ]]; then
        printf '%s\n' "new requires a title" >&2
        continue
      fi
      create_issue "$argument" ${extra:+"$extra"}
      ;;
    c|comment|answer|decide)
      if [[ -z "$argument" || -z "$extra" ]]; then
        printf '%s\n' "decide requires an Issue number and answers" >&2
        continue
      fi
      decide_issue "$argument" "$extra"
      ;;
    ok|approve)
      if [[ -n "$extra" ]]; then
        printf '%s\n' "approve requires one positive Issue number" >&2
        continue
      fi
      set_approved approve "$argument"
      ;;
    unapprove)
      if [[ -n "$extra" ]]; then
        printf '%s\n' "unapprove requires one positive Issue number" >&2
        continue
      fi
      set_approved unapprove "$argument"
      ;;
    h|help|'?')
      print_usage
      ;;
    q|quit|exit)
      break
      ;;
    *)
      printf 'Unknown choice: %s\n' "$command" >&2
      ;;
  esac
done
