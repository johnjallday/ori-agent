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
  zsh -c 'source "$1"; shift; wt away "$@"' _ "$repo_root/scripts/wt.sh" "$@"
}

output="$(run_wt status)"
grep -q '^Away dispatcher: disarmed$' <<< "$output"
grep -q '^Queue verdicts:$' <<< "$output"
grep -q '^Last tick:$' <<< "$output"

# A disarmed tick is silent and exits before the queue authorization boundary.
mkdir "$dev_root/tasks/away-queue.md"
output="$(run_wt tick)"
[[ -z "$output" ]]

output="$(run_wt arm)"
[[ "$output" == "Away dispatcher armed." ]]
[[ -f "$dev_root/tasks/.away-armed" ]]
[[ "$(stat -f '%Lp' "$dev_root/tasks/.away-armed")" == "600" ]]

output="$(run_wt arm)"
[[ "$output" == "Away dispatcher is already armed." ]]
output="$(run_wt status)"
grep -q '^Away dispatcher: armed$' <<< "$output"

output="$(run_wt disarm)"
[[ "$output" == "Away dispatcher disarmed. Running agents were not interrupted." ]]
[[ ! -e "$dev_root/tasks/.away-armed" ]]

output="$(run_wt disarm)"
[[ "$output" == "Away dispatcher is already disarmed." ]]

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
tasks_dir="$away_tasks_dir"
rmdir "$away_queue_file"

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

printf '%s\n' "away-dispatch.test.sh: OK"
