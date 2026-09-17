#!/bin/zsh
# Verify bare `wt done` is a chooser rather than a guess from the current
# directory: it lists only removable feature worktrees, labels merged PRs from a
# fresh read, hands each pick to the named `wt done <name>` path with the right
# flags, and removes nothing on quit, EOF, or an invalid choice.
#
# The named path itself (Herdr guard, Issue closure, archival, removal) is
# covered by wt-herd.test.sh. Here it is replaced by a recorder, so this suite
# never touches Git, GitHub, or Herdr.
set -euo pipefail

exec < /dev/null

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-wt-done-repl.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT
# Resolved so expectations match $PWD. The fresh worktree below deliberately
# keeps an unnormalized spelling to prove the chooser compares real paths.
fixture_root="${fixture_root:A}"

source "$repo_root/scripts/wt.sh"

failures=0
function fail {
  print -r -- "FAIL: $1" >&2
  if [[ -n "${2:-}" && -f "$2" ]]; then
    print -r -- "  output:" >&2
    sed 's/^/    /' "$2" >&2
  fi
  failures=$(( failures + 1 ))
}

function expect_has {
  grep -Fq -- "$3" "$2" || fail "$1: expected '$3'" "$2"
}

function expect_lacks {
  if grep -Fq -- "$3" "$2"; then
    fail "$1: did not expect '$3'" "$2"
  fi
}

function expect_eq {
  [[ "$2" == "$3" ]] || fail "$1: expected '$3', got '$2'"
}

# --- fixture ---------------------------------------------------------------

typeset -ga FIXTURE_PATHS FIXTURE_BRANCHES
dev_root="$fixture_root/ori-agent-dev"
out="$fixture_root/out"
done_calls="$fixture_root/done-calls"
gh_calls="$fixture_root/gh-calls"

function reset_fixture {
  FIXTURE_PATHS=(
    "$fixture_root/ori-agent"
    "$dev_root"
    "$fixture_root/shipped"
    "$fixture_root/in-review"
    "$fixture_root//fresh"
  )
  FIXTURE_BRANCHES=(main dev feature/shipped feature/in-review feature/fresh)
  mkdir -p "${FIXTURE_PATHS[@]}"
  : > "$done_calls"
  : > "$gh_calls"
  typeset -gA WT_MERGED_SET
  WT_MERGED_SET=()
  typeset -g WT_MERGED_LOADED=""
}

function wt_load_worktrees {
  typeset -ga WT_PATHS WT_BRANCHES
  WT_PATHS=("${FIXTURE_PATHS[@]}")
  WT_BRANCHES=("${FIXTURE_BRANCHES[@]}")
}

function wt_get_dev_worktree {
  print -r -- "$dev_root"
}

function wt_branch_status {
  case "$1" in
    feature/shipped) print -r -- "3 1 active" ;;
    feature/in-review) print -r -- "2 0 active" ;;
    # No commits yet. The shared helper calls this "merged" because the branch
    # is an ancestor of dev; the chooser must not repeat that.
    feature/fresh) print -r -- "0 0 merged" ;;
    *) print -r -- "0 0 (base)" ;;
  esac
}

# Only the in-review worktree has uncommitted changes. Any other git call
# would mean the chooser reached past its own listing.
function git {
  if [[ "$1" == -C && "$3" == status ]]; then
    [[ "$2" == */in-review ]] && print -r -- " M work.go"
    return 0
  fi
  print -r -- "unexpected git call: $*" >&2
  return 97
}

# Only feature/shipped has a merged PR.
function gh {
  print -r -- "$*" >> "$gh_calls"
  print -r -- "feature/shipped"
}

