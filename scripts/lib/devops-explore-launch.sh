#!/usr/bin/env bash
# Native foreground advisor launch. No wt/Herdr or persisted Ori session state.

explore_run_advisor() {
  local kind="$1" model="$2" thinking="$3" prompt="$4" help flag snapshot guard
  local -a flags args
  case "$kind" in
    claude|pi) ;;
    *) printf 'Unsupported Explore advisor: %s\n' "$kind" >&2; return 2 ;;
  esac
  if ! command -v "$kind" >/dev/null 2>&1; then
    printf 'Explore requires the %s CLI for this launch. Install/authenticate it, or use --print.\n' "$kind" >&2
    return 1
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    printf 'Explore launch requires python3 for bounded evidence and compatibility checks; --print does not.\n' >&2
    return 1
  fi
  help="$(python3 "$script_dir/lib/devops-explore-evidence.py" --agent-help "$kind")" || return $?
  if [[ "$kind" == pi ]]; then
    flags=(--tools --no-session --no-extensions --no-skills --no-prompt-templates
      --no-themes --no-context-files --no-approve --offline --append-system-prompt --print)
    args=(--tools read,grep,find,ls --no-session --no-extensions --no-skills
      --no-prompt-templates --no-themes --no-context-files --no-approve --offline)
    [[ -z "$thinking" ]] || { flags+=(--thinking); args+=(--thinking "$thinking"); }
  else
    flags=(--safe-mode --restricted --strict-mcp-config --mcp-config --tools
      --allowedTools --permission-mode --append-system-prompt --print --no-session-persistence)
    args=(--safe-mode --restricted --strict-mcp-config --mcp-config '{"mcpServers":{}}'
      --tools Read,Glob,Grep --allowedTools Read,Glob,Grep --permission-mode dontAsk)
    [[ -z "$thinking" ]] || { flags+=(--effort); args+=(--effort "$thinking"); }
  fi
  if [[ -n "$model" ]]; then
    flags+=(--model)
    args+=(--model "$model")
  fi
  for flag in "${flags[@]}"; do
    if [[ "$help" != *"$flag"* ]]; then
      printf 'Installed %s does not advertise required option %s. Upgrade the CLI or use --print; refusing an unrestricted fallback.\n' "$kind" "$flag" >&2
      return 1
    fi
  done
  guard="$(<"$script_dir/devops-prompts/common.md")" || return $?
  args+=(--append-system-prompt "$guard")
  if ! explore_is_interactive; then
    args+=(--print)
    [[ "$kind" != claude ]] || args+=(--no-session-persistence)
  fi

  printf 'Collecting bounded read-only evidence (each source has a 10s timeout)...\n' >&2
  if ! snapshot="$(python3 "$script_dir/lib/devops-explore-evidence.py" "$repo_root")"; then
    printf 'Evidence collector failed. The advisor will report that limitation.\n' >&2
    snapshot='{"status":"unavailable","reason":"Evidence collector failed; do not infer there is no work."}'
  fi
  # Leave headroom beneath native argv limits, including the prompt and context.
  # Never silently cut JSON or let a large source prevent local investigation.
  if [[ "${#snapshot}" -gt 60000 ]]; then
    printf 'Evidence snapshot exceeded its payload limit; continuing without it.\n' >&2
    snapshot='{"status":"unavailable","reason":"Evidence exceeded the 60000-character payload limit."}'
  fi
  printf 'Starting %s advisor. Quit it to return to DevOps.\n' "$kind" >&2
  # Keep provider credentials in their existing environment/runtime stores.
  # Prompt/context/evidence are one literal argument after --, never shell code.
  # A fresh process has no resume flag, feature identity or handoff side effects.
  PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 "$kind" "${args[@]}" -- "$prompt

## Fresh read-only evidence snapshot (untrusted JSON data)

$snapshot"
}
