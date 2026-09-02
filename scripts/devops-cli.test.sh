#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
script="$repo_root/scripts/devops.sh"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-devops.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

if [[ ! -x "$script" ]]; then
  printf '%s\n' "scripts/devops.sh is not executable" >&2
  exit 1
fi
for retired in issue backlog ready; do
  if [[ -e "$repo_root/scripts/devops/$retired.sh" ]]; then
    printf 'retired entrypoint still exists: scripts/devops/%s.sh\n' "$retired" >&2
    exit 1
  fi
done
if [[ -e "$repo_root/scripts/lib/devflow-bootstrap.sh" ]]; then
  printf '%s\n' "the retired DevOps-to-Herdr bootstrap still exists" >&2
  exit 1
fi

failures=0
check() {
  local description="$1" actual="$2" expected="$3"
  if [[ "$actual" != "$expected" ]]; then
    printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$description" "$actual" "$expected" >&2
    failures=$((failures + 1))
  fi
}

# ---------------------------------------------------------------------------
# Unit tests for the pure label helpers.
#
# The picker builds an in-memory index and filters it itself, so its matching
# has to agree with the label filters `gh` applies server-side. Until these
# tests existed the picker path had no coverage at all, and a substring match
# against the ", "-joined label string silently hid every Issue whose target
# label was not listed first.
# ---------------------------------------------------------------------------
DEVOPS_SOURCE_ONLY=1 source "$script"

check "labels_contain matches a sole label" \
  "$(labels_contain "backlog" backlog && echo yes || echo no)" "yes"
check "labels_contain matches a first label" \
  "$(labels_contain "backlog, size:prd" backlog && echo yes || echo no)" "yes"
# The regression: `backlog` sits after `type:fix`, exactly like Issue #329.
check "labels_contain matches a middle label" \
  "$(labels_contain "type:fix, backlog, size:quick" backlog && echo yes || echo no)" "yes"
check "labels_contain matches a last label" \
  "$(labels_contain "type:fix, backlog" backlog && echo yes || echo no)" "yes"
check "labels_contain rejects a missing label" \
  "$(labels_contain "type:fix, needs-decision" backlog && echo yes || echo no)" "no"
# A prefix must not count: `backlog` is not `backlog-archive`.
check "labels_contain rejects a prefix collision" \
  "$(labels_contain "backlog-archive" backlog && echo yes || echo no)" "no"
check "labels_contain rejects an empty label set" \
  "$(labels_contain "" backlog && echo yes || echo no)" "no"
check "labels_contain tolerates a spaced label name" \
  "$(labels_contain "good first issue, backlog" "good first issue" && echo yes || echo no)" "yes"

# `ready` is the list he acts on: proposals plus backlog that is neither already
# covered by a proposal nor already chosen.
check "ready accepts a proposal" \
  "$(labels_are_ready "feature-proposal" && echo yes || echo no)" "yes"
check "ready accepts plain backlog" \
  "$(labels_are_ready "backlog, size:quick" && echo yes || echo no)" "yes"
check "ready hides a bundled member" \
  "$(labels_are_ready "backlog, bundled, size:quick" && echo yes || echo no)" "no"
check "ready hides an approved issue" \
  "$(labels_are_ready "backlog, approved" && echo yes || echo no)" "no"
check "ready hides an approved proposal" \
  "$(labels_are_ready "feature-proposal, approved" && echo yes || echo no)" "no"
check "ready hides a blocked issue" \
  "$(labels_are_ready "needs-decision" && echo yes || echo no)" "no"

# Size is the newest signal and the one that says whether to write a PRD, so it
# gets its own column instead of being truncated out of a joined label blob.
check "size is extracted" "$(size_label_of "type:fix, backlog, size:quick")" "quick"
check "size prd is extracted" "$(size_label_of "backlog, size:prd")" "prd"
check "missing size is empty" "$(size_label_of "needs-decision")" ""
check "other labels drop size" "$(other_labels_of "type:fix, backlog, size:quick")" "type:fix, backlog"
check "other labels survive alone" "$(other_labels_of "needs-decision")" "needs-decision"

# row_matches_filter is what the picker actually calls per row.
check "picker backlog view includes a non-first label" \
  "$(row_matches_filter backlog "type:fix, backlog, size:quick" && echo yes || echo no)" "yes"
check "picker all view includes everything" \
  "$(row_matches_filter all "" && echo yes || echo no)" "yes"
check "picker proposals view is exact" \
  "$(row_matches_filter proposals "feature-proposal" && echo yes || echo no)" "yes"

# `plan-new` parsing has an explicit Bash 3.2 result contract: title words stay
# in an indexed array until they are joined for GitHub, body bytes have their
# own field, and workflow/planner/confirmation choices never become shell text.
parse_plan_new_args Coordinate based map \
  --body 'The map jumps; $(echo inert)' --size quick --kind pi \
  --model '[openai] gpt 5.1' --thinking max --yes
check "plan-new parser preserves title words" "${plan_new_title_words[*]}" "Coordinate based map"
check "plan-new parser joins the GitHub title once" "$plan_new_title" "Coordinate based map"
check "plan-new parser preserves opaque body text" "$plan_new_body" 'The map jumps; $(echo inert)'
check "plan-new parser keeps size separate" "$plan_new_size" "quick"
check "plan-new parser keeps kind separate" "$plan_new_kind" "pi"
check "plan-new parser keeps model separate" "$plan_new_model" "[openai] gpt 5.1"
check "plan-new parser keeps thinking separate" "$plan_new_thinking" "max"
check "plan-new parser records confirmation separately" "$plan_new_assume_yes" "1"

parser_body_file="$fixture_root/parser-plan-new-body.md"
printf 'line one\nline two\n' > "$parser_body_file"
parse_plan_new_args "File-backed context" --body-file "$parser_body_file" --size planned
check "plan-new parser preserves body-file trailing newlines" "$plan_new_body" $'line one\nline two\n'
check "plan-new parser leaves optional planner kind empty" "$plan_new_kind" ""
check "plan-new parser leaves optional planner model empty" "$plan_new_model" ""
check "plan-new parser leaves optional thinking empty" "$plan_new_thinking" ""
check "plan-new parser leaves terminal confirmation pending" "$plan_new_assume_yes" "0"

ready_create_calls="$fixture_root/ready-create-unit-calls"
: > "$ready_create_calls"
ready_create_result="$({
  confirm_write() { return 0; }
  gh() {
    printf '%s\n' "$@" > "$ready_create_calls"
    printf '%s\n' 'https://git.example.test/team/ori/issues/481'
  }
  create_ready_issue 'Ready map work' 'Reviewed problem context.' planned 0 > /dev/null
  printf '%s/%s/%s' "$ready_issue_created" "$ready_issue_number" "$ready_issue_url"
})"
check "Ready-Issue creation exposes durable URL and number only after success" \
  "$ready_create_result" "1/481/https://git.example.test/team/ori/issues/481"
check "Ready-Issue creation keeps title as one gh argument" \
  "$(grep -Fxc 'Ready map work' "$ready_create_calls" || true)" "1"
check "Ready-Issue creation keeps body as one gh argument" \
  "$(grep -Fxc 'Reviewed problem context.' "$ready_create_calls" || true)" "1"
check "Ready-Issue creation adds backlog once" \
  "$(grep -Fxc 'backlog' "$ready_create_calls" || true)" "1"
check "Ready-Issue creation adds exactly one selected size" \
  "$(grep -Fxc 'size:planned' "$ready_create_calls" || true)" "1"
if grep -Eq 'approved|feature-proposal|needs-decision|answered|bundled' "$ready_create_calls"; then
  printf 'FAIL Ready-Issue creation added a forbidden workflow label: %s\n' \
    "$(cat "$ready_create_calls")" >&2
  failures=$((failures + 1))
fi

: > "$ready_create_calls"
ready_decline_result="$({
  confirm_write() { return 1; }
  gh() { printf 'unexpected write\n' >> "$ready_create_calls"; }
  create_ready_issue 'Declined work' 'Reviewed context.' quick 0 > /dev/null 2>&1 || true
  printf '%s/<%s>/<%s>' "$ready_issue_created" "$ready_issue_number" "$ready_issue_url"
})"
check "declined Ready-Issue creation exposes no durable identity" \
  "$ready_decline_result" "0/<>/<>"
check "declined Ready-Issue creation performs no gh call" \
  "$(wc -c < "$ready_create_calls" | tr -d ' ')" "0"

check "Issue URL recovery accepts a host-agnostic positive suffix" \
  "$({ recover_ready_issue_number 'https://ghe.example.test/org/repo/issues/77'; printf '%s' "$ready_issue_number"; })" "77"
check "Issue URL recovery accepts one trailing slash" \
  "$({ recover_ready_issue_number 'http://localhost:3000/org/repo/issues/8/'; printf '%s' "$ready_issue_number"; })" "8"
for malformed_issue_result in \
  'https://github.com/org/repo/issues/0' \
  'https://github.com/org/repo/issues/-4' \
  'https://github.com/org/repo/issues/42?tab=body' \
  'https://github.com/org/repo/pulls/42' \
  $'https://github.com/org/repo/issues/42\nextra output' \
  '42'; do
  recovery_status=0
  recover_ready_issue_number "$malformed_issue_result" || recovery_status=$?
  check "Issue URL recovery rejects malformed result '$malformed_issue_result'" "$recovery_status" "1"
  check "malformed Issue URL exposes no guessed number" "$ready_issue_number" ""
done

malformed_create_state="$fixture_root/ready-create-malformed-state"
: > "$ready_create_calls"
(
  confirm_write() { return 0; }
  gh() {
    printf '%s\n' "$@" > "$ready_create_calls"
    printf '%s\n' 'created Issue but no URL was returned' 'second output line'
  }
  malformed_status=0
  create_ready_issue 'Created without parseable URL' 'Reviewed context.' prd 1 \
    > "$fixture_root/ready-create-malformed-output" \
    2> "$fixture_root/ready-create-malformed-error" || malformed_status=$?
  printf '%s/%s/<%s>' "$malformed_status" "$ready_issue_created" "$ready_issue_number" \
    > "$malformed_create_state"
)
check "an unparseable successful create is a durable partial result" \
  "$(<"$malformed_create_state")" "1/1/<>"
check "an unparseable create still performs only one GitHub write" \
  "$(grep -c '^issue$' "$ready_create_calls" || true)" "1"
if ! grep -Fq 'created Issue but no URL was returned' "$fixture_root/ready-create-malformed-error" || \
   ! grep -Fq 'second output line' "$fixture_root/ready-create-malformed-error" || \
   ! grep -Fq 'no planner was launched and no rollback was attempted' "$fixture_root/ready-create-malformed-error" || \
   ! grep -Fq 'wt plan --issue <number>' "$fixture_root/ready-create-malformed-error"; then
  printf 'FAIL malformed successful create did not provide honest manual recovery:\n%s\n' \
    "$(cat "$fixture_root/ready-create-malformed-error")" >&2
  failures=$((failures + 1))
fi

# Stub the process boundary for pure gating tests. Separate integration coverage
# below proves launch_planner_plan invokes the repository's wt function through zsh.
eval "$(declare -f launch_planner_plan | sed '1s/launch_planner_plan/launch_planner_plan_real/')"
eval "$(declare -f prompt_planner_model | sed '1s/prompt_planner_model/prompt_planner_model_real/')"
eval "$(declare -f prompt_planner_selection | sed '1s/prompt_planner_selection/prompt_planner_selection_real/')"
eval "$(declare -f load_pi_model_catalog | sed '1s/load_pi_model_catalog/load_pi_model_catalog_real/')"

# Catalog discovery is local, offline, and extension-free. Parse only safe
# provider/model identifiers, preserve Pi's order, and deduplicate exact pairs.
catalog_bin="$fixture_root/model-catalog-bin"
catalog_calls="$fixture_root/model-catalog-calls"
mkdir -p "$catalog_bin"
cat > "$catalog_bin/pi" <<'SH'
#!/bin/sh
printf 'offline=%s skip=%s args=%s\n' "$PI_OFFLINE" "$PI_SKIP_VERSION_CHECK" "$*" > "$PI_CATALOG_CALLS"
cat <<'MODELS'
provider   model             context  max-out  thinking  images
openai     gpt-5.6-sol       272K     128K     yes       yes
openai     gpt-5.4           272K     128K     yes       yes
anthropic  claude-sonnet-5   1M       128K     yes       yes
openai     gpt-5.6-sol       272K     128K     yes       yes
openai-codex gpt-5.6-sol     272K     128K     yes       yes
bad$name   unsafe            1K       1K       no        no
MODELS
SH
chmod +x "$catalog_bin/pi"
PI_CATALOG_CALLS="$catalog_calls" PATH="$catalog_bin:$PATH" load_pi_model_catalog_real
check "Pi catalog promotes openai-codex ahead of discovered providers" \
  "${planner_model_catalog_providers[*]}" "openai-codex openai anthropic"
check "Pi catalog keeps unique provider/model options" \
  "${planner_model_catalog_model[*]}" \
  "openai/gpt-5.6-sol openai/gpt-5.4 anthropic/claude-sonnet-5 openai-codex/gpt-5.6-sol"
if ! grep -Fq 'offline=1 skip=1 args=--offline --no-extensions --no-skills --no-prompt-templates --no-themes --no-context-files --no-approve --list-models' "$catalog_calls"; then
  printf 'Pi catalog discovery was not offline and resource-disabled: %s\n' "$(cat "$catalog_calls")" >&2
  exit 1
fi

load_pi_model_catalog() {
  planner_model_catalog_providers=(openai-codex openai anthropic)
  planner_model_catalog_provider=(openai-codex openai openai anthropic)
  planner_model_catalog_model=(openai-codex/gpt-5.6-sol openai/gpt-5.6-sol openai/gpt-5.4 anthropic/claude-sonnet-5)
  return 0
}
check "planner model prompt chooses a numbered provider/model option" \
  "$(printf '3\n1\n' | (prompt_planner_model_real >/dev/null && printf '%s' "$planner_model_choice"))" \
  "anthropic/claude-sonnet-5"
check "planner model prompt can go back and choose another provider" \
  "$(printf '1\nb\n3\n1\n' | (prompt_planner_model_real >/dev/null && printf '%s' "$planner_model_choice"))" \
  "anthropic/claude-sonnet-5"
check "planner model prompt preserves an opaque custom model" \
  "$(printf '%s\n' c '[openai] gpt 5.1; $(echo inert)' | (prompt_planner_model_real >/dev/null && printf '%s' "$planner_model_choice"))" \
  '[openai] gpt 5.1; $(echo inert)'
check "planner model prompt treats Enter as the integration default" \
  "$(printf '\n' | (prompt_planner_model_real >/dev/null && printf '<%s>' "$planner_model_choice"))" "<>"
check "planner model prompt cancels on q" \
  "$(printf 'q\n' | (prompt_planner_model_real >/dev/null 2>&1 && echo yes || echo no))" "no"
check "planner selection chooses Claude model and thinking level" \
  "$(printf '1\n1\n4\n' | (prompt_planner_selection_real >/dev/null && printf '%s/%s/%s' "$planner_kind_choice" "$planner_model_choice" "$planner_thinking_choice"))" \
  "claude/sonnet/xhigh"
check "planner selection offers Fable for Claude" \
  "$(printf '1\n3\n3\n' | (prompt_planner_selection_real >/dev/null && printf '%s/%s/%s' "$planner_kind_choice" "$planner_model_choice" "$planner_thinking_choice"))" \
  "claude/fable/high"
check "planner selection keeps Claude integration defaults" \
  "$(printf '1\n\n\n' | (prompt_planner_selection_real >/dev/null && printf '%s/<%s>/<%s>' "$planner_kind_choice" "$planner_model_choice" "$planner_thinking_choice"))" \
  "claude/<>/<>"
check "planner selection chooses Pi before its model and thinking options" \
  "$(printf '2\n1\n1\n7\n' | (prompt_planner_selection_real >/dev/null && printf '%s/%s/%s' "$planner_kind_choice" "$planner_model_choice" "$planner_thinking_choice"))" \
  "pi/openai-codex/gpt-5.6-sol/max"
load_pi_model_catalog() {
  planner_model_catalog_providers=()
  planner_model_catalog_provider=()
  planner_model_catalog_model=()
  return 1
}
check "planner prompt keeps custom fallback when Pi catalog is unavailable" \
  "$(printf '%s\n' c 'custom/provider-model' | (prompt_planner_model_real >/dev/null && printf '%s' "$planner_model_choice"))" \
  "custom/provider-model"

prompt_planner_selection() {
  planner_kind_choice="pi"
  planner_model_choice='openai-codex/planner-model'
  planner_thinking_choice="max"
  return 0
}
launch_planner_plan() {
  printf 'launched %s for #%s with %s/%s\n' "$2" "$1" "$3" "$4"
}

prepare_calls="$fixture_root/plan-new-prepare-calls"
: > "$prepare_calls"
explicit_prepare_result="$({
  plan_new_is_interactive() { return 1; }
  prompt_planner_selection() { printf 'unexpected prompt\n' >> "$prepare_calls"; return 1; }
  plan_new_kind="claude"
  plan_new_model=""
  plan_new_thinking=""
  plan_new_assume_yes=1
  prepare_plan_new_execution
  printf '%s/<%s>/<%s>' "$plan_new_kind" "$plan_new_model" "$plan_new_thinking"
})"
check "explicit non-interactive planner intent keeps integration defaults" \
  "$explicit_prepare_result" "claude/<>/<>"
check "explicit planner intent does not open interactive prompts" \
  "$(wc -c < "$prepare_calls" | tr -d ' ')" "0"

interactive_prepare_result="$({
  plan_new_is_interactive() { return 0; }
  prompt_planner_selection() {
    planner_kind_choice="pi"
    planner_model_choice="openai-codex/gpt-5.6-sol"
    planner_thinking_choice="max"
    return 0
  }
  plan_new_kind=""
  plan_new_model=""
  plan_new_thinking=""
  plan_new_assume_yes=0
  prepare_plan_new_execution
  printf '%s/%s/%s' "$plan_new_kind" "$plan_new_model" "$plan_new_thinking"
})"
check "interactive plan-new resolves planner selection before a write" \
  "$interactive_prepare_result" "pi/openai-codex/gpt-5.6-sol/max"

