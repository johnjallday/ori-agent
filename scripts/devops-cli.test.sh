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
    if [ "${4:-}" = "--comments" ]; then
      printf 'Issue #%s detail\n' "$3"
      printf 'Issue #%s decision comment\n' "$3"
    else
      printf 'Issue #%s detail\n' "$3"
    fi
    ;;
  "issue comment")
    printf 'commented on #%s\n' "$3"
    ;;
  "issue create")
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

# Writes are confirm-gated. Without a terminal and without --yes they must
# refuse BEFORE contacting GitHub - a pipe can never post by accident.
: > "$gh_calls"
status=0
"$script" answer 334 "1B, 2A" < /dev/null > /dev/null 2> "$fixture_root/answer-refused" || status=$?
if [[ "$status" -ne 2 ]]; then
  printf 'unconfirmed answer exited %s, want 2\n' "$status" >&2
  exit 1
fi
grep -Fq "pass --yes to confirm" "$fixture_root/answer-refused"
assert_no_github "an unconfirmed answer"

: > "$gh_calls"
status=0
"$script" approve 334 < /dev/null > /dev/null 2>&1 || status=$?
if [[ "$status" -ne 2 ]]; then
  printf 'unconfirmed approve exited %s, want 2\n' "$status" >&2
  exit 1
fi
assert_no_github "an unconfirmed approve"

# With --yes the write goes through, and the comment body survives as one
# argument rather than being split on spaces.
: > "$gh_calls"
"$script" answer 334 "1B, 2A" --yes > /dev/null
assert_call $'CALL\tissue\tcomment\t334\t--body\t1B, 2A'

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
for invalid in "view" "view nope" "view 0" "view 334 extra" "all extra" "ready extra" "unknown" "answer" "answer 334" "answer nope text" "approve" "approve nope" "approve 334 extra" "new" "new --yes" "new title --body" "status extra"; do
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

printf '%s\n' "devops.sh tests passed"