# Record named `wt done <name> ...` calls and drop the worktree, the way a real
# finish would. Everything else, including bare `wt done`, runs for real.
functions -c wt_dispatch wt_dispatch_real
function wt_dispatch {
  if [[ "$1" == done && -n "${2:-}" && "$2" != --* ]]; then
    print -r -- "${(j: :)@} @ $PWD" >> "$done_calls"
    local i
    for (( i = 1; i <= ${#FIXTURE_PATHS[@]}; i++ )); do
      if [[ "${FIXTURE_PATHS[$i]:t}" == "$2" ]]; then
        FIXTURE_PATHS[$i]=()
        FIXTURE_BRANCHES[$i]=()
        break
      fi
    done
    return 0
  fi
  wt_dispatch_real "$@"
}

function row_of {
  grep -F -- "[$1]" "$out" | head -n 1
}

cd "$fixture_root"

# --- quitting lists removable worktrees and changes nothing ----------------

reset_fixture
rc=0
wt done <<< "q" > "$out" 2>&1 || rc=$?
expect_eq "quit exits cleanly" "$rc" "0"
expect_has "chooser heading" "$out" "Finish an implementation"
expect_has "feature worktree listed" "$out" "[feature/shipped]"
expect_has "feature worktree listed" "$out" "[feature/in-review]"
expect_has "feature worktree listed" "$out" "[feature/fresh]"
expect_lacks "protected dev worktree hidden" "$out" "[dev]"
expect_lacks "protected main checkout hidden" "$out" "[main]"
expect_eq "quit finishes nothing" "$(<"$done_calls")" ""

# Only a merged PR earns "merged"; a branch with no commits is "empty".
[[ "$(row_of feature/shipped)" == *merged ]] || fail "merged PR is labeled merged: $(row_of feature/shipped)"
[[ "$(row_of feature/in-review)" == *"active, dirty" ]] || fail "unmerged dirty work is labeled active, dirty: $(row_of feature/in-review)"
[[ "$(row_of feature/fresh)" == *empty ]] || fail "clean commit-less branch is labeled empty: $(row_of feature/fresh)"
expect_lacks "unexpected git calls" "$out" "unexpected git call"

# --- a stale shell cache is never trusted ----------------------------------

reset_fixture
WT_MERGED_LOADED=1
WT_MERGED_SET[feature/in-review]=1
wt done <<< $'r\nq' > "$out" 2>&1
expect_eq "entry and r each re-read merged PRs" "$(grep -c 'pr list --state merged' "$gh_calls")" "2"
[[ "$(row_of feature/in-review)" == *"active, dirty" ]] || fail "stale merged cache was trusted: $(row_of feature/in-review)"

# --- EOF behaves like quit ---------------------------------------------------

reset_fixture
rc=0
wt done < /dev/null > "$out" 2>&1 || rc=$?
expect_eq "EOF exits cleanly" "$rc" "0"
expect_eq "EOF finishes nothing" "$(<"$done_calls")" ""

# --- a number picks by position, then the list is shown again --------------

reset_fixture
wt done <<< $'1\nq' > "$out" 2>&1
expect_eq "number runs the named wt done" "$(<"$done_calls")" "done shipped @ $fixture_root"
expect_eq "list is shown again after a pick" "$(grep -c 'Finish an implementation' "$out")" "2"

# --- a name picks too, and a pick can carry its own flags ------------------

reset_fixture
wt done <<< $'in-review --keep-issue-open\nq' > "$out" 2>&1
expect_eq "name with pick flag" "$(<"$done_calls")" "done in-review --keep-issue-open @ $fixture_root"

# --- flags on the bare command apply to every pick -------------------------

reset_fixture
wt done --keep-issue-open <<< $'1\n1\nq' > "$out" 2>&1
expect_eq "bare flags apply to each pick" "$(<"$done_calls")" \
  "done shipped --keep-issue-open @ $fixture_root"$'\n'"done in-review --keep-issue-open @ $fixture_root"

# --- invalid choices re-prompt and finish nothing --------------------------

reset_fixture
wt done <<< $'9\n0\nnope\nq' > "$out" 2>&1
expect_has "out-of-range number rejected" "$out" "Not in the list: 9"
expect_has "zero rejected" "$out" "Not in the list: 0"
expect_has "unknown name rejected" "$out" "Not in the list: nope"
expect_eq "invalid choices finish nothing" "$(<"$done_calls")" ""

# --- an unknown flag is refused before the chooser opens -------------------

reset_fixture
rc=0
wt done --bogus <<< "1" > "$out" 2>&1 || rc=$?
expect_eq "unknown flag fails" "$rc" "1"
expect_has "unknown flag named" "$out" "Unknown wt done option: --bogus"
expect_eq "unknown flag opens no chooser" "$(<"$gh_calls")" ""
expect_eq "unknown flag finishes nothing" "$(<"$done_calls")" ""

# --- finishing everything ends the loop by itself --------------------------

reset_fixture
rc=0
wt done <<< $'1\n1\n1' > "$out" 2>&1 || rc=$?
expect_eq "empty list exits cleanly" "$rc" "0"
expect_eq "every pick was finished" "$(wc -l < "$done_calls" | tr -d ' ')" "3"
expect_has "empty list is announced" "$out" "No feature worktrees left to finish."

# --- finishing the worktree you stand in moves you to dev first ------------

reset_fixture
cd "$fixture_root/fresh/"
wt done <<< $'fresh\nq' > "$out" 2>&1
expect_eq "moved to dev before finishing the current worktree" "$(<"$done_calls")" "done fresh @ $dev_root"
expect_eq "shell is left in dev" "$PWD" "$dev_root"
cd "$fixture_root"

if (( failures > 0 )); then
  print -r -- "$failures wt done chooser assertion(s) failed" >&2
  exit 1
fi
print -r -- "wt done chooser tests passed"
