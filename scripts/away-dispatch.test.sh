#!/usr/bin/env bash
set -euo pipefail

exec < /dev/null

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-away-dispatch.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

dev_root="$fixture_root/dev"
mkdir -p "$dev_root/tasks"

# Drive the public zsh wrapper without consulting the checkout's real worktree
# list. The fake supports only the read-only Git calls these skeleton actions
# need; an armed dispatch engine is tested through sourced functions below.
fake_bin="$fixture_root/bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/git" <<'FAKE_GIT'
#!/usr/bin/env bash
if [[ "${1:-}" == "-C" ]]; then shift 2; fi
case "$*" in
  "rev-parse --show-toplevel") printf '%s\n' "$REAL_REPO_ROOT" ;;
  "worktree list --porcelain")
    printf 'worktree %s\nHEAD deadbeef\nbranch refs/heads/dev\n\n' "$FAKE_DEV_ROOT"
    ;;
  "fetch --quiet origin dev"|"branch --all --format=%(refname:short)") ;;
  *) printf 'fake git: unsupported call: %s\n' "$*" >&2; exit 1 ;;
esac
FAKE_GIT
chmod +x "$fake_bin/git"

export REAL_REPO_ROOT="$repo_root" FAKE_DEV_ROOT="$dev_root"
original_path="$PATH"
export PATH="$fake_bin:$PATH"

run_wt() {
  mkdir -p "$fixture_root/home"
  HOME="$fixture_root/home" zsh -c 'source "$1"; shift; wt away "$@"' _ "$repo_root/scripts/wt.sh" "$@"
}

output="$(run_wt --help)"
grep -q '^Usage: wt away <command>$' <<< "$output"

# A disarmed tick is silent and exits before the queue authorization boundary.
mkdir "$dev_root/tasks/away-queue.md"
output="$(run_wt tick)"
[[ -z "$output" ]]

if run_wt > "$fixture_root/no-command.out" 2>&1; then
  printf '%s\n' "wt away without a subcommand succeeded" >&2
  exit 1
fi
grep -q '^Usage: wt away <command>$' "$fixture_root/no-command.out"

if run_wt launch > "$fixture_root/unknown.out" 2>&1; then
  printf '%s\n' "wt away accepted an unknown subcommand" >&2
  exit 1
fi
grep -q '^Unknown wt away command: launch$' "$fixture_root/unknown.out"

if run_wt tick --force > "$fixture_root/flag.out" 2>&1; then
  printf '%s\n' "wt away tick accepted an unknown flag" >&2
  exit 1
fi
grep -q '^wt away tick accepts no arguments$' "$fixture_root/flag.out"

# Unit-test the Bash 3.2 queue and eligibility engine without network calls.
export PATH="$original_path"
AWAY_DISPATCH_SOURCE_ONLY=1 source "$repo_root/scripts/away-dispatch.sh"
away_dev_root="$dev_root"
away_tasks_dir="$dev_root/tasks"
away_queue_file="$away_tasks_dir/away-queue.md"
away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
away_wake_state_file="$away_tasks_dir/.away-wake.json"
tasks_dir="$away_tasks_dir"
rmdir "$away_queue_file"
rm -f "$away_ledger_file"

