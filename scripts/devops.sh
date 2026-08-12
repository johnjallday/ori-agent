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
# Writes are limited to two actions, both confirm-gated: answering an Issue's
# open questions with a comment, and toggling the `approved` label. `approved`
# is the pipeline's only human gate, so it is never written by the grooming
# agent - only from here, or by hand on github.com.
set -uo pipefail

readonly issue_limit=1000

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
  ./scripts/devops.sh view <issue-number>
  ./scripts/devops.sh new <title...> [--body <text>] [--yes]
  ./scripts/devops.sh answer <issue-number> <text...> [--yes]
  ./scripts/devops.sh approve <issue-number> [--yes]
  ./scripts/devops.sh unapprove <issue-number> [--yes]

With no arguments in a terminal, the script opens a keyboard-driven Issue picker.
In a pipe or redirected shell, it lists every open Issue and starts the line REPL.
One-shot commands print their result and exit.

`ready` is what you can actually pick up now: feature proposals plus backlog
Issues that are neither already covered by a proposal (`bundled`) nor already
chosen (`approved`).

new/answer/approve/unapprove write to GitHub. They prompt for confirmation on a
terminal, and require --yes when stdin is not a terminal. A new Issue is created
with no labels - capture takes ten seconds and the grooming routine specs it.
EOF
}

print_menu() {
  printf '\n%s\n' \
    "[1/a] All  [2/d] Needs my decision  [3/b] Backlog  [4/f] Proposals  [5/y] Ready" \
    "[v #] View  [n title] New  [c # text] Answer  [ok #] Approve  [q] Quit"
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

answer_issue() {
  local assume_yes=0
  local -a rest=()
  local argument number body

  for argument in "$@"; do
    if [[ "$argument" == "--yes" ]]; then
      assume_yes=1
    else
      rest+=("$argument")
    fi
  done

  if [[ "${#rest[@]}" -lt 2 || ! "${rest[0]}" =~ ^[1-9][0-9]*$ ]]; then
    printf '%s\n' "answer requires an Issue number and comment text" >&2
    return 2
  fi
  number="${rest[0]}"
  body="${rest[*]:1}"
  if [[ -z "${body// /}" ]]; then
    printf '%s\n' "answer requires non-empty comment text" >&2
    return 2
  fi

  printf '\nWill comment on #%s:\n  %s\n' "$number" "$body"
  confirm_write "Post this comment?" "$assume_yes" || return $?
  gh issue comment "$number" --body "$body"
}

# Capture is meant to take ten seconds: a title is enough, and the grooming
# routine researches the rest on its next run. The new Issue therefore carries
# NO labels - applying `backlog` here would skip the spec step the whole
# pipeline is built around, and `needs-decision` would assert a spec exists.
create_issue() {
  local assume_yes=0 expecting_body=0
  local -a rest=()
  local argument title body=""

  for argument in "$@"; do
    if [[ "$expecting_body" -eq 1 ]]; then
      body="$argument"
      expecting_body=0
      continue
    fi
    case "$argument" in
      --yes) assume_yes=1 ;;
      --body) expecting_body=1 ;;
      *) rest+=("$argument") ;;
    esac
  done

  if [[ "$expecting_body" -eq 1 ]]; then
    printf '%s\n' "--body requires text" >&2
    return 2
  fi
  if [[ "${#rest[@]}" -eq 0 ]]; then
    printf '%s\n' "new requires a title" >&2
    return 2
  fi
  title="${rest[*]}"
  if [[ -z "${title// /}" ]]; then
    printf '%s\n' "new requires a non-empty title" >&2
    return 2
  fi

  printf '\nWill create an Issue titled:\n  %s\n' "$title"
  if [[ -n "$body" ]]; then
    printf 'with body:\n  %s\n' "$body"
  fi
  printf 'It gets no labels; the grooming routine specs and triages it next run.\n'
  confirm_write "Create this Issue?" "$assume_yes" || return $?
  # Pass the title through verbatim. GitHub stores it as given, so escaping an
  # ampersand here would leave a literal `&amp;` in the title forever.
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
    answer)
      answer_issue "$@"
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
picker_error=""

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
  local current_filter="${picker_filters[$filter_index]}" index marker title labels size updated
  local id id_width row labels_cell
  local -r title_width=56 size_width=7 labels_width=28

  printf '\033[H\033[2J'
  style '1;36' 'Ori DevOps'
  printf '  '
  style '2' 'open GitHub Issues'
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
  printf '\n\n'

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
      marker=' '
      id="#${issue_numbers[$index]}"
      title="$(truncate "${issue_titles[$index]}" "$title_width")"
      size="$(size_label_of "${issue_labels[$index]}")"
      labels="$(other_labels_of "${issue_labels[$index]}")"
      updated="${issue_updates[$index]:0:10}"

      # The selection highlight covers the whole id+title block, so it has to be
      # one already-padded string rather than several styled fragments.
      if [[ "$index" -eq "$selected_index" ]]; then
        marker='›'
      fi
      row="$(printf '%s %-*s %-*s' "$marker" "$id_width" "$id" "$title_width" "$title")"
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
  style '2' '↑/↓ or j/k select  •  ←/→ or h/l change view  •  Enter open  •  n new  •  c answer  •  o approve  •  r refresh  •  q quit'
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