noninteractive_prepare_status=0
(
  plan_new_is_interactive() { return 1; }
  plan_new_kind="pi"
  plan_new_assume_yes=0
  prepare_plan_new_execution
) > /dev/null 2>&1 || noninteractive_prepare_status=$?
check "non-interactive planner preparation requires --yes" "$noninteractive_prepare_status" "2"

noninteractive_prepare_status=0
(
  plan_new_is_interactive() { return 1; }
  plan_new_kind=""
  plan_new_assume_yes=1
  prepare_plan_new_execution
) > /dev/null 2>&1 || noninteractive_prepare_status=$?
check "non-interactive planner preparation requires an explicit kind" "$noninteractive_prepare_status" "2"

cancelled_prepare_result="$({
  plan_new_is_interactive() { return 0; }
  prompt_planner_selection() { return 1; }
  plan_new_kind=""
  plan_new_model=""
  plan_new_thinking=""
  plan_new_assume_yes=0
  prepare_plan_new_execution >/dev/null 2>&1 || true
  printf '%s/<%s>' "$plan_new_cancelled" "$plan_new_kind"
})"
check "planner cancellation is recorded before creation" "$cancelled_prepare_result" "1/<>"

# start_plan is the picker's `s` key: launch only for a valid selected Ready
# row, with no GitHub read of its own and no shell execution for rejected input.
check "plan asks for and forwards an agent/model for a selected Ready row" \
  "$(start_plan ready 3 934 2>/dev/null)" "launched pi for #934 with openai-codex/planner-model/max"
check "plan refuses a non-Ready view" \
  "$(start_plan all 3 934 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "plan refuses an empty Ready view" \
  "$(start_plan ready 0 "" >/dev/null 2>&1 && echo yes || echo no)" "no"
check "plan refuses a non-numeric issue number" \
  "$(start_plan ready 3 abc >/dev/null 2>&1 && echo yes || echo no)" "no"
check "plan refuses zero" \
  "$(start_plan ready 3 0 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "plan refuses a negative-looking issue number" \
  "$(start_plan ready 3 -5 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "a rejected plan explains itself on stderr" \
  "$(start_plan all 3 934 2>&1 >/dev/null)" "Switch to the Ready view to start planning."

# start_issue_plan is the opened-Issue action bar equivalent, gated on the
# pre-computed live-label result for the one Issue currently open.
check "issue plan asks for and forwards an agent/model for a Ready Issue" \
  "$(start_issue_plan 353 1 2>/dev/null)" "launched pi for #353 with openai-codex/planner-model/max"
check "issue plan refuses when can_plan is 0" \
  "$(start_issue_plan 334 0 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "an issue plan refusal explains itself on stderr" \
  "$(start_issue_plan 334 0 2>&1 >/dev/null)" \
  "#334 is not Ready; refusing to start planning."

# Bundle selection is a Bash 3.2-compatible immutable-number set. It accepts
# only ordinary Ready backlog Issues, toggles uniquely, survives presentation
# changes, and prunes only against a freshly loaded live index.
bundle_mark_numbers=()
bundle_mark_notice=""
bundle_mark_toggle ready 101 "backlog, size:quick"
check "bundle mark adds an ordinary Ready backlog Issue" \
  "$(bundle_mark_contains 101 && echo yes || echo no)" "yes"
check "bundle mark set stays unique" "${#bundle_mark_numbers[@]}" "1"
bundle_mark_toggle ready 101 "backlog, size:quick"
check "bundle mark toggles off" \
  "$(bundle_mark_contains 101 && echo yes || echo no)" "no"
bundle_mark_toggle all 101 "backlog, size:quick" >/dev/null 2>&1 || true
check "bundle marking refuses another view" "${#bundle_mark_numbers[@]}" "0"
bundle_mark_toggle ready 102 "feature-proposal, size:prd" >/dev/null 2>&1 || true
check "bundle marking refuses feature proposals" "${#bundle_mark_numbers[@]}" "0"
bundle_mark_toggle ready 103 "backlog, bundled, size:planned" >/dev/null 2>&1 || true
check "bundle marking refuses non-Ready backlog" "${#bundle_mark_numbers[@]}" "0"

bundle_mark_numbers=(101 202 303)
all_issue_numbers=(101 202)
all_issue_labels=("backlog, size:quick" "feature-proposal, size:prd")
prune_bundle_marks
check "refresh pruning keeps only live ordinary Ready marks" \
  "${bundle_mark_numbers[*]}" "101"
check "refresh pruning reports every removed mark" "$bundle_mark_notice" \
  "Refresh removed 2 stale or ineligible bundle mark(s): #202, #303."

eval "$(declare -f launch_planner_bundle_plan | sed '1s/launch_planner_bundle_plan/launch_planner_bundle_plan_real/')"
launch_planner_bundle_plan() {
  local kind="$1" model="$2" thinking="$3"
  shift 3
  printf 'launched %s bundle with %s/%s:' "$kind" "$model" "$thinking"
  printf ' #%s' "$@"
  printf '\n'
}
all_issue_numbers=(101 202)
all_issue_labels=("backlog, size:quick" "backlog, size:prd")
check "bundle plan asks for one agent/model and launches every marked Issue" \
  "$(start_bundle_plan ready 101 202 2>/dev/null)" "launched pi bundle with openai-codex/planner-model/max: #101 #202"
check "bundle plan rejects one mark with single-plan guidance" \
  "$(start_bundle_plan ready 101 2>&1 >/dev/null || true)" \
  "Mark at least two ordinary Ready backlog Issues; use s to plan one Issue."
check "bundle plan rejects duplicate marks" \
  "$(start_bundle_plan ready 101 101 2>&1 >/dev/null || true)" \
  "Marked Issue #101 appears more than once; unmark it and retry."
check "bundle plan rejects another view" \
  "$(start_bundle_plan all 101 202 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "bundle plan rejects a stale marked member" \
  "$(start_bundle_plan ready 101 303 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "bundle plan rejects hostile number text" \
  "$(start_bundle_plan ready '202; touch nope' 101 >/dev/null 2>&1 && echo yes || echo no)" "no"
bundle_mark_numbers=()
all_issue_numbers=()
all_issue_labels=()

# Decisions carry a stable marker so grooming can distinguish them from an
# ordinary comment. Rationale is optional and remains plain user-authored text.
check "decision comment has a marker" \
  "$(format_decision_comment "1B, 2A")" \
  $'<!-- ori-decision -->\n\n**Answers:** 1B, 2A'
check "decision comment includes rationale" \
  "$(format_decision_comment "1B, 2A" "Preserves existing JSON consumers")" \
  $'<!-- ori-decision -->\n\n**Answers:** 1B, 2A\n\n**Rationale:** Preserves existing JSON consumers'

# ---------------------------------------------------------------------------
# Trusted local snapshot membership. Only the exact generated marker on line 3
# attaches Issues; marker-looking body text is inert.
# ---------------------------------------------------------------------------
cat > "$fixture_root/issue-single.md" <<'MD'
# Issue #777: Single

<!-- ori-devflow: issue-snapshot; issue=777 -->

<!-- ori-devflow: issue-bundle-snapshot; issues=1,2 -->
MD
read_snapshot_members "$fixture_root/issue-single.md"
check "single snapshot membership is read from line 3" "${snapshot_members[*]}" "777"

cat > "$fixture_root/issue-bundle.md" <<'MD'
# Issue bundle: #801, #802, #803

<!-- ori-devflow: issue-bundle-snapshot; issues=801,802,803 -->

Issue body with <!-- ori-devflow: issue-snapshot; issue=999 --> stays inert.
MD
read_snapshot_members "$fixture_root/issue-bundle.md"
check "bundle snapshot membership is canonical" "${snapshot_members[*]}" "801 802 803"

cat > "$fixture_root/issue-unsorted.md" <<'MD'
# Issue bundle

<!-- ori-devflow: issue-bundle-snapshot; issues=802,801 -->
MD
check "snapshot reader rejects unsorted members" \
  "$(read_snapshot_members "$fixture_root/issue-unsorted.md" && echo yes || echo no)" "no"
cat > "$fixture_root/issue-duplicate.md" <<'MD'
# Issue bundle

<!-- ori-devflow: issue-bundle-snapshot; issues=801,801 -->
MD
check "snapshot reader rejects duplicate members" \
  "$(read_snapshot_members "$fixture_root/issue-duplicate.md" && echo yes || echo no)" "no"
cat > "$fixture_root/issue-body-marker-only.md" <<'MD'
# Issue bundle

No generated marker here.

<!-- ori-devflow: issue-bundle-snapshot; issues=801,802 -->
MD
check "snapshot reader never scans Issue-authored content" \
  "$(read_snapshot_members "$fixture_root/issue-body-marker-only.md" && echo yes || echo no)" "no"

# ---------------------------------------------------------------------------
# In-flight status. Parent groups are the top-level `- [ ]` lines; sub-tasks are
# indented, so an anchored match must not count them.
# ---------------------------------------------------------------------------
cat > "$fixture_root/tasks-777-sample.md" <<'MD'
## Tasks

- [x] 0.0 Create feature branch
  - [x] 0.1 a sub-task that must not be counted as a group
- [x] 1.0 First group
  - [ ] 1.1 an unchecked sub-task inside a done group
- [ ] 2.0 Second group
  - [ ] 2.1 another sub-task
MD
check "task groups count parents only" \
  "$(task_groups_of_file "$fixture_root/tasks-777-sample.md")" "2/3"

cat > "$fixture_root/tasks-778-none.md" <<'MD'
# A PRD with no checkboxes at all
MD
check "a file with no groups reports nothing" \
  "$(task_groups_of_file "$fixture_root/tasks-778-none.md")" ""

# A branch is registered under its Issue number AND its full slug, so features
# predating the number-first convention still resolve.
flight_numbers=()
flight_states=()
remember_branch_flight "fix/339-workspace-map-camera-framing" branch
check "branch resolves by issue number" "$(flight_state_of 339)" "branch"
check "branch resolves by slug" \
  "$(flight_state_of "339-workspace-map-camera-framing")" "branch"

flight_numbers=()
flight_states=()
remember_branch_flight "feature/workspace-ticket-management" worktree
check "a numberless branch resolves by slug" \
  "$(flight_state_of "workspace-ticket-management")" "worktree"
check "an unknown key resolves to nothing" "$(flight_state_of 12345)" ""

# A checked-out worktree is the stronger signal and must never be downgraded to
# a bare branch, whichever order the two sources are read in.
flight_numbers=()
flight_states=()
remember_branch_flight "fix/339-slug" worktree
remember_branch_flight "fix/339-slug" branch
check "worktree survives a later branch record" "$(flight_state_of 339)" "worktree"

flight_numbers=()
flight_states=()
remember_branch_flight "fix/339-slug" branch
remember_branch_flight "fix/339-slug" worktree
check "branch is upgraded to worktree" "$(flight_state_of 339)" "worktree"

flight_numbers=()
flight_states=()
remember_branch_flight "dev" worktree
check "a branch with no type prefix is ignored" "$(flight_state_of dev)" ""

# The picker cell puts progress first - "which group am I on" is the question
# the column exists to answer.
tasks_dir="$fixture_root"
flight_numbers=()
flight_states=()
remember_branch_flight "fix/777-sample" worktree
check "cell shows progress then worktree" "$(flight_cell_of 777)" "2/3 wt"

flight_numbers=()
flight_states=()
remember_branch_flight "fix/777-sample" branch
check "cell shows progress then branch" "$(flight_cell_of 777)" "2/3 br"

flight_numbers=()
flight_states=()
remember_branch_flight "fix/888-nolist" worktree
check "cell falls back to location alone" "$(flight_cell_of 888)" "wt"

flight_numbers=()
flight_states=()
check "cell is empty when nothing is in flight" "$(flight_cell_of 999)" ""

# Label-ready work that already has a branch/worktree belongs in the ongoing
# implementation summary, not in the pickable Ready view.
all_issue_numbers=(320 339)
all_issue_titles=("Already building" "Still ready")
all_issue_labels=("backlog, size:quick" "feature-proposal, size:planned")
all_issue_updates=("2026-08-08" "2026-08-09")
flight_numbers=()
flight_states=()
remember_branch_flight "feature/320-already-building" worktree
apply_picker_filter ready
check "Ready picker excludes an in-flight Issue" "${issue_numbers[*]}" "339"
ready_list_output="$(
  gh() {
    printf '320\tOPEN\tAlready building\tbacklog, size:quick\t2026-08-08T00:00:00Z\n'
    printf '339\tOPEN\tStill ready\tfeature-proposal, size:planned\t2026-08-09T00:00:00Z\n'
  }
  resolve_tasks_dir() { tasks_dir=""; }
  load_flight_index() {
    flight_numbers=()
    flight_states=()
    remember_branch_flight "feature/320-already-building" worktree
  }
  list_issues ready
)"
check "Ready one-shot excludes an in-flight Issue" \
  "$(grep -c $'^320\t' <<< "$ready_list_output" || true)" "0"
check "Ready one-shot retains pickable work" \
  "$(grep -c $'^339\t' <<< "$ready_list_output" || true)" "1"
all_issue_numbers=()
all_issue_titles=()
all_issue_labels=()
all_issue_updates=()
issue_numbers=()
issue_titles=()
issue_labels=()
issue_updates=()

cat > "$fixture_root/tasks-801-802-803-shared.md" <<'MD'
## Tasks
- [x] 1.0 First group
- [ ] 2.0 Second group
MD
cp "$fixture_root/issue-bundle.md" "$fixture_root/issue-801-802-803-shared.md"
flight_numbers=()
flight_states=()
remember_branch_flight "feature/801-802-803-shared" branch
index_attached_plan_flights
check "bundle branch indexes the middle member" "$(flight_state_of 802)" "branch"
check "bundle branch indexes the last member" "$(flight_state_of 803)" "branch"
check "bundle first member cell shares progress" "$(flight_cell_of 801)" "1/2 br"
check "bundle middle member cell shares progress" "$(flight_cell_of 802)" "1/2 br"
check "bundle last member cell shares progress" "$(flight_cell_of 803)" "1/2 br"

# The active worktree carries the trusted snapshot after implementation starts.
# Every bundle member must remain in flight even if dev's planning copy is gone.
active_bundle="$fixture_root/active-bundle"
mkdir -p "$active_bundle/tasks"
cp "$fixture_root/issue-bundle.md" \
  "$active_bundle/tasks/issue-801-802-803-shared.md"
flight_numbers=()
flight_states=()
index_worktree_attachment_flight "$active_bundle" "feature/801-802-803-shared"
check "active bundle snapshot indexes first member" "$(flight_state_of 801)" "worktree"
check "active bundle snapshot indexes middle member" "$(flight_state_of 802)" "worktree"
check "active bundle snapshot indexes last member" "$(flight_state_of 803)" "worktree"

# DevOps consumes the Go-owned implementation table as an opaque summary; it
# does not recreate lifecycle, PR, or agent cells in Bash.
implementation_overview_calls="$fixture_root/implementation-overview-calls"
devflow_helper() {
  printf '<%s>\n' "$@" > "$implementation_overview_calls"
  printf '%s\n' \
    'FEATURE  PHASE         PLAN   GIT    REMOTE  AGENT            ATTENTION' \
    'demo     Implementing  1/2    dirty  no PR   builder working  ok'
}
load_implementation_summary
check "implementation summary uses the checked-out-worktree selector" \
  "$(<"$implementation_overview_calls")" \
  $'<feature-overview>\n<--implementations>\n<--summary>\n<--color>'
check "implementation summary preserves the shared table" \
  "$(grep -c 'demo     Implementing' <<< "$implementation_summary")" "1"
check "successful implementation summary has no error" "$implementation_summary_error" ""

devflow_helper() {
  printf 'demo  Implementing\n'
  return 1
}
load_implementation_summary
check "partial implementation facts survive a degraded collector" \
  "$implementation_summary" "demo  Implementing"
check "degraded implementation snapshot is explicit" \
  "$implementation_summary_error" "Implementation snapshot is incomplete — press r to retry."

devflow_helper() {
  printf '<%s>\n' "$@" > "$implementation_overview_calls"
  printf '%s\n' \
    'FEATURE  PHASE         PLAN   GIT    REMOTE  AGENT            ATTENTION' \
    'demo     Implementing  1/2    dirty  no PR   builder working  ok'
}
list_status > "$fixture_root/implementation-report"
check "status requests the full implementation report" \
  "$(<"$implementation_overview_calls")" \
  $'<feature-overview>\n<--implementations>'
grep -Fq 'demo     Implementing' "$fixture_root/implementation-report"

tasks_dir=""

# Starting implementation resolves only the dev worktree's local, number-first
# task artifacts. Stub the local directory/flight readers inside each subshell
# so these tests cannot touch GitHub, Herdr, or this checkout's real branches.
implementation_tasks="$fixture_root/implementation-tasks"
implementation_side_effects="$fixture_root/implementation-side-effects"
mkdir -p "$implementation_tasks"
: > "$implementation_side_effects"
implementation_fixture_result() {
  local issue_number="$1" fixture_state="${2:-}"
  (
    resolve_tasks_dir() { tasks_dir="$implementation_tasks"; }
    load_flight_index() { :; }
    flight_state_of() { printf '%s' "$fixture_state"; }
    gh() { printf 'github\n' >> "$implementation_side_effects"; return 97; }
    resolve_implementation_feature "$issue_number" || return $?
    printf '%s' "$implementation_feature"
  )
}

check "implementation resolver rejects an invalid Issue number" \
  "$(implementation_fixture_result 0 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "implementation resolver refuses a missing plan" \
  "$(implementation_fixture_result 777 2>&1 >/dev/null || true)" \
  "No completed local plan found for #777. Press s to start planning, then return after the planner writes the real task list."

cat > "$implementation_tasks/tasks-777-sample.md" <<'MD'
## Tasks
- [ ] 1.0 Real implementation work
MD
check "implementation resolver returns the number-first feature slug" \
  "$(implementation_fixture_result 777 2>/dev/null)" "777-sample"

cat > "$implementation_tasks/tasks-778-starter.md" <<'MD'
<!-- ori-devflow: planning-starter; do not implement until the planner replaces this file -->
## Tasks
MD
check "implementation resolver refuses a planning starter" \
  "$(implementation_fixture_result 778 2>&1 >/dev/null || true)" \
  "Planning for #778 is not complete: $implementation_tasks/tasks-778-starter.md is still a planning starter. Return after the planner replaces it with the real task list."

printf '%s\n' '## Tasks' > "$implementation_tasks/tasks-779-first.md"
printf '%s\n' '## Tasks' > "$implementation_tasks/tasks-779-second.md"
multiple_result="$(implementation_fixture_result 779 2>&1 >/dev/null || true)"
if [[ "$multiple_result" != *"Multiple task lists match #779"* || \
  "$multiple_result" != *"tasks-779-first.md"* || \
  "$multiple_result" != *"tasks-779-second.md"* ]]; then
  printf 'FAIL implementation resolver did not describe every ambiguous artifact:\n%s\n' \
    "$multiple_result" >&2
  failures=$((failures + 1))
fi

printf '%s\n' '## Tasks' > "$implementation_tasks/tasks-780-branch.md"
check "implementation resolver refuses an existing branch" \
  "$(implementation_fixture_result 780 branch 2>&1 >/dev/null || true)" \
  "Implementation for #780 already has a branch (780-branch). Resume it instead of starting duplicate work."
printf '%s\n' '## Tasks' > "$implementation_tasks/tasks-781-worktree.md"
check "implementation resolver refuses an existing worktree" \
  "$(implementation_fixture_result 781 worktree 2>&1 >/dev/null || true)" \
  "Implementation for #781 already has a checked-out worktree (781-worktree). Use that worktree instead of starting another."

bundle_feature="801-802-803-shared"
cat > "$implementation_tasks/issue-$bundle_feature.md" <<'MD'
# Issue bundle: #801, #802, #803

<!-- ori-devflow: issue-bundle-snapshot; issues=801,802,803 -->
MD
cat > "$implementation_tasks/tasks-$bundle_feature.md" <<'MD'
## Tasks
- [ ] 1.0 Shared implementation
MD
for bundle_member in 801 802 803; do
  check "implementation resolver maps bundle member #$bundle_member" \
    "$(implementation_fixture_result "$bundle_member" 2>/dev/null)" "$bundle_feature"
done

cat > "$implementation_tasks/tasks-$bundle_feature.md" <<'MD'
<!-- ori-devflow: planning-starter; do not implement until the planner replaces this file -->
## Tasks
MD
check "bundle middle member refuses a pending starter" \
  "$(implementation_fixture_result 802 2>&1 >/dev/null || true)" \
  "Planning for #802 is not complete: $implementation_tasks/tasks-$bundle_feature.md is still a planning starter. Return after the planner replaces it with the real task list."
printf '%s\n' '## Tasks' '- [ ] 1.0 Shared implementation' > "$implementation_tasks/tasks-$bundle_feature.md"
check "bundle middle member refuses an existing shared branch" \
  "$(implementation_fixture_result 802 branch 2>&1 >/dev/null || true)" \
  "Implementation for #802 already has a branch ($bundle_feature). Resume it instead of starting duplicate work."

cat > "$implementation_tasks/issue-802-individual.md" <<'MD'
# Issue #802: Individual

<!-- ori-devflow: issue-snapshot; issue=802 -->
MD
printf '%s\n' '## Tasks' > "$implementation_tasks/tasks-802-individual.md"
overlap_result="$(implementation_fixture_result 802 2>&1 >/dev/null || true)"
if [[ "$overlap_result" != *"Multiple task lists match #802"* || \
      "$overlap_result" != *"tasks-$bundle_feature.md"* || \
      "$overlap_result" != *"tasks-802-individual.md"* ]]; then
  printf 'FAIL implementation resolver did not reject overlapping plans:\n%s\n' "$overlap_result" >&2
  failures=$((failures + 1))
fi
rm -f "$implementation_tasks/issue-802-individual.md" "$implementation_tasks/tasks-802-individual.md"

cat > "$implementation_tasks/issue-804-805-malformed.md" <<'MD'
# Issue bundle

<!-- ori-devflow: issue-bundle-snapshot; issues=805,804 -->
MD
printf '%s\n' '## Tasks' > "$implementation_tasks/tasks-804-805-malformed.md"
malformed_result="$(implementation_fixture_result 804 2>&1 >/dev/null || true)"
if [[ "$malformed_result" != *"malformed generated attachment marker on line 3"* ]]; then
  printf 'FAIL implementation resolver did not reject malformed bundle identity:\n%s\n' "$malformed_result" >&2
  failures=$((failures + 1))
fi

check "implementation resolver performs no GitHub or Herdr side effect" \
  "$(wc -c < "$implementation_side_effects" | tr -d ' ')" "0"

implementation_choice_result() {
  local choice="$1"
  (
    prompt_implementation_agent 777 777-sample <<< "$choice" >/dev/null 2>&1
    printf '%s' "$implementation_mode"
  )
}
check "Claude choice maps to its wt kind" "$(implementation_choice_result 1)" "claude"
check "Codex choice maps to its wt kind" "$(implementation_choice_result 2)" "codex"
check "Pi choice maps to its wt kind" "$(implementation_choice_result 3)" "pi"
check "worktree-only choice maps to no-herdr" "$(implementation_choice_result 4)" "no-herdr"
check "cancel maps to no launch mode" "$(implementation_choice_result q)" ""
implementation_prompt_output="$(prompt_implementation_agent 777 777-sample <<< q 2>/dev/null)"
if [[ "$implementation_prompt_output" == *"model"* || "$implementation_prompt_output" == *"Model"* ]]; then
  printf 'FAIL the one-run implementation picker gained a model prompt: %s\n' "$implementation_prompt_output" >&2
  failures=$((failures + 1))
fi

# Persistent agent defaults stay behind the Go helper boundary. These tests
# stub only that process, then assert current rendering, preview/confirmation,
# validation, clear forms, and exact separate arguments without touching TOML,
# GitHub, or Herdr.
agent_defaults_calls="$fixture_root/agent-defaults-calls"
agent_defaults_forbidden="$fixture_root/agent-defaults-forbidden"
agent_defaults_helper() {
  {
    printf 'CALL\n'
    local item
    for item in "$@"; do
      printf '<%s>\n' "$item"
    done
  } >> "$agent_defaults_calls"
  case " $* " in
    *" --tsv "*)
      [[ "${AGENT_DEFAULTS_FAIL_STAGE:-}" != read ]] || return 9
      printf 'primary\tclaude\t\nrole_fallback\tclaude\t\n'
      ;;
    *" --validate-only "*)
      [[ " $* " != *" invented "* ]] || { printf 'unsupported kind\n' >&2; return 2; }
      [[ "${AGENT_DEFAULTS_FAIL_STAGE:-}" != validate ]] || return 8
      ;;
    *)
      [[ "${AGENT_DEFAULTS_FAIL_STAGE:-}" != update ]] || return 7
      printf 'Agent defaults (updated)\n'
      ;;
  esac
}