# Arm/disarm orchestration is exercised with fixed resource mocks so this suite
# never changes the user's real launchd or pmset state.
launch_install_calls=0
launch_uninstall_calls=0
wake_schedule_calls=0
wake_cancel_calls=0
caffeinate_uninstall_calls=0
away_require_macos() { return 0; }
away_verify_herdr_remote() { return 0; }
away_caffeinate_resource_safe() { return 0; }
away_resolve_dev() {
  away_dev_root="$dev_root"
  away_tasks_dir="$dev_root/tasks"
  away_queue_file="$away_tasks_dir/away-queue.md"
  away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
  away_wake_state_file="$away_tasks_dir/.away-wake.json"
  tasks_dir="$away_tasks_dir"
}
away_install_launch_agent() { launch_install_calls=$((launch_install_calls + 1)); }
away_uninstall_launch_agent() { launch_uninstall_calls=$((launch_uninstall_calls + 1)); }
away_schedule_next_wake() { wake_schedule_calls=$((wake_schedule_calls + 1)); }
away_cancel_pending_wake() { wake_cancel_calls=$((wake_cancel_calls + 1)); }
away_uninstall_caffeinate_assertion() { caffeinate_uninstall_calls=$((caffeinate_uninstall_calls + 1)); }
away_arm > "$fixture_root/arm.out"
output="$(<"$fixture_root/arm.out")"
[[ "$output" == "Away dispatcher armed." ]]
[[ -f "$away_tasks_dir/.away-armed" ]]
[[ "$(stat -f '%Lp' "$away_tasks_dir/.away-armed")" == "600" ]]
away_arm > "$fixture_root/arm.out"
output="$(<"$fixture_root/arm.out")"
[[ "$output" == "Away dispatcher is already armed; schedule reconciled." ]]
[[ "$launch_install_calls" -eq 2 && "$wake_schedule_calls" -eq 2 ]]
away_disarm > "$fixture_root/disarm.out"
output="$(<"$fixture_root/disarm.out")"
[[ "$output" == "Away dispatcher disarmed. Running agents were not interrupted." ]]
[[ ! -e "$away_tasks_dir/.away-armed" ]]
away_disarm > "$fixture_root/disarm.out"
output="$(<"$fixture_root/disarm.out")"
[[ "$output" == "Away dispatcher is already disarmed; schedule is clear." ]]
[[ "$launch_uninstall_calls" -eq 2 && "$wake_cancel_calls" -eq 2 && "$caffeinate_uninstall_calls" -eq 2 ]]

# Restore the real resource functions for the isolated launchd/pmset fixtures.
AWAY_DISPATCH_SOURCE_ONLY=1 source "$repo_root/scripts/away-dispatch.sh"
away_dev_root="$dev_root"
away_tasks_dir="$dev_root/tasks"
away_queue_file="$away_tasks_dir/away-queue.md"
away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
away_wake_state_file="$away_tasks_dir/.away-wake.json"
tasks_dir="$away_tasks_dir"

