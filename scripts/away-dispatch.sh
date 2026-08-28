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
    --arg error "$away_tick_error" \
    '{ts: $ts, action: $action, dispatched: $dispatched, skips: $skips, armed: $armed}
      + (if $error == "" then {} else {error: $error} end)')" || return 1

  (umask 027; mkdir -p "$away_tasks_dir") || return 1
  if [[ ! -e "$away_ledger_file" ]]; then
    (umask 077; : > "$away_ledger_file") || return 1
  fi
  printf '%s\n' "$line" >> "$away_ledger_file"
  chmod 600 "$away_ledger_file" 2>/dev/null || true
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

away_arm() {
  away_require_no_args arm "$@" || return 1
  away_resolve_dev || return 1
  if [[ -f "$away_tasks_dir/.away-armed" ]]; then
    printf 'Away dispatcher is already armed.\n'
    return 0
  fi
  (umask 027; mkdir -p "$away_tasks_dir") || return 1
  (umask 077; : > "$away_tasks_dir/.away-armed") || return 1
  chmod 600 "$away_tasks_dir/.away-armed" 2>/dev/null || true
  printf 'Away dispatcher armed.\n'
}

away_disarm() {
  away_require_no_args disarm "$@" || return 1
  away_resolve_dev || return 1
  if [[ ! -e "$away_tasks_dir/.away-armed" ]]; then
    printf 'Away dispatcher is already disarmed.\n'
    return 0
  fi
  rm -f -- "$away_tasks_dir/.away-armed"
  printf 'Away dispatcher disarmed. Running agents were not interrupted.\n'
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
    if .action == "dispatched" then
      "  \(.ts) dispatched \(.dispatched.slug) (\(.dispatched.kind), \(.dispatched.model))"
    elif .error then
      "  \(.ts) noop: \(.error)"
    elif (.skips | length) > 0 then
      "  \(.ts) noop: " + ([.skips[] | ((.slug // "") + "=" + .reason)] | join(", "))
    else
      "  \(.ts) noop"
    end' 2>/dev/null; then
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
  if ! away_fetch_dev; then
    away_tick_error="fetch-failed: origin/dev"
    away_append_ledger true || true
    printf 'Away dispatcher could not fetch origin/dev; no plan was dispatched.\n' >&2
    return 1
  fi
  away_run_armed_tick || tick_status=$?
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