: > "$agent_defaults_calls"
current_defaults_output="$(agent_defaults_action < /dev/null)"
if [[ "$current_defaults_output" != *"Primary:       kind=claude  model=integration default"* ||
      "$current_defaults_output" != *"Role fallback: kind=claude  model=integration default"* ]]; then
  printf 'FAIL current agent defaults output: %s\n' "$current_defaults_output" >&2
  failures=$((failures + 1))
fi
check "current defaults perform one helper read" "$(grep -c '^CALL$' "$agent_defaults_calls")" "1"

: > "$agent_defaults_calls"
update_defaults_output="$(
  gh() { printf 'github\n' >> "$agent_defaults_forbidden"; return 97; }
  herdr() { printf 'herdr\n' >> "$agent_defaults_forbidden"; return 97; }
  agent_defaults_action --primary-kind pi --primary-model '[openai] gpt 5.1' \
    --role-kind codex --role-model 'openai/role model' --yes < /dev/null
)"
if [[ "$update_defaults_output" != *"Agent defaults preview"* ||
      "$update_defaults_output" != *"claude -> pi"* ||
      "$update_defaults_output" != *"integration default -> [openai] gpt 5.1"* ]]; then
  printf 'FAIL agent defaults preview: %s\n' "$update_defaults_output" >&2
  failures=$((failures + 1))
fi
check "agent defaults update reads validates and writes" "$(grep -c '^CALL$' "$agent_defaults_calls")" "3"
check "model with spaces and brackets is one helper argument" \
  "$(grep -Fxc '<[openai] gpt 5.1>' "$agent_defaults_calls")" "2"
check "role model with spaces is one helper argument" \
  "$(grep -Fxc '<openai/role model>' "$agent_defaults_calls")" "2"
: > "$agent_defaults_calls"
rm -f "$fixture_root/model-shell-injection"
agent_defaults_action --primary-kind pi \
  --primary-model '[openai] x; $(touch model-shell-injection)' --yes < /dev/null > /dev/null
check "shell-looking model remains one inert helper argument" \
  "$(grep -Fxc '<[openai] x; $(touch model-shell-injection)>' "$agent_defaults_calls")" "2"
check "shell-looking model executes nothing" \
  "$([[ -e "$fixture_root/model-shell-injection" || -e model-shell-injection ]] && echo yes || echo no)" "no"
check "agent defaults action contacts neither GitHub nor Herdr" \
  "$([[ -e "$agent_defaults_forbidden" ]] && wc -c < "$agent_defaults_forbidden" | tr -d ' ' || printf 0)" "0"

: > "$agent_defaults_calls"
agent_defaults_action --primary-kind pi --role-kind codex --clear-primary-model --clear-role-model --yes < /dev/null > /dev/null
check "clear primary model reaches validate and update" \
  "$(grep -c '^<--clear-primary-model>$' "$agent_defaults_calls")" "2"
check "clear role model reaches validate and update" \
  "$(grep -c '^<--clear-role-model>$' "$agent_defaults_calls")" "2"

: > "$agent_defaults_calls"
noop_defaults_output="$(agent_defaults_action --primary-kind claude --clear-primary-model --role-kind claude --clear-role-model --yes < /dev/null)"
check "a no-op reports no write" "$noop_defaults_output" "Agent defaults are unchanged; no file was written."
check "a no-op performs only the current-value read" "$(grep -c '^CALL$' "$agent_defaults_calls")" "1"

: > "$agent_defaults_calls"
decline_status=0
(
  confirm_write() { return 1; }
  agent_defaults_action --primary-kind pi < /dev/null > /dev/null
) || decline_status=$?
check "declining an agent defaults preview is non-success" "$decline_status" "1"
check "decline stops after read and validation" "$(grep -c '^CALL$' "$agent_defaults_calls")" "2"

agent_default_prompt_cancelled=0
prompt_agent_default_value "Primary kind" claude 0 <<< q > /dev/null 2>&1 || true
check "interactive agent defaults cancellation is recorded" "$agent_default_prompt_cancelled" "1"

: > "$agent_defaults_calls"
check "invalid kind fails through Go-side validation" \
  "$(agent_defaults_action --primary-kind invented --yes < /dev/null > /dev/null 2>&1 && echo yes || echo no)" "no"
check "invalid kind never reaches the update call" "$(grep -c '^CALL$' "$agent_defaults_calls")" "2"

: > "$agent_defaults_calls"
AGENT_DEFAULTS_FAIL_STAGE=read
check "helper read failure is preserved" \
  "$(agent_defaults_action < /dev/null > /dev/null 2>&1 && echo yes || echo no)" "no"
unset AGENT_DEFAULTS_FAIL_STAGE
: > "$agent_defaults_calls"
AGENT_DEFAULTS_FAIL_STAGE=update
check "helper update failure is preserved" \
  "$(agent_defaults_action --primary-kind pi --yes < /dev/null > /dev/null 2>&1 && echo yes || echo no)" "no"
unset AGENT_DEFAULTS_FAIL_STAGE
check "failed update still passed validation first" "$(grep -c '^CALL$' "$agent_defaults_calls")" "3"

# Drive the full local resolve -> prompt -> launch flow against the same
# isolated fixture. The launcher is a recorder here; exact zsh vectors are
# asserted again below once the fake zsh executable is installed.
implementation_start_calls="$fixture_root/implementation-start-calls"
implementation_start_fixture() {
  local issue_number="$1" fixture_state="$2" choice="$3"
  (
    resolve_tasks_dir() { tasks_dir="$implementation_tasks"; }
    load_flight_index() { :; }
    flight_state_of() { printf '%s' "$fixture_state"; }
    gh() { printf 'github\n' >> "$implementation_side_effects"; return 97; }
    launch_implementation() {
      printf '%s\t%s\n' "$1" "$2" >> "$implementation_start_calls"
    }
    start_issue_implementation "$issue_number" <<< "$choice" >/dev/null
  )
}

: > "$implementation_start_calls"
check "the implementation action refuses a planning starter before launch" \
  "$(implementation_start_fixture 778 "" 1 2>/dev/null && echo yes || echo no)" "no"
check "a refused planning starter launches nothing" \
  "$(wc -c < "$implementation_start_calls" | tr -d ' ')" "0"

printf '%s\n' '## Tasks' > "$implementation_tasks/tasks-782-demo.md"
for implementation_choice_fixture in '1:claude' '2:codex' '3:pi' '4:no-herdr'; do
  choice="${implementation_choice_fixture%%:*}"
  expected_mode="${implementation_choice_fixture#*:}"
  : > "$implementation_start_calls"
  implementation_start_fixture 782 "" "$choice"
  check "the implementation action launches $expected_mode" \
    "$(<"$implementation_start_calls")" $'782-demo\t'"$expected_mode"
done
: > "$implementation_start_calls"
check "the implementation action refuses duplicate work" \
  "$(implementation_start_fixture 782 worktree 1 2>/dev/null && echo yes || echo no)" "no"
check "duplicate-work refusal launches nothing" \
  "$(wc -c < "$implementation_start_calls" | tr -d ' ')" "0"

# The picker's :edit path supports a conventional editor command with arguments
# and returns multiline Markdown without printing it through command substitution.
fake_editor="$fixture_root/fake-editor"
cat > "$fake_editor" <<'SH'
#!/bin/sh
for target in "$@"; do :; done
printf '## Context\n\nMultiline detail.' > "$target"
SH
chmod +x "$fake_editor"
VISUAL="$fake_editor --wait"
edit_issue_body
unset VISUAL
check "editor body survives" "$edited_issue_body" $'## Context\n\nMultiline detail.'

# Prompt helpers are tested with their write functions stubbed: this exercises
# the actual title/body and answers/rationale collection without needing a pty.
prompt_capture="$fixture_root/prompt-capture"
create_issue() {
  printf '%s\n' "$@" > "$prompt_capture"
}
printf 'Map contrast\nDashed borders disappear\n' | prompt_create_issue > /dev/null
check "picker new passes an inline body" "$(<"$prompt_capture")" \
  $'Map contrast\n--body\nDashed borders disappear'
printf 'Fast capture\n\n' | prompt_create_issue > /dev/null
check "picker new keeps body optional" "$(<"$prompt_capture")" "Fast capture"

# New & Plan owns a distinct prompt contract: title and short context are
# required, :edit remains available for multiline context, sizing is explicit,
# and planner selection completes before the one-shot primitive can write.
eval "$(declare -f plan_new_issue | sed '1s/plan_new_issue/plan_new_issue_prompt_real/')"
plan_new_prompt_calls="$fixture_root/plan-new-prompt-calls"
plan_new_prompt_order="$fixture_root/plan-new-prompt-order"
plan_new_issue() {
  printf 'plan-new\n' >> "$plan_new_prompt_order"
  printf '<%s>\n' "$@" > "$plan_new_prompt_calls"
  ready_issue_created="${PLAN_NEW_PROMPT_STUB_CREATED:-1}"
  ready_issue_number="${PLAN_NEW_PROMPT_STUB_NUMBER:-481}"
  return "${PLAN_NEW_PROMPT_STUB_STATUS:-0}"
}
prompt_planner_selection() {
  printf 'planner\n' >> "$plan_new_prompt_order"
  planner_kind_choice="claude"
  planner_model_choice="sonnet"
  planner_thinking_choice="high"
  return 0
}

: > "$plan_new_prompt_calls"
: > "$plan_new_prompt_order"
printf '\n' | prompt_plan_new_issue > "$fixture_root/plan-new-prompt-title-cancel" 2>/dev/null || true
check "New & Plan empty title cancels" \
  "$(grep -Fc 'Cancelled.' "$fixture_root/plan-new-prompt-title-cancel" || true)" "1"
check "New & Plan title cancellation makes no planner or write call" \
  "$(wc -c < "$plan_new_prompt_order" | tr -d ' ')" "0"

: > "$plan_new_prompt_calls"
: > "$plan_new_prompt_order"
printf 'Camera framing\n   \n' | prompt_plan_new_issue \
  > "$fixture_root/plan-new-prompt-context-cancel" 2>/dev/null || true
check "New & Plan requires non-empty short context" \
  "$(grep -Fc 'Problem context is required; cancelled.' "$fixture_root/plan-new-prompt-context-cancel" || true)" "1"
check "New & Plan context cancellation makes no planner or write call" \
  "$(wc -c < "$plan_new_prompt_order" | tr -d ' ')" "0"

edit_issue_body() {
  edited_issue_body=$'## Context\n\nMultiline map detail.'
}
: > "$plan_new_prompt_calls"
: > "$plan_new_prompt_order"
prompt_plan_new_issue <<< $'Multiline brief\n:edit\n1\n' \
  > "$fixture_root/plan-new-prompt-edit" 2>/dev/null || true
check "New & Plan :edit preserves multiline context heading" \
  "$(grep -Fc '<## Context' "$plan_new_prompt_calls" || true)" "1"
check "New & Plan :edit preserves multiline context detail" \
  "$(grep -Fc 'Multiline map detail.>' "$plan_new_prompt_calls" || true)" "1"
check "New & Plan quick route reaches the one-shot primitive" \
  "$(grep -Fxc '<quick>' "$plan_new_prompt_calls" || true)" "1"
check "New & Plan records a durable created Issue for picker refresh" \
  "$plan_new_prompt_created/$plan_new_prompt_number" "1/481"

for size_fixture in '1:quick' '2:planned' '3:prd'; do
  size_choice="${size_fixture%%:*}"
  expected_size="${size_fixture#*:}"
  : > "$plan_new_prompt_calls"
  : > "$plan_new_prompt_order"
  printf 'Sized brief\nReviewed context\n%s\n' "$size_choice" | prompt_plan_new_issue \
    > "$fixture_root/plan-new-prompt-size-$expected_size" 2>/dev/null || true
  check "New & Plan maps size choice $size_choice to $expected_size" \
    "$(grep -Fxc "<$expected_size>" "$plan_new_prompt_calls" || true)" "1"
  check "New & Plan selects the planner before invoking plan-new for $expected_size" \
    "$(<"$plan_new_prompt_order")" $'planner\nplan-new'