# Installed LaunchAgent rendering uses an absolute stable entrypoint, a
# 30-minute interval, and logs under the caller's TMPDIR.
away_launch_agents_dir="$fixture_root/LaunchAgents"
away_launch_plist="$away_launch_agents_dir/com.ori.wt-away-tick.plist"
launch_loaded=0
: > "$fixture_root/launchctl.calls"
away_launchctl() {
  printf '%s\n' "$*" >> "$fixture_root/launchctl.calls"
  case "$1" in
    print) [[ "$launch_loaded" -eq 1 ]] ;;
    bootstrap) launch_loaded=1 ;;
    bootout) launch_loaded=0 ;;
  esac
}
away_install_launch_agent
[[ "$launch_loaded" -eq 1 ]]
/usr/bin/plutil -lint "$away_launch_plist" >/dev/null
[[ "$(/usr/libexec/PlistBuddy -c 'Print :StartInterval' "$away_launch_plist")" == "1800" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:1' "$away_launch_plist")" == "$repo_root/scripts/away-tick.sh" ]]
grep -Fq "${TMPDIR:-/tmp}/ori-wt-away-tick.out.log" "$away_launch_plist"
away_uninstall_launch_agent
[[ "$launch_loaded" -eq 0 && ! -e "$away_launch_plist" ]]

# Active work uses its own narrowly scoped idle-sleep assertion LaunchAgent.
away_caffeinate_plist="$away_launch_agents_dir/com.ori.wt-away-caffeinate.plist"
away_caffeinate_template="$repo_root/scripts/away/com.ori.wt-away-caffeinate.plist"
away_install_caffeinate_assertion
[[ "$launch_loaded" -eq 1 ]]
/usr/bin/plutil -lint "$away_caffeinate_plist" >/dev/null
[[ "$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$away_caffeinate_plist")" == "/usr/bin/caffeinate" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:1' "$away_caffeinate_plist")" == "-i" ]]
away_uninstall_caffeinate_assertion
[[ "$launch_loaded" -eq 0 && ! -e "$away_caffeinate_plist" ]]

# The pmset chain tracks one exact fixed-owner wake, cancels only that event,
# and leaves a foreign scheduled event untouched.
fake_pmset_date=""
fake_foreign_line=" [0]  wake at 08/29/2026 03:00:00 by 'com.example.foreign'"
: > "$fixture_root/pmset.calls"
away_pmset_read() {
  local four_digit
  printf '%s\n' "Scheduled power events:" "$fake_foreign_line"
  if [[ -n "$fake_pmset_date" ]]; then
    four_digit="$(away_wake_date_four_digit "$fake_pmset_date")"
    printf " [1]  %s at %s by '%s'\n" "$away_pmset_type" "$four_digit" "$away_pmset_owner"
  fi
}
away_pmset_mutate() {
  printf '%s\n' "$*" >> "$fixture_root/pmset.calls"
  if [[ "$1" == "schedule" && "$2" == "$away_pmset_type" ]]; then
    fake_pmset_date="$3"
    return 0
  fi
  if [[ "$1" == "schedule" && "$2" == "cancel" && "$3" == "$away_pmset_type" && "$4" == "$fake_pmset_date" ]]; then
    fake_pmset_date=""
    return 0
  fi
  return 1
}
away_schedule_next_wake
first_wake="$fake_pmset_date"
[[ -n "$first_wake" && -f "$away_wake_state_file" ]]
jq -e --arg date "$first_wake" \
  '.phase == "scheduled" and .pmset_date == $date and .owner == "com.ori.wt-away-tick"' \
  "$away_wake_state_file" >/dev/null
away_schedule_next_wake
[[ "$(grep -c '^schedule cancel wakeorpoweron ' "$fixture_root/pmset.calls")" -eq 1 ]]
[[ "$(grep -c '^schedule wakeorpoweron ' "$fixture_root/pmset.calls")" -eq 2 ]]
away_cancel_pending_wake
[[ -z "$fake_pmset_date" && ! -e "$away_wake_state_file" ]]
[[ "$fake_foreign_line" == *"com.example.foreign"* ]]

# State/output disagreement refuses cancellation rather than reaching for an
# owner-wide or cancelall operation.
away_write_wake_state "01/01/30 00:00:00" scheduled
fake_pmset_date="01/01/30 00:01:00"
before_cancel_calls="$(wc -l < "$fixture_root/pmset.calls" | tr -d ' ')"
if away_cancel_pending_wake >/dev/null 2>&1; then
  printf '%s\n' "mismatched wake state was cancelled" >&2
  exit 1
fi
[[ "$(wc -l < "$fixture_root/pmset.calls" | tr -d ' ')" == "$before_cancel_calls" ]]
rm -f "$away_wake_state_file"
fake_pmset_date=""

# The unprivileged LaunchDaemon client emits only the fixed protocol and
# accepts a root-owned nonce-matching response.
helper_dir="$fixture_root/helper-requests"
helper_path="$fixture_root/helper-bin"
helper_plist="$fixture_root/helper.plist"
mkdir -p "$helper_dir"
chmod 700 "$helper_dir"
cp "$away_script_dir/away/pmset-helper.sh" "$helper_path"
: > "$helper_plist"
chmod 755 "$helper_path"
chmod 644 "$helper_plist"
away_pmset_request_dir="$helper_dir"
away_pmset_helper_path="$helper_path"
away_pmset_helper_plist="$helper_plist"
real_owner_uid() { stat -f '%u' "$1"; }
away_file_owner_uid() {
  case "$1" in
    "$helper_path"|"$helper_plist"|"$helper_dir/response") printf '0\n' ;;
    *) real_owner_uid "$1" ;;
  esac
}
(
  while [[ ! -f "$helper_dir/request" ]]; do /bin/sleep 0.02; done
  cp "$helper_dir/request" "$fixture_root/helper-request-captured"
  nonce="$(awk -F= '$1 == "nonce" {print substr($0, 7)}' "$helper_dir/request")"
  rm -f "$helper_dir/request"
  temporary_response="$(mktemp "$helper_dir/.fixture-response.XXXXXX")"
  printf 'version=1\nnonce=%s\nstatus=ok\n' "$nonce" > "$temporary_response"
  chmod 644 "$temporary_response"
  mv "$temporary_response" "$helper_dir/response"
) &
helper_writer_pid=$!
away_pmset_helper_mutate schedule wakeorpoweron "01/02/30 03:04:05" com.ori.wt-away-tick
wait "$helper_writer_pid"
grep -q '^operation=schedule$' "$fixture_root/helper-request-captured"
grep -q '^date=01/02/30 03:04:05$' "$fixture_root/helper-request-captured"
[[ ! -e "$helper_dir/request" && ! -e "$helper_dir/response" ]]
if away_pmset_helper_mutate repeat wakeorpoweron "01/02/30 03:04:05" com.ori.wt-away-tick >/dev/null 2>&1; then
  printf '%s\n' "wake helper accepted an unsupported operation" >&2
  exit 1
fi

