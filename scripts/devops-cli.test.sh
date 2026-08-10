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
    previous=""
    for argument in "$@"; do
      if [ "$previous" = "--label" ]; then
        label="$argument"
      fi
      previous="$argument"
    done
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

if grep -Eq -- '--author|api[[:space:]]+graphql|project' "$gh_calls"; then
  printf 'a label filter reached an author or Project query: %s\n' "$(cat "$gh_calls")" >&2
  exit 1
fi

# Viewing is the only non-filter convenience, and it remains read-only.
: > "$gh_calls"
"$script" view 334 > "$fixture_root/view-output"
grep -Fq "Issue #334 detail" "$fixture_root/view-output"
grep -Fq "Issue #334 decision comment" "$fixture_root/view-output"
assert_call $'CALL\tissue\tview\t334\t--comments'

# Invalid invocations fail before contacting GitHub.
for invalid in "view" "view nope" "view 0" "view 334 extra" "all extra" "unknown"; do
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
printf '2\n3\n4\nv 334\n1\nq\n' | "$script" > "$fixture_root/repl-output"
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tneeds-decision'
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tbacklog'
assert_call $'CALL\tissue\tlist\t--state\topen\t--limit\t1000\t--label\tfeature-proposal'
assert_call $'CALL\tissue\tview\t334\t--comments'

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