done
size_prompt_output="$(cat "$fixture_root/plan-new-prompt-size-quick")"
if [[ "$size_prompt_output" != *"Quick — direct task planning, no PRD"* || \
      "$size_prompt_output" != *"Planned — detailed task planning, no PRD"* || \
      "$size_prompt_output" != *"PRD — clarify requirements before task planning"* ]]; then
  printf 'FAIL New & Plan size prompt does not describe all routes: %s\n' "$size_prompt_output" >&2
  failures=$((failures + 1))
fi

: > "$plan_new_prompt_calls"
: > "$plan_new_prompt_order"
printf 'Retry size\nReviewed context\ninvalid\n2\n' | prompt_plan_new_issue \
  > "$fixture_root/plan-new-prompt-size-retry" 2>&1 || true
check "New & Plan retries an invalid size choice" \
  "$(grep -Fxc '<planned>' "$plan_new_prompt_calls" || true)" "1"
check "New & Plan explains an invalid size choice" \
  "$(grep -Fc 'Choose Quick, Planned, PRD, or Cancel.' "$fixture_root/plan-new-prompt-size-retry" || true)" "1"

: > "$plan_new_prompt_calls"
: > "$plan_new_prompt_order"
printf 'Cancelled size\nReviewed context\nq\n' | prompt_plan_new_issue \
  > "$fixture_root/plan-new-prompt-size-cancel" 2>/dev/null || true
check "New & Plan size cancellation occurs before planner selection" \
  "$(wc -c < "$plan_new_prompt_order" | tr -d ' ')" "0"

prompt_planner_selection() {
  printf 'planner\n' >> "$plan_new_prompt_order"
  return 1
}
: > "$plan_new_prompt_calls"
: > "$plan_new_prompt_order"
prompt_plan_new_issue <<< $'Planner cancel\nReviewed context\n1\n' \
  > "$fixture_root/plan-new-prompt-planner-cancel" 2>/dev/null || true
check "New & Plan planner cancellation creates nothing" \
  "$(wc -c < "$plan_new_prompt_calls" | tr -d ' ')" "0"
check "New & Plan reaches planner selection exactly once before cancellation" \
  "$(grep -c '^planner$' "$plan_new_prompt_order" || true)" "1"
check "planner cancellation leaves no durable refresh signal" \
  "$plan_new_prompt_created/<$plan_new_prompt_number>" "0/<>"

# A child planning failure happens after creation. The prompt preserves both
# the non-zero status and the durable refresh signal so the picker can show the
# Ready row and its retry receipt.
prompt_planner_selection() {
  planner_kind_choice="pi"
  planner_model_choice=""
  planner_thinking_choice="high"
  return 0
}
: > "$plan_new_prompt_calls"
: > "$plan_new_prompt_order"
PLAN_NEW_PROMPT_STUB_STATUS=9
prompt_failure_status=0
prompt_plan_new_issue <<< $'Durable partial result\nReviewed context\n2\n' \
  > "$fixture_root/plan-new-prompt-child-failure" 2>/dev/null || prompt_failure_status=$?
unset PLAN_NEW_PROMPT_STUB_STATUS
check "New & Plan preserves a planner child failure status" "$prompt_failure_status" "9"
check "planner child failure retains the durable refresh signal" \
  "$plan_new_prompt_created/$plan_new_prompt_number" "1/481"

# A declined/failed create has no durable signal, so the picker must retain its
# cached list rather than perform a refresh-dependent mutation.
PLAN_NEW_PROMPT_STUB_CREATED=0
PLAN_NEW_PROMPT_STUB_STATUS=7
prompt_failure_status=0
prompt_plan_new_issue <<< $'Failed create\nReviewed context\n1\n' \
  > "$fixture_root/plan-new-prompt-create-failure" 2>/dev/null || prompt_failure_status=$?
unset PLAN_NEW_PROMPT_STUB_CREATED PLAN_NEW_PROMPT_STUB_STATUS
check "New & Plan preserves a create failure status" "$prompt_failure_status" "7"
check "create failure leaves no durable refresh signal" \
  "$plan_new_prompt_created/<$plan_new_prompt_number>" "0/<>"

# Restore the fixed selector used by the existing opened-Issue unit fixtures and
# the real one-shot primitive for any later direct helper checks.
prompt_planner_selection() {
  planner_kind_choice="pi"
  planner_model_choice='openai-codex/planner-model'
  planner_thinking_choice="max"
  return 0
}
eval "$(declare -f plan_new_issue_prompt_real | sed '1s/plan_new_issue_prompt_real/plan_new_issue/')"

issue_numbers=(320 481 999)
check "picker can select a newly created Issue by immutable number" \
  "$(picker_row_index_of 481)" "1"
check "picker leaves another view unselected when the new Issue is absent" \
  "$(picker_row_index_of 777 >/dev/null 2>&1 && echo yes || echo no)" "no"
issue_numbers=()

view_issue() {
  printf 'viewed #%s\n' "$1"
}
decide_issue() {
  printf '%s\n' "$@" > "$prompt_capture"
  decision_recorded=1
}
start_issue_implementation() {
  printf 'started implementation for #%s\n' "$1"
}
# Stubbed so the action bar's eligibility read stays local: the fake `gh` is not
# on PATH until the integration section, and a unit test must never reach the
# network.
stub_labels="needs-decision"
issue_labels_of() {
  printf '%s\n' "$stub_labels"
}
decision_recorded=0
printf 'c\n1B, 2A\nPreserves existing callers\n\n' | \
  prompt_open_issue 334 > "$fixture_root/prompt-decision-output"
grep -Fq '[c] Decide' "$fixture_root/prompt-decision-output"
view_count="$(grep -Fc 'viewed #334' "$fixture_root/prompt-decision-output")"
check "opened Issue refreshes after a decision" "$view_count" "2"
check "opened Issue decision passes rationale" "$(<"$prompt_capture")" \
  $'334\n1B, 2A\n--rationale\nPreserves existing callers'

# The action bar is drawn for ONE known Issue, so unlike the picker's shared
# footer it can drop Decide where nothing is pending. #353-style Issues
# (`backlog`, spec says "Open questions: None") must not be offered the action.
stub_labels="backlog, size:planned"
: > "$prompt_capture"
decision_recorded=0
printf 'c\n\n' | prompt_open_issue 353 > "$fixture_root/prompt-ineligible-output" \
  2> "$fixture_root/prompt-ineligible-error"
check "an ineligible Issue is not offered Decide" \
  "$(grep -Fc '[c] Decide' "$fixture_root/prompt-ineligible-output" || true)" "0"
check "an ineligible Issue still offers the other actions" \
  "$(grep -Fc '[r] Refresh  [Enter] Back' "$fixture_root/prompt-ineligible-output" || true)" "2"
# Typing c anyway (or arriving from the picker's c key) is refused locally,
# without collecting answers the write would only reject.
grep -Fq '#353 is not needs-decision' "$fixture_root/prompt-ineligible-error"
check "an ineligible Issue collects no answers" "$(<"$prompt_capture")" ""
check "an ineligible Issue does not prompt for answers" \
  "$(grep -Fc 'Record a decision' "$fixture_root/prompt-ineligible-output" || true)" "0"
check "an ineligible Issue records nothing" "$decision_recorded" "0"

# Plan sits beside Decide on the same action bar and reads the SAME live
# labels, but through labels_are_ready rather than needs-decision, so the two
# actions disagree exactly where the underlying rule says they should. #353
# above is plain `backlog`: not needs-decision, but Ready to build. The bar is
# redrawn once before the "s" is read and again before the trailing blank
# line backs out, so its offered actions each appear twice.
: > "$prompt_capture"
decision_recorded=0
printf 's\n\n' | prompt_open_issue 353 > "$fixture_root/prompt-plan-output" \
  2> "$fixture_root/prompt-plan-error"
check "a Ready Issue is offered Plan" \
  "$(grep -Fc '[s] Plan' "$fixture_root/prompt-plan-output" || true)" "2"
check "a Ready Issue's Plan action starts the selected agent" \
  "$(grep -Fc 'launched pi for #353' "$fixture_root/prompt-plan-output" || true)" "1"
check "pressing s makes no decision write" "$(<"$prompt_capture")" ""
check "pressing s records no decision" "$decision_recorded" "0"

printf 'i\n\n' | prompt_open_issue 353 > "$fixture_root/prompt-implementation-output" \
  2> "$fixture_root/prompt-implementation-error"
check "an opened Issue offers Start implementation" \
  "$(grep -Fc '[i] Start implementation' "$fixture_root/prompt-implementation-output" || true)" "2"
check "the opened-Issue implementation action uses that Issue number" \
  "$(grep -Fc 'started implementation for #353' "$fixture_root/prompt-implementation-output" || true)" "1"

# needs-decision alone is the mirror image: eligible for Decide, not for Plan.
stub_labels="needs-decision"
: > "$prompt_capture"
decision_recorded=0
printf 's\nr\n\n' | prompt_open_issue 334 > "$fixture_root/prompt-plan-ineligible-output" \
  2> "$fixture_root/prompt-plan-ineligible-error"
check "a non-Ready Issue is not offered Plan" \
  "$(grep -Fc '[s] Plan' "$fixture_root/prompt-plan-ineligible-output" || true)" "0"
grep -Fq '#334 is not Ready; refusing to start planning.' \
  "$fixture_root/prompt-plan-ineligible-error"
check "a non-Ready Issue's s does not launch Pi" \
  "$(grep -Fc 'launched Pi' "$fixture_root/prompt-plan-ineligible-output" || true)" "0"
# Refresh and Back still work immediately after a refused Plan.
check "Refresh still works right after a refused Plan" \
  "$(grep -Fc 'viewed #334' "$fixture_root/prompt-plan-ineligible-output")" "2"

# A label read that fails must default Plan closed, unlike Decide's fail-open
# default: launching a planner must never fail open. issue_labels_of is restored
# immediately after.
issue_labels_of() { return 1; }
: > "$prompt_capture"
decision_recorded=0
printf 's\n\n' | prompt_open_issue 999 > "$fixture_root/prompt-plan-unreadable-output" \
  2> "$fixture_root/prompt-plan-unreadable-error"
check "an unreadable label state is not offered Plan" \
  "$(grep -Fc '[s] Plan' "$fixture_root/prompt-plan-unreadable-output" || true)" "0"
grep -Fq '#999 is not Ready; refusing to start planning.' \
  "$fixture_root/prompt-plan-unreadable-error"
check "an unreadable label state still offers Decide (fails open, unlike Plan)" \
  "$(grep -Fc '[c] Decide' "$fixture_root/prompt-plan-unreadable-output" || true)" "2"
issue_labels_of() {
  printf '%s\n' "$stub_labels"
}

# The picker's c key opens the Issue with an initial c action; an eligible Issue
# must still skip straight to answers rather than redrawing the bar.
stub_labels="type:fix, needs-decision"
: > "$prompt_capture"
decision_recorded=0
printf '1A\n\n\n' | prompt_open_issue 334 c > "$fixture_root/prompt-picker-c-output"
check "the picker c key still reaches answers on an eligible Issue" \
  "$(<"$prompt_capture")" $'334\n1A'

stub_labels="needs-decision"

if [[ "$failures" -ne 0 ]]; then
  printf '%s unit assertion(s) failed\n' "$failures" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Integration tests against a fake `gh`.
# ---------------------------------------------------------------------------
fake_bin="$fixture_root/bin"
mkdir -p "$fake_bin"
gh_calls="$fixture_root/gh-calls"
gh_body="$fixture_root/gh-body"
wt_calls="$fixture_root/wt-calls"

cat > "$fake_bin/gh" <<'SH'
#!/bin/sh
tab="$(printf '\t')"
call_line="CALL"
for argument in "$@"; do
  call_line="$call_line$tab$argument"
done
# One append keeps concurrent picker collectors from interleaving call records.
printf '%s\n' "$call_line" >> "$GH_CALLS"
if [ -n "${GH_ARGV:-}" ]; then
  {
    printf 'CALL\n'
    for argument in "$@"; do
      printf '<%s>\n' "$argument"
    done
  } >> "$GH_ARGV"
fi

if [ -n "${GH_FAIL:-}" ]; then
  printf '%s\n' "simulated GitHub failure" >&2
  exit 7
fi

case "$1 $2" in
  "issue list")
    label=""
    search=""
    previous=""
    for argument in "$@"; do
      if [ "$previous" = "--label" ]; then
        label="$argument"
      fi
      if [ "$previous" = "--search" ]; then
        search="$argument"
      fi
      previous="$argument"
    done
    if [ -n "$search" ]; then
      printf '339\tOPEN\tCamera framing bundle\tfeature-proposal\t2026-08-11T09:10:00Z\n'
      printf '320\tOPEN\tOnboarding no workspace\tbacklog\t2026-08-08T09:06:15Z\n'
      exit 0
    fi
    case "$label" in
      "")
        printf '334\tOPEN\tHome redesign\tneeds-decision\t2026-08-10T12:45:22Z\n'
        printf '320\tOPEN\tOnboarding no workspace\tbacklog\t2026-08-08T09:06:15Z\n'
        ;;
      needs-decision)
        printf '334\tOPEN\tHome redesign\tneeds-decision\t2026-08-10T12:45:22Z\n'
        ;;
      backlog)
        printf '320\tOPEN\tOnboarding no workspace\tbacklog\t2026-08-08T09:06:15Z\n'
        ;;
      feature-proposal)
        ;;
      *)
        printf 'unexpected label: %s\n' "$label" >&2
        exit 98
        ;;
    esac
    ;;
  "issue view")
    if [ "${4:-}" = "--json" ]; then
      # The eligibility lookup. Each fixture Issue has one fixed label set, so a
      # decision test asserts eligibility rather than list ordering: 334 carries
      # needs-decision in a NON-first position, 320 is plain backlog, 321 is a
      # prefix-alike that must not qualify, and anything else is unlabelled.
      case "$3" in
        334) printf 'type:fix, needs-decision, size:quick\n' ;;
        320) printf 'backlog, size:quick\n' ;;
        321) printf 'needs-decision-later\n' ;;
        *) printf '\n' ;;
      esac
    elif [ "${4:-}" = "--comments" ]; then
      printf 'Issue #%s detail\n' "$3"
      printf 'Issue #%s decision comment\n' "$3"
    else
      printf 'Issue #%s detail\n' "$3"
    fi
    ;;
  "issue comment")
    if [ -n "${GH_FAIL_COMMENT:-}" ]; then
      printf '%s\n' "simulated decision comment failure" >&2
      exit 8
    fi
    previous=""
    for argument in "$@"; do
      if [ "$previous" = "--body" ]; then
        printf '%s' "$argument" > "$GH_BODY"
      fi
      previous="$argument"
    done
    printf 'commented on #%s\n' "$3"
    ;;
  "issue create")
    previous=""
    for argument in "$@"; do
      if [ "$previous" = "--body" ]; then
        printf '%s' "$argument" > "$GH_BODY"
      fi
      previous="$argument"
    done
    if [ -n "${GH_CREATE_OUTPUT+x}" ]; then
      printf '%s\n' "$GH_CREATE_OUTPUT"
    else
      printf 'https://github.com/johnjallday/ori-agent/issues/999\n'
    fi
    ;;
  "issue edit")
    if [ -n "${GH_FAIL_ANSWERED_LABEL:-}" ] && \
      [ "${4:-}" = "--add-label" ] && [ "${5:-}" = "answered" ]; then
      printf '%s\n' "simulated answered-label failure" >&2
      exit 9
    fi
    printf 'edited #%s\n' "$3"
    ;;
  "release view")
    if [ -n "${GH_RELEASE_NONE:-}" ]; then
      printf 'no releases found\n' >&2
      exit 1
    fi
    printf 'v0.0.106\t2026-08-15T10:00:00Z\thttps://github.com/johnjallday/ori-agent/releases/tag/v0.0.106\n'
    ;;
  "pr list")
    if [ -n "${GH_FAIL_PR:-}" ]; then
      printf 'simulated GitHub failure\n' >&2
      exit 7
    fi
    if [ -n "${GH_PR_EMPTY:-}" ]; then
      exit 0
    fi
    # Two PRs merged strictly after the release's publish instant (381, 380);
    # three that must NOT count - one at the exact same instant (379), one
    # earlier the SAME DAY (378), and one from a prior day (377). The boundary
    # is the exact instant, not the calendar date.
    printf '381\t2026-08-18T12:00:00Z\tanother PR after release\n'
    printf '380\t2026-08-19T08:00:00Z\tnewest PR after release\n'
    printf '379\t2026-08-15T10:00:00Z\tsame instant as release, must not count\n'
    printf '378\t2026-08-15T09:59:59Z\tsame day before release, must not count\n'
    printf '377\t2026-08-14T09:00:00Z\tbefore release, must not count\n'
    ;;
  *)
    printf 'unexpected gh invocation: %s\n' "$*" >&2
    exit 99
    ;;
esac
SH
chmod +x "$fake_bin/gh"

# launch_planner_plan crosses from bash into the sourced zsh wt function. Record the
# exact child-process argument vector without loading wt or contacting Herdr.
cat > "$fake_bin/zsh" <<'SH'
#!/bin/sh
{
  printf 'CALL'
  for argument in "$@"; do
    printf '\t%s' "$argument"
  done
  printf '\n'
} >> "$WT_CALLS"
if [ -n "${WT_FAIL:-}" ]; then
  printf 'simulated planner child failure\n' >&2
  exit 11
fi
case "$3" in
  devops-plan)
    if [ -n "${WT_DECLINE:-}" ]; then
      printf 'Planning declined; nothing was changed.\n'
    else
      printf 'Planner launched for #%s\n' "$5"
    fi
    ;;
  devops-bundle-plan) printf 'Bundle planner launched\n' ;;
  devops-start) printf 'Implementation start launched for %s with %s\n' "$5" "$6" ;;
esac
SH
chmod +x "$fake_bin/zsh"

# Planner selection discovers only the installed Pi catalog. This fake keeps
# the integration test hermetic and proves the REPL can render real options.
cat > "$fake_bin/pi" <<'SH'
#!/bin/sh
cat <<'MODELS'
provider   model            context  max-out  thinking  images
openai     gpt-5.6-sol      272K     128K     yes       yes
anthropic  claude-sonnet-5  1M       128K     yes       yes
openai-codex gpt-5.6-sol    272K     128K     yes       yes
MODELS
SH
chmod +x "$fake_bin/pi"