# Restore real file-inspection functions and repository paths after the helper
# protocol fixture.
AWAY_DISPATCH_SOURCE_ONLY=1 source "$repo_root/scripts/away-dispatch.sh"
away_dev_root="$dev_root"
away_tasks_dir="$dev_root/tasks"
away_queue_file="$away_tasks_dir/away-queue.md"
away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
away_wake_state_file="$away_tasks_dir/.away-wake.json"
away_notify_state_file="$away_tasks_dir/.away-notify-state.json"
tasks_dir="$away_tasks_dir"

cat > "$away_queue_file" <<'QUEUE'
# Trip queue

- eligible owner note
- dependent (after: first-dep, second-dep) waits for both
- malformed (after: first-dep
- bad/slug
- self-cycle (after: self-cycle)
-
QUEUE
away_parse_queue "$away_queue_file"
[[ "${#away_queue_slugs[@]}" -eq 6 ]]
[[ "${away_queue_slugs[0]}" == "eligible" ]]
[[ "${away_queue_notes[0]}" == "owner note" ]]
[[ "${away_queue_dependencies[1]}" == "first-dep,second-dep" ]]
[[ "${away_queue_notes[1]}" == "waits for both" ]]
[[ "${away_queue_errors[2]}" == "unterminated after clause" ]]
[[ "${away_queue_errors[3]}" == "invalid slug" ]]
[[ "${away_queue_errors[4]}" == "self dependency" ]]
[[ "${away_queue_errors[5]}" == "malformed list line" ]]

write_plan() {
  local slug="$1" agent="$2" model="$3" path="${4:-internal/example.go}"
  cat > "$away_tasks_dir/tasks-$slug.md" <<PLAN
# $slug

## Relevant Files

- \`$path\` - Fixture file.

## Instructions for Completing Tasks

Implementation agent: \`$agent\`
Implementation model: \`$model\`

## Tasks

- [ ] 1.0 Fixture task
PLAN
}

set_entry() {
  away_queue_slugs=("$1")
  away_queue_dependencies=("${2:-}")
  away_queue_notes=("")
  away_queue_errors=("")
  away_queue_raw=("- $1")
  AWAY_AT_CAPACITY=0
  flight_numbers=()
  flight_states=()
  flight_refs=()
  flight_branch_names=()
  flight_branch_states=()
  flight_branch_refs=()
}

set_entry missing
away_evaluate_entry 0
[[ "$away_verdict" == "missing-plan" ]]

cat > "$away_tasks_dir/tasks-starter.md" <<'STARTER'
<!-- ori-devflow: planning-starter; fixture -->
STARTER
set_entry starter
away_evaluate_entry 0
[[ "$away_verdict" == "planning-starter" ]]

write_plan no-agent worktree-only model-a
set_entry no-agent
away_evaluate_entry 0
[[ "$away_verdict" == "no-agent" ]]

write_plan no-model pi ""
set_entry no-model
away_evaluate_entry 0
[[ "$away_verdict" == "no-model" ]]

write_plan active pi model-a
set_entry active
remember_branch_flight feature/active worktree feature/active
away_evaluate_entry 0
[[ "$away_verdict" == "in-flight" ]]

write_plan prior pi model-a
printf '%s\n' '{"action":"dispatched","dispatched":{"slug":"prior","branch":"feature/prior"}}' > "$away_ledger_file"
set_entry prior
away_evaluate_entry 0
[[ "$away_verdict" == "in-flight" ]]
rm -f "$away_ledger_file"

write_plan dependent pi model-a
set_entry dependent first-dep
away_dependency_satisfied() { away_detail="feature/first-dep is not merged to origin/dev"; return 1; }
away_evaluate_entry 0
[[ "$away_verdict" == "dep-unmerged" ]]

write_plan overlapping pi model-a shared.txt
set_entry overlapping
away_check_overlap() {
  away_overlap_files=("shared.txt")
  away_overlap_branch="feature/inflight"
  return 0
}
away_evaluate_entry 0
[[ "$away_verdict" == "overlap" ]]
[[ "$away_detail" == "branch=feature/inflight files=shared.txt" ]]

write_plan eligible pi 'openai/model-a'
set_entry eligible
away_check_overlap() { return 1; }
away_evaluate_entry 0
[[ "$away_verdict" == "eligible" ]]
[[ "$away_kind" == "pi" ]]
[[ "$away_model" == "openai/model-a" ]]

set_entry eligible
away_active_count=3
AWAY_AT_CAPACITY=1
away_evaluate_entry 0
[[ "$away_verdict" == "at-capacity" ]]

# Exercise the real footprint intersection against a temporary Git repository.
overlap_repo="$fixture_root/overlap-repo"
mkdir -p "$overlap_repo"
git -C "$overlap_repo" init -q -b dev
git -C "$overlap_repo" config user.email away@example.invalid
git -C "$overlap_repo" config user.name 'Away Test'
printf 'base\n' > "$overlap_repo/shared.txt"
printf 'base\n' > "$overlap_repo/disjoint.txt"
git -C "$overlap_repo" add shared.txt disjoint.txt
git -C "$overlap_repo" commit -qm base
dev_head="$(git -C "$overlap_repo" rev-parse HEAD)"
git -C "$overlap_repo" update-ref refs/remotes/origin/dev "$dev_head"
git -C "$overlap_repo" switch -qc feature/inflight
printf 'changed\n' > "$overlap_repo/shared.txt"
git -C "$overlap_repo" commit -qam overlap
git -C "$overlap_repo" switch -q dev

# Restore real engine functions after the focused verdict stubs above.
AWAY_DISPATCH_SOURCE_ONLY=1 source "$REAL_REPO_ROOT/scripts/away-dispatch.sh"
repo_root="$overlap_repo"
away_dev_root="$dev_root"
away_tasks_dir="$dev_root/tasks"
away_queue_file="$away_tasks_dir/away-queue.md"
away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
tasks_dir="$away_tasks_dir"

# A merged PR satisfies dependencies even when squash merge means the branch
# tip is not an ancestor. If GitHub itself fails, the gate fails closed.
AWAY_FAKE_GH_MODE=merged
gh() {
  case "$AWAY_FAKE_GH_MODE" in
    merged) printf '1\n' ;;
    open) printf '0\n' ;;
    fail) return 1 ;;
  esac
}
away_dependency_satisfied squash-dependency
AWAY_FAKE_GH_MODE=fail
if away_dependency_satisfied squash-dependency; then
  printf '%s\n' "dependency passed while the merged-PR lookup failed" >&2
  exit 1
fi
[[ "$away_detail" == "GitHub merged-PR lookup failed for feature/squash-dependency" ]]
AWAY_FAKE_GH_MODE=open
git -C "$overlap_repo" branch feature/ancestor "$dev_head"
away_dependency_satisfied ancestor

# Capacity counts unique ledger-dispatched slugs whose branch still exists and
# whose PR is not merged. Unknown PR state is conservatively active.
away_ledger_file="$dev_root/tasks/away-ledger.jsonl"
cat > "$away_ledger_file" <<'LEDGER'
{"action":"dispatched","dispatched":{"slug":"one","branch":"feature/inflight"}}
{"action":"dispatched","dispatched":{"slug":"two","branch":"feature/inflight"}}
{"action":"dispatched","dispatched":{"slug":"three","branch":"feature/inflight"}}
LEDGER
away_pr_merged() { return 1; }
away_active_dispatch_count
[[ "$away_active_count" -eq 3 ]]
rm -f "$away_ledger_file"

write_plan footprint-overlap pi model-a shared.txt
write_plan footprint-disjoint pi model-a disjoint.txt
(
  # Re-source to restore the real overlap function after the focused verdict
  # stubs above, then point all Git reads at the isolated repository.
  AWAY_DISPATCH_SOURCE_ONLY=1 source "$REAL_REPO_ROOT/scripts/away-dispatch.sh"
  repo_root="$overlap_repo"
  cd "$overlap_repo"
  tasks_dir="$dev_root/tasks"
  load_flight_index
  away_branch_is_inactive() { return 1; }
  away_check_overlap "$dev_root/tasks/tasks-footprint-overlap.md"
  [[ "$away_overlap_branch" == "feature/inflight" ]]
  [[ "${away_overlap_files[*]}" == "shared.txt" ]]
  if away_check_overlap "$dev_root/tasks/tasks-footprint-disjoint.md"; then
    printf '%s\n' "disjoint plan was reported as overlapping" >&2
    exit 1
  fi
)

# Tick dispatch, ledger shape, double-dispatch guard, and status rendering.
AWAY_DISPATCH_SOURCE_ONLY=1 source "$REAL_REPO_ROOT/scripts/away-dispatch.sh"
repo_root="$REAL_REPO_ROOT"
away_dev_root="$dev_root"
away_tasks_dir="$dev_root/tasks"
away_queue_file="$away_tasks_dir/away-queue.md"
away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
tasks_dir="$away_tasks_dir"
write_plan tick-plan pi 'openai/tick-model' internal/tick.go
printf '%s\n' '- tick-plan' > "$away_queue_file"
: > "$away_tasks_dir/.away-armed"
rm -f "$away_ledger_file"
dispatch_calls=0
away_resolve_dev() {
  away_dev_root="$dev_root"
  away_tasks_dir="$dev_root/tasks"
  away_queue_file="$away_tasks_dir/away-queue.md"
  away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
  tasks_dir="$away_tasks_dir"
}
away_fetch_dev() { return 0; }
away_schedule_next_wake() { return 0; }
away_process_notifications() { return 0; }
away_reconcile_caffeinate() { return 0; }
away_render_schedule_status() {
  printf 'LaunchAgent: loaded (fixture)\nScheduled wake: fixture\n'
}
load_flight_index() {
  flight_numbers=()
  flight_states=()
  flight_refs=()
  flight_branch_names=()
  flight_branch_states=()
  flight_branch_refs=()
}
away_active_dispatch_count() { away_active_count=0; }
away_check_overlap() { return 1; }
away_dispatch_plan() {
  dispatch_calls=$((dispatch_calls + 1))
  printf '%s\n' "$*" > "$fixture_root/dispatch-args"
  return 0
}
away_worktree_for_branch() { printf '%s' "$fixture_root/tick-plan"; }
away_tick
[[ "$dispatch_calls" -eq 1 ]]
[[ "$(<"$fixture_root/dispatch-args")" == "tick-plan pi openai/tick-model" ]]
[[ "$(wc -l < "$away_ledger_file" | tr -d ' ')" == "1" ]]
jq -e '
  .action == "dispatched" and .armed == true and
  .dispatched == {
    slug: "tick-plan",
    kind: "pi",
    model: "openai/tick-model",
    branch: "feature/tick-plan",
    worktree: $worktree
  } and .skips == [] and (.ts | type == "string")
' --arg worktree "$fixture_root/tick-plan" "$away_ledger_file" >/dev/null
[[ "$(stat -f '%Lp' "$away_ledger_file")" == "600" ]]

# A second tick sees the append-only dispatch record immediately before any
# possible start and records a no-op instead of calling the dispatcher again.
away_tick
[[ "$dispatch_calls" -eq 1 ]]
[[ "$(wc -l < "$away_ledger_file" | tr -d ' ')" == "2" ]]
tail -n 1 "$away_ledger_file" | jq -e '
  .action == "noop" and .dispatched == null and
  any(.skips[]; .slug == "tick-plan" and .reason == "in-flight")
' >/dev/null

away_branch_exists() { return 0; }
away_pr_merged() { return 1; }
status_output="$(away_status)"
grep -q '^Away dispatcher: armed$' <<< "$status_output"
grep -q 'tick-plan.*feature/tick-plan.*0/1' <<< "$status_output"
grep -q 'tick-plan.*in-flight' <<< "$status_output"
grep -q 'noop: tick-plan=in-flight' <<< "$status_output"

rm -f "$away_tasks_dir/.away-armed"
away_tick
[[ "$(wc -l < "$away_ledger_file" | tr -d ' ')" == "3" ]]
tail -n 1 "$away_ledger_file" | jq -e '
  .action == "noop" and .armed == false and
  any(.skips[]; .reason == "disarmed")
' >/dev/null

# Daily digest delivery is once per local day. Stall alerts fire once per state
# transition, and a relay failure records the structured digest for retry.
AWAY_DISPATCH_SOURCE_ONLY=1 source "$REAL_REPO_ROOT/scripts/away-dispatch.sh"
repo_root="$REAL_REPO_ROOT"
away_dev_root="$dev_root"
away_tasks_dir="$dev_root/tasks"
away_queue_file="$away_tasks_dir/away-queue.md"
away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
away_wake_state_file="$away_tasks_dir/.away-wake.json"
away_notify_state_file="$away_tasks_dir/.away-notify-state.json"
tasks_dir="$away_tasks_dir"

# Caffeinate is acquired immediately for a new dispatch, released when known
# active work is no longer working, and conservatively retained when Herdr is
# temporarily unavailable but an active dispatch remains.
caffeinate_install_calls=0
caffeinate_uninstall_calls=0
away_install_caffeinate_assertion() { caffeinate_install_calls=$((caffeinate_install_calls + 1)); }
away_uninstall_caffeinate_assertion() { caffeinate_uninstall_calls=$((caffeinate_uninstall_calls + 1)); }
away_has_working_dispatched_agent() { return 1; }
away_dispatched_slug="new-dispatch"
away_active_count=1
away_reconcile_caffeinate
[[ "$caffeinate_install_calls" -eq 1 && "$caffeinate_uninstall_calls" -eq 0 ]]
away_dispatched_slug=""
away_reconcile_caffeinate
[[ "$caffeinate_uninstall_calls" -eq 1 ]]
away_has_working_dispatched_agent() { return 2; }
away_active_count=1
away_reconcile_caffeinate
[[ "$caffeinate_install_calls" -eq 2 ]]
away_active_count=0
away_reconcile_caffeinate
[[ "$caffeinate_uninstall_calls" -eq 2 ]]

# Restore the real detection/reconciliation functions and prove worktree-scoped
# Herdr status matching.
AWAY_DISPATCH_SOURCE_ONLY=1 source "$REAL_REPO_ROOT/scripts/away-dispatch.sh"
away_dev_root="$dev_root"
away_tasks_dir="$dev_root/tasks"
away_queue_file="$away_tasks_dir/away-queue.md"
away_ledger_file="$away_tasks_dir/away-ledger.jsonl"
away_notify_state_file="$away_tasks_dir/.away-notify-state.json"
tasks_dir="$away_tasks_dir"
cat > "$away_ledger_file" <<LEDGER
{"action":"dispatched","dispatched":{"slug":"working-plan","branch":"feature/working-plan","worktree":"$fixture_root/working-plan"}}
LEDGER
away_active_count=1
away_branch_exists() { return 0; }
away_pr_merged() { return 1; }
herdr() {
  printf '%s\n' "{\"result\":{\"snapshot\":{\"agents\":[{\"agent_status\":\"working\",\"cwd\":\"$fixture_root/working-plan\"}]}}}"
}
away_has_working_dispatched_agent
herdr() {
  printf '%s\n' "{\"result\":{\"snapshot\":{\"agents\":[{\"agent_status\":\"idle\",\"cwd\":\"$fixture_root/working-plan\"}]}}}"
}
if away_has_working_dispatched_agent; then
  printf '%s\n' "idle dispatched agent requested caffeinate" >&2
  exit 1
fi

AWAY_TODAY="2030-01-02"
cat > "$away_ledger_file" <<'LEDGER'
{"ts":"2030-01-02T08:00:00Z","action":"dispatched","dispatched":{"slug":"digest-plan","branch":"feature/digest-plan"},"skips":[],"armed":true}
LEDGER
rm -f "$away_notify_state_file"
away_dispatched_slug=""
away_dispatched_branch=""
away_active_count=0
away_queue_slugs=("blocked-plan")
away_skip_slugs=("blocked-plan")
away_skip_reasons=("dep-unmerged")
away_skip_details=("dependency is not merged")
away_notifications_json='[]'
notify_calls=0
: > "$fixture_root/notify-messages"
away_send_telegram() {
  notify_calls=$((notify_calls + 1))
  printf '%s\n---\n' "$1" >> "$fixture_root/notify-messages"
  return 0
}
gh() {
  printf '%s\n' '[{"number":42,"headRefName":"feature/digest-plan","title":"Digest PR","url":"https://example.invalid/42","isDraft":false}]'
}
herdr() {
  printf '%s\n' '{"result":{"snapshot":{"agents":[{"pane_id":"p1","agent":"pi","agent_status":"blocked","cwd":"/tmp/digest-plan"}]}}}'
}
away_process_notifications
[[ "$notify_calls" -eq 2 ]]
jq -e '.last_digest_date == "2030-01-02" and .stall_state == "all-blocked"' \
  "$away_notify_state_file" >/dev/null
printf '%s\n' "$away_notifications_json" | jq -e '
  any(.[]; .kind == "daily-digest" and .status == "sent" and
    .payload.dispatched[0].slug == "digest-plan" and
    .payload.prs_awaiting_approval[0].number == 42 and
    .payload.blocked_agents[0].pane_id == "p1") and
  any(.[]; .kind == "stall-alert" and .status == "sent" and .payload.state == "all-blocked")
' >/dev/null
grep -q 'Away Dispatcher daily digest' "$fixture_root/notify-messages"
grep -q 'every remaining queue entry is blocked' "$fixture_root/notify-messages"

# Same-day/same-state ticks are debounced.
away_notifications_json='[]'
away_process_notifications
[[ "$notify_calls" -eq 2 ]]
[[ "$away_notifications_json" == '[]' ]]

# Clearing the stall rearms the transition. A later empty queue sends exactly
# one distinct immediate alert.
away_active_count=1
away_process_notifications
jq -e '.stall_state == "none"' "$away_notify_state_file" >/dev/null
away_active_count=0
away_queue_slugs=()
away_skip_slugs=()
away_skip_reasons=()
away_skip_details=()
away_notifications_json='[]'
away_process_notifications
[[ "$notify_calls" -eq 3 ]]
printf '%s\n' "$away_notifications_json" | jq -e '
  length == 1 and .[0].kind == "stall-alert" and .[0].payload.state == "queue-empty"
' >/dev/null
away_notifications_json='[]'
away_process_notifications
[[ "$notify_calls" -eq 3 ]]

# Failure leaves the date unsent, records the full digest on the tick, and does
# not fail the dispatcher cycle.
jq -cn '{last_digest_date:"2030-01-01",stall_state:"none",updated_at:"fixture"}' \
  > "$away_notify_state_file"
away_queue_slugs=("static-problem")
away_skip_slugs=("static-problem")
away_skip_reasons=("missing-plan")
away_skip_details=("fixture")
away_notifications_json='[]'
away_send_telegram() {
  away_notify_error="relay-unreachable"
  return 1
}
away_process_notifications
printf '%s\n' "$away_notifications_json" | jq -e '
  any(.[]; .kind == "daily-digest" and .status == "deferred" and
    .error == "relay-unreachable" and .payload.date == "2030-01-02")
' >/dev/null
jq -e '.last_digest_date == "2030-01-01"' "$away_notify_state_file" >/dev/null
away_tick_error=""
away_append_ledger true
tail -n 1 "$away_ledger_file" | jq -e '
  any(.notifications[]; .kind == "daily-digest" and .status == "deferred" and
    (.payload.standing_skips | length) == 1)
' >/dev/null

# Arming's herdr prerequisite gate checks the exact pin/config/service/plugin
# chain and rejects any non-loopback listener.
(
  source_dir="$fixture_root/herdr-source"
  config_dir="$fixture_root/herdr-config"
  mkdir -p "$source_dir/.git" "$source_dir/relay" "$config_dir"
  cat > "$config_dir/config.env" <<CONFIG
HERDR_RELAY_PORT=8375
HERDR_RELAY_DIR=$source_dir/relay
HERDR_TUNNEL_MODE=none
HERDR_TG_ENABLED=true
HERDR_TG_CHAT_TYPE=private
CONFIG
  cat > "$config_dir/secrets.env" <<'SECRETS'
HERDR_RELAY_TOKEN=0123456789abcdef0123456789abcdef
HERDR_TG_TOKEN=123456:fixture_token
HERDR_TG_CHAT_ID=123456
HERDR_RELAY=ws://127.0.0.1:8375?token=0123456789abcdef0123456789abcdef
SECRETS
  chmod 644 "$config_dir/config.env"
  chmod 600 "$config_dir/secrets.env"
  away_herdr_source_dir="$source_dir"
  away_herdr_expected_commit="fixture-pin"
  away_herdr_config_file="$config_dir/config.env"
  away_herdr_secrets_file="$config_dir/secrets.env"
  git() {
    case "$*" in
      *"rev-parse HEAD"*) printf '%s\n' fixture-pin ;;
      *"status --porcelain"*) ;;
      *) command git "$@" ;;
    esac
  }
  herdr() {
    printf '%s\n' "- herdr-remote.relay (Herdr Remote Relay) enabled [local:$source_dir/relay]"
  }
  away_launchctl() {
    case "$*" in
      "print gui/$(id -u)/com.herdr-remote.relay"|"print gui/$(id -u)/com.herdr-remote.telegram") return 0 ;;
      "print gui/$(id -u)/com.herdr-remote.tunnel") return 1 ;;
      "getenv HERDR_RELAY_HOST") return 0 ;;
      *) return 1 ;;
    esac
  }
  lsof() { printf '%s\n' p123 n127.0.0.1:8375; }
  away_verify_herdr_remote
  lsof() { printf '%s\n' p123 'n*:8375'; }
  if away_verify_herdr_remote >/dev/null 2>&1; then
    printf '%s\n' "public herdr listener passed the arming gate" >&2
    exit 1
  fi
)

printf '%s\n' "away-dispatch.test.sh: OK"
