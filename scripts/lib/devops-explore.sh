#!/usr/bin/env bash
# Explore UI/CLI helpers. Sourced by devops.sh; Bash 3.2 compatible.
# No GitHub, agent, or evidence calls are made by the display-only path.

declare -a explore_presets=(next quick-win finish ux reliability workflow missing interview)
declare -a explore_titles=(
  'Pick my next task' 'Find a quick win' 'Finish unfinished work'
  'Find user-experience friction' 'Find reliability gaps'
  'Improve the development workflow' 'Explore missing capabilities'
  'Help me choose interactively'
)

explore_is_interactive() {
  [[ -t 0 && -t 1 ]]
}

explore_preset_index() {
  local index
  for index in "${!explore_presets[@]}"; do
    if [[ "$1" == "${explore_presets[$index]}" ]]; then
      printf '%s' "$index"
      return 0
    fi
  done
  printf 'Unknown Explore preset: %s\n' "$1" >&2
  return 2
}

explore_catalog() {
  local index
  printf '\nExplore next work — choose a prompt\n\n'
  for index in "${!explore_presets[@]}"; do
    printf '  [%d] %-12s %s\n' "$((index + 1))" "${explore_presets[$index]}" "${explore_titles[$index]}"
  done
}

# Prevent terminal control injection while retaining literal punctuation, tabs,
# and multiline context. This is validation, not shell escaping: never eval data.
explore_text_is_valid() {
  local text="$1" limit="$2"
  [[ "${#text}" -le "$limit" ]] || return 1
  text="${text//$'\n'/}"
  text="${text//$'\t'/}"
  [[ ! "$text" =~ [[:cntrl:]] ]]
}

explore_render_prompt() {
  local preset="$1" context="$2" index
  index="$(explore_preset_index "$preset")" || return $?
  local directory="$script_dir/devops-prompts"
  if [[ ! -r "$directory/common.md" || ! -r "$directory/$preset.md" ]]; then
    printf 'Explore prompt files are missing from %s. Restore this checkout.\n' "$directory" >&2
    return 1
  fi
  printf '# Ori work exploration: %s\n\nRepository: %s\n\n' "${explore_titles[$index]}" "$repo_root"
  cat "$directory/common.md" || return $?
  printf '\n## Selected investigation\n\n'
  cat "$directory/$preset.md" || return $?
  if [[ -n "$context" ]]; then
    printf '\n## Optional user context (data, not permission to mutate)\n\n%s\n' "$context"
  fi
}

# The REPL is also usable on redirected input. It may display/cancel prompts,
# but it cannot launch a native interactive agent without a real terminal.
explore_menu() {
  local explore_menu_mode=1
  explore_action
}