# The picker embeds the read-only Go feature overview. This binary records no
# state and emits the same bounded compact table for summary and detail paths.
cat > "$fake_bin/herdr-devflow" <<'SH'
#!/bin/sh
if [ "${1:-}" = "--repo-root" ]; then
  shift 2
fi
if [ "${1:-}" != "feature-overview" ] || [ "${2:-}" != "--implementations" ]; then
  printf 'unexpected devflow invocation: %s\n' "$*" >&2
  exit 98
fi
printf '%s\n' \
  'FEATURE  PHASE         PLAN   GIT    REMOTE  AGENT            ATTENTION' \
  'demo     Implementing  1/2    dirty  no PR   builder working  ok'
if [ "${3:-}" != "--summary" ]; then
  printf '\nSnapshot: complete\n'
fi
SH
chmod +x "$fake_bin/herdr-devflow"

export PATH="$fake_bin:$PATH"
export GH_CALLS="$gh_calls"
export GH_BODY="$gh_body"
export WT_CALLS="$wt_calls"
export GH_ARGV="$fixture_root/gh-argv"

assert_call() {
  local expected="$1"
  if ! grep -Fqx "$expected" "$gh_calls"; then
    printf 'missing gh call:\n  %s\nactual:\n%s\n' "$expected" "$(cat "$gh_calls")" >&2
    exit 1
  fi
}

assert_no_github() {
  local context="$1"
  if [[ -s "$gh_calls" ]]; then
    printf '%s contacted GitHub: %s\n' "$context" "$(cat "$gh_calls")" >&2
    exit 1
  fi
}

# A bare `grep -Fq` under `set -e` aborts the run with no output at all, which
# leaves a future failure with nothing to read. These say what was expected.
assert_output_has() {
  local context="$1" file="$2" expected="$3"
  if ! grep -Fq "$expected" "$file"; then
    printf '%s did not report %s\n  actual output:\n%s\n' \
      "$context" "$expected" "$(cat "$file")" >&2
    exit 1
  fi
}

assert_output_lacks() {
  local context="$1" file="$2" unexpected="$3"
  if grep -Fq "$unexpected" "$file"; then
    printf '%s unexpectedly reported %s\n  actual output:\n%s\n' \
      "$context" "$unexpected" "$(cat "$file")" >&2
    exit 1
  fi
}

# Deciding now needs one live label READ before it can refuse, so the boundary
# that matters is "no GitHub WRITE", not "no GitHub contact". Reads stay free;
# comment/edit/create are the calls that change something on github.com.
assert_no_github_write() {
  local context="$1"
  if grep -Eq -- $'issue\t(comment|edit|create)' "$gh_calls"; then
    printf '%s wrote to GitHub: %s\n' "$context" "$(cat "$gh_calls")" >&2
    exit 1
  fi
}

count_label_reads() {
  grep -Fc $'CALL\tissue\tview\t'"$1"$'\t--json\tlabels' "$gh_calls" || true
}

# A recorded body is multiline, so its continuation lines land in the transcript
# too. Only the anchored CALL lines are calls.
count_gh_calls() {
  grep -c '^CALL' "$gh_calls" || true
}

count_wt_calls() {
  grep -c '^CALL' "$wt_calls" || true
}

assert_ready_create_labels() {
  local context="$1" expected_size="$2"
  check "$context passes exactly two --label flags" \
    "$(grep -Fxc '<--label>' "$GH_ARGV" || true)" "2"
  check "$context applies backlog exactly once" \
    "$(grep -Fxc '<backlog>' "$GH_ARGV" || true)" "1"
  check "$context applies its selected size exactly once" \
    "$(grep -Fxc "<size:$expected_size>" "$GH_ARGV" || true)" "1"
  check "$context applies no second size route" \
    "$(grep -Ec '^<size:(quick|planned|prd)>$' "$GH_ARGV" || true)" "1"
  if grep -Eq '^<(approved|feature-proposal|needs-decision|answered|bundled)>$' "$GH_ARGV"; then
    printf '%s applied a forbidden workflow label: %s\n' "$context" "$(cat "$GH_ARGV")" >&2
    exit 1
  fi
}

# Drive commands behind a real pseudo-terminal without depending on the
# platform-specific `script(1)` interface. This is deliberately tiny: input is
# supplied up front, output is copied verbatim, and the child's exit status is
# preserved. It lets the command-contract tests distinguish terminal prompts
# from the non-interactive --yes path.
pty_driver="$fixture_root/pty-driver.py"
cat > "$pty_driver" <<'PY'
#!/usr/bin/env python3
import errno
import os
import pty
import sys

pid, master = pty.fork()
if pid == 0:
    os.execv(sys.argv[1], sys.argv[1:])

pending = os.environ.get("PTY_INPUT", "").encode()
while pending:
    written = os.write(master, pending)
    pending = pending[written:]

while True:
    try:
        chunk = os.read(master, 4096)
    except OSError as error:
        if error.errno == errno.EIO:
            break
        raise
    if not chunk:
        break
    os.write(sys.stdout.fileno(), chunk)

_, child_status = os.waitpid(pid, 0)
if os.WIFEXITED(child_status):
    raise SystemExit(os.WEXITSTATUS(child_status))
if os.WIFSIGNALED(child_status):
    raise SystemExit(128 + os.WTERMSIG(child_status))
raise SystemExit(1)
PY
chmod +x "$pty_driver"

# The real one-shot command must honor HERDR_DEVFLOW_CONFIG, work without any
# GitHub call, and leave both the repository config and every refused fixture
# byte-identical. The Go helper is real here; only gh remains the recorder above.
agent_defaults_config="$fixture_root/devflow-agent-defaults.toml"
cp "$repo_root/.herdr/devflow.toml" "$agent_defaults_config"
repo_config_before="$(cksum < "$repo_root/.herdr/devflow.toml")"
: > "$gh_calls"
HERDR_DEVFLOW_CONFIG="$agent_defaults_config" "$script" agent-defaults \
  --primary-kind pi --primary-model '[openai] gpt 5.1' \
  --role-kind codex --role-model 'openai/role model' --yes \
  > "$fixture_root/agent-defaults-real-output"
assert_no_github "agent-defaults one-shot update"
assert_output_has "agent-defaults update" "$fixture_root/agent-defaults-real-output" "Agent defaults preview"
grep -Fq 'kind = "pi"' "$agent_defaults_config"
grep -Fq 'model = "[openai] gpt 5.1"' "$agent_defaults_config"
grep -Fq 'default_kind = "codex"' "$agent_defaults_config"
grep -Fq 'default_model = "openai/role model"' "$agent_defaults_config"
check "HERDR_DEVFLOW_CONFIG isolates the repository config" \
  "$(cksum < "$repo_root/.herdr/devflow.toml")" "$repo_config_before"

refused_before="$(cksum < "$agent_defaults_config")"
if HERDR_DEVFLOW_CONFIG="$agent_defaults_config" "$script" agent-defaults --primary-kind invented --yes \
  > "$fixture_root/agent-defaults-invalid-output" 2>&1; then
  printf 'invalid agent kind was accepted by the real helper\n' >&2
  exit 1
fi
check "invalid real-helper path preserves config" "$(cksum < "$agent_defaults_config")" "$refused_before"
if HERDR_DEVFLOW_CONFIG="$agent_defaults_config" "$script" agent-defaults --primary-kind claude \
  < /dev/null > "$fixture_root/agent-defaults-unconfirmed-output" 2>&1; then
  printf 'non-TTY agent-defaults write succeeded without --yes\n' >&2
  exit 1
fi
check "unconfirmed real-helper path preserves config" "$(cksum < "$agent_defaults_config")" "$refused_before"

# A no-option one-shot is a pure current-value read and still skips GitHub.
: > "$gh_calls"
HERDR_DEVFLOW_CONFIG="$agent_defaults_config" "$script" agent-defaults \
  < /dev/null > "$fixture_root/agent-defaults-current-output"
assert_no_github "agent-defaults one-shot read"
assert_output_has "agent-defaults current read" "$fixture_root/agent-defaults-current-output" "Primary:       kind=pi"

# All remaining full-script picker/status fixtures use the hermetic read-only
# implementation overview above. Agent-defaults forced the checked-in source
# explicitly, so its contract tests were unaffected by this binary override.
export HERDR_DEVFLOW_BINARY="$fake_bin/herdr-devflow"

gh_call_sequence() {
  awk '/^CALL/ {printf "%s %s;", $2, $3}' "$gh_calls"
}

# The planning launcher keeps kind, model, and thinking as inert zsh positional
# arguments. The adapter maps thinking to each native integration's flag.
plan_bridge='source "$1" || exit; typeset -a plan_args; plan_args=(--issue "$2" --kind "$3"); [[ -n "$4" ]] && plan_args+=(--model "$4"); [[ -n "$5" ]] && plan_args+=(--thinking "$5"); [[ "$6" == 1 ]] && plan_args+=(--yes); wt plan "${plan_args[@]}"'
: > "$wt_calls"
launch_planner_plan_real 934 pi '[openai] gpt 5.1; $(echo inert)' max > /dev/null
check "single Pi planner launcher preserves one opaque model argument" \
  "$(<"$wt_calls")" \
  $'CALL\t-c\t'"$plan_bridge"$'\tdevops-plan\t'"$repo_root"$'/scripts/wt.sh\t934\tpi\t[openai] gpt 5.1; $(echo inert)\tmax\t0'
: > "$wt_calls"
launch_planner_plan_real 934 claude 'fable; $(echo inert)' xhigh > /dev/null
check "single Claude planner launcher preserves model and thinking intent" \
  "$(<"$wt_calls")" \
  $'CALL\t-c\t'"$plan_bridge"$'\tdevops-plan\t'"$repo_root"$'/scripts/wt.sh\t934\tclaude\tfable; $(echo inert)\txhigh\t0'
: > "$wt_calls"
launch_planner_plan_real 934 pi 'openai/confirmed' high 1 > /dev/null
check "confirmed planner launcher transports one explicit confirmation bit" \
  "$(tail -c 2 "$wt_calls")" "1"
: > "$wt_calls"
check "planner launcher rejects an invalid confirmation bit" \
  "$(launch_planner_plan_real 934 pi '' high yes >/dev/null 2>&1 && echo yes || echo no)" "no"
check "invalid planner confirmation launches no child" \
  "$(wc -c < "$wt_calls" | tr -d ' ')" "0"

# The implementation launcher crosses the same bash-to-zsh boundary as Plan.
# Its constrained child receives the feature and validated mode as separate
# words, then chooses exactly --kind or --no-herdr inside the child.
implementation_bridge=$'source "$1" && if [[ "$3" == no-herdr ]]; then wt start "$2" --no-herdr; else wt start "$2" --kind "$3"; fi'
for implementation_mode_fixture in claude codex pi no-herdr; do
  : > "$wt_calls"
  launch_implementation "777-sample" "$implementation_mode_fixture" > /dev/null
  check "implementation $implementation_mode_fixture uses the exact zsh argument vector" \
    "$(<"$wt_calls")" \
    $'CALL\t-c\t'"$implementation_bridge"$'\tdevops-start\t'"$repo_root"$'/scripts/wt.sh\t777-sample\t'"$implementation_mode_fixture"
done
: > "$wt_calls"
check "an unsupported implementation mode is rejected" \
  "$(launch_implementation 777-sample shell >/dev/null 2>&1 && echo yes || echo no)" "no"
check "an unsupported implementation mode launches no child" \
  "$(wc -c < "$wt_calls" | tr -d ' ')" "0"

# Exercise the real bundle launcher after the pure unit section replaced it
# with a recorder. Every Issue is a separate zsh positional argument; the fixed
# child builds a quoted array rather than flattening or evaluating them.
launch_real_bundle() (
  launch_planner_bundle_plan_real "$@"
)
bundle_bridge='source "$1" || exit; kind="$2"; model="$3"; thinking="$4"; shift 4; typeset -a plan_args; plan_args=(); for issue in "$@"; do plan_args+=(--issue "$issue"); done; plan_args+=(--kind "$kind"); [[ -n "$model" ]] && plan_args+=(--model "$model"); [[ -n "$thinking" ]] && plan_args+=(--thinking "$thinking"); wt plan "${plan_args[@]}"'
: > "$wt_calls"
launch_real_bundle pi '[openai] bundle model; $(echo inert)' high 202 101 > /dev/null
check "bundle launcher preserves separate kind, model, and Issue arguments" \
  "$(<"$wt_calls")" \
  $'CALL\t-c\t'"$bundle_bridge"$'\tdevops-bundle-plan\t'"$repo_root"$'/scripts/wt.sh\tpi\t[openai] bundle model; $(echo inert)\thigh\t202\t101'
: > "$wt_calls"
check "real bundle launcher rejects hostile Issue argument text" \
  "$(launch_real_bundle claude '' high 101 '202; touch nope' >/dev/null 2>&1 && echo yes || echo no)" "no"
check "hostile bundle argument launches no child" \
  "$(wc -c < "$wt_calls" | tr -d ' ')" "0"

# With no arguments in a non-TTY, the command lists every open Issue before
# showing the line prompt. EOF or q exits cleanly, so automated callers cannot
# hang; a TTY instead gets the keyboard-driven picker.
: > "$gh_calls"
printf 'q\n' | "$script" > "$fixture_root/default-output"
grep -Fq "Open issues" "$fixture_root/default-output"
grep -Fq "Home redesign" "$fixture_root/default-output"
grep -Fq "Needs my decision" "$fixture_root/default-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000'

: > "$gh_calls"
"$script" < /dev/null > "$fixture_root/eof-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000'

# Each one-shot filter is one direct `gh issue list` call. There is no author
# restriction, Project query, helper binary, or hidden second request.
: > "$gh_calls"
"$script" all > "$fixture_root/all-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000'

: > "$gh_calls"
"$script" decisions > "$fixture_root/decisions-output"
grep -Fq "Issues needing my decision" "$fixture_root/decisions-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tneeds-decision'

: > "$gh_calls"
"$script" backlog > "$fixture_root/backlog-output"
grep -Fq "Backlog issues" "$fixture_root/backlog-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tbacklog'

: > "$gh_calls"
"$script" proposals > "$fixture_root/proposals-output"
grep -Fq "No open issues labeled feature-proposal." "$fixture_root/proposals-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tfeature-proposal'

# `ready` stays a single server-side query rather than a second client-side
# model, and its search must exclude both bundled members and approved work.
: > "$gh_calls"
"$script" ready > "$fixture_root/ready-output"
grep -Fq "Ready to build" "$fixture_root/ready-output"
grep -Fq "Camera framing bundle" "$fixture_root/ready-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--search\tlabel:backlog,feature-proposal -label:bundled -label:approved'

if grep -Eq -- '--author|api[[:space:]]+graphql|project' "$gh_calls"; then
  printf 'a label filter reached an author or Project query: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi

# `status` is now the same checked-out-worktree overview as wt status rather
# than a second dev-tasks-only model in Bash. The hermetic helper stands in for
# its GitHub/Herdr collectors here.
: > "$gh_calls"
"$script" status > "$fixture_root/status-output"
grep -Fq "demo     Implementing" "$fixture_root/status-output"
grep -Fq "Snapshot: complete" "$fixture_root/status-output"
assert_no_github "status shell (the overview helper owns remote reads)"

# `release` reads the latest GitHub Release, then counts PRs merged into
# `dev` strictly after its publish instant. Two calls, both reads.
: > "$gh_calls"
"$script" release > "$fixture_root/release-output"
grep -Fq "Latest release: v0.0.106 (published 2026-08-15T10:00:00Z)" \
  "$fixture_root/release-output"
grep -Fq "https://github.com/johnjallday/ori-agent/releases/tag/v0.0.106" \
  "$fixture_root/release-output"
# The boundary is exact: only the two PRs strictly after the publish instant
# count. The same-instant PR, the same-day-but-earlier PR, and the prior-day
# PR must all be excluded - a date-only comparison would wrongly count two
# of those three.
check "release counts only PRs strictly after the publish instant" \
  "$(grep -Fc 'PR(s) merged into dev since v0.0.106' "$fixture_root/release-output" || true)" "1"
grep -Fq "2 PR(s) merged into dev since v0.0.106." "$fixture_root/release-output"
grep -Fq $'CALL\trelease\tview\t--json\ttagName,publishedAt,url\t--template' \
  "$gh_calls"
grep -Fq $'CALL\tpr\tlist\t--state\tmerged\t--base\tdev\t--limit\t500\t--json\tnumber,mergedAt,title' \
  "$gh_calls"
check "release makes exactly two calls" "$(count_gh_calls)" "2"
assert_no_github_write "release"

# Zero matching PRs is reported explicitly, not as a blank line.
: > "$gh_calls"
GH_PR_EMPTY=1 "$script" release > "$fixture_root/release-zero-output"
grep -Fq "No PRs merged into dev since v0.0.106." "$fixture_root/release-zero-output"

# The full-screen picker embeds the shared implementation table and reuses the
# exact same release timestamp count. Loading happens once per refresh;
# rendering itself makes no network call.
: > "$gh_calls"
load_implementation_summary
check "picker implementation summary loads the shared table" \
  "$(grep -c 'demo     Implementing' <<< "$implementation_summary")" "1"
load_picker_release_status
check "picker release summary uses the exact merged-PR count" \
  "$picker_release_summary" "2 PRs merged into dev since v0.0.106."
check "picker release summary retains the numeric count" "$picker_release_count" "2"
render_picker 0 0 0 > "$fixture_root/picker-release-output"
grep -Fq "Ongoing implementations  [w] full details" \
  "$fixture_root/picker-release-output"
grep -Fq "demo     Implementing" "$fixture_root/picker-release-output"
grep -Fq "Release  2 PRs merged into dev since v0.0.106." \
  "$fixture_root/picker-release-output"
check "picker release refresh makes exactly two reads" "$(count_gh_calls)" "2"

# A banner refresh failure is visible but does not masquerade as zero or make
# the picker unusable. The one-shot command below still preserves gh's error.
: > "$gh_calls"
GH_RELEASE_NONE=1 load_picker_release_status
check "picker release failure clears stale summary" "$picker_release_summary" ""
check "picker release failure is actionable" \
  "$picker_release_error" "Release status unavailable — press r to retry."
render_picker 0 0 0 > "$fixture_root/picker-release-unavailable-output"
grep -Fq "Release  Release status unavailable — press r to retry." \
  "$fixture_root/picker-release-unavailable-output"
