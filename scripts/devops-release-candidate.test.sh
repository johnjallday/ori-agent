#!/usr/bin/env bash
# Product-owned RC coverage split from devops-cli.test.sh at Ori 8c076a15.
# No companion, native agents, downloads or real GitHub writes are required.
set -euo pipefail
repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
script_dir="$repo_root/scripts"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-rc-contract.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT
source "$script_dir/lib/devops-release-candidate.sh"
failures=0
check() {
  local description="$1" actual="$2" expected="$3"
  if [[ "$actual" != "$expected" ]]; then
    printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$description" "$actual" "$expected" >&2
    failures=$((failures + 1))
  fi
}

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
  "release list")
    if [ -n "${GH_RC_FAIL:-}" ]; then
      printf 'simulated release list failure\n' >&2
      exit 1
    fi
    # Newest first, like gh. RCs of shipped versions and older stables must
    # never become the candidate; GH_RC_ORDER lists rc.9 AFTER rc.10 and an
    # older two-digit stable after a three-digit one to prove numeric order.
    if [ -n "${GH_RC_DRAFT:-}" ]; then
      printf 'v0.0.107-rc.2\ttrue\ttrue\n'
    fi
    if [ -n "${GH_RC_ORDER:-}" ]; then
      printf 'v0.0.107-rc.10\tfalse\ttrue\n'
      printf 'v0.0.107-rc.9\tfalse\ttrue\n'
    fi
    if [ -z "${GH_RC_NONE:-}" ]; then
      printf 'v0.0.107-rc.1\tfalse\ttrue\n'
    fi
    printf 'v0.0.106\tfalse\tfalse\n'
    printf 'v0.0.106-rc.2\tfalse\ttrue\n'
    printf 'v0.0.99\tfalse\tfalse\n'
    ;;
  "api repos/{owner}/{repo}/releases/tags/"*)
    tag="${2##*/}"
    if [ -n "${GH_RC_UNPUBLISHED:-}" ]; then
      printf 'gh: Not Found (HTTP 404)\n' >&2
      exit 1
    fi
    prerelease=true
    if [ -n "${GH_RC_NOT_PRERELEASE:-}" ]; then
      prerelease=false
    fi
    printf '%s\thttps://github.com/johnjallday/ori-agent/releases/tag/%s\n' "$prerelease" "$tag"
    printf 'checksums.txt\tsha256:0000\n'
    printf 'OriAgent-%s-arm64.dmg\tsha256:%s\n' "${tag#v}" "${GH_DMG_DIGEST:-}"
    printf 'rc-test-report-%s.md\tsha256:1111\n' "$tag"
    ;;
  "release download")
    tag="$3"
    dir=""
    previous=""
    for argument in "$@"; do
      if [ "$previous" = "--dir" ]; then
        dir="$argument"
      fi
      previous="$argument"
    done
    previous=""
    for argument in "$@"; do
      if [ "$previous" = "--pattern" ] && [ ! -e "$dir/$argument" ]; then
        case "$argument" in
          *.dmg) printf 'fake dmg for %s\n' "$tag" > "$dir/$argument" ;;
          *) printf '# RC card %s\n' "$tag" > "$dir/$argument" ;;
        esac
      fi
      previous="$argument"
    done
    ;;
  "repo view")
    printf 'https://github.com/johnjallday/ori-agent'
    ;;
  "api --paginate")
    case "${3:-}" in
      'repos/{owner}/{repo}/compare/v0.0.106...dev?per_page=100') ;;
      *) printf 'unexpected comparison: %s\n' "$*" >&2; exit 99 ;;
    esac
    if [ -n "${GH_FAIL_PR:-}" ]; then
      printf 'simulated GitHub failure\n' >&2
      exit 7
    fi
    if [ -n "${GH_PR_EMPTY:-}" ]; then
      exit 0
    fi
    # Compare returns commits absent from the frozen release, including work
    # merged BEFORE publication while its RC was being tested. No date filter.
    printf 'feat: merged during RC testing (#381)\n'
    printf 'feat: merged after stable publication (#380)\n'
    printf 'Merge pull request #382 from johnjallday/feature/landed-with-a-merge-commit\n'
    printf 'chore: local maintenance without a delivery PR\n'
    printf 'Merge pull request #379 from johnjallday/release/v0.0.106\n'
    printf 'Merge released branch back into dev\n'
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
  devops-start) printf 'Implementation start launched for %s with %s%s\n' "$5" "$6" "${7:+ ($7)}" ;;
  devops-done) printf 'Finish chooser launched\n' ;;
esac
SH
chmod +x "$fake_bin/zsh"

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

count_gh_calls() {
  grep -c '^CALL' "$gh_calls" || true
}

