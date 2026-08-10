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

With no arguments, the script lists every open Issue and starts the REPL.
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
