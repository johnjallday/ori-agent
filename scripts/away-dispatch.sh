#!/usr/bin/env bash
# Queue authorization and eligibility engine for `wt away`.
# Keep this script compatible with the macOS system Bash (3.2).
set -uo pipefail

away_script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if ! repo_root="$(git -C "$away_script_dir/.." rev-parse --show-toplevel 2>/dev/null)"; then
  printf '%s\n' "away-dispatch.sh must live inside a Git checkout" >&2
  exit 2
fi
# shellcheck source=scripts/lib/devflow-common.sh
source "$away_script_dir/lib/devflow-common.sh"

away_dev_root=""
away_tasks_dir=""
away_queue_file=""
away_ledger_file=""
away_wake_state_file=""
away_notify_state_file=""
away_herdr_config_file="$HOME/.config/herdr-remote/config.env"
away_herdr_secrets_file="$HOME/.config/herdr-remote/secrets.env"
away_herdr_source_dir="$HOME/.local/share/herdr-remote/v0.7.5"
away_herdr_expected_commit="ea5a8e2a9820e84d0ca27278b46cbb6e33045916"
away_launch_agents_dir="$HOME/Library/LaunchAgents"
away_launch_plist="$away_launch_agents_dir/com.ori.wt-away-tick.plist"

away_launch_label="com.ori.wt-away-tick"
away_pmset_owner="com.ori.wt-away-tick"
away_pmset_type="wakeorpoweron"
away_pmset_helper_label="com.ori.wt-away-pmset"
away_pmset_helper_path="/Library/PrivilegedHelperTools/com.ori.wt-away-pmset"
away_pmset_helper_plist="/Library/LaunchDaemons/com.ori.wt-away-pmset.plist"
away_pmset_request_dir="/Library/Application Support/Ori/AwayDispatcher/$(id -u)"
away_tick_interval_seconds=1800
away_wake_lead_seconds=120

away_queue_slugs=()
away_queue_dependencies=()
away_queue_notes=()
away_queue_errors=()
away_queue_raw=()

away_plan_files=()
away_overlap_files=()
away_overlap_branch=""
away_metadata_value=""
away_metadata_count=0
away_verdict=""
away_detail=""
away_kind=""
away_model=""
away_active_count=0
away_dispatched_slug=""
away_dispatched_kind=""
away_dispatched_model=""
away_dispatched_branch=""
away_dispatched_worktree=""
away_tick_error=""
away_skip_slugs=()
away_skip_reasons=()
away_skip_details=()
away_notifications_json='[]'
away_notify_error=""

away_usage() {
  cat <<'EOF'
Usage: wt away <command>
  wt away arm      - Allow queued plans to start on scheduled ticks
  wt away disarm   - Stop new dispatches; running agents continue
  wt away status   - Show armed state and dispatcher status
  wt away tick     - Run one dispatch cycle (safe to invoke manually)
EOF
}

away_trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

away_resolve_dev() {
  local line path=""
  away_dev_root=""
  while IFS= read -r line; do
    case "$line" in
      "worktree "*) path="${line#worktree }" ;;
      "branch refs/heads/dev") away_dev_root="$path"; break ;;
    esac
  done < <(git -C "$repo_root" worktree list --porcelain 2>/dev/null)

  if [[ -z "$away_dev_root" ]]; then
    printf 'Could not find dev worktree\n' >&2
    return 1
  fi
  away_tasks_dir="$away_dev_root/tasks"
  away_queue_file="$away_tasks_dir/away-queue.md"
  away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
  away_wake_state_file="$away_tasks_dir/.away-wake.json"
  away_notify_state_file="$away_tasks_dir/.away-notify-state.json"
  tasks_dir="$away_tasks_dir"
}

away_require_no_args() {
  local command="$1"
  shift
  if [[ "$#" -eq 0 ]]; then
    return 0
  fi
  printf 'wt away %s accepts no arguments\n' "$command" >&2
  away_usage >&2
  return 1
}