export PATH="$fake_bin:$PATH"
export GH_CALLS="$gh_calls" GH_BODY="$gh_body" WT_CALLS="$wt_calls"
export GH_ARGV="$fixture_root/gh-argv"
# Release candidates: selection, the test kit, and promotion dispatch.
# ---------------------------------------------------------------------------
: > "$gh_calls"
load_rc_status
check "the candidate is the newest RC above the latest stable" "$rc_tag" "v0.0.107-rc.1"
check "a published candidate is a prerelease" "$rc_state" "prerelease"
check "the latest stable comes from the same listing" "$rc_stable" "v0.0.106"
check "candidate status is one read" "$(count_gh_calls)" "1"
GH_RC_ORDER=1 load_rc_status
check "candidate numbers compare numerically, not as text" "$rc_tag" "v0.0.107-rc.10"
check "stable versions compare numerically, not as text" "$rc_stable" "v0.0.106"
GH_RC_DRAFT=1 load_rc_status
check "a newer draft RC supersedes the published one" "$rc_tag" "v0.0.107-rc.2"
check "a draft candidate is reported as a draft" "$rc_state" "draft"
GH_RC_NONE=1 load_rc_status
check "RCs of shipped versions are history, not candidates" "$rc_tag" ""

# test-rc: macOS gets the DMG verified against GitHub's own digest, plus the
# blank card. The launch prompt is terminal-only and never reached here.
rc_platform() { printf 'Darwin:arm64'; }
rc_stdin_is_terminal() { return 1; }
rc_kit_root="$fixture_root/rc-kits"
rc_kit="$rc_kit_root/v0.0.107-rc.1"
rc_dmg="OriAgent-0.0.107-rc.1-arm64.dmg"
export GH_DMG_DIGEST="$(printf 'fake dmg for v0.0.107-rc.1\n' | shasum -a 256 | awk '{print $1}')"
: > "$gh_calls"
ORI_RC_DIR="$rc_kit_root" test_rc_action > "$fixture_root/test-rc-output"
check "test-rc downloads the DMG into the kit" "$(<"$rc_kit/$rc_dmg")" "fake dmg for v0.0.107-rc.1"
check "test-rc downloads the blank test card" \
  "$(<"$rc_kit/rc-test-report-v0.0.107-rc.1.md")" "# RC card v0.0.107-rc.1"
assert_call $'CALL\trelease\tdownload\tv0.0.107-rc.1\t--dir\t'"$rc_kit"$'\t--skip-existing\t--pattern\trc-test-report-v0.0.107-rc.1.md\t--pattern\t'"$rc_dmg"
assert_output_has "test-rc" "$fixture_root/test-rc-output" "SHA-256    $GH_DMG_DIGEST (matches GitHub)"
assert_output_has "test-rc" "$fixture_root/test-rc-output" \
  "Release    https://github.com/johnjallday/ori-agent/releases/tag/v0.0.107-rc.1"
assert_output_has "test-rc" "$fixture_root/test-rc-output" \
  "After an APPROVE decision: ./scripts/devops.sh promote v0.0.107-rc.1"
assert_output_lacks "test-rc without a terminal" "$fixture_root/test-rc-output" "Launch this DMG"
check "test-rc lists, reads the release, and downloads" "$(count_gh_calls)" "3"
assert_no_github_write "test-rc"

# A card already in the kit is never replaced by a re-run.
printf 'my notes\n' > "$rc_kit/rc-test-report-v0.0.107-rc.1.md"
ORI_RC_DIR="$rc_kit_root" test_rc_action > /dev/null
check "test-rc keeps a card already in the kit" \
  "$(<"$rc_kit/rc-test-report-v0.0.107-rc.1.md")" "my notes"

printf 'tampered\n' > "$rc_kit/$rc_dmg"
status=0
ORI_RC_DIR="$rc_kit_root" test_rc_action > /dev/null 2> "$fixture_root/test-rc-mismatch" || status=$?
check "a digest mismatch fails test-rc" "$status" "1"
assert_output_has "a digest mismatch" "$fixture_root/test-rc-mismatch" "SHA-256 mismatch for $rc_dmg"

rc_platform() { printf 'Linux:x86_64'; }
ORI_RC_DIR="$fixture_root/rc-linux" test_rc_action > "$fixture_root/test-rc-linux-output"
check "other platforms fetch only the card" \
  "$(ls "$fixture_root/rc-linux/v0.0.107-rc.1")" "rc-test-report-v0.0.107-rc.1.md"
assert_output_has "test-rc on another platform" "$fixture_root/test-rc-linux-output" \
  "download yours from the release page"
rc_platform() { printf 'Darwin:arm64'; }