check "failed picker release refresh stops after one read" "$(count_gh_calls)" "1"

: > "$gh_calls"
GH_RELEASE_NONE=1 load_picker_index
check "release failure does not mark the Issue index failed" "$picker_error" ""
check "release failure still loads Issues" "${#all_issue_numbers[@]}" "2"
check "release failure plus Issue load makes two reads" "$(count_gh_calls)" "2"

# Extra arguments are rejected before any GitHub call, like every other
# one-shot command; covered exhaustively (exit 2, no GitHub contact) by
# "release extra" in the shared invalid-invocation loop below.

# A failed release read propagates gh's own non-zero status and message, and
# never reaches the PR read.
: > "$gh_calls"
status=0
GH_RELEASE_NONE=1 "$script" release > "$fixture_root/release-missing-output" \
  2> "$fixture_root/release-missing-error" || status=$?
if [[ "$status" -eq 0 ]]; then
  printf 'a missing release reported success\n' >&2
  exit 1
fi
assert_output_has "a missing release" \
  "$fixture_root/release-missing-error" "no releases found"
check "a missing release makes exactly one call" "$(count_gh_calls)" "1"
if [[ -s "$fixture_root/release-missing-output" ]]; then
  printf 'a missing release printed a report: %s\n' "$(cat "$fixture_root/release-missing-output")" >&2
  exit 1
fi

# A failed PR read (release succeeded) also propagates a clear non-zero
# failure instead of reporting a misleading zero count.
: > "$gh_calls"
status=0
GH_FAIL_PR=1 "$script" release > "$fixture_root/release-pr-fail-output" \
  2> "$fixture_root/release-pr-fail-error" || status=$?
if [[ "$status" -eq 0 ]]; then
  printf 'a failed PR read reported success\n' >&2
  exit 1
fi
assert_output_has "a failed PR read" \
  "$fixture_root/release-pr-fail-error" "simulated GitHub failure"
if grep -Fq "PR(s) merged" "$fixture_root/release-pr-fail-output"; then
  printf 'a failed PR read still printed a count: %s\n' \
    "$(cat "$fixture_root/release-pr-fail-output")" >&2
  exit 1
fi

# Viewing remains read-only.
: > "$gh_calls"
"$script" view 334 > "$fixture_root/view-output"
grep -Fq "Issue #334 detail" "$fixture_root/view-output"
grep -Fq "Issue #334 decision comment" "$fixture_root/view-output"
assert_call $'CALL\tissue\tview\t334\t--comments'

# A decision is only meaningful on an Issue that is actually waiting for one.
# #320 is plain backlog, so the attempt is rejected on the target's LIVE labels
# before the preview, before the confirmation gate, and before any write. The
# guard lives in decide_issue(), so it is checked ahead of --yes rather than
# being something --yes can skip past.
: > "$gh_calls"
status=0
"$script" decide 320 "1B, 2A" < /dev/null \
  > "$fixture_root/decision-ineligible" 2> "$fixture_root/decision-ineligible-error" || status=$?
if [[ "$status" -eq 0 ]]; then
  printf 'deciding a non-needs-decision Issue succeeded\n' >&2
  exit 1
fi
assert_output_has "an ineligible decision" \
  "$fixture_root/decision-ineligible-error" "#320 is not needs-decision"
assert_output_lacks "an ineligible decision" \
  "$fixture_root/decision-ineligible" "Will record this decision"
# The generic "pass --yes" refusal would mean the attempt reached confirm_write,
# i.e. that it was stopped by the wrong gate.
assert_output_lacks "an ineligible decision" \
  "$fixture_root/decision-ineligible-error" "pass --yes to confirm"
check "an ineligible decision reads labels exactly once" "$(count_label_reads 320)" "1"
check "an ineligible decision makes no other call" "$(count_gh_calls)" "1"
assert_no_github_write "an ineligible decision"

# Eligibility is exact, not a prefix match: `needs-decision-later` is a
# different label and must not unlock the write, even with --yes.
: > "$gh_calls"
status=0
"$script" decide 321 "1A" --yes > /dev/null 2> "$fixture_root/decision-prefix" || status=$?
if [[ "$status" -eq 0 ]]; then
  printf 'a prefix-alike label unlocked a decision\n' >&2
  exit 1
fi
assert_output_has "a prefix-alike label" \
  "$fixture_root/decision-prefix" "#321 is not needs-decision"
assert_no_github_write "a decision on a prefix-alike label"

# Writes are confirm-gated. Eligibility is established first, so a valid attempt
# on a genuinely needs-decision Issue reads its labels and then refuses BEFORE
# any GitHub WRITE - a pipe can never post by accident.
: > "$gh_calls"
status=0
"$script" decide 334 "1B, 2A" < /dev/null \
  > "$fixture_root/decision-refused-preview" 2> "$fixture_root/decision-refused" || status=$?
if [[ "$status" -ne 2 ]]; then
  printf 'unconfirmed decision exited %s, want 2\n' "$status" >&2
  exit 1
fi
grep -Fq "pass --yes to confirm" "$fixture_root/decision-refused"
assert_output_has "an unconfirmed decision preview" \
  "$fixture_root/decision-refused-preview" "the answered label is added; needs-decision stays"
check "an unconfirmed decision reads labels exactly once" "$(count_label_reads 334)" "1"
check "an unconfirmed decision makes no other call" "$(count_gh_calls)" "1"
assert_no_github_write "an unconfirmed decision"

# A label lookup that fails must fail closed: the original status survives, the
# message is distinguishable from ordinary ineligibility, and nothing is
# previewed, prompted for, or written.
: > "$gh_calls"
status=0
GH_FAIL=1 "$script" decide 334 "1A" --yes \
  > "$fixture_root/decision-lookup-failed" 2> "$fixture_root/decision-lookup-error" || status=$?
if [[ "$status" -ne 7 ]]; then
  printf 'a failed label lookup exited %s, want 7\n' "$status" >&2
  exit 1
fi
assert_output_has "a failed label lookup" \
  "$fixture_root/decision-lookup-error" "could not read labels for #334"
# Distinguishable from ordinary ineligibility: a network error is not a verdict
# about the Issue's labels.
assert_output_lacks "a failed label lookup" \
  "$fixture_root/decision-lookup-error" "is not needs-decision"
if [[ -s "$fixture_root/decision-lookup-failed" ]]; then
  printf 'a failed label lookup printed a preview: %s\n' "$(cat "$fixture_root/decision-lookup-failed")" >&2
  exit 1
fi
check "a failed label lookup makes exactly one call" "$(count_gh_calls)" "1"
assert_no_github_write "a failed label lookup"

# Blank arguments are local validation failures and must still stop before the
# eligibility read - an empty decision is never worth a round trip. The
# whitespace-only cases cannot go in the word-split table below, which would
# collapse them into a missing argument instead.
: > "$gh_calls"
status=0
"$script" decide 334 "   " --yes > /dev/null 2>&1 || status=$?
if [[ "$status" -ne 2 ]]; then
  printf 'a blank-answer decision exited %s, want 2\n' "$status" >&2
  exit 1
fi
assert_no_github "a blank-answer decision"

: > "$gh_calls"
status=0
"$script" decide 334 "1A" --rationale "   " --yes > /dev/null 2>&1 || status=$?
if [[ "$status" -ne 2 ]]; then
  printf 'a blank-rationale decision exited %s, want 2\n' "$status" >&2
  exit 1
fi
assert_no_github "a blank-rationale decision"

: > "$gh_calls"
status=0
"$script" approve 334 < /dev/null > /dev/null 2>&1 || status=$?
if [[ "$status" -ne 2 ]]; then
  printf 'unconfirmed approve exited %s, want 2\n' "$status" >&2
  exit 1
fi
assert_no_github "an unconfirmed approve"

# With --yes the confirmed write goes through. The body carries a
# machine-recognizable marker and optional rationale; only after that comment
# succeeds is the additive answered receipt applied. needs-decision is untouched.
: > "$gh_calls"
: > "$gh_body"
"$script" decide 334 "1B, 2A" --rationale "Preserves JSON consumers" --yes > "$fixture_root/decision-output"
grep -Fq $'CALL\tissue\tcomment\t334\t--body\t<!-- ori-decision -->' "$gh_calls"
assert_call $'CALL\tissue\tedit\t334\t--add-label\tanswered'
check "posted decision body" "$(<"$gh_body")" \
  $'<!-- ori-decision -->\n\n**Answers:** 1B, 2A\n\n**Rationale:** Preserves JSON consumers'
grep -Fq "Decision recorded and marked answered" "$fixture_root/decision-output"
if grep -Fq -- $'--remove-label\tneeds-decision' "$gh_calls"; then
  printf 'recording a decision removed needs-decision: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi
check "an eligible decision reads labels exactly once" "$(count_label_reads 334)" "1"
check "an eligible decision makes exactly three calls" "$(count_gh_calls)" "3"
check "the decision comment precedes the answered receipt" \
  "$(gh_call_sequence)" "issue view;issue comment;issue edit;"

# A failed comment remains a failed decision and must never attempt the receipt.
: > "$gh_calls"
status=0
GH_FAIL_COMMENT=1 "$script" decide 334 "1A" --yes \
  > "$fixture_root/decision-comment-failed-output" \
  2> "$fixture_root/decision-comment-failed-error" || status=$?
if [[ "$status" -ne 8 ]]; then
  printf 'a failed decision comment exited %s, want 8\n' "$status" >&2
  exit 1
fi
assert_output_has "a failed decision comment" \
  "$fixture_root/decision-comment-failed-error" "simulated decision comment failure"
check "a failed comment stops before the answered receipt" \
  "$(gh_call_sequence)" "issue view;issue comment;"
if grep -Fq $'issue\tedit' "$gh_calls"; then
  printf 'a failed decision comment still attempted answered: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi

# The comment is the answer of record. If its additive receipt fails, report
# the partial result honestly but return success and leave the UI refresh flag
# to the decision_recorded assertions below.
: > "$gh_calls"
status=0
GH_FAIL_ANSWERED_LABEL=1 "$script" decide 334 "1A" --yes \
  > "$fixture_root/decision-label-failed-output" \
  2> "$fixture_root/decision-label-failed-error" || status=$?
if [[ "$status" -ne 0 ]]; then
  printf 'an answered-label failure exited %s, want 0\n' "$status" >&2
  exit 1
fi
assert_output_has "an answered-label failure" \
  "$fixture_root/decision-label-failed-error" \
  "Decision recorded, but the answered label could not be added (GitHub exited 9)."
assert_output_lacks "an answered-label failure" \
  "$fixture_root/decision-label-failed-output" "marked answered"
check "an answered-label failure still follows comment ordering" \
  "$(gh_call_sequence)" "issue view;issue comment;issue edit;"

# Plan is exercised against the real gh-backed issue_labels_of too, not just
# the stubbed unit section above: #320 is plain backlog (Ready) and launches wt
# through zsh after one label read; #334 is needs-decision (not Ready) and is
# refused before any process launch. Neither path writes to GitHub.
prompt_open_issue_plan_output() {
  local issue_number="$1" planner_answers="${2:-}"
  (
    set +e
    # Re-sourced because the unit section above replaced issue_labels_of and
    # view_issue with stubs; this subshell gets the real gh-backed versions.
    DEVOPS_SOURCE_ONLY=1 source "$script" > /dev/null 2>&1
    printf 's\n%s\n' "$planner_answers" | prompt_open_issue "$issue_number"
  )
}

: > "$gh_calls"
: > "$wt_calls"
prompt_open_issue_plan_output 320 $'1\n1\n4' > "$fixture_root/prompt-plan-real-output" \
  2> "$fixture_root/prompt-plan-real-error"
check "a real Ready Issue's Plan launches the selected agent" \
  "$(grep -Fc 'Planner launched for #320' "$fixture_root/prompt-plan-real-output" || true)" "1"
assert_output_has "a real Ready Issue's Plan" "$fixture_root/prompt-plan-real-output" "agent>"
assert_output_lacks "a Claude planning choice" "$fixture_root/prompt-plan-real-output" "provider>"
check "a real Ready Issue's Claude Plan invokes wt through zsh" \
  "$(<"$wt_calls")" \
  $'CALL\t-c\t'"$plan_bridge"$'\tdevops-plan\t'"$repo_root"$'/scripts/wt.sh\t320\tclaude\tsonnet\txhigh\t0'
check "a real Ready Issue's Plan reads labels exactly once" "$(count_label_reads 320)" "1"
assert_no_github_write "a real Ready Issue's Plan action"

: > "$gh_calls"
: > "$wt_calls"
prompt_open_issue_plan_output 320 $'2\n3\n1\n7' > "$fixture_root/prompt-plan-model-option-output" \
  2> "$fixture_root/prompt-plan-model-option-error"
assert_output_has "a numbered planning-model choice" \
  "$fixture_root/prompt-plan-model-option-output" "anthropic/claude-sonnet-5"
check "a numbered planning-model choice reaches wt as one argument" \
  "$(<"$wt_calls")" \
  $'CALL\t-c\t'"$plan_bridge"$'\tdevops-plan\t'"$repo_root"$'/scripts/wt.sh\t320\tpi\tanthropic/claude-sonnet-5\tmax\t0'
check "a numbered planning-model choice reads labels exactly once" "$(count_label_reads 320)" "1"
assert_no_github_write "a numbered planning-model choice"

: > "$gh_calls"
: > "$wt_calls"
prompt_open_issue_plan_output 334 > "$fixture_root/prompt-plan-real-ineligible-output" \
  2> "$fixture_root/prompt-plan-real-ineligible-error"
grep -Fq '#334 is not Ready; refusing to start planning.' \
  "$fixture_root/prompt-plan-real-ineligible-error"
check "a real non-Ready Issue's Plan launches no process" \
  "$(wc -c < "$wt_calls" | tr -d ' ')" "0"
assert_no_github_write "a real non-Ready Issue's Plan action"

# `answer` remains a backwards-compatible alias for the guided decision write,
# and inherits the same eligibility read rather than carrying its own copy.
: > "$gh_calls"
: > "$gh_body"
"$script" answer 334 "1A" --yes > /dev/null
check "answer alias posts a decision" "$(<"$gh_body")" \
  $'<!-- ori-decision -->\n\n**Answers:** 1A'
check "answer alias reads labels exactly once" "$(count_label_reads 334)" "1"
check "answer alias makes exactly three calls" "$(count_gh_calls)" "3"

: > "$gh_calls"
status=0
"$script" answer 320 "1A" --yes > /dev/null 2> "$fixture_root/answer-ineligible" || status=$?
if [[ "$status" -eq 0 ]]; then
  printf 'the answer alias decided a non-needs-decision Issue\n' >&2
  exit 1
fi
assert_output_has "the answer alias" \
  "$fixture_root/answer-ineligible" "#320 is not needs-decision"
assert_no_github_write "an ineligible answer alias"

# `plan-new` is deliberately separate from ordinary capture. Its one-shot
# contract requires title words, non-empty context, one explicit size route,
# and (outside a terminal) an explicit planner kind plus --yes. Valid calls
# create exactly one Ready Issue and then hand its recovered number to the
# existing planner bridge.
: > "$gh_calls"
"$script" help > "$fixture_root/plan-new-help-output"
check "one-shot usage exposes the complete plan-new shape" \
  "$(grep -Fc './scripts/devops.sh plan-new <title...> (--body <text> | --body-file <path|->) --size <quick|planned|prd>' "$fixture_root/plan-new-help-output" || true)" "1"
assert_no_github "plan-new help"

: > "$gh_calls"
: > "$GH_ARGV"
: > "$wt_calls"
status=0
"$script" plan-new Coordinate based map \
  --body "The viewport jumps after Fit All." \
  --size quick --kind pi --yes \
  > "$fixture_root/plan-new-quick-output" \
  2> "$fixture_root/plan-new-quick-error" || status=$?
check "plan-new accepts title words and inline context" "$status" "0"
check "plan-new quick creates exactly one Issue" \
  "$(grep -Fc $'CALL\tissue\tcreate' "$gh_calls" || true)" "1"
check "plan-new quick creates the exact Ready label pair" \
  "$(grep -Fxc $'CALL\tissue\tcreate\t--title\tCoordinate based map\t--body\tThe viewport jumps after Fit All.\t--label\tbacklog\t--label\tsize:quick' "$gh_calls" || true)" "1"
assert_ready_create_labels "plan-new quick" quick
check "plan-new quick launches exactly one planner" "$(count_wt_calls)" "1"
check "plan-new quick forwards the recovered number and explicit kind" \
  "$(grep -Ec $'\tdevops-plan\t.*\/scripts/wt.sh\t999\tpi(\t|$)' "$wt_calls" || true)" "1"
check "non-interactive plan-new propagates its confirmation bit" \
  "$(tail -c 2 "$wt_calls")" "1"
check "plan-new always prints its durable Issue number" \
  "$(grep -Fc 'Ready Issue: #999' "$fixture_root/plan-new-quick-output" || true)" "1"
check "plan-new prints an idempotent non-interactive retry" \
  "$(grep -Fxc 'Retry/resume: wt plan --issue 999 --kind pi --yes' "$fixture_root/plan-new-quick-output" || true)" "1"

# File and stdin body sources preserve bytes and all three supported size
# routes use the same command. Model/thinking are optional explicit overrides;
# when present they remain opaque, separate arguments.
cat > "$fixture_root/plan-new-body.md" <<'MD'
## Problem

The selected workspace loses its camera framing.
MD
: > "$gh_calls"
: > "$GH_ARGV"
: > "$gh_body"
: > "$wt_calls"
status=0
"$script" plan-new "Preserve workspace framing" \
  --body-file "$fixture_root/plan-new-body.md" \
  --size planned --kind claude --yes \
  > "$fixture_root/plan-new-planned-output" \
  2> "$fixture_root/plan-new-planned-error" || status=$?
check "plan-new accepts a body file and optional integration-default model/thinking" "$status" "0"
if ! cmp -s "$fixture_root/plan-new-body.md" "$gh_body"; then
  printf 'FAIL plan-new changed body-file bytes while creating the Issue\n' >&2
  failures=$((failures + 1))
fi
check "plan-new planned applies one planned size label" \
  "$(grep -Fc $'\t--label\tbacklog\t--label\tsize:planned' "$gh_calls" || true)" "1"
assert_ready_create_labels "plan-new planned" planned
check "plan-new planned forwards Claude without inventing model text" \
  "$(grep -Ec $'\tdevops-plan\t.*\/scripts/wt.sh\t999\tclaude\t\t' "$wt_calls" || true)" "1"