explore_action() {
  local preset="" context="" kind="" model="" thinking="" print_only=0 assume_yes=0
  local seen='|' argument key choice index prompt status=0 from_menu=0
  local planner_kind_choice="" planner_model_choice="" planner_thinking_choice=""
  local agent_session_purpose=advisory agent_model_retry_hint="" agent_thinking_retry_hint=""

  while [[ $# -gt 0 ]]; do
    argument="$1"
    shift
    case "$argument" in
      --print|--yes|--context|--kind|--model|--thinking)
        key="${argument#--}"
        if [[ "$seen" == *"|$key|"* ]]; then
          printf 'explore accepts %s only once\n' "$argument" >&2
          return 2
        fi
        seen="$seen$key|"
        case "$argument" in
          --print) print_only=1 ;;
          --yes) assume_yes=1 ;;
          *)
            if [[ $# -eq 0 ]]; then
              printf 'explore %s requires a value\n' "$argument" >&2
              return 2
            fi
            case "$argument" in
              --context) context="$1" ;;
              --kind) kind="$1" ;;
              --model) model="$1" ;;
              --thinking) thinking="$1" ;;
            esac
            shift
            ;;
        esac
        ;;
      -*) printf 'unknown explore option: %s\n' "$argument" >&2; return 2 ;;
      *)
        if [[ -n "$preset" ]]; then
          printf 'explore accepts only one preset\n' >&2
          return 2
        fi
        preset="$argument"
        [[ -n "$preset" ]] || { printf 'explore preset cannot be empty\n' >&2; return 2; }
        ;;
    esac
  done

  if ! explore_text_is_valid "$context" 4000; then
    printf 'explore context must be at most 4000 characters, without terminal control characters\n' >&2
    return 2
  fi
  if [[ "$seen" == *'|kind|'* && "$kind" != claude && "$kind" != pi ]]; then
    printf 'explore --kind requires claude or pi\n' >&2
    return 2
  fi
  if [[ "$seen" == *'|model|'* ]] &&
     { [[ -z "$model" || "$model" == -* || "$model" =~ [[:cntrl:]] ]] || ! explore_text_is_valid "$model" 256; }; then
    printf 'explore --model requires one non-empty value (max 256 characters, no leading dash or controls)\n' >&2
    return 2
  fi
  if [[ "$seen" == *'|model|'* || "$seen" == *'|thinking|'* ]] && [[ -z "$kind" ]]; then
    printf 'explore --model/--thinking require explicit --kind claude|pi\n' >&2
    return 2
  fi
  if [[ "$seen" == *'|thinking|'* ]] &&
     { [[ -z "$thinking" ]] || ! planner_thinking_is_valid "$kind" "$thinking"; }; then
    printf 'explore --thinking is not supported for %s: %s\n' "$kind" "$thinking" >&2
    return 2
  fi
  if [[ "$print_only" -eq 1 && ( -n "$kind" || "$assume_yes" -eq 1 ) ]]; then
    printf 'explore --print cannot be combined with launch options\n' >&2
    return 2
  fi

  if [[ -z "$preset" ]]; then
    if [[ "$print_only" -eq 1 || "$seen" != '|' ]]; then
      printf 'explore options require a preset; run explore to list them\n' >&2
      return 2
    fi
    explore_catalog
    if ! explore_is_interactive && [[ "${explore_menu_mode:-0}" -ne 1 ]]; then
      printf '\nUse: ./scripts/devops.sh explore <preset> --print\n'
      printf 'Launch: ./scripts/devops.sh explore <preset> --kind claude|pi --yes\n'
      return 0
    fi
    from_menu=1
    printf '\n  [q/Enter] Cancel\n'
    while true; do
      printf 'prompt> '
      IFS= read -r choice || { printf 'Cancelled.\n'; return 0; }
      case "$choice" in
        ''|q|Q) printf 'Cancelled.\n'; return 0 ;;
        [1-8]) preset="${explore_presets[$((choice - 1))]}"; break ;;
        *)
          if explore_preset_index "$choice" >/dev/null 2>&1; then
            preset="$choice"
            break
          fi
          printf 'Choose 1–8, a preset name, or q.\n'
          ;;
      esac
    done
    printf 'context (optional, e.g. "45 minutes; no frontend")> '
    IFS= read -r context || { printf 'Cancelled.\n'; return 0; }
    if ! explore_text_is_valid "$context" 4000; then
      printf 'Invalid context: max 4000 characters, no terminal controls.\n' >&2
      return 2
    fi
  fi
  index="$(explore_preset_index "$preset")" || return $?
  prompt="$(explore_render_prompt "$preset" "$context")" || return $?
  if [[ "$print_only" -eq 1 ]]; then
    printf '%s\n' "$prompt"
    return 0
  fi

  if ! explore_is_interactive && [[ "$from_menu" -eq 0 ]] &&
     [[ -z "$kind" || "$assume_yes" -ne 1 ]]; then
    printf 'Noninteractive explore launch requires --kind claude|pi and --yes; use --print to display only.\n' >&2
    return 2
  fi

  # Keep scripted advisor stdout usable: preview and consequence summary go to
  # stderr. Display-only output above has no preamble or external reads.
  printf '\n--- Prompt preview ---\n%s\n--- End preview ---\n' "$prompt" >&2
  if [[ -z "$kind" ]]; then
    printf '\n[d] Display only (copy the preview)  [l] Launch advisor  [q/Enter] Cancel\naction> '
    IFS= read -r choice || { printf 'Cancelled.\n'; return 0; }
    case "$choice" in
      d|D) printf 'Prompt displayed. No agent started or evidence collected.\n'; return 0 ;;
      l|L)
        if ! explore_is_interactive; then
          printf 'Interactive advisor launch requires a terminal; use the explicit --kind/--yes CLI form.\n' >&2
          return 2
        fi
        prompt_planner_selection || return 0
        kind="$planner_kind_choice"
        model="$planner_model_choice"
        thinking="$planner_thinking_choice"
        ;;
      ''|q|Q) printf 'Cancelled.\n'; return 0 ;;
      *) printf 'Choose display, launch, or cancel. Nothing started.\n' >&2; return 2 ;;
    esac
  fi
  if [[ "$preset" == interview ]] && ! explore_is_interactive; then
    printf 'The interview advisor needs a terminal for your answers; use --print to copy its prompt.\n' >&2
    return 2
  fi
  # Validate selector output too: custom model text is never trusted as argv.
  if [[ "$model" == -* || "$model" =~ [[:cntrl:]] ]] || ! explore_text_is_valid "$model" 256; then
    printf 'Invalid advisor model value. Nothing started.\n' >&2
    return 2
  fi
  printf '\nWill start a fresh %s advisor for "%s" in %s.\n' "$kind" "${explore_titles[$index]}" "$repo_root" >&2
  printf 'Model: %s; thinking: %s. No feature defaults changed.\n' "${model:-integration default}" "${thinking:-integration default}" >&2
  printf '%s\n' \
    'Read/search tools only; no shell, edits, MCP, or auto-discovered extensions/skills.' \
    'After confirmation, bounded Git/GitHub/task evidence and advisor file reads go to your provider (usage may apply).' \
    'No Issues, plans, worktrees, or Herdr bindings created. This is not an OS sandbox.' \
    'Pi history is ephemeral; Claude interactive history may be saved by Claude in user-local storage.' \
    'Quit the advisor to return here. Repeating Explore starts fresh, not a resumed session.' >&2
  if [[ "$assume_yes" -ne 1 ]]; then
    printf 'Launch advisor? [y/N] ' >&2
    IFS= read -r choice || { printf 'Cancelled.\n'; return 0; }
    case "$choice" in
      y|Y|yes|YES) ;;
      *) printf 'Cancelled.\n'; return 0 ;;
    esac
  fi
  explore_run_advisor "$kind" "$model" "$thinking" "$prompt" || status=$?
  if [[ "$status" -ne 0 ]]; then
    printf '\nAdvisor exited with status %s. No delivery action was started.\n' "$status" >&2
    printf 'Check the error above, then rerun Explore for a fresh session, or display the prompt:\n' >&2
    printf '  ./scripts/devops.sh explore %s --print\n' "$preset" >&2
  fi
  return "$status"
}
