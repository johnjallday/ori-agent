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

# print_plan_command is the picker's `s` key: format only, no GitHub read, no
# `wt` invocation, no clipboard, safe on an empty or non-Ready view.
check "plan command prints for a selected Ready row" \
  "$(print_plan_command ready 3 934 2>/dev/null)" "wt plan --issue 934"
check "plan command refuses a non-Ready view" \
  "$(print_plan_command all 3 934 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "plan command refuses an empty Ready view" \
  "$(print_plan_command ready 0 "" >/dev/null 2>&1 && echo yes || echo no)" "no"
check "plan command refuses a non-numeric issue number" \
  "$(print_plan_command ready 3 abc >/dev/null 2>&1 && echo yes || echo no)" "no"
check "plan command refuses zero" \
  "$(print_plan_command ready 3 0 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "plan command refuses a negative-looking issue number" \
  "$(print_plan_command ready 3 -5 >/dev/null 2>&1 && echo yes || echo no)" "no"
check "a rejected plan command explains itself on stderr" \
  "$(print_plan_command all 3 934 2>&1 >/dev/null)" "Switch to the Ready view to print a planning command."

# Decisions carry a stable marker so grooming can distinguish them from an
# ordinary comment. Rationale is optional and remains plain user-authored text.
check "decision comment has a marker" \
  "$(format_decision_comment "1B, 2A")" \
  $'<!-- ori-decision -->\n\n**Answers:** 1B, 2A'
check "decision comment includes rationale" \
  "$(format_decision_comment "1B, 2A" "Preserves existing JSON consumers")" \
  $'<!-- ori-decision -->\n\n**Answers:** 1B, 2A\n\n**Rationale:** Preserves existing JSON consumers'

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
tasks_dir=""

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

view_issue() {
  printf 'viewed #%s\n' "$1"
}
decide_issue() {
  printf '%s\n' "$@" > "$prompt_capture"
  decision_recorded=1
}
decision_recorded=0
printf 'c\n1B, 2A\nPreserves existing callers\n\n' | \
  prompt_open_issue 334 > "$fixture_root/prompt-decision-output"
grep -Fq '[c] Decide' "$fixture_root/prompt-decision-output"
view_count="$(grep -Fc 'viewed #334' "$fixture_root/prompt-decision-output")"
check "opened Issue refreshes after a decision" "$view_count" "2"
check "opened Issue decision passes rationale" "$(<"$prompt_capture")" \
  $'334\n1B, 2A\n--rationale\nPreserves existing callers'

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

cat > "$fake_bin/gh" <<'SH'
#!/bin/sh
{
  printf 'CALL'
  for argument in "$@"; do
    printf '\t%s' "$argument"
  done
  printf '\n'
} >> "$GH_CALLS"

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
    printf 'https://github.com/johnjallday/ori-agent/issues/999\n'
    ;;
  "issue edit")
    printf 'edited #%s\n' "$3"
    ;;
  *)
    printf 'unexpected gh invocation: %s\n' "$*" >&2
    exit 99
    ;;
esac
SH
chmod +x "$fake_bin/gh"

export PATH="$fake_bin:$PATH"
export GH_CALLS="$gh_calls"
export GH_BODY="$gh_body"

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

gh_call_sequence() {
  awk '/^CALL/ {printf "%s %s;", $2, $3}' "$gh_calls"
}

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

# `status` is entirely local: it reads git and the filesystem, and must never
# reach GitHub. That is what makes it usable offline and instant.
: > "$gh_calls"
"$script" status > "$fixture_root/status-output"
grep -Fq "In flight" "$fixture_root/status-output"
assert_no_github "status"

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
"$script" decide 334 "1B, 2A" < /dev/null > /dev/null 2> "$fixture_root/decision-refused" || status=$?
if [[ "$status" -ne 2 ]]; then
  printf 'unconfirmed decision exited %s, want 2\n' "$status" >&2
  exit 1
fi
grep -Fq "pass --yes to confirm" "$fixture_root/decision-refused"
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

# With --yes the write goes through. The body carries a machine-recognizable
# marker and optional rationale, while no label write is attempted here.
: > "$gh_calls"
: > "$gh_body"
"$script" decide 334 "1B, 2A" --rationale "Preserves JSON consumers" --yes > "$fixture_root/decision-output"
grep -Fq $'CALL\tissue\tcomment\t334\t--body\t<!-- ori-decision -->' "$gh_calls"
check "posted decision body" "$(<"$gh_body")" \
  $'<!-- ori-decision -->\n\n**Answers:** 1B, 2A\n\n**Rationale:** Preserves JSON consumers'
grep -Fq "will remain in Needs my decision" "$fixture_root/decision-output"
if grep -Fq $'issue\tedit' "$gh_calls"; then
  printf 'recording a decision changed labels: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi
# The whole transcript, in order: establish eligibility, then post. Nothing else.
check "an eligible decision reads labels exactly once" "$(count_label_reads 334)" "1"
check "an eligible decision makes exactly two calls" "$(count_gh_calls)" "2"
check "the label read precedes the decision comment" \
  "$(gh_call_sequence)" "issue view;issue comment;"

# `answer` remains a backwards-compatible alias for the guided decision write,
# and inherits the same eligibility read rather than carrying its own copy.
: > "$gh_calls"
: > "$gh_body"
"$script" answer 334 "1A" --yes > /dev/null
check "answer alias posts a decision" "$(<"$gh_body")" \
  $'<!-- ori-decision -->\n\n**Answers:** 1A'
check "answer alias reads labels exactly once" "$(count_label_reads 334)" "1"
check "answer alias makes exactly two calls" "$(count_gh_calls)" "2"

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
for invalid in "view" "view nope" "view 0" "view 334 extra" "all extra" "ready extra" "unknown" "decide" "decide 334" "decide nope text" "decide 334 1A --rationale" "answer" "answer 334" "approve" "approve nope" "approve 334 extra" "new" "new --yes" "new title --body" "new title --body-file" "new title --body-file /missing" "new title --body text --body-file /missing" "status extra"; do
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
    if [[ "$mode" == fail ]]; then
      export GH_FAIL=1
    fi
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
# The picker's `s` key.
#
# run_picker only runs on a real TTY (it enters the alternate screen and puts
# the terminal in raw mode), so the key itself is asserted structurally rather
# than by driving a pseudo-terminal: the branch must route through the same
# with_normal_terminal escape hatch every other full-screen action uses, and
# must hand print_plan_command the CURRENT view plus the SELECTED row's
# immutable Issue number — never a position, a title, or a stale filter name.
# print_plan_command's own behaviour is covered exhaustively by the pure unit
# assertions above.
# ---------------------------------------------------------------------------
if ! grep -Fq 'with_normal_terminal print_plan_command "${picker_filters[$filter_index]}" "$count" "${issue_numbers[$selected_index]:-}"' "$script"; then
  printf 'the picker s key is not wired to print_plan_command with the current view and selected Issue number\n' >&2
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

if [[ "$failures" -ne 0 ]]; then
  printf '%s assertion(s) failed\n' "$failures" >&2
  exit 1
fi

printf '%s\n' "devops.sh tests passed"