away_is_safe_slug() {
  local slug="$1"
  [[ "$slug" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || return 1
  [[ "$slug" != "." && "$slug" != ".." ]]
}

away_queue_add() {
  away_queue_slugs+=("$1")
  away_queue_dependencies+=("$2")
  away_queue_notes+=("$3")
  away_queue_errors+=("$4")
  away_queue_raw+=("$5")
}

away_queue_slug_seen() {
  local wanted="$1" slug
  for slug in ${away_queue_slugs[@]+"${away_queue_slugs[@]}"}; do
    [[ "$slug" == "$wanted" ]] && return 0
  done
  return 1
}

away_parse_queue() {
  local file="$1" line content slug tail deps note error raw
  local clause dep old_ifs duplicate_dep existing
  local -a dependency_parts existing_dependencies

  away_queue_slugs=()
  away_queue_dependencies=()
  away_queue_notes=()
  away_queue_errors=()
  away_queue_raw=()

  [[ -f "$file" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    raw="$line"
    if [[ "$line" =~ ^[[:space:]]*$ ]]; then
      continue
    fi
    if [[ ! "$line" =~ ^[[:space:]]*-[[:space:]]+(.+)$ ]]; then
      if [[ "$line" =~ ^[[:space:]]*- ]]; then
        away_queue_add "(invalid)" "" "" "malformed list line" "$raw"
      fi
      continue
    fi

    content="$(away_trim "${BASH_REMATCH[1]}")"
    slug="${content%%[[:space:]]*}"
    if [[ "$content" == "$slug" ]]; then
      tail=""
    else
      tail="$(away_trim "${content#"$slug"}")"
    fi
    deps=""
    note=""
    error=""

    if ! away_is_safe_slug "$slug"; then
      error="invalid slug"
    elif away_queue_slug_seen "$slug"; then
      error="duplicate slug"
    fi

    if [[ -z "$error" && "$tail" == "(after:"* ]]; then
      if [[ "$tail" != *")"* ]]; then
        error="unterminated after clause"
      else
        clause="${tail%%)*}"
        clause="${clause#\(after:}"
        note="$(away_trim "${tail#*)}")"
        clause="$(away_trim "$clause")"
        if [[ -z "$clause" ]]; then
          error="empty after clause"
        else
          old_ifs="$IFS"
          IFS=',' read -r -a dependency_parts <<< "$clause"
          IFS="$old_ifs"
          for dep in "${dependency_parts[@]}"; do
            dep="$(away_trim "$dep")"
            if ! away_is_safe_slug "$dep"; then
              error="invalid dependency"
              break
            fi
            if [[ "$dep" == "$slug" ]]; then
              error="self dependency"
              break
            fi
            duplicate_dep=0
            existing_dependencies=()
            if [[ -n "$deps" ]]; then
              old_ifs="$IFS"
              IFS=',' read -r -a existing_dependencies <<< "$deps"
              IFS="$old_ifs"
            fi
            for existing in ${existing_dependencies[@]+"${existing_dependencies[@]}"}; do
              [[ "$existing" == "$dep" ]] && duplicate_dep=1
            done
            if [[ "$duplicate_dep" -eq 1 ]]; then
              error="duplicate dependency"
              break
            fi
            if [[ -z "$deps" ]]; then
              deps="$dep"
            else
              deps="$deps,$dep"
            fi
          done
        fi
      fi
    elif [[ -z "$error" ]]; then
      if [[ "$tail" == *"(after:"* || "$tail" == "(after"* ]]; then
        error="malformed after clause"
      else
        note="$tail"
      fi
    fi

    away_queue_add "$slug" "$deps" "$note" "$error" "$raw"
  done < "$file"
}

away_read_metadata() {
  local file="$1" label="$2" line trimmed value
  away_metadata_value=""
  away_metadata_count=0
  while IFS= read -r line; do
    trimmed="$(away_trim "$line")"
    case "$trimmed" in
      "$label:"*)
        value="${trimmed#"$label:"}"
        value="$(away_trim "$value")"
        if [[ "$value" == \`*\` && ${#value} -ge 2 ]]; then
          value="${value#\`}"
          value="${value%\`}"
          value="$(away_trim "$value")"
        fi
        away_metadata_count=$((away_metadata_count + 1))
        away_metadata_value="$value"
        ;;
    esac
  done < "$file"
}

away_was_dispatched() {
  local slug="$1"
  [[ -f "$away_ledger_file" ]] || return 1
  command -v jq >/dev/null 2>&1 || return 0
  jq -e --arg slug "$slug" \
    'select(.action == "dispatched" and .dispatched.slug == $slug)' \
    "$away_ledger_file" >/dev/null 2>&1
}

away_pr_merged() {
  local branch="$1" count
  if ! count="$(gh pr list --state merged --search "head:$branch base:dev" \
      --json number --limit 1 --jq 'length' 2>/dev/null)"; then
    return 2
  fi
  [[ "$count" =~ ^[0-9]+$ ]] || return 2
  [[ "$count" -gt 0 ]]
}

away_ref_for_branch() {
  local branch="$1"
  if git -C "$repo_root" rev-parse --verify --quiet "$branch^{commit}" >/dev/null; then
    printf '%s' "$branch"
    return 0
  fi
  if git -C "$repo_root" rev-parse --verify --quiet "origin/$branch^{commit}" >/dev/null; then
    printf 'origin/%s' "$branch"
    return 0
  fi
  return 1
}

away_dependency_satisfied() {
  local dep="$1" branch ref merged_status=0
  branch="feature/$dep"
  away_detail=""

  away_pr_merged "$branch" || merged_status=$?
  case "$merged_status" in
    0) return 0 ;;
    2)
      away_detail="GitHub merged-PR lookup failed for $branch"
      return 1
      ;;
  esac

  if ! ref="$(away_ref_for_branch "$branch")"; then
    away_detail="$branch has no merged PR and no branch ref"
    return 1
  fi
  if git -C "$repo_root" merge-base --is-ancestor "$ref" origin/dev 2>/dev/null; then
    return 0
  fi
  away_detail="$branch is not merged to origin/dev"
  return 1
}

away_parse_plan_files() {
  local file="$1" line content path in_section=0
  away_plan_files=()
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    if [[ "$line" == "## Relevant Files" ]]; then
      in_section=1
      continue
    fi
    if [[ "$in_section" -eq 1 && "$line" == "## "* ]]; then
      break
    fi
    [[ "$in_section" -eq 1 ]] || continue
    [[ "$line" =~ ^[[:space:]]*-[[:space:]]+(.+)$ ]] || continue
    content="$(away_trim "${BASH_REMATCH[1]}")"
    if [[ "$content" == \`* ]]; then
      path="${content#\`}"
      path="${path%%\`*}"
    else
      path="$(printf '%s\n' "$content" | sed 's/[[:space:]]-[[:space:]].*$//')"
      path="$(away_trim "$path")"
      path="${path#\`}"
      path="${path%\`}"
    fi
    [[ -n "$path" ]] && away_plan_files+=("$path")
  done < "$file"
}

away_branch_is_inactive() {
  local branch="$1" ref="$2" merged_status=0
  if git -C "$repo_root" merge-base --is-ancestor "$ref" origin/dev 2>/dev/null; then
    return 0
  fi
  away_pr_merged "$branch" || merged_status=$?
  [[ "$merged_status" -eq 0 ]]
}

away_check_overlap() {
  local plan_file="$1" index branch state ref changed file collision
  local -a changed_files
  away_overlap_files=()
  away_overlap_branch=""
  away_parse_plan_files "$plan_file"

  for index in ${flight_branch_names[@]+"${!flight_branch_names[@]}"}; do
    branch="${flight_branch_names[$index]}"
    state="${flight_branch_states[$index]}"
    ref="${flight_branch_refs[$index]}"
    [[ -n "$ref" ]] || continue
    if [[ "$state" != "worktree" ]] && away_branch_is_inactive "$branch" "$ref"; then
      continue
    fi

    changed="$(git -C "$repo_root" diff --name-only "origin/dev...$ref" 2>/dev/null)" || {
      away_overlap_files=("<footprint-unavailable>")
      away_overlap_branch="$branch"
      return 0
    }
    changed_files=()
    while IFS= read -r file; do
      [[ -n "$file" ]] && changed_files+=("$file")
    done <<< "$changed"

    collision=0
    for plan_file in ${away_plan_files[@]+"${away_plan_files[@]}"}; do
      for file in ${changed_files[@]+"${changed_files[@]}"}; do
        if [[ "$plan_file" == "$file" ]]; then
          away_overlap_files+=("$file")
          collision=1
        fi
      done
    done
    if [[ "$collision" -eq 1 ]]; then
      away_overlap_branch="$branch"
      return 0
    fi
  done
  return 1
}

away_branch_exists() {
  local branch="$1"
  git -C "$repo_root" rev-parse --verify --quiet "$branch^{commit}" >/dev/null ||
    git -C "$repo_root" rev-parse --verify --quiet "origin/$branch^{commit}" >/dev/null
}

away_active_dispatch_count() {
  local slug branch seen_slug merged_status already_seen rows
  local -a seen
  away_active_count=0
  [[ -f "$away_ledger_file" ]] || return 0
  if ! command -v jq >/dev/null 2>&1; then
    # Ledger state cannot be interpreted safely, so fail capacity closed.
    away_active_count=3
    return 0
  fi
  if ! rows="$(jq -r 'select(.action == "dispatched" and .dispatched != null) |
      [.dispatched.slug, .dispatched.branch] | @tsv' "$away_ledger_file" 2>/dev/null)"; then
    away_active_count=3
    return 0
  fi

  while IFS=$'\t' read -r slug branch; do
    [[ -n "$slug" && -n "$branch" ]] || continue
    already_seen=0
    for seen_slug in ${seen[@]+"${seen[@]}"}; do
      [[ "$seen_slug" == "$slug" ]] && already_seen=1
    done
    [[ "$already_seen" -eq 0 ]] || continue
    seen+=("$slug")
    away_branch_exists "$branch" || continue
    merged_status=0
    away_pr_merged "$branch" || merged_status=$?
    if [[ "$merged_status" -ne 0 ]]; then
      away_active_count=$((away_active_count + 1))
    fi
  done <<< "$rows"
}

away_set_verdict() {
  away_verdict="$1"
  away_detail="${2:-}"
}

away_add_skip() {
  away_skip_slugs+=("$1")
  away_skip_reasons+=("$2")
  away_skip_details+=("${3:-}")
}

away_reset_tick_result() {
  away_dispatched_slug=""
  away_dispatched_kind=""
  away_dispatched_model=""
  away_dispatched_branch=""
  away_dispatched_worktree=""
  away_tick_error=""
  away_skip_slugs=()
  away_skip_reasons=()
  away_skip_details=()
  away_notifications_json='[]'
  away_notify_error=""
}

away_json_skips() {
  local json='[]' index
  for index in ${away_skip_slugs[@]+"${!away_skip_slugs[@]}"}; do
    json="$(jq -cn \
      --argjson current "$json" \
      --arg slug "${away_skip_slugs[$index]}" \
      --arg reason "${away_skip_reasons[$index]}" \
      --arg detail "${away_skip_details[$index]}" \
      '$current + [{slug: $slug, reason: $reason, detail: $detail}]')" || return 1
  done
  printf '%s' "$json"
}

away_append_ledger() {
  local armed="$1" action="noop" timestamp skips_json line dispatched_json='null'
  command -v jq >/dev/null 2>&1 || {
    printf 'Away dispatcher requires jq to write %s\n' "$away_ledger_file" >&2
    return 1
  }
  timestamp="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  skips_json="$(away_json_skips)" || return 1
  if [[ -n "$away_dispatched_slug" ]]; then
    action="dispatched"
    dispatched_json="$(jq -cn \
      --arg slug "$away_dispatched_slug" \
      --arg kind "$away_dispatched_kind" \
      --arg model "$away_dispatched_model" \
      --arg branch "$away_dispatched_branch" \
      --arg worktree "$away_dispatched_worktree" \
      '{slug: $slug, kind: $kind, model: $model, branch: $branch, worktree: $worktree}')" || return 1
  fi
  line="$(jq -cn \
    --arg ts "$timestamp" \
    --arg action "$action" \
    --argjson dispatched "$dispatched_json" \
    --argjson skips "$skips_json" \
    --argjson armed "$armed" \
    --argjson notifications "$away_notifications_json" \
    --arg error "$away_tick_error" \
    '{ts: $ts, action: $action, dispatched: $dispatched, skips: $skips, armed: $armed}
      + (if ($notifications | length) == 0 then {} else {notifications: $notifications} end)
      + (if $error == "" then {} else {error: $error} end)')" || return 1

  (umask 027; mkdir -p "$away_tasks_dir") || return 1
  if [[ ! -e "$away_ledger_file" ]]; then
    (umask 077; : > "$away_ledger_file") || return 1
  fi
  printf '%s\n' "$line" >> "$away_ledger_file"
  chmod 600 "$away_ledger_file" 2>/dev/null || true
}

away_add_notification() {
  local kind="$1" status="$2" date_value="$3" payload="$4" error_value="${5:-}"
  away_notifications_json="$(jq -cn \
    --argjson current "$away_notifications_json" \
    --arg kind "$kind" \
    --arg status "$status" \
    --arg date "$date_value" \
    --argjson payload "$payload" \
    --arg error "$error_value" \
    '$current + [{kind: $kind, status: $status, date: $date, payload: $payload}
      + (if $error == "" then {} else {error: $error} end)]')" || return 1
}

away_config_value() {
  local file="$1" key="$2"
  [[ -r "$file" ]] || return 1
  awk -v key="$key" '
    index($0, key "=") == 1 { count += 1; value = substr($0, length(key) + 2) }
    END { if (count == 1) print value; else exit 1 }
  ' "$file"
}

away_file_mode() {
  if [[ "$(uname -s)" == "Darwin" ]]; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

away_file_owner_uid() {
  if [[ "$(uname -s)" == "Darwin" ]]; then
    stat -f '%u' "$1"
  else
    stat -c '%u' "$1"
  fi
}

away_relay_reachable() {
  local port="$1"
  [[ "$port" =~ ^[0-9]+$ ]] || return 1
  if [[ -x /usr/bin/nc ]]; then
    /usr/bin/nc -z -w 2 127.0.0.1 "$port" >/dev/null 2>&1
  elif command -v nc >/dev/null 2>&1; then
    nc -z -w 2 127.0.0.1 "$port" >/dev/null 2>&1
  else
    return 1
  fi
}

away_send_telegram() {
  local text="$1" enabled chat_type tunnel_mode port relay_token token chat_id response
  away_notify_error=""
  if [[ ! -f "$away_herdr_config_file" || ! -f "$away_herdr_secrets_file" ]]; then
    away_notify_error="herdr-config-missing"
    return 1
  fi
  if [[ "$(away_file_owner_uid "$away_herdr_config_file" 2>/dev/null || true)" != "$(id -u)" ]] ||
      [[ ! "$(away_file_mode "$away_herdr_config_file" 2>/dev/null || true)" =~ ^(600|640|644)$ ]]; then
    away_notify_error="herdr-config-permissions"
    return 1
  fi
  if [[ "$(away_file_owner_uid "$away_herdr_secrets_file" 2>/dev/null || true)" != "$(id -u)" ]] ||
      [[ "$(away_file_mode "$away_herdr_secrets_file" 2>/dev/null || true)" != "600" ]]; then
    away_notify_error="herdr-secrets-permissions"
    return 1
  fi

  enabled="$(away_config_value "$away_herdr_config_file" HERDR_TG_ENABLED 2>/dev/null || true)"
  chat_type="$(away_config_value "$away_herdr_config_file" HERDR_TG_CHAT_TYPE 2>/dev/null || true)"
  tunnel_mode="$(away_config_value "$away_herdr_config_file" HERDR_TUNNEL_MODE 2>/dev/null || true)"
  port="$(away_config_value "$away_herdr_config_file" HERDR_RELAY_PORT 2>/dev/null || true)"
  relay_token="$(away_config_value "$away_herdr_secrets_file" HERDR_RELAY_TOKEN 2>/dev/null || true)"
  token="$(away_config_value "$away_herdr_secrets_file" HERDR_TG_TOKEN 2>/dev/null || true)"
  chat_id="$(away_config_value "$away_herdr_secrets_file" HERDR_TG_CHAT_ID 2>/dev/null || true)"

  if [[ "$enabled" != "true" || "$chat_type" != "private" || "$tunnel_mode" != "none" ]]; then
    away_notify_error="herdr-telegram-not-private-loopback"
    return 1
  fi
  if [[ ! "$port" =~ ^[0-9]+$ ]] || [[ ! "$relay_token" =~ ^[A-Za-z0-9_-]{16,128}$ ]] ||
      [[ ! "$token" =~ ^[0-9]+:[A-Za-z0-9_-]+$ ]] || [[ ! "$chat_id" =~ ^[1-9][0-9]*$ ]]; then
    away_notify_error="herdr-credentials-invalid"
    return 1
  fi
  if ! away_relay_reachable "$port"; then
    away_notify_error="relay-unreachable"
    return 1
  fi

  response="$(/usr/bin/curl -fsS --max-time 20 -X POST --config - \
    --data-urlencode "chat_id=$chat_id" \
    --data-urlencode "text=$text" <<EOF
url = "https://api.telegram.org/bot${token}/sendMessage"
EOF
  )" || {
    away_notify_error="telegram-unreachable"
    return 1
  }
  if ! printf '%s\n' "$response" | jq -e '.ok == true' >/dev/null 2>&1; then
    away_notify_error="telegram-rejected-message"
    return 1
  fi
}

away_read_notify_state() {
  away_last_digest_date=""
  away_last_stall_state="none"
  [[ -f "$away_notify_state_file" ]] || return 1
  command -v jq >/dev/null 2>&1 || return 2
  if ! away_last_digest_date="$(jq -er '.last_digest_date | select(type == "string")' \
      "$away_notify_state_file" 2>/dev/null)"; then
    return 2
  fi
  if ! away_last_stall_state="$(jq -er \
      '.stall_state | select(. == "none" or . == "queue-empty" or . == "all-blocked")' \
      "$away_notify_state_file" 2>/dev/null)"; then
    return 2
  fi
}

away_write_notify_state() {
  local digest_date="$1" stall_state="$2" temporary_state
  (umask 027; mkdir -p "$away_tasks_dir") || return 1
  temporary_state="$(mktemp "$away_tasks_dir/.away-notify-state.XXXXXX")" || return 1
  if ! jq -cn \
      --arg last_digest_date "$digest_date" \
      --arg stall_state "$stall_state" \
      --arg updated_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
      '{last_digest_date: $last_digest_date, stall_state: $stall_state, updated_at: $updated_at}' \
      > "$temporary_state"; then
    rm -f -- "$temporary_state"
    return 1
  fi
  chmod 600 "$temporary_state"
  mv -f -- "$temporary_state" "$away_notify_state_file"
}

away_digest_build() {
  local today="$1" dispatched='[]' current='[]' branches='[]' prs='[]' blocked='[]'
  local standing source_errors='[]' gh_output snapshot_output
  away_digest_payload='null'
  away_digest_text=""

  if [[ -s "$away_ledger_file" ]]; then
    dispatched="$(jq -cs --arg today "$today" '
      [.[] | select(.action == "dispatched" and .dispatched != null) |
       select((try (.ts | fromdateiso8601 | strflocaltime("%Y-%m-%d")) catch "") == $today) |
       .dispatched | {slug, branch}] | unique_by(.slug)
    ' "$away_ledger_file" 2>/dev/null)" || {
      dispatched='[]'
      source_errors='["ledger-unreadable"]'
    }
  fi
  if [[ -n "$away_dispatched_slug" ]]; then
    current="$(jq -cn --arg slug "$away_dispatched_slug" --arg branch "$away_dispatched_branch" \
      '[{slug: $slug, branch: $branch}]')" || current='[]'
    dispatched="$(jq -cn --argjson old "$dispatched" --argjson current "$current" \
      '$old + $current | unique_by(.slug)')" || dispatched='[]'
  fi
  branches="$(printf '%s\n' "$dispatched" | jq -c '[.[].branch] | unique' 2>/dev/null)" || branches='[]'

  if gh_output="$(gh pr list --state open --base dev --limit 100 \
      --json number,headRefName,title,url,isDraft 2>/dev/null)"; then
    prs="$(printf '%s\n' "$gh_output" | jq -c --argjson branches "$branches" '
      [.[] | select(.isDraft != true and (.headRefName as $head | $branches | index($head))) |
       {number, branch: .headRefName, title, url}]
    ' 2>/dev/null)" || prs='[]'
  else
    source_errors="$(jq -cn --argjson errors "$source_errors" '$errors + ["github-unreachable"]')"
  fi

  if snapshot_output="$(herdr api snapshot 2>/dev/null)"; then
    blocked="$(printf '%s\n' "$snapshot_output" | jq -c '
      [.result.snapshot.agents[]? |
       select((.agent_status // "" | ascii_downcase) == "blocked") |
       {pane_id, agent: (.agent // "unknown"), project: ((.cwd // "") | split("/") | last)}]
    ' 2>/dev/null)" || blocked='[]'
  else
    source_errors="$(jq -cn --argjson errors "$source_errors" '$errors + ["herdr-unreachable"]')"
  fi

  standing="$(away_json_skips 2>/dev/null)" || standing='[]'
  away_digest_payload="$(jq -cn \
    --arg date "$today" \
    --argjson dispatched "$dispatched" \
    --argjson prs "$prs" \
    --argjson blocked_agents "$blocked" \
    --argjson standing_skips "$standing" \
    --argjson source_errors "$source_errors" \
    '{date: $date, dispatched: $dispatched, prs_awaiting_approval: $prs,
      blocked_agents: $blocked_agents, standing_skips: $standing_skips,
      source_errors: $source_errors}')" || return 1

  away_digest_text="$(printf '%s\n' "$away_digest_payload" | jq -r '
    def listed($values; $empty):
      if ($values | length) == 0 then $empty
      else ($values[0:8] | join(", ")) +
        (if ($values | length) > 8 then " (+" + (($values | length) - 8 | tostring) + " more)" else "" end)
      end;
    "Away Dispatcher daily digest — " + .date + "\n" +
    "Dispatched today: " + listed([.dispatched[] | .slug]; "none") + "\n" +
    "PRs awaiting approval: " + listed([.prs_awaiting_approval[] | "#" + (.number | tostring) + " " + .branch]; "none") + "\n" +
    "Blocked agents: " + listed([.blocked_agents[] | .project + " (" + .agent + ", " + .pane_id + ")"]; "none") + "\n" +
    "Standing skips: " + listed([.standing_skips[] | ((.slug // "queue") + "=" + .reason)]; "none") +
    (if (.source_errors | length) == 0 then "" else "\nUnavailable sources: " + (.source_errors | join(", ")) end)
  ' 2>/dev/null)" || return 1
  if [[ ${#away_digest_text} -gt 3900 ]]; then
    away_digest_text="${away_digest_text:0:3890}..."
  fi
}

away_stall_state() {
  local index reason remaining=0 blocked=0
  away_current_stall_state="none"
  [[ -z "$away_dispatched_slug" ]] || return 0
  [[ "$away_active_count" -eq 0 ]] || return 0
  if [[ "${#away_queue_slugs[@]}" -eq 0 ]]; then
    away_current_stall_state="queue-empty"
    return 0
  fi

  for index in ${away_skip_reasons[@]+"${!away_skip_reasons[@]}"}; do
    reason="${away_skip_reasons[$index]}"
    case "$reason" in
      in-flight) ;;
      dep-unmerged|overlap)
        remaining=$((remaining + 1))
        blocked=$((blocked + 1))
        ;;
      *) remaining=$((remaining + 1)) ;;
    esac
  done
  if [[ "$remaining" -eq 0 ]]; then
    away_current_stall_state="queue-empty"
  elif [[ "$remaining" -eq "$blocked" ]]; then
    away_current_stall_state="all-blocked"
  fi
}

away_process_notifications() {
  local today state_status=0 digest_date stall_state payload text
  today="${AWAY_TODAY:-$(date '+%Y-%m-%d')}"
  away_read_notify_state || state_status=$?
  if [[ "$state_status" -eq 1 ]]; then
    away_last_digest_date=""
    away_last_stall_state="none"
  elif [[ "$state_status" -ne 0 ]]; then
    away_add_notification "notification-state" "deferred" "$today" null "state-unreadable" || true
    return 0
  fi
  digest_date="$away_last_digest_date"
  stall_state="$away_last_stall_state"

  if [[ "$digest_date" != "$today" ]]; then
    if away_digest_build "$today"; then
      payload="$away_digest_payload"
      if away_send_telegram "$away_digest_text"; then
        away_add_notification "daily-digest" "sent" "$today" "$payload" || true
        digest_date="$today"
      else
        away_add_notification "daily-digest" "deferred" "$today" "$payload" "$away_notify_error" || true
      fi
    else
      away_add_notification "daily-digest" "deferred" "$today" null "digest-build-failed" || true
    fi
  fi

  away_stall_state
  if [[ "$away_current_stall_state" == "none" ]]; then
    stall_state="none"
  elif [[ "$away_current_stall_state" != "$stall_state" ]]; then
    if [[ "$away_current_stall_state" == "queue-empty" ]]; then
      text="Away Dispatcher stalled: the authorized queue is empty and no dispatched agent is active."
    else
      text="Away Dispatcher stalled: every remaining queue entry is blocked by a dependency or file overlap."
    fi
    payload="$(jq -cn --arg state "$away_current_stall_state" '{state: $state}')" || payload='null'
    if away_send_telegram "$text"; then
      away_add_notification "stall-alert" "sent" "$today" "$payload" || true
      stall_state="$away_current_stall_state"
    else
      away_add_notification "stall-alert" "deferred" "$today" "$payload" "$away_notify_error" || true
    fi
  fi

  if ! away_write_notify_state "$digest_date" "$stall_state"; then
    away_add_notification "notification-state" "deferred" "$today" null "state-write-failed" || true
  fi
  return 0
}

away_evaluate_entry() {
  local index="$1" slug deps plan state dep files=""
  local -a dependency_parts
  slug="${away_queue_slugs[$index]}"
  deps="${away_queue_dependencies[$index]}"
  away_kind=""
  away_model=""
  away_overlap_files=()
  away_overlap_branch=""

  if [[ -n "${away_queue_errors[$index]}" ]]; then
    away_set_verdict "parse-error" "${away_queue_errors[$index]}: ${away_queue_raw[$index]}"
    return 0
  fi
  if [[ "${AWAY_AT_CAPACITY:-0}" == "1" ]]; then
    away_set_verdict "at-capacity" "$away_active_count dispatcher-started agents are active"
    return 0
  fi

  plan="$away_tasks_dir/tasks-$slug.md"
  if [[ ! -f "$plan" ]]; then
    away_set_verdict "missing-plan" "$plan does not exist"
    return 0
  fi
  if task_is_planning_starter "$plan"; then
    away_set_verdict "planning-starter" "$plan is still a planning starter"
    return 0
  fi

  away_read_metadata "$plan" "Implementation agent"
  if [[ "$away_metadata_count" -ne 1 ]]; then
    away_set_verdict "no-agent" "expected exactly one Implementation agent line"
    return 0
  fi
  away_kind="$(printf '%s' "$away_metadata_value" | tr '[:upper:]' '[:lower:]')"
  case "$away_kind" in
    claude|codex|pi) ;;
    *)
      away_set_verdict "no-agent" "unsupported Implementation agent: $away_metadata_value"
      return 0
      ;;
  esac

  away_read_metadata "$plan" "Implementation model"
  if [[ "$away_metadata_count" -ne 1 || -z "$away_metadata_value" ]]; then
    away_set_verdict "no-model" "expected exactly one non-empty Implementation model line"
    return 0
  fi
  away_model="$away_metadata_value"

  state="$(flight_state_of "$slug")"
  if [[ -z "$state" && "$slug" =~ ^([1-9][0-9]*)- ]]; then
    state="$(flight_state_of "${BASH_REMATCH[1]}")"
  fi
  if [[ -n "$state" ]]; then
    away_set_verdict "in-flight" "$slug already has a $state"
    return 0
  fi
  if away_was_dispatched "$slug"; then
    away_set_verdict "in-flight" "$slug was already dispatched according to the append-only ledger"
    return 0
  fi

  if [[ -n "$deps" ]]; then
    IFS=',' read -r -a dependency_parts <<< "$deps"
    for dep in "${dependency_parts[@]}"; do
      if ! away_dependency_satisfied "$dep"; then
        away_set_verdict "dep-unmerged" "$away_detail"
        return 0
      fi
    done
  fi

  if away_check_overlap "$plan"; then
    for dep in ${away_overlap_files[@]+"${away_overlap_files[@]}"}; do
      if [[ -z "$files" ]]; then files="$dep"; else files="$files,$dep"; fi
    done
    away_set_verdict "overlap" "branch=$away_overlap_branch files=$files"
    return 0
  fi

  away_set_verdict "eligible" "ready for $away_kind with model $away_model"
}

away_prepare_verdicts() {
  away_parse_queue "$away_queue_file"
  load_flight_index
  away_active_dispatch_count
  if [[ "$away_active_count" -ge 3 ]]; then
    AWAY_AT_CAPACITY=1
  else
    AWAY_AT_CAPACITY=0
  fi
}

away_fetch_dev() {
  git -C "$away_dev_root" fetch --quiet origin dev
}

away_worktree_for_branch() {
  local wanted="$1" line path=""
  while IFS= read -r line; do
    case "$line" in
      "worktree "*) path="${line#worktree }" ;;
      "branch refs/heads/$wanted") printf '%s' "$path"; return 0 ;;
    esac
  done < <(git -C "$away_dev_root" worktree list --porcelain 2>/dev/null)
  return 1
}

away_dispatch_plan() {
  local slug="$1" kind="$2" model="$3"
  (
    cd "$away_dev_root" || exit 1
    zsh -c 'source "$1"; shift; wt start "$@"' _ \
      "$away_dev_root/scripts/wt.sh" "$slug" \
      --kind "$kind" --model "$model" --yes
  )
}

away_double_dispatch_guard() {
  local slug="$1" state
  load_flight_index
  state="$(flight_state_of "$slug")"
  if [[ -z "$state" && "$slug" =~ ^([1-9][0-9]*)- ]]; then
    state="$(flight_state_of "${BASH_REMATCH[1]}")"
  fi
  if [[ -n "$state" ]]; then
    away_detail="$slug gained a $state before dispatch"
    return 1
  fi
  if away_was_dispatched "$slug"; then
    away_detail="$slug was dispatched by another tick"
    return 1
  fi
  return 0
}

away_run_armed_tick() {
  local index slug kind model branch worktree dispatch_status=0
  away_prepare_verdicts

  for index in ${away_queue_slugs[@]+"${!away_queue_slugs[@]}"}; do
    away_evaluate_entry "$index"
    slug="${away_queue_slugs[$index]}"
    if [[ "$away_verdict" != "eligible" ]]; then
      away_add_skip "$slug" "$away_verdict" "$away_detail"
      continue
    fi

    kind="$away_kind"
    model="$away_model"
    if ! away_double_dispatch_guard "$slug"; then
      away_add_skip "$slug" "in-flight" "$away_detail"
      continue
    fi

    away_dispatch_plan "$slug" "$kind" "$model" || dispatch_status=$?
    if [[ "$dispatch_status" -ne 0 ]]; then
      away_tick_error="dispatch-failed: slug=$slug status=$dispatch_status"
      printf 'Away dispatcher failed to start %s (status %s).\n' "$slug" "$dispatch_status" >&2
      return 1
    fi

    branch="feature/$slug"
    worktree="$(away_worktree_for_branch "$branch" 2>/dev/null || true)"
    if [[ -z "$worktree" ]]; then
      worktree="${away_dev_root%/*}/$slug"
    fi
    away_dispatched_slug="$slug"
    away_dispatched_kind="$kind"
    away_dispatched_model="$model"
    away_dispatched_branch="$branch"
    away_dispatched_worktree="$worktree"
    return 0
  done
  return 0
}

away_require_macos() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    printf 'Away Dispatcher scheduling is supported only on macOS.\n' >&2
    return 1
  fi
}

away_launchctl() {
  /bin/launchctl "$@"
}

away_launch_agent_loaded() {
  away_launchctl print "gui/$(id -u)/$away_launch_label" >/dev/null 2>&1
}

away_xml_escape() {
  printf '%s' "$1" | sed \
    -e 's/&/\&amp;/g' \
    -e 's/</\&lt;/g' \
    -e 's/>/\&gt;/g' \
    -e 's/"/\&quot;/g' \
    -e "s/'/\\\&apos;/g"
}

away_sed_replacement() {
  printf '%s' "$1" | sed -e 's/[&|\\]/\\&/g'
}

away_tick_entrypoint() {
  if [[ -f "$away_dev_root/scripts/away-tick.sh" ]]; then
    printf '%s' "$away_dev_root/scripts/away-tick.sh"
  elif [[ -f "$repo_root/scripts/away-tick.sh" ]]; then
    # Supports a pre-merge rehearsal. Production arm from dev takes the first
    # path, so the installed job never depends on a removable feature checkout.
    printf '%s' "$repo_root/scripts/away-tick.sh"
  else
    return 1
  fi
}

away_render_launch_plist() {
  local destination="$1" template tick_path stdout_path stderr_path
  local tick_xml stdout_xml stderr_xml tick_replacement stdout_replacement stderr_replacement
  template="$away_script_dir/away/com.ori.wt-away-tick.plist"
  [[ -f "$template" ]] || { printf 'Missing launchd template: %s\n' "$template" >&2; return 1; }
  tick_path="$(away_tick_entrypoint)" || { printf 'Missing scripts/away-tick.sh\n' >&2; return 1; }
  stdout_path="${TMPDIR:-/tmp}/ori-wt-away-tick.out.log"
  stderr_path="${TMPDIR:-/tmp}/ori-wt-away-tick.err.log"
  tick_xml="$(away_xml_escape "$tick_path")"
  stdout_xml="$(away_xml_escape "$stdout_path")"
  stderr_xml="$(away_xml_escape "$stderr_path")"
  tick_replacement="$(away_sed_replacement "$tick_xml")"
  stdout_replacement="$(away_sed_replacement "$stdout_xml")"
  stderr_replacement="$(away_sed_replacement "$stderr_xml")"
  sed \
    -e "s|__ORI_AWAY_TICK_PATH__|$tick_replacement|g" \
    -e "s|__ORI_AWAY_STDOUT_PATH__|$stdout_replacement|g" \
    -e "s|__ORI_AWAY_STDERR_PATH__|$stderr_replacement|g" \
    "$template" > "$destination"
  /usr/bin/plutil -lint "$destination" >/dev/null
}

away_install_launch_agent() {
  local temporary_plist loaded=0
  (umask 027; mkdir -p "$away_launch_agents_dir") || return 1
  temporary_plist="$(mktemp "$away_launch_agents_dir/.com.ori.wt-away-tick.XXXXXX")" || return 1
  if ! away_render_launch_plist "$temporary_plist"; then
    rm -f -- "$temporary_plist"
    return 1
  fi
  chmod 600 "$temporary_plist"

  away_launch_agent_loaded && loaded=1
  if [[ "$loaded" -eq 1 && -f "$away_launch_plist" ]] && cmp -s "$temporary_plist" "$away_launch_plist"; then
    rm -f -- "$temporary_plist"
    return 0
  fi
  if [[ "$loaded" -eq 1 ]]; then
    if ! away_launchctl bootout "gui/$(id -u)/$away_launch_label"; then
      rm -f -- "$temporary_plist"
      return 1
    fi
  fi
  mv -f -- "$temporary_plist" "$away_launch_plist"
  if ! away_launchctl bootstrap "gui/$(id -u)" "$away_launch_plist"; then
    return 1
  fi
  away_launch_agent_loaded
}

away_uninstall_launch_agent() {
  local result=0
  if away_launch_agent_loaded; then
    away_launchctl bootout "gui/$(id -u)/$away_launch_label" || result=1
  fi
  rm -f -- "$away_launch_plist"
  return "$result"
}

away_pmset_read() {
  /usr/bin/pmset -g sched
}

away_pmset_helper_mutate() {
  local operation pmset_date request_file response_file temporary_request nonce
  local attempt response_owner response_mode response_version response_nonce response_status
  request_file="$away_pmset_request_dir/request"
  response_file="$away_pmset_request_dir/response"

  if [[ "$#" -eq 4 && "$1" == "schedule" && "$2" == "$away_pmset_type" && "$4" == "$away_pmset_owner" ]]; then
    operation="schedule"
    pmset_date="$3"
  elif [[ "$#" -eq 5 && "$1" == "schedule" && "$2" == "cancel" &&
      "$3" == "$away_pmset_type" && "$5" == "$away_pmset_owner" ]]; then
    operation="cancel"
    pmset_date="$4"
  else
    printf '%s\n' "Refusing an unsupported Away Dispatcher wake-helper request." >&2
    return 1
  fi

  [[ ! -L "$away_pmset_request_dir" && -d "$away_pmset_request_dir" ]] || return 1
  [[ "$(away_file_owner_uid "$away_pmset_request_dir" 2>/dev/null || true)" == "$(id -u)" ]] || return 1
  [[ "$(away_file_mode "$away_pmset_request_dir" 2>/dev/null || true)" == "700" ]] || return 1
  [[ -f "$away_pmset_helper_path" && ! -L "$away_pmset_helper_path" ]] || return 1
  [[ -f "$away_script_dir/away/pmset-helper.sh" ]] || return 1
  cmp -s "$away_script_dir/away/pmset-helper.sh" "$away_pmset_helper_path" || return 1
  [[ "$(away_file_owner_uid "$away_pmset_helper_path" 2>/dev/null || true)" == "0" ]] || return 1
  [[ "$(away_file_mode "$away_pmset_helper_path" 2>/dev/null || true)" == "755" ]] || return 1
  [[ -f "$away_pmset_helper_plist" && ! -L "$away_pmset_helper_plist" ]] || return 1
  [[ "$(away_file_owner_uid "$away_pmset_helper_plist" 2>/dev/null || true)" == "0" ]] || return 1
  [[ "$(away_file_mode "$away_pmset_helper_plist" 2>/dev/null || true)" == "644" ]] || return 1
  [[ ! -e "$request_file" ]] || {
    printf 'A previous wake-helper request is still pending: %s\n' "$request_file" >&2
    return 1
  }
  /bin/rm -f -- "$response_file" || return 1

  nonce="$(/usr/bin/uuidgen | /usr/bin/tr -d '-')" || return 1
  [[ "$nonce" =~ ^[A-Za-z0-9_-]{16,64}$ ]] || return 1
  temporary_request="$(mktemp "$away_pmset_request_dir/.request.XXXXXX")" || return 1
  if ! (umask 077; printf 'version=1\noperation=%s\ndate=%s\nnonce=%s\n' \
      "$operation" "$pmset_date" "$nonce" > "$temporary_request"); then
    /bin/rm -f -- "$temporary_request"
    return 1
  fi
  /bin/chmod 0600 "$temporary_request" || { /bin/rm -f -- "$temporary_request"; return 1; }
  /bin/mv -f -- "$temporary_request" "$request_file" || { /bin/rm -f -- "$temporary_request"; return 1; }

  attempt=0
  while [[ "$attempt" -lt 100 ]]; do
    if [[ -f "$response_file" && ! -L "$response_file" ]]; then
      response_owner="$(away_file_owner_uid "$response_file" 2>/dev/null || true)"
      response_mode="$(away_file_mode "$response_file" 2>/dev/null || true)"
      if [[ "$response_owner" != "0" || "$response_mode" != "644" ]]; then
        printf '%s\n' "Wake-helper response ownership or permissions are invalid." >&2
        return 1
      fi
      response_version="$(away_config_value "$response_file" version 2>/dev/null || true)"
      response_nonce="$(away_config_value "$response_file" nonce 2>/dev/null || true)"
      response_status="$(away_config_value "$response_file" status 2>/dev/null || true)"
      /bin/rm -f -- "$response_file"
      if [[ "$response_version" == "1" && "$response_nonce" == "$nonce" && "$response_status" == "ok" ]]; then
        return 0
      fi
      printf 'Wake-helper request failed (status: %s).\n' "${response_status:-invalid-response}" >&2
      return 1
    fi
    /bin/sleep 0.1
    attempt=$((attempt + 1))
  done
  printf '%s\n' "Timed out waiting for the fixed-purpose wake helper." >&2
  return 1
}

away_pmset_mutate() {
  if [[ "$(id -u)" -eq 0 ]]; then
    /usr/bin/pmset "$@"
  elif [[ -e "$away_pmset_helper_path" || -d "$away_pmset_request_dir" ]]; then
    away_pmset_helper_mutate "$@"
  else
    /usr/bin/sudo -n -- /usr/bin/pmset "$@"
  fi
}

away_owned_wake_lines() {
  local output="$1"
  printf '%s\n' "$output" | grep -F "$away_pmset_type at " | grep -F "by '$away_pmset_owner'" || true
}

away_wake_date_four_digit() {
  /bin/date -j -f '%m/%d/%y %H:%M:%S' "$1" '+%m/%d/%Y %H:%M:%S' 2>/dev/null
}

away_exact_wake_is_listed() {
  local output="$1" pmset_date="$2" four_digit
  four_digit="$(away_wake_date_four_digit "$pmset_date")" || return 1
  printf '%s\n' "$output" | grep -Fq \
    "$away_pmset_type at $four_digit by '$away_pmset_owner'"
}

away_read_wake_state() {
  away_wake_phase=""
  away_wake_date=""
  [[ -f "$away_wake_state_file" ]] || return 1
  command -v jq >/dev/null 2>&1 || return 2
  if ! away_wake_phase="$(jq -er \
      --arg owner "$away_pmset_owner" --arg type "$away_pmset_type" \
      'select(.owner == $owner and .type == $type) | .phase |
       select(. == "candidate" or . == "scheduled")' \
      "$away_wake_state_file" 2>/dev/null)"; then
    return 2
  fi
  if ! away_wake_date="$(jq -er '.pmset_date | select(type == "string" and length > 0)' \
      "$away_wake_state_file" 2>/dev/null)"; then
    return 2
  fi
  away_wake_date_four_digit "$away_wake_date" >/dev/null || return 2
}

away_write_wake_state() {
  local pmset_date="$1" phase="$2" temporary_state
  temporary_state="$(mktemp "$away_tasks_dir/.away-wake.XXXXXX")" || return 1
  if ! jq -cn \
      --arg type "$away_pmset_type" \
      --arg owner "$away_pmset_owner" \
      --arg pmset_date "$pmset_date" \
      --arg phase "$phase" \
      --arg updated_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
      '{type: $type, owner: $owner, pmset_date: $pmset_date, phase: $phase, updated_at: $updated_at}' \
      > "$temporary_state"; then
    rm -f -- "$temporary_state"
    return 1
  fi
  chmod 600 "$temporary_state"
  mv -f -- "$temporary_state" "$away_wake_state_file"
}

away_cancel_pending_wake() {
  local output owned count state_status=0
  output="$(away_pmset_read)" || return 1
  owned="$(away_owned_wake_lines "$output")"
  if [[ -n "$owned" ]]; then
    count="$(printf '%s\n' "$owned" | grep -c .)"
  else
    count=0
  fi

  away_read_wake_state || state_status=$?
  if [[ "$state_status" -eq 1 ]]; then
    if [[ "$count" -gt 0 ]]; then
      printf 'Found an untracked %s wake; refusing to guess which event to cancel.\n' "$away_pmset_owner" >&2
      return 1
    fi
    return 0
  fi
  if [[ "$state_status" -ne 0 ]]; then
    printf 'Invalid Away Dispatcher wake state: %s\n' "$away_wake_state_file" >&2
    return 1
  fi
  if [[ "$count" -eq 0 ]]; then
    rm -f -- "$away_wake_state_file"
    return 0
  fi
  if [[ "$count" -ne 1 ]] || ! away_exact_wake_is_listed "$output" "$away_wake_date"; then
    printf 'Tracked wake does not exactly match pmset; refusing cancellation.\n' >&2
    return 1
  fi

  away_pmset_mutate schedule cancel "$away_pmset_type" "$away_wake_date" "$away_pmset_owner" || return 1
  output="$(away_pmset_read)" || return 1
  owned="$(away_owned_wake_lines "$output")"
  if [[ -n "$owned" ]]; then
    printf 'Away Dispatcher wake remained after exact cancellation.\n' >&2
    return 1
  fi
  rm -f -- "$away_wake_state_file"
}

away_schedule_next_wake() {
  local lead_minutes pmset_date output owned count
  away_cancel_pending_wake || return 1
  lead_minutes=$(( (away_tick_interval_seconds - away_wake_lead_seconds) / 60 ))
  pmset_date="$(/bin/date -v+"${lead_minutes}"M '+%m/%d/%y %H:%M:%S')" || return 1

  # Persist the exact candidate before mutation. A crash after pmset returns can
  # still be reconciled and exactly cancelled; no owner-wide cancel is needed.
  away_write_wake_state "$pmset_date" candidate || return 1
  if ! away_pmset_mutate schedule "$away_pmset_type" "$pmset_date" "$away_pmset_owner"; then
    printf '%s\n' "Could not schedule the next wake. Install the fixed-purpose helper with:"
    printf '  %s\n' "$away_script_dir/away/install-pmset-helper.sh"
    return 1
  fi
  output="$(away_pmset_read)" || return 1
  owned="$(away_owned_wake_lines "$output")"
  if [[ -n "$owned" ]]; then
    count="$(printf '%s\n' "$owned" | grep -c .)"
  else
    count=0
  fi
  if [[ "$count" -ne 1 ]] || ! away_exact_wake_is_listed "$output" "$pmset_date"; then
    printf 'pmset did not confirm exactly one tracked Away Dispatcher wake.\n' >&2
    return 1
  fi
  away_write_wake_state "$pmset_date" scheduled
}

away_herdr_not_ready() {
  printf 'Away Dispatcher herdr prerequisite is not ready: %s\n' "$1" >&2
  return 1
}

away_verify_herdr_remote() {
  local head dirty config_mode config_owner secrets_mode secrets_owner
  local port relay_dir tunnel_mode tg_enabled tg_type relay_token tg_token chat_id relay_url
  local plugin_list listeners listener launch_host

  [[ -d "$away_herdr_source_dir/.git" ]] ||
    away_herdr_not_ready "missing pinned source at $away_herdr_source_dir" || return 1
  head="$(git -C "$away_herdr_source_dir" rev-parse HEAD 2>/dev/null || true)"
  [[ "$head" == "$away_herdr_expected_commit" ]] ||
    away_herdr_not_ready "pinned source commit mismatch" || return 1
  dirty="$(git -C "$away_herdr_source_dir" status --porcelain 2>/dev/null || true)"
  [[ -z "$dirty" ]] || away_herdr_not_ready "pinned source has local changes" || return 1

  [[ -f "$away_herdr_config_file" && -f "$away_herdr_secrets_file" ]] ||
    away_herdr_not_ready "service configuration is missing" || return 1
  config_owner="$(away_file_owner_uid "$away_herdr_config_file" 2>/dev/null || true)"
  config_mode="$(away_file_mode "$away_herdr_config_file" 2>/dev/null || true)"
  secrets_owner="$(away_file_owner_uid "$away_herdr_secrets_file" 2>/dev/null || true)"
  secrets_mode="$(away_file_mode "$away_herdr_secrets_file" 2>/dev/null || true)"
  [[ "$config_owner" == "$(id -u)" && "$config_mode" =~ ^(600|640|644)$ ]] ||
    away_herdr_not_ready "config.env ownership or permissions are unsafe" || return 1
  [[ "$secrets_owner" == "$(id -u)" && "$secrets_mode" == "600" ]] ||
    away_herdr_not_ready "secrets.env must be owner-only mode 0600" || return 1

  port="$(away_config_value "$away_herdr_config_file" HERDR_RELAY_PORT 2>/dev/null || true)"
  relay_dir="$(away_config_value "$away_herdr_config_file" HERDR_RELAY_DIR 2>/dev/null || true)"
  tunnel_mode="$(away_config_value "$away_herdr_config_file" HERDR_TUNNEL_MODE 2>/dev/null || true)"
  tg_enabled="$(away_config_value "$away_herdr_config_file" HERDR_TG_ENABLED 2>/dev/null || true)"
  tg_type="$(away_config_value "$away_herdr_config_file" HERDR_TG_CHAT_TYPE 2>/dev/null || true)"
  relay_token="$(away_config_value "$away_herdr_secrets_file" HERDR_RELAY_TOKEN 2>/dev/null || true)"
  tg_token="$(away_config_value "$away_herdr_secrets_file" HERDR_TG_TOKEN 2>/dev/null || true)"
  chat_id="$(away_config_value "$away_herdr_secrets_file" HERDR_TG_CHAT_ID 2>/dev/null || true)"
  relay_url="$(away_config_value "$away_herdr_secrets_file" HERDR_RELAY 2>/dev/null || true)"

  [[ "$port" == "8375" && "$relay_dir" == "$away_herdr_source_dir/relay" && "$tunnel_mode" == "none" ]] ||
    away_herdr_not_ready "relay must use the pinned source on loopback port 8375 with tunnel mode none" || return 1
  [[ "$tg_enabled" == "true" && "$tg_type" == "private" && "$chat_id" =~ ^[1-9][0-9]*$ ]] ||
    away_herdr_not_ready "Telegram must target one private account chat" || return 1
  [[ "$relay_token" =~ ^[A-Za-z0-9_-]{16,128}$ && "$tg_token" =~ ^[0-9]+:[A-Za-z0-9_-]+$ ]] ||
    away_herdr_not_ready "relay or Telegram credentials are missing" || return 1
  [[ "$relay_url" == "ws://127.0.0.1:8375?token=$relay_token" ]] ||
    away_herdr_not_ready "relay URL is not the token-protected loopback endpoint" || return 1

  plugin_list="$(herdr plugin list 2>/dev/null || true)"
  printf '%s\n' "$plugin_list" | grep -Fq \
    "herdr-remote.relay (Herdr Remote Relay) enabled [local:$away_herdr_source_dir/relay]" ||
    away_herdr_not_ready "the pinned bundled event plugin is not enabled" || return 1

  away_launchctl print "gui/$(id -u)/com.herdr-remote.relay" >/dev/null 2>&1 ||
    away_herdr_not_ready "relay LaunchAgent is not loaded" || return 1
  away_launchctl print "gui/$(id -u)/com.herdr-remote.telegram" >/dev/null 2>&1 ||
    away_herdr_not_ready "Telegram LaunchAgent is not loaded" || return 1
  if away_launchctl print "gui/$(id -u)/com.herdr-remote.tunnel" >/dev/null 2>&1 ||
      [[ -e "$HOME/Library/LaunchAgents/com.herdr-remote.tunnel.plist" ]]; then
    away_herdr_not_ready "a herdr public tunnel service is present"
    return 1
  fi
  launch_host="$(away_launchctl getenv HERDR_RELAY_HOST 2>/dev/null || true)"
  [[ -z "$launch_host" || "$launch_host" == "127.0.0.1" ]] ||
    away_herdr_not_ready "launchd overrides the relay host away from loopback" || return 1

  if command -v lsof >/dev/null 2>&1; then
    listeners="$(lsof -nP -iTCP:8375 -sTCP:LISTEN -F n 2>/dev/null | awk '/^n/ {print substr($0, 2)}')"
  elif [[ -x /usr/sbin/lsof ]]; then
    listeners="$(/usr/sbin/lsof -nP -iTCP:8375 -sTCP:LISTEN -F n 2>/dev/null | awk '/^n/ {print substr($0, 2)}')"
  else
    away_herdr_not_ready "lsof is unavailable for listener verification"
    return 1
  fi
  [[ -n "$listeners" ]] || away_herdr_not_ready "relay port 8375 is not listening" || return 1
  while IFS= read -r listener; do
    [[ "$listener" == "127.0.0.1:8375" ]] ||
      away_herdr_not_ready "relay has a non-loopback listener" || return 1
  done <<< "$listeners"
}

away_arm() {
  local was_armed=0
  away_require_no_args arm "$@" || return 1
  away_require_macos || return 1
  away_resolve_dev || return 1
  away_verify_herdr_remote || return 1
  [[ -f "$away_tasks_dir/.away-armed" ]] && was_armed=1
  (umask 027; mkdir -p "$away_tasks_dir") || return 1

  away_install_launch_agent || return 1
  if ! away_schedule_next_wake; then
    if [[ "$was_armed" -eq 0 ]]; then
      away_uninstall_launch_agent || true
    fi
    return 1
  fi
  if [[ "$was_armed" -eq 0 ]]; then
    (umask 077; : > "$away_tasks_dir/.away-armed") || return 1
    chmod 600 "$away_tasks_dir/.away-armed" 2>/dev/null || true
    printf 'Away dispatcher armed.\n'
  else
    printf 'Away dispatcher is already armed; schedule reconciled.\n'
  fi
}

away_disarm() {
  local was_armed=0 result=0
  away_require_no_args disarm "$@" || return 1
  away_require_macos || return 1
  away_resolve_dev || return 1
  [[ -e "$away_tasks_dir/.away-armed" ]] && was_armed=1

  # Stop authorization first. Even if launchd or pmset cleanup needs repair,
  # no subsequent tick may start new work.
  rm -f -- "$away_tasks_dir/.away-armed"
  away_uninstall_launch_agent || result=1
  away_cancel_pending_wake || result=1
  if [[ "$result" -ne 0 ]]; then
    printf 'Away dispatcher is disarmed, but scheduled-resource cleanup needs attention.\n' >&2
    return 1
  fi
  if [[ "$was_armed" -eq 1 ]]; then
    printf 'Away dispatcher disarmed. Running agents were not interrupted.\n'
  else
    printf 'Away dispatcher is already disarmed; schedule is clear.\n'
  fi
}

away_render_schedule_status() {
  local state_status=0 output owned count
  if away_launch_agent_loaded; then
    printf 'LaunchAgent: loaded (%s)\n' "$away_launch_label"
  else
    printf 'LaunchAgent: not loaded\n'
  fi

  away_read_wake_state || state_status=$?
  if [[ "$state_status" -eq 1 ]]; then
    output="$(away_pmset_read 2>/dev/null || true)"
    owned="$(away_owned_wake_lines "$output")"
    if [[ -n "$owned" ]]; then
      printf 'Scheduled wake: untracked owner event (manual repair required)\n'
    else
      printf 'Scheduled wake: none\n'
    fi
    return 0
  fi
  if [[ "$state_status" -ne 0 ]]; then
    printf 'Scheduled wake: invalid state (%s)\n' "$away_wake_state_file"
    return 0
  fi

  output="$(away_pmset_read 2>/dev/null || true)"
  owned="$(away_owned_wake_lines "$output")"
  if [[ -n "$owned" ]]; then
    count="$(printf '%s\n' "$owned" | grep -c .)"
  else
    count=0
  fi
  if [[ "$count" -eq 1 ]] && away_exact_wake_is_listed "$output" "$away_wake_date"; then
    printf 'Scheduled wake: %s (%s)\n' "$away_wake_date" "$away_wake_phase"
  else
    printf 'Scheduled wake: state does not match pmset\n'
  fi
}

away_render_active() {
  local slug branch worktree progress_file progress merged_status already_seen seen_slug
  local -a seen
  local rows=0
  printf '\nActive dispatched agents:\n'
  if [[ ! -f "$away_ledger_file" ]] || ! command -v jq >/dev/null 2>&1; then
    printf '  none\n'
    return 0
  fi

  while IFS=$'\t' read -r slug branch worktree; do
    [[ -n "$slug" && -n "$branch" ]] || continue
    already_seen=0
    for seen_slug in ${seen[@]+"${seen[@]}"}; do
      [[ "$seen_slug" == "$slug" ]] && already_seen=1
    done
    [[ "$already_seen" -eq 0 ]] || continue
    seen+=("$slug")
    away_branch_exists "$branch" || continue
    merged_status=0
    away_pr_merged "$branch" || merged_status=$?
    [[ "$merged_status" -ne 0 ]] || continue

    progress_file="$away_tasks_dir/tasks-$slug.md"
    if [[ -n "$worktree" && -f "$worktree/tasks/tasks-$slug.md" ]]; then
      progress_file="$worktree/tasks/tasks-$slug.md"
    fi
    progress="$(task_groups_of_file "$progress_file")"
    printf '  %-28s %-42s %s\n' "$slug" "$branch" "${progress:--}"
    rows=$((rows + 1))
  done < <(jq -r 'select(.action == "dispatched" and .dispatched != null) |
    [.dispatched.slug, .dispatched.branch, .dispatched.worktree] | @tsv' \
    "$away_ledger_file" 2>/dev/null)
  [[ "$rows" -gt 0 ]] || printf '  none\n'
}

away_render_queue() {
  local index slug rows=0
  printf '\nQueue verdicts:\n'
  for index in ${away_queue_slugs[@]+"${!away_queue_slugs[@]}"}; do
    away_evaluate_entry "$index"
    slug="${away_queue_slugs[$index]}"
    printf '  %-28s %-18s %s\n' "$slug" "$away_verdict" "$away_detail"
    rows=$((rows + 1))
  done
  [[ "$rows" -gt 0 ]] || printf '  (empty)\n'
}

away_render_last_tick() {
  local last
  printf '\nLast tick:\n'
  if [[ ! -s "$away_ledger_file" ]] || ! command -v jq >/dev/null 2>&1; then
    printf '  none\n'
    return 0
  fi
  last="$(tail -n 1 "$away_ledger_file")"
  if ! printf '%s\n' "$last" | jq -er '
    (if .action == "dispatched" then
      "  \(.ts) dispatched \(.dispatched.slug) (\(.dispatched.kind), \(.dispatched.model))"
    elif .error then
      "  \(.ts) noop: \(.error)"
    elif (.skips | length) > 0 then
      "  \(.ts) noop: " + ([.skips[] | ((.slug // "") + "=" + .reason)] | join(", "))
    else
      "  \(.ts) noop"
    end) +
    (if ((.notifications // []) | length) > 0 then
      "; notifications: " + ([.notifications[] | (.kind + "=" + .status)] | join(", "))
    else "" end)' 2>/dev/null; then
    printf '  unreadable ledger tail\n'
  fi
}

away_status() {
  away_require_no_args status "$@" || return 1
  away_resolve_dev || return 1
  if [[ -f "$away_tasks_dir/.away-armed" ]]; then
    printf 'Away dispatcher: armed\n'
  else
    printf 'Away dispatcher: disarmed\n'
  fi
  away_render_schedule_status
  if away_verify_herdr_remote >/dev/null 2>&1; then
    printf 'herdr-remote: ready (token-protected loopback + private Telegram)\n'
  else
    printf 'herdr-remote: not ready; arming is blocked\n'
  fi
  if ! away_fetch_dev; then
    printf 'Warning: could not refresh origin/dev; verdicts use local refs.\n' >&2
  fi
  away_prepare_verdicts
  away_render_active
  away_render_queue
  away_render_last_tick
}

away_tick() {
  local tick_status=0
  away_require_no_args tick "$@" || return 1
  away_resolve_dev || return 1
  away_reset_tick_result
  # Authorization boundary: when disarmed, append the audit no-op without ever
  # reading away-queue.md or fetching remote state.
  if [[ ! -f "$away_tasks_dir/.away-armed" ]]; then
    away_add_skip "" "disarmed" "new dispatches are disabled"
    away_append_ledger false
    return $?
  fi
  if ! away_schedule_next_wake; then
    away_tick_error="wake-schedule-failed"
    away_append_ledger true || true
    return 1
  fi
  if ! away_fetch_dev; then
    away_tick_error="fetch-failed: origin/dev"
    away_append_ledger true || true
    printf 'Away dispatcher could not fetch origin/dev; no plan was dispatched.\n' >&2
    return 1
  fi
  away_run_armed_tick || tick_status=$?
  # Notification delivery is observability, never an authorization or liveness
  # dependency. Failures are recorded on this tick and retried from state.
  away_process_notifications || true
  away_append_ledger true || return 1
  return "$tick_status"
}

away_main() {
  local command="${1:-}"
  case "$command" in
    arm) shift; away_arm "$@" ;;
    disarm) shift; away_disarm "$@" ;;
    status) shift; away_status "$@" ;;
    tick) shift; away_tick "$@" ;;
    help|-h|--help) away_usage ;;
    "")
      printf 'wt away needs a command\n' >&2
      away_usage >&2
      return 1
      ;;
    *)
      printf 'Unknown wt away command: %s\n' "$command" >&2
      away_usage >&2
      return 1
      ;;
  esac
}

if [[ "${AWAY_DISPATCH_SOURCE_ONLY:-0}" != "1" ]]; then
  away_main "$@"
fi