check "omitted planner defaults stay omitted in the retry" \
  "$(grep -Fxc 'Retry/resume: wt plan --issue 999 --kind claude --yes' "$fixture_root/plan-new-planned-output" || true)" "1"

: > "$gh_calls"
: > "$GH_ARGV"
: > "$gh_body"
: > "$wt_calls"
status=0
printf 'Context supplied on stdin.' | \
  "$script" plan-new "Plan a guided map reset" \
    --body-file - --size prd --kind pi \
    --model '[openai] planner model; $(echo inert)' --thinking max --yes \
    > "$fixture_root/plan-new-prd-output" \
    2> "$fixture_root/plan-new-prd-error" || status=$?
check "plan-new accepts stdin context plus explicit planner overrides" "$status" "0"
check "plan-new stdin body survives" "$([[ -f "$gh_body" ]] && cat "$gh_body" || true)" \
  "Context supplied on stdin."
check "plan-new prd applies one prd size label" \
  "$(grep -Fc $'\t--label\tbacklog\t--label\tsize:prd' "$gh_calls" || true)" "1"
assert_ready_create_labels "plan-new prd" prd
check "plan-new keeps an opaque model as one zsh argument" \
  "$(grep -Foc $'\t[openai] planner model; $(echo inert)\tmax' "$wt_calls" || true)" "1"

# Hostile shell-looking values remain inert through the complete one-shot path.
# The title, body-file bytes, and model each contain syntax that would create a
# marker if any layer evaluated them rather than transporting quoted argv.
hostile_marker="$fixture_root/plan-new-hostile-executed"
hostile_title="Hostile ; \$(touch $hostile_marker) [title]"
hostile_model="[openai] model; \$(touch $hostile_marker)"
hostile_body_file="$fixture_root/plan-new-hostile-body.md"
printf '## Context\n\nBody with `code`, --flags, ; and $(touch %s).\n' \
  "$hostile_marker" > "$hostile_body_file"
rm -f "$hostile_marker"
: > "$gh_calls"
: > "$GH_ARGV"
: > "$gh_body"
: > "$wt_calls"
status=0
"$script" plan-new "$hostile_title" --body-file "$hostile_body_file" \
  --size planned --kind pi --model "$hostile_model" --thinking max --yes \
  > "$fixture_root/plan-new-hostile-output" \
  2> "$fixture_root/plan-new-hostile-error" || status=$?
check "hostile one-shot plan-new succeeds as inert data" "$status" "0"
check "hostile title remains one exact gh argument" \
  "$(grep -Fxc "<$hostile_title>" "$GH_ARGV" || true)" "1"
if ! cmp -s "$hostile_body_file" "$gh_body"; then
  printf 'FAIL hostile body-file bytes changed in the full plan-new path\n' >&2
  failures=$((failures + 1))
fi
check "hostile model remains one exact zsh argument" \
  "$(grep -Foc $'\t'"$hostile_model"$'\tmax\t1' "$wt_calls" || true)" "1"
check "hostile plan-new values execute no command" \
  "$([[ -e "$hostile_marker" ]] && echo yes || echo no)" "no"
check "hostile retry shell-quotes the model instead of printing command syntax" \
  "$(grep -Fc '\$\(touch' "$fixture_root/plan-new-hostile-output" || true)" "1"

# The picker path exercises the same hostile transport through :edit and a
# custom planner model before delegating to plan_new_issue. A small sourced
# wrapper keeps the test on the real prompt without entering run_picker's full
# alternate-screen loop (whose p-key wiring is asserted structurally below).
picker_hostile_marker="$fixture_root/plan-new-picker-hostile-executed"
picker_hostile_title="Picker ; \$(touch $picker_hostile_marker) [title]"
picker_hostile_model="custom/model; \$(touch $picker_hostile_marker)"
picker_hostile_body="$fixture_root/plan-new-picker-hostile-body.md"
printf '## Picker context\n\nMultiline `body`; $(touch %s)\n' \
  "$picker_hostile_marker" > "$picker_hostile_body"
picker_editor="$fixture_root/plan-new-picker-editor"
cat > "$picker_editor" <<'SH'
#!/bin/sh
cat "$PICKER_HOSTILE_BODY" > "$1"
SH
picker_prompt_wrapper="$fixture_root/plan-new-picker-wrapper"
cat > "$picker_prompt_wrapper" <<'SH'
#!/usr/bin/env bash
DEVOPS_SOURCE_ONLY=1 source "$DEVOPS_SCRIPT"
prompt_plan_new_issue
SH
chmod +x "$picker_editor" "$picker_prompt_wrapper"
rm -f "$picker_hostile_marker"
: > "$gh_calls"
: > "$GH_ARGV"
: > "$gh_body"
: > "$wt_calls"
picker_input="$(printf '%s\n:edit\n2\n1\nc\n%s\n3\ny\n' \
  "$picker_hostile_title" "$picker_hostile_model")"
picker_input="$picker_input"$'\n'
status=0
DEVOPS_SCRIPT="$script" PICKER_HOSTILE_BODY="$picker_hostile_body" \
VISUAL="$picker_editor" PTY_INPUT="$picker_input" python3 "$pty_driver" \
  "$picker_prompt_wrapper" \
  > "$fixture_root/plan-new-picker-hostile-output" \
  2> "$fixture_root/plan-new-picker-hostile-error" || status=$?
check "hostile picker New & Plan succeeds as inert data" "$status" "0"
check "hostile picker title remains one exact gh argument" \
  "$(grep -Fxc "<$picker_hostile_title>" "$GH_ARGV" || true)" "1"
if ! cmp -s "$picker_hostile_body" "$gh_body"; then
  printf 'FAIL hostile picker :edit body changed before GitHub creation\n' >&2
  failures=$((failures + 1))
fi
check "hostile custom picker model remains one exact zsh argument" \
  "$(grep -Foc $'\t'"$picker_hostile_model"$'\thigh\t0' "$wt_calls" || true)" "1"
check "hostile picker values execute no command" \
  "$([[ -e "$picker_hostile_marker" ]] && echo yes || echo no)" "no"

: > "$gh_calls"
: > "$wt_calls"
status=0
GH_FAIL=1 "$script" plan-new "Creation failure" --body "Reviewed context." \
  --size quick --kind pi --yes \
  > "$fixture_root/plan-new-create-failure-output" \
  2> "$fixture_root/plan-new-create-failure-error" || status=$?
check "plan-new preserves the gh create failure status" "$status" "7"
check "a failed plan-new creation attempts exactly one GitHub call" "$(count_gh_calls)" "1"
check "a failed plan-new creation launches no planner" "$(count_wt_calls)" "0"
check "a failed plan-new creation retains gh's error" \
  "$(grep -Fc 'simulated GitHub failure' "$fixture_root/plan-new-create-failure-error" || true)" "1"

# Successful create output is trusted only when the whole result is one
# anchored Issue URL. A malformed or multiline success is a durable partial
# result: no guessed planner launch, no rollback, and explicit manual recovery.
for create_output_fixture in \
  'created without a parseable URL' \
  $'https://github.com/johnjallday/ori-agent/issues/444\nextra output'; do
  : > "$gh_calls"
  : > "$wt_calls"
  status=0
  GH_CREATE_OUTPUT="$create_output_fixture" \
    "$script" plan-new "Unparseable create result" --body "Reviewed context." \
      --size planned --kind claude --yes \
      > "$fixture_root/plan-new-unparseable-output" \
      2> "$fixture_root/plan-new-unparseable-error" || status=$?
  check "unparseable create output exits as an incomplete flow" "$status" "1"
  check "unparseable create output performs one durable write" "$(count_gh_calls)" "1"
  check "unparseable create output launches no planner" "$(count_wt_calls)" "0"
  check "unparseable create output is printed for recovery" \
    "$(grep -Fc "${create_output_fixture%%$'\n'*}" "$fixture_root/plan-new-unparseable-error" || true)" "1"
  check "unparseable create output reports no rollback" \
    "$(grep -Fc 'no rollback was attempted' "$fixture_root/plan-new-unparseable-error" || true)" "1"
  check "unparseable create output gives manual wt recovery" \
    "$(grep -Fc 'wt plan --issue <number>' "$fixture_root/plan-new-unparseable-error" || true)" "1"
  if grep -Eq -- $'CALL\tissue\t(close|delete|edit)' "$gh_calls"; then
    printf 'an unparseable create result attempted rollback: %s\n' "$(cat "$gh_calls")" >&2
    exit 1
  fi
done

# The create-then-plan boundary is also durable when the child fails or the
# user declines wt plan's own evidence gate. Both paths retain the number and
# exact retry command; only the child failure preserves a non-zero child status.
: > "$gh_calls"
: > "$wt_calls"
status=0
WT_FAIL=1 "$script" plan-new "Planner child failure" --body "Reviewed context." \
  --size prd --kind pi --model openai-codex/failing --thinking high --yes \
  > "$fixture_root/plan-new-child-failure-output" \
  2> "$fixture_root/plan-new-child-failure-error" || status=$?
check "plan-new preserves the planner child failure status" "$status" "11"
check "planner child failure happens after one durable create" "$(count_gh_calls)" "1"
check "planner child failure attempts one constrained launch" "$(count_wt_calls)" "1"
check "planner child failure retains the durable number" \
  "$(grep -Fc 'Ready Issue: #999' "$fixture_root/plan-new-child-failure-output" || true)" "1"
check "planner child failure retains an exact retry" \
  "$(grep -Fxc 'Retry/resume: wt plan --issue 999 --kind pi --model openai-codex/failing --thinking high --yes' "$fixture_root/plan-new-child-failure-output" || true)" "1"

: > "$gh_calls"
: > "$wt_calls"
status=0
WT_DECLINE=1 PTY_INPUT=$'y\n' python3 "$pty_driver" \
  "$script" plan-new "Interactive planning decline" --body "Reviewed context." \
    --size quick --kind claude --model sonnet --thinking high \
    > "$fixture_root/plan-new-planning-decline-output" \
    2> "$fixture_root/plan-new-planning-decline-error" || status=$?
check "interactive wt planning decline is non-error" "$status" "0"
check "interactive planning decline retains one Ready Issue" "$(count_gh_calls)" "1"
check "interactive planning decline reaches wt once" "$(count_wt_calls)" "1"
check "interactive planning decline keeps retry interactive" \
  "$(grep -Fc 'Retry/resume: wt plan --issue 999 --kind claude --model sonnet --thinking high' "$fixture_root/plan-new-planning-decline-output" || true)" "1"
if grep -Eq -- $'CALL\tissue\t(close|delete|edit)' "$gh_calls"; then
  printf 'a post-create planner result attempted rollback: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi

# A non-interactive caller must make both decisions explicitly before any
# outward-facing write: select a planner kind and pass --yes. Model and thinking
# remain optional integration defaults once the kind establishes intent.
for noninteractive_case in \
  "plan-new Missing confirmation --body context --size quick --kind pi" \
  "plan-new Missing planner --body context --size quick --yes" \
  "plan-new Model is not a kind --body context --size quick --model sonnet --thinking high --yes"; do
  : > "$gh_calls"
  : > "$wt_calls"
  status=0
  # Intentional splitting builds each fixed argument vector.
  "$script" $noninteractive_case < /dev/null > /dev/null 2>&1 || status=$?
  check "non-interactive '$noninteractive_case' is a usage refusal" "$status" "2"
  check "non-interactive '$noninteractive_case' writes no Issue" "$(count_gh_calls)" "0"
  check "non-interactive '$noninteractive_case' launches no planner" "$(count_wt_calls)" "0"
done

# In a terminal the same omissions are interactive choices, not usage errors.
# Planner selection happens first; only after it succeeds does the Ready-Issue
# preview ask for write confirmation.
: > "$gh_calls"
: > "$wt_calls"
status=0
PTY_INPUT=$'1\n1\n4\ny\n' python3 "$pty_driver" \
  "$script" plan-new "Terminal planning" \
    --body "Reviewed in the terminal." --size quick \
    > "$fixture_root/plan-new-terminal-output" \
    2> "$fixture_root/plan-new-terminal-error" || status=$?
check "terminal plan-new can select and confirm a planner" "$status" "0"
check "terminal plan-new prompts for planner selection" \
  "$(grep -Fc 'Choose the planning agent.' "$fixture_root/plan-new-terminal-output" || true)" "1"
check "terminal plan-new previews before its confirmed write" \
  "$(grep -Fc 'Create this Ready Issue?' "$fixture_root/plan-new-terminal-output" || true)" "1"
check "terminal plan-new writes only after confirmation" \
  "$(grep -Fc $'CALL\tissue\tcreate' "$gh_calls" || true)" "1"
check "terminal plan-new launches the selected planner" "$(count_wt_calls)" "1"
check "terminal plan-new keeps the downstream evidence gate interactive" \
  "$(tail -c 2 "$wt_calls")" "0"
check "terminal plan-new receipt records the selected planner without --yes" \
  "$(grep -Fc 'Retry/resume: wt plan --issue 999 --kind claude --model sonnet --thinking xhigh' "$fixture_root/plan-new-terminal-output" || true)" "1"

# Every interactive cancellation before the Ready-Issue write is a no-op. The
# title/context/size picker cancellations are covered by the prompt unit matrix;
# these real-terminal cases cover each stage of the shared planner selector and
# the final create confirmation against the gh/zsh recorders.
for cancellation_fixture in \
  $'agent:q\n' \
  $'model:1\nq\n' \
  $'thinking:1\n1\nq\n' \
  $'create:1\n1\n3\nn\n'; do
  cancellation_name="${cancellation_fixture%%:*}"
  cancellation_input="${cancellation_fixture#*:}"
  : > "$gh_calls"
  : > "$wt_calls"
  cancellation_status=0
  PTY_INPUT="$cancellation_input" python3 "$pty_driver" \
    "$script" plan-new "Cancel at $cancellation_name" \
      --body "Reviewed cancellation context." --size quick \
      > "$fixture_root/plan-new-cancel-$cancellation_name" 2>&1 || cancellation_status=$?
  case "$cancellation_name" in
    agent|model|thinking)
      check "terminal $cancellation_name cancellation is a completed no-op" "$cancellation_status" "0"
      ;;
    create)
      check "declining the create confirmation remains a refusal" "$cancellation_status" "1"
      ;;
  esac
  check "$cancellation_name cancellation performs zero GitHub calls" "$(count_gh_calls)" "0"
  check "$cancellation_name cancellation performs zero zsh launches" "$(count_wt_calls)" "0"
done

# Every parser rejection is local and exits 2. Duplicate value sources and
# options are forbidden; every value-taking option must have a value; unknown
# options, sizes, and planner kinds are rejected rather than becoming title
# words or reaching GitHub.
plan_new_invalid_cases=(
  "plan-new"
  "plan-new --body context --size quick --kind pi --yes"
  "plan-new title --size quick --kind pi --yes"
  "plan-new title --body context --kind pi --yes"
  "plan-new title --body context --size tiny --kind pi --yes"
  "plan-new title --body context --size quick --kind codex --yes"
  "plan-new title --body"
  "plan-new title --body-file"
  "plan-new title --body-file /missing --size quick --kind pi --yes"
  "plan-new title --body context --body other --size quick --kind pi --yes"
  "plan-new title --body-file one --body-file two --size quick --kind pi --yes"
  "plan-new title --body context --body-file file --size quick --kind pi --yes"
  "plan-new title --body context --size"
  "plan-new title --body context --size quick --size planned --kind pi --yes"
  "plan-new title --body context --size quick --kind"
  "plan-new title --body context --size quick --kind pi --kind claude --yes"
  "plan-new title --body context --size quick --kind pi --model"
  "plan-new title --body context --size quick --kind pi --model one --model two --yes"
  "plan-new title --body context --size quick --kind pi --model --leading --yes"
  "plan-new title --body context --size quick --kind pi --thinking"
  "plan-new title --body context --size quick --kind pi --thinking high --thinking max --yes"
  "plan-new title --body context --size quick --kind pi --thinking unbounded --yes"
  "plan-new title --body context --size quick --kind claude --thinking minimal --yes"
  "plan-new title --body context --size quick --kind pi --unknown value --yes"
)
for invalid in "${plan_new_invalid_cases[@]}"; do
  : > "$gh_calls"
  : > "$wt_calls"
  status=0
  # Intentional splitting builds each fixed invalid argument vector.
  "$script" $invalid > /dev/null 2>&1 || status=$?
  check "invalid invocation '$invalid' exits 2" "$status" "2"
  check "invalid invocation '$invalid' contacts no GitHub" "$(count_gh_calls)" "0"
  check "invalid invocation '$invalid' launches no planner" "$(count_wt_calls)" "0"
done

# Context means actual problem information, not merely the presence of a body
# option. Inline, file, and stdin whitespace-only forms all fail before writes.
: > "$gh_calls"
: > "$wt_calls"
status=0
"$script" plan-new "Blank inline context" --body "   " \
  --size quick --kind pi --yes > /dev/null 2>&1 || status=$?
check "plan-new rejects whitespace-only inline context" "$status" "2"
check "blank inline context contacts no GitHub" "$(count_gh_calls)" "0"
check "blank inline context launches no planner" "$(count_wt_calls)" "0"

printf ' \n\t\n' > "$fixture_root/plan-new-blank-body.md"
: > "$gh_calls"
: > "$wt_calls"
status=0
"$script" plan-new "Blank file context" \
  --body-file "$fixture_root/plan-new-blank-body.md" \
  --size planned --kind claude --yes > /dev/null 2>&1 || status=$?
check "plan-new rejects whitespace-only file context" "$status" "2"
check "blank file context contacts no GitHub" "$(count_gh_calls)" "0"
check "blank file context launches no planner" "$(count_wt_calls)" "0"

: > "$gh_calls"
: > "$wt_calls"
status=0
printf '\n\t' | "$script" plan-new "Blank stdin context" --body-file - \
  --size prd --kind pi --yes > /dev/null 2>&1 || status=$?
check "plan-new rejects whitespace-only stdin context" "$status" "2"
check "blank stdin context contacts no GitHub" "$(count_gh_calls)" "0"
check "blank stdin context launches no planner" "$(count_wt_calls)" "0"

# Capture is confirm-gated like every other write.
: > "$gh_calls"
status=0
"$script" new "Map zoom is jumpy" < /dev/null > /dev/null 2>&1 || status=$?
if [[ "$status" -ne 2 ]]; then
  printf 'unconfirmed new exited %s, want 2\n' "$status" >&2
  exit 1