prompt_answer_issue() {
  local issue_number="$1" body

  printf 'Answer #%s (e.g. "1B, 2A"); empty line cancels.\n' "$issue_number"
  printf 'answer> '
  IFS= read -r body || return 0
  if [[ -z "${body// /}" ]]; then
    printf 'Cancelled.\n'
    return 0
  fi
  answer_issue "$issue_number" "$body"
}

prompt_approve_issue() {
  local issue_number="$1"
  set_approved approve "$issue_number"
}

prompt_create_issue() {
  local title

  printf 'Capture a new Issue. A title is enough; empty line cancels.\n'
  printf 'title> '
  IFS= read -r title || return 0
  if [[ -z "${title// /}" ]]; then
    printf 'Cancelled.\n'
    return 0
  fi
  create_issue "$title"
}

run_picker() {
  local filter_index=0 selected_index=0 count key reload
  enter_picker_screen
  trap restore_terminal EXIT INT TERM
  load_picker_index || true
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
      1) filter_index=0; selected_index=0; reload=1 ;;
      2) filter_index=1; selected_index=0; reload=1 ;;
      3) filter_index=2; selected_index=0; reload=1 ;;
      4) filter_index=3; selected_index=0; reload=1 ;;
      5) filter_index=4; selected_index=0; reload=1 ;;
      c)
        if [[ "$count" -gt 0 ]]; then
          with_normal_terminal prompt_answer_issue "${issue_numbers[$selected_index]}"
          load_picker_index || true
          apply_picker_filter "${picker_filters[$filter_index]}"
        fi
        ;;
      o)
        if [[ "$count" -gt 0 ]]; then
          with_normal_terminal prompt_approve_issue "${issue_numbers[$selected_index]}"
          load_picker_index || true
          apply_picker_filter "${picker_filters[$filter_index]}"
        fi
        ;;
      n)
        # Capture does not need a selected row, so it works on an empty list.
        with_normal_terminal prompt_create_issue
        load_picker_index || true
        apply_picker_filter "${picker_filters[$filter_index]}"
        ;;
      ''|$'\r'|$'\n')
        if [[ "$count" -gt 0 ]]; then
          with_normal_terminal view_issue "${issue_numbers[$selected_index]}"
        fi
        ;;
    esac
    if [[ "$reload" -eq 1 ]]; then
      if [[ "$key" == r ]]; then
        load_picker_index || true
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
    c|comment|answer)
      if [[ -z "$argument" || -z "$extra" ]]; then
        printf '%s\n' "answer requires an Issue number and comment text" >&2
        continue
      fi
      answer_issue "$argument" "$extra"
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
