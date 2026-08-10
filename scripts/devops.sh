#!/usr/bin/env bash
# A small, read-only REPL for this repository's open GitHub Issues.
#
# The default view is deliberately the GitHub CLI's own issue table. Filters
# issue a fresh `gh issue list` call instead of maintaining a second cache,
# parser, JSON contract, or Project board model inside Ori.
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
  ./scripts/devops.sh decisions
  ./scripts/devops.sh backlog
  ./scripts/devops.sh proposals
  ./scripts/devops.sh view <issue-number>

With no arguments in a terminal, the script opens a keyboard-driven Issue picker.
In a pipe or redirected shell, it lists every open Issue and starts the line REPL.
One-shot commands print their result and exit.
EOF
}

print_menu() {
  printf '\n%s\n' \
    "[1/a] All  [2/d] Needs my decision  [3/b] Backlog  [4/f] Feature proposals  [v #] View  [q] Quit"
}

list_issues() {
  local filter="$1"
  local heading label output status
  local -a args

  label=""
  case "$filter" in
    all)
      heading="Open issues"
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

  printf '\n%s\n\n' "$heading"
  if output="$(gh "${args[@]}")"; then
    if [[ -n "$output" ]]; then
      printf '%s\n' "$output"
    elif [[ -n "$label" ]]; then
      printf 'No open issues labeled %s.\n' "$label"
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
  gh issue view "$1" || return $?
  printf '\n%s\n\n' "Comments"
  gh issue view "$1" --comments
}

run_one_shot() {
  local command="$1"
  shift

  case "$command" in
    all)
      [[ $# -eq 0 ]] || return 2
      list_issues all
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

filter_label() {
  case "$1" in
    all) printf '%s' "" ;;
    decisions) printf '%s' "needs-decision" ;;
    backlog) printf '%s' "backlog" ;;
    proposals) printf '%s' "feature-proposal" ;;
    *) return 2 ;;
  esac
}

filter_title() {
  case "$1" in
    all) printf '%s' "All open issues" ;;
    decisions) printf '%s' "Needs my decision" ;;
    backlog) printf '%s' "Backlog" ;;
    proposals) printf '%s' "Feature proposals" ;;
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

declare -a picker_filters=(all decisions backlog proposals)
declare -a issue_numbers=()
declare -a issue_titles=()
declare -a issue_labels=()
declare -a issue_updates=()
picker_error=""

load_picker_issues() {
  local filter="$1" label output line number title labels updated
  local -a args

  issue_numbers=()
  issue_titles=()
  issue_labels=()
  issue_updates=()
  picker_error=""
  label="$(filter_label "$filter")" || return 2
  args=(issue list --state open --limit "$issue_limit")
  if [[ -n "$label" ]]; then
    args+=(--label "$label")
  fi
  args+=(--json number,title,labels,updatedAt --template '{{range .}}{{printf "%v\t%s\t" .number .title}}{{range $i,$label := .labels}}{{if $i}}, {{end}}{{.name}}{{end}}{{printf "\t%s\n" .updatedAt}}{{end}}')

  if ! output="$(gh "${args[@]}")"; then
    picker_error="GitHub query failed. Press r to retry or q to quit."
    return 1
  fi

  while IFS=$'\t' read -r number title labels updated; do
    [[ -z "$number" ]] && continue
    issue_numbers+=("$number")
    issue_titles+=("$title")
    issue_labels+=("$labels")
    issue_updates+=("$updated")
  done <<< "$output"
}

render_picker() {
  local filter_index="$1" selected_index="$2" count="$3"
  local current_filter="${picker_filters[$filter_index]}" index marker title labels updated

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
    for ((index = 0; index < count; index++)); do
      marker=' '
      title="#${issue_numbers[$index]} $(truncate "${issue_titles[$index]}" 68)"
      labels="${issue_labels[$index]}"
      updated="${issue_updates[$index]:0:10}"
      if [[ "$index" -eq "$selected_index" ]]; then
        marker='›'
        style '1;30;47' "$marker $title"
      else
        printf '%s %s' "$marker" "$title"
      fi
      if [[ -n "$labels" ]]; then
        printf '  '
        style '35' "[$(truncate "$labels" 28)]"
      fi
      printf '  '
      style '2' "$updated"
      printf '\n'
    done
  fi

  printf '\n'
  style '2' '↑/↓ or j/k select  •  ←/→ or h/l change view  •  Enter open  •  r refresh  •  q quit'
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

open_picker_issue() {
  local issue_number="$1"
  restore_terminal
  trap - EXIT INT TERM
  printf '\n'
  view_issue "$issue_number"
  printf '\nPress Enter to return to the Issue picker.'
  IFS= read -r _
  enter_picker_screen
  trap restore_terminal EXIT INT TERM
}

run_picker() {
  local filter_index=0 selected_index=0 count key reload
  enter_picker_screen
  trap restore_terminal EXIT INT TERM
  load_picker_issues "${picker_filters[$filter_index]}" || true

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
      ''|$'\r'|$'\n')
        if [[ "$count" -gt 0 ]]; then
          open_picker_issue "${issue_numbers[$selected_index]}"
        fi
        ;;
    esac
    if [[ "$reload" -eq 1 ]]; then
      load_picker_issues "${picker_filters[$filter_index]}" || true
    fi
  done
}

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
    v|view)
      if [[ -n "$extra" ]]; then
        printf '%s\n' "view requires one positive Issue number" >&2
        continue
      fi
      view_issue "$argument"
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