fi
assert_no_github "an unconfirmed new"

# A multi-word title stays ONE argument, and the Issue is created with no
# labels: a raw capture must reach the grooming routine unlabelled, or it skips
# the spec step the pipeline is built around.
: > "$gh_calls"
"$script" new "Map zoom is jumpy" --yes > "$fixture_root/new-output"
assert_call $'CALL\tissue\tcreate\t--title\tMap zoom is jumpy\t--body\t'
grep -Fq "issues/999" "$fixture_root/new-output"
if grep -Eq -- '--label|backlog|needs-decision' "$gh_calls"; then
  printf 'capture applied a label: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi

: > "$gh_calls"
"$script" new "Map zoom is jumpy" --body "happens at 30% zoom" --yes > /dev/null
assert_call $'CALL\tissue\tcreate\t--title\tMap zoom is jumpy\t--body\thappens at 30% zoom'

: > "$gh_calls"
"$script" create "Map zoom is jumpy" --body "happens at 30% zoom" --yes > /dev/null
assert_call $'CALL\tissue\tcreate\t--title\tMap zoom is jumpy\t--body\thappens at 30% zoom'
if grep -Eq -- '--label|backlog|needs-decision' "$gh_calls"; then
  printf 'the create alias no longer behaves like unlabelled new: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi

# Bodies can come from a Markdown file or stdin. They are read once before the
# preview, then passed verbatim as one argument to gh.
cat > "$fixture_root/issue-body.md" <<'MD'
## What happened

Zoom jumps at 30%.
MD
: > "$gh_calls"
: > "$gh_body"
"$script" new "Map zoom is jumpy" --body-file "$fixture_root/issue-body.md" --yes > /dev/null
if ! cmp -s "$fixture_root/issue-body.md" "$gh_body"; then
  printf 'multiline body file changed while being posted\n' >&2
  exit 1
fi

: > "$gh_calls"
: > "$gh_body"
printf 'captured from stdin' | "$script" new "Map zoom is jumpy" --body-file - --yes > /dev/null
check "stdin body survives" "$(<"$gh_body")" "captured from stdin"

# An ampersand must survive verbatim; HTML-escaping it here would leave a
# literal &amp; in the Issue title forever.
: > "$gh_calls"
"$script" new "Fit All & zoom floor" --yes > /dev/null
assert_call $'CALL\tissue\tcreate\t--title\tFit All & zoom floor\t--body\t'

# approved is toggled with --add-label/--remove-label, never a labels array
# write, so the Issue's other labels cannot be dropped.
: > "$gh_calls"
"$script" approve 334 --yes > /dev/null
assert_call $'CALL\tissue\tedit\t334\t--add-label\tapproved'

: > "$gh_calls"
"$script" unapprove 334 --yes > /dev/null
assert_call $'CALL\tissue\tedit\t334\t--remove-label\tapproved'

if grep -Eq -- '--add-label[[:space:]]*$|labels' "$gh_calls"; then
  printf 'a label write used an unexpected shape: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi

# Invalid invocations fail before contacting GitHub.
for invalid in "view" "view nope" "view 0" "view 334 extra" "all extra" "ready extra" "unknown" "decide" "decide 334" "decide nope text" "decide 334 1A --rationale" "answer" "answer 334" "approve" "approve nope" "approve 334 extra" "new" "new --yes" "new title --body" "new title --body-file" "new title --body-file /missing" "new title --body text --body-file /missing" "status extra" "release extra"; do
  : > "$gh_calls"
  status=0
  # Intentional word splitting turns each fixture into an argument vector.
  "$script" $invalid > /dev/null 2>&1 || status=$?
  if [[ "$status" -ne 2 ]]; then
    printf 'invalid invocation %q exited %s, want 2\n' "$invalid" "$status" >&2
    exit 1
  fi
  if [[ -s "$gh_calls" ]]; then
    printf 'invalid invocation %q contacted GitHub: %s\n' "$invalid" "$(cat "$gh_calls")" >&2
    exit 1
  fi
done

# The REPL can move between every view, inspect an Issue, and refresh the
# current filter without changing labels or any other GitHub state.
: > "$gh_calls"
printf '2\n3\n4\n5\nv 334\n1\nq\n' | "$script" > "$fixture_root/repl-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tneeds-decision'
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tbacklog'
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tfeature-proposal'
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--search\tlabel:backlog,feature-proposal -label:bundled -label:approved'
assert_call $'CALL\tissue\tview\t334\t--comments'
if grep -Eq -- 'issue\tcomment|issue\tedit' "$gh_calls"; then
  printf 'browsing the REPL wrote to GitHub: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi

: > "$gh_calls"
printf '2\nr\nq\n' | "$script" > /dev/null
decision_count="$(grep -Fc $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tneeds-decision' "$gh_calls")"
if [[ "$decision_count" -ne 2 ]]; then
  printf 'refresh queried the current filter %s times, want 2\n' "$decision_count" >&2
  exit 1
fi

# Every entry point funnels through decide_issue(), so the REPL's own decide
# command inherits the guard without a second implementation: an ineligible
# Issue is rejected on eligibility, an eligible one gets as far as the existing
# confirmation gate, and neither writes.
: > "$gh_calls"
printf 'c 320 1A\nq\n' | "$script" \
  > "$fixture_root/repl-ineligible" 2> "$fixture_root/repl-ineligible-error"
assert_output_has "a REPL decision on an ineligible Issue" \
  "$fixture_root/repl-ineligible-error" "#320 is not needs-decision"
check "a REPL decision on an ineligible Issue reads labels once" "$(count_label_reads 320)" "1"
assert_no_github_write "a REPL decision on an ineligible Issue"

: > "$gh_calls"
printf 'c 334 1A\nq\n' | "$script" \
  > "$fixture_root/repl-eligible" 2> "$fixture_root/repl-eligible-error"
assert_output_has "an eligible REPL decision" \
  "$fixture_root/repl-eligible-error" "pass --yes to confirm"
check "a REPL decision on an eligible Issue reads labels once" "$(count_label_reads 334)" "1"
assert_no_github_write "an unconfirmed REPL decision"

# A GitHub failure stays a failure instead of being rendered as an empty list.
: > "$gh_calls"
status=0
GH_FAIL=1 "$script" all > /dev/null 2> "$fixture_root/failure-output" || status=$?
if [[ "$status" -ne 7 ]]; then
  printf 'GitHub failure exited %s, want 7\n' "$status" >&2
  exit 1
fi
grep -Fq "simulated GitHub failure" "$fixture_root/failure-output"

# Resolution follows the script's checkout even when invoked from elsewhere.
: > "$gh_calls"
(cd "$fixture_root" && "$script" all > /dev/null)
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000'

# decision_recorded is the flag the opened-Issue action bar reads to decide
# whether to re-display the Issue. A rejected or failed decision must leave it
# at 0, or the UI would refresh as though something had been recorded. The real
# decide_issue is exercised here against the fake gh, not the stub used above.
decision_recorded_after() {
  local mode="$1"
  shift
  (
    set +e
    case "$mode" in
      fail) export GH_FAIL=1 ;;
      comment_fail) export GH_FAIL_COMMENT=1 ;;
      label_fail) export GH_FAIL_ANSWERED_LABEL=1 ;;
    esac
    # Re-sourced because the unit section above replaced decide_issue with a
    # stub. The script's readonly declarations are already set in this shell, so
    # their harmless re-assignment warnings are discarded.
    DEVOPS_SOURCE_ONLY=1 source "$script" > /dev/null 2>&1
    decide_issue "$@" > /dev/null 2>&1
    printf '%s' "$decision_recorded"
  )
}

: > "$gh_calls"
check "an ineligible decision records nothing" \
  "$(decision_recorded_after ok 320 "1A" --yes)" "0"
: > "$gh_calls"
check "a failed label lookup records nothing" \
  "$(decision_recorded_after fail 334 "1A" --yes)" "0"
: > "$gh_calls"
check "an unconfirmed decision records nothing" \
  "$(decision_recorded_after ok 334 "1A" < /dev/null)" "0"
: > "$gh_calls"
check "an invalid decision records nothing" \
  "$(decision_recorded_after ok 334 --yes)" "0"
: > "$gh_calls"
check "a failed decision comment records nothing" \
  "$(decision_recorded_after comment_fail 334 "1A" --yes)" "0"
: > "$gh_calls"
check "an answered-label failure preserves the recorded decision" \
  "$(decision_recorded_after label_fail 334 "1A" --yes)" "1"
: > "$gh_calls"
check "an eligible decision records the write" \
  "$(decision_recorded_after ok 334 "1A" --yes)" "1"

# ---------------------------------------------------------------------------
# Picker-only interactions are wired structurally because run_picker requires a
# real TTY. Enter and c must share the opened-Issue session (c starts its Decide
# action), `?` must expose help, and number shortcuts must match the line REPL.
# ---------------------------------------------------------------------------
if ! grep -Fq 'with_normal_terminal_session prompt_open_issue "${issue_numbers[$selected_index]}" c' "$script"; then
  printf 'the picker c key does not enter Decide within the opened Issue\n' >&2
  exit 1
fi
if ! grep -Fqx '          with_normal_terminal_session prompt_open_issue "${issue_numbers[$selected_index]}"' "$script"; then
  printf 'the picker Enter key does not open the interactive Issue view\n' >&2
  exit 1
fi
if ! grep -Fq 'with_normal_terminal print_picker_help' "$script"; then
  printf 'the picker ? key does not show help\n' >&2
  exit 1
fi
for mapping in \
  '1) filter_index=1' \
  '2) filter_index=2' \
  '3) filter_index=3' \
  '4) filter_index=4' \
  '5) filter_index=0'; do
  if ! grep -Fq "$mapping" "$script"; then
    printf 'picker shortcut mapping is missing: %s\n' "$mapping" >&2
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# The picker's global `p` New & Plan key.
#
# Like n, it must work with an empty list and from every view. Unlike n, it
# refreshes only after prompt_plan_new_issue reports a durable create; planner
# selection and all create/launch details stay in the prompt/one-shot path.
# ---------------------------------------------------------------------------
p_branch="$(awk '/^      p\)$/{inside=1} inside{print} inside && /^        ;;$/{exit}' "$script")"
if [[ -z "$p_branch" ]]; then
  printf 'could not read the picker p) branch\n' >&2
  exit 1
fi
check "picker p invokes the New & Plan prompt once" \
  "$(grep -Fc 'with_normal_terminal prompt_plan_new_issue' <<< "$p_branch" || true)" "1"
if grep -Eq '\$count|selected_index.*issue_numbers|issue_numbers\[\$selected_index\]' <<< "$p_branch"; then
  printf 'the picker p key incorrectly requires a selected row: %s\n' "$p_branch" >&2
  exit 1
fi
if ! grep -Fq 'if [[ "$plan_new_prompt_created" -eq 1 ]]' <<< "$p_branch" || \
   [[ "$(grep -c 'load_picker_index' <<< "$p_branch" || true)" -ne 1 ]]; then
  printf 'the picker p key does not refresh exactly once behind the durable-create gate: %s\n' "$p_branch" >&2
  exit 1
fi
if grep -Eq 'prompt_planner_selection|create_ready_issue|launch_planner_plan|bundle_mark_(toggle|remove)|start_bundle_plan' <<< "$p_branch"; then
  printf 'the picker p key duplicates planning logic or mutates bundle actions: %s\n' "$p_branch" >&2
  exit 1
fi
if ! grep -Fq 'p new & plan' "$script" || \
   ! grep -Fq 'n capture' "$script" || \
   ! grep -Fq 's plan selected' "$script"; then
  printf 'the picker footer does not distinguish capture, New & Plan, and selected planning\n' >&2
  exit 1
fi
if ! grep -Fq 'New & Plan a reviewed brief' "$script" || \
   ! grep -Fq 'never adds approved' "$script"; then
  printf 'picker help does not explain the New & Plan boundary\n' >&2
  exit 1
fi
n_branch="$(awk '/^      n\)$/{inside=1} inside{print} inside && /^        ;;$/{exit}' "$script")"
if [[ "$(grep -Fc 'with_normal_terminal prompt_create_issue' <<< "$n_branch" || true)" -ne 1 ]] || \
   grep -Fq 'prompt_plan_new_issue' <<< "$n_branch"; then
  printf 'the picker n capture path changed while adding p: %s\n' "$n_branch" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# The picker's `s` key.
#
# run_picker only runs on a real TTY (it enters the alternate screen and puts
# the terminal in raw mode), so the key itself is asserted structurally rather
# than by driving a pseudo-terminal: the branch must route through the same
# with_normal_terminal escape hatch every other full-screen action uses, and
# must hand start_plan the CURRENT view plus the SELECTED row's immutable Issue
# number — never a position, a title, or a stale filter name. start_plan's own
# behaviour is covered exhaustively by the pure unit assertions above.
# ---------------------------------------------------------------------------
if ! grep -Fq 'with_normal_terminal start_plan "${picker_filters[$filter_index]}" "$count" "${issue_numbers[$selected_index]:-}"' "$script"; then
  printf 'the picker s key is not wired to start_plan with the current view and selected Issue number\n' >&2
  exit 1
fi
if ! grep -Fq 's plan' "$script"; then
  printf 'the picker footer does not document the s key\n' >&2
  exit 1
fi
# The action must not reload the index: printing a command is not a reason to
# re-query GitHub, and a reload would also reset the selection the user is
# standing on.
s_branch="$(awk '/^      s\)$/{inside=1} inside{print} inside && /^        ;;$/{exit}' "$script")"
if [[ -z "$s_branch" ]]; then
  printf 'could not read the picker s) branch\n' >&2
  exit 1
fi
if grep -Eq 'load_picker_index|apply_picker_filter|reload=1' <<< "$s_branch"; then
  printf 'the s key re-queries GitHub or resets the view: %s\n' "$s_branch" >&2
  exit 1
fi

# Space marks by immutable Issue number and b launches the complete mark set.
if ! grep -Fq 'bundle_mark_toggle "${picker_filters[$filter_index]}"' "$script" || \
   ! grep -Fq '"${issue_numbers[$selected_index]}" "${issue_labels[$selected_index]}"' "$script"; then
  printf 'the picker Space key does not toggle the current immutable Issue number\n' >&2
  exit 1
fi
if ! grep -Fq 'with_normal_terminal start_bundle_plan "${picker_filters[$filter_index]}"' "$script" || \
   ! grep -Fq '"${bundle_mark_numbers[@]+"${bundle_mark_numbers[@]}"}"' "$script"; then
  printf 'the picker b key does not launch the complete marked Issue set\n' >&2
  exit 1
fi
if ! grep -Fq 'Space mark/unmark' "$script" || ! grep -Fq 'b plan marked bundle' "$script"; then
  printf 'the picker footer does not document bundle selection controls\n' >&2
  exit 1
fi
b_branch="$(awk '/^      b\)$/{inside=1} inside{print} inside && /^        ;;$/{exit}' "$script")"
if [[ -z "$b_branch" ]] || grep -Eq 'load_picker_index|apply_picker_filter|reload=1' <<< "$b_branch"; then
  printf 'the b key is missing or resets the cached picker after planning: %s\n' "$b_branch" >&2
  exit 1
fi

# The later `i` action starts from current local planning artifacts in any view.
# After wt returns it refreshes only local flight state and the shared
# implementation summary, then reapplies the cached view without querying
# GitHub.
if ! grep -Fq 'with_normal_terminal start_issue_implementation "${issue_numbers[$selected_index]}"' "$script"; then
  printf 'the picker i key is not wired to the selected Issue number\n' >&2
  exit 1
fi
if ! grep -Fq 'i start implementation' "$script"; then
  printf 'the picker footer does not document the i key\n' >&2
  exit 1
fi
i_branch="$(awk '/^      i\)$/{inside=1} inside{print} inside && /^        ;;$/{exit}' "$script")"
if [[ -z "$i_branch" ]]; then
  printf 'could not read the picker i) branch\n' >&2
  exit 1
fi
if grep -Eq 'load_picker_index|reload=1|issue_labels_of|\bgh\b' <<< "$i_branch" || \
   ! grep -Fq 'load_implementation_summary' <<< "$i_branch" || \
   ! grep -Fq 'apply_picker_filter "${picker_filters[$filter_index]}"' <<< "$i_branch"; then
  printf 'the i key does not refresh cached implementation state safely: %s\n' "$i_branch" >&2
  exit 1
fi

# w opens the full shared implementation report and refreshes only that cached
# summary on return.
if ! grep -Fq 'with_normal_terminal implementation_report' "$script" || \
   ! grep -Fq 'w implementation details' "$script"; then
  printf 'the picker w key is not wired or documented\n' >&2
  exit 1
fi
w_branch="$(awk '/^      w\)$/{inside=1} inside{print} inside && /^        ;;$/{exit}' "$script")"
if [[ -z "$w_branch" ]] || grep -Eq 'load_picker_index|issue_labels_of|\bgh\b' <<< "$w_branch"; then
  printf 'the w key is missing or re-queries the Issue index: %s\n' "$w_branch" >&2
  exit 1
fi

# The picker g action is local and documented. It must escape the alternate
# screen for prompts, then return without refreshing or reading GitHub.
if ! grep -Fq 'with_normal_terminal agent_defaults_action' "$script"; then
  printf 'the picker g key is not wired to agent_defaults_action\n' >&2
  exit 1
fi
if ! grep -Fq 'g agent defaults' "$script"; then
  printf 'the picker footer does not document the g key\n' >&2
  exit 1
fi
g_branch="$(awk '/^      g\)$/{inside=1} inside{print} inside && /^        ;;$/{exit}' "$script")"
if [[ -z "$g_branch" ]]; then
  printf 'could not read the picker g) branch\n' >&2
  exit 1
fi
if grep -Eq 'load_picker_index|apply_picker_filter|reload=1|issue_labels_of|\bgh\b|wt_herd' <<< "$g_branch"; then
  printf 'the g key contacts a remote service or resets the view: %s\n' "$g_branch" >&2
  exit 1
fi
if ! grep -Fq 'g|agent-defaults)' "$script"; then
  printf 'the line REPL does not expose agent-defaults\n' >&2
  exit 1
fi

if [[ "$failures" -ne 0 ]]; then
  printf '%s assertion(s) failed\n' "$failures" >&2
  exit 1
fi

printf '%s\n' "devops.sh tests passed"