for refusal in draft none unpublished not-prerelease; do
  : > "$gh_calls"
  status=0
  case "$refusal" in
    draft) GH_RC_DRAFT=1 ORI_RC_DIR="$fixture_root/rc-refused" test_rc_action \
      > /dev/null 2> "$fixture_root/test-rc-refusal" || status=$? ;;
    none) GH_RC_NONE=1 ORI_RC_DIR="$fixture_root/rc-refused" test_rc_action \
      > /dev/null 2> "$fixture_root/test-rc-refusal" || status=$? ;;
    unpublished) GH_RC_UNPUBLISHED=1 ORI_RC_DIR="$fixture_root/rc-refused" test_rc_action v0.0.108-rc.1 \
      > /dev/null 2> "$fixture_root/test-rc-refusal" || status=$? ;;
    not-prerelease) GH_RC_NOT_PRERELEASE=1 ORI_RC_DIR="$fixture_root/rc-refused" test_rc_action \
      > /dev/null 2> "$fixture_root/test-rc-refusal" || status=$? ;;
  esac
  check "test-rc refuses a $refusal candidate" "$status" "1"
  assert_output_lacks "test-rc refusing a $refusal candidate" "$gh_calls" $'release\tdownload'
done
GH_RC_DRAFT=1 test_rc_action > /dev/null 2> "$fixture_root/test-rc-refusal" || true
assert_output_has "a draft candidate" "$fixture_root/test-rc-refusal" "v0.0.107-rc.2 is still a draft"
GH_RC_NONE=1 test_rc_action > /dev/null 2> "$fixture_root/test-rc-refusal" || true
assert_output_has "no candidate" "$fixture_root/test-rc-refusal" \
  "No release candidate is in testing (latest stable: v0.0.106)."

# promote: the local check and the dispatch are the two seams. Nothing below
# runs release-candidate.py (it fetches) or release.sh (it dispatches).
promote_calls="$fixture_root/promote-calls"
release_candidate_check() {
  printf 'CHECK\t%s\n' "$1" >> "$promote_calls"
  if [[ -n "${RC_CHECK_REFUSE:-}" ]]; then
    printf 'REFUSED — %s has been superseded by a newer RC\n' "$1"
    return 1
  fi
  printf 'Approve **%s** → **v0.0.107**\n' "$1"
}
release_dispatch_promote() {
  printf 'DISPATCH\t%s\n' "$1" >> "$promote_calls"
}

rc_stdin_is_terminal() { return 0; }
: > "$promote_calls"
printf 'v0.0.107-rc.1\n' | promote_rc_action > "$fixture_root/promote-output"
check "typing the exact tag checks then dispatches it" \
  "$(<"$promote_calls")" $'CHECK\tv0.0.107-rc.1\nDISPATCH\tv0.0.107-rc.1'
assert_output_has "promote" "$fixture_root/promote-output" \
  "Next: GitHub checks v0.0.107-rc.1 again"
assert_output_has "promote" "$fixture_root/promote-output" \
  "https://github.com/johnjallday/ori-agent/actions/workflows/promote-release.yml"

: > "$promote_calls"
printf 'yes\n' | promote_rc_action > "$fixture_root/promote-cancel-output"
check "anything but the exact tag cancels after the check" "$(<"$promote_calls")" $'CHECK\tv0.0.107-rc.1'
assert_output_has "a cancelled promotion" "$fixture_root/promote-cancel-output" \
  "Cancelled; nothing was dispatched."

: > "$promote_calls"
status=0
printf '' | promote_rc_action > /dev/null || status=$?
check "end of input cancels without dispatching" "$(<"$promote_calls")" $'CHECK\tv0.0.107-rc.1'
check "end of input is not success" "$status" "1"

: > "$promote_calls"
status=0
RC_CHECK_REFUSE=1 promote_rc_action --yes > "$fixture_root/promote-refused-output" \
  2> "$fixture_root/promote-refused-error" || status=$?
check "a refused check stops promotion" "$status" "1"
check "a refused check never dispatches" "$(<"$promote_calls")" $'CHECK\tv0.0.107-rc.1'
assert_output_has "a refused check" "$fixture_root/promote-refused-output" "REFUSED —"
assert_output_has "a refused check" "$fixture_root/promote-refused-error" "Nothing was dispatched."

: > "$promote_calls"
status=0
GH_RC_DRAFT=1 promote_rc_action --yes > /dev/null 2>&1 || status=$?
check "a draft candidate is refused before the check" "$status" "1"
check "a draft candidate is never checked or dispatched" "$(<"$promote_calls")" ""

rc_stdin_is_terminal() { return 1; }
: > "$promote_calls"
: > "$gh_calls"
status=0
promote_rc_action > /dev/null 2> "$fixture_root/promote-no-tty" || status=$?
check "promotion without a terminal needs --yes" "$status" "2"
assert_output_has "promotion without a terminal" "$fixture_root/promote-no-tty" "pass --yes to confirm"
check "promotion without a terminal contacts nothing" "$(<"$promote_calls")$(count_gh_calls)" "0"

: > "$promote_calls"
: > "$gh_calls"
promote_rc_action v0.0.107-rc.1 --yes > /dev/null
check "--yes dispatches an explicit tag without prompting" \
  "$(<"$promote_calls")" $'CHECK\tv0.0.107-rc.1\nDISPATCH\tv0.0.107-rc.1'
assert_output_lacks "an explicit tag" "$gh_calls" $'release\tlist'

if [[ "$failures" -ne 0 ]]; then exit 1; fi
printf 'devops-release-candidate.test.sh: ok\n'
