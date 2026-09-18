#!/bin/zsh
# Verify `wt done` archives a finished worktree's tasks/ into dev as labelled
# "<name> (done #PR)" copies: a finished plan never overwrites a live plan of
# the same name, never reappears as startable in `wt start`, and a failed copy
# stops wt done before the worktree (the only other copy) is removed.
#
# Git, GitHub, and Herdr are replaced by recorders, so this suite never touches
# a real repository.
set -euo pipefail

exec < /dev/null

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-wt-done-archive.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT
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

function expect_file {
  [[ -f "$2" ]] || fail "$1: expected file ${2#$fixture_root/}"
}

function expect_no_file {
  [[ ! -e "$2" ]] || fail "$1: did not expect ${2#$fixture_root/}"
}

function expect_content {
  [[ -f "$2" && "$(<"$2")" == "$3" ]] || fail "$1: expected ${2#$fixture_root/} to contain '$3'"
}

function expect_has {
  grep -Fq -- "$3" "$2" || fail "$1: expected '$3'" "$2"
}

function expect_lacks {
  if grep -Fq -- "$3" "$2"; then
    fail "$1: did not expect '$3'" "$2"
  fi
}

# --- fixture ---------------------------------------------------------------

dev_root="$fixture_root/ori-agent-dev"
target_root="$fixture_root/foo"
out="$fixture_root/out"
git_calls="$fixture_root/git-calls"
merged_pr="518"

function reset_fixture {
  rm -rf -- "$dev_root" "$target_root"
  mkdir -p "$dev_root/tasks" "$target_root/tasks/screenshots"
  print -r -- "worktree prd" > "$target_root/tasks/prd-foo.md"
  print -r -- "worktree tasks" > "$target_root/tasks/tasks-foo.md"
  print -r -- "worktree issue" > "$target_root/tasks/issue-foo.md"
  print -r -- "png" > "$target_root/tasks/screenshots/map.png"
  print -r -- "finder junk" > "$target_root/tasks/.DS_Store"
  # A PRD kept in dev for a later slice is newer than the worktree's copy.
  print -r -- "live dev prd" > "$dev_root/tasks/prd-foo.md"
  : > "$git_calls"
  merged_pr="518"
}

function wt_get_dev_worktree { print -r -- "$dev_root" }
function wt_resolve_worktree_path { print -r -- "$target_root" }
function wt_resolve_worktree_branch { print -r -- "feature/foo" }
function wt_is_protected_worktree { return 1 }
function wt_herd_cleanup_preflight { return 0 }
function gh { print -r -- "$merged_pr" }
function git { print -r -- "$*" >> "$git_calls" }

# --- labelled copies ---------------------------------------------------------

reset_fixture
wt_done_archive_tasks "$target_root/tasks" "$dev_root/tasks" 518 > "$out"
expect_content "PRD archived under the PR label" "$dev_root/tasks/prd-foo (done #518).md" "worktree prd"
expect_content "task list archived under the PR label" "$dev_root/tasks/tasks-foo (done #518).md" "worktree tasks"
expect_content "Issue snapshot archived under the PR label" "$dev_root/tasks/issue-foo (done #518).md" "worktree issue"
expect_content "directory archived under the PR label" "$dev_root/tasks/screenshots (done #518)/map.png" "png"
expect_content "live dev PRD is not overwritten" "$dev_root/tasks/prd-foo.md" "live dev prd"
expect_no_file "unlabelled task list is not written" "$dev_root/tasks/tasks-foo.md"
expect_no_file "dotfiles are not archived" "$dev_root/tasks/.DS_Store"
expect_has "each archived name is reported" "$out" "tasks/prd-foo (done #518).md"

# Retrying after an aborted wt done rewrites the same copies, never nests them.
wt_done_archive_tasks "$target_root/tasks" "$dev_root/tasks" 518 > "$out"
expect_no_file "retry does not nest directories" "$dev_root/tasks/screenshots (done #518)/screenshots"
expect_file "retry keeps the directory copy" "$dev_root/tasks/screenshots (done #518)/map.png"

# An already-labelled name keeps its original label.
reset_fixture
print -r -- "older" > "$target_root/tasks/prd-bar (done #7).md"
wt_done_archive_tasks "$target_root/tasks" "$dev_root/tasks" 518 > "$out"
expect_content "labelled name is kept" "$dev_root/tasks/prd-bar (done #7).md" "older"
expect_no_file "label is not doubled" "$dev_root/tasks/prd-bar (done #7) (done #518).md"

# Without a confirmed merged PR number the label is just "(done)".
for missing in "" "null"; do
  reset_fixture
  wt_done_archive_tasks "$target_root/tasks" "$dev_root/tasks" "$missing" > "$out"
  expect_content "unmerged label for '$missing'" "$dev_root/tasks/prd-foo (done).md" "worktree prd"
done

# --- wt done end to end ------------------------------------------------------
# Ad-hoc features only: an issue-*.md snapshot opts into the Issue lifecycle,
# which wt-herd.test.sh covers.

reset_fixture
rm -f -- "$target_root/tasks/issue-foo.md"
done_status=0
wt done foo > "$out" 2>&1 || done_status=$?
[[ "$done_status" == "0" ]] || fail "wt done succeeds (status $done_status)" "$out"
expect_content "wt done archives under the merged PR" "$dev_root/tasks/tasks-foo (done #518).md" "worktree tasks"
expect_content "wt done leaves the live dev PRD" "$dev_root/tasks/prd-foo.md" "live dev prd"
expect_has "wt done removes the worktree after archiving" "$git_calls" "worktree remove $target_root --force"

# The archive is the only record once the worktree is gone, so a failed copy
# must stop wt done before anything is removed.
reset_fixture
rm -f -- "$target_root/tasks/issue-foo.md"
rm -rf -- "$dev_root/tasks"
print -r -- "not a directory" > "$dev_root/tasks"
done_status=0
wt done foo > "$out" 2>&1 || done_status=$?
[[ "$done_status" == "1" ]] || fail "failed archive stops wt done (status $done_status)" "$out"
expect_has "failed archive explains itself" "$out" "worktree preserved"
expect_lacks "failed archive removes nothing" "$git_calls" "worktree remove"

# --- archived plans are history, not startable features ----------------------

reset_fixture
print -r -- "archived" > "$dev_root/tasks/tasks-foo (done #518).md"
print -r -- "archived" > "$dev_root/tasks/prd-old (done #400).md"
print -r -- "live" > "$dev_root/tasks/prd-next.md"
function wt_plan_is_interactive { return 0 }
wt start > "$out" 2>&1 || true
expect_has "picker lists the live PRD" "$out" "prd-next.md"
expect_has "picker lists the PRD kept in dev" "$out" "prd-foo.md"
expect_lacks "picker hides archived plans" "$out" "(done #"

rm -f -- "$dev_root/tasks/prd-next.md" "$dev_root/tasks/prd-foo.md"
function wt_plan_is_interactive { return 1 }
wt start > "$out" 2>&1 || true
expect_has "only archived plans means nothing to start" "$out" "No PRDs or task lists found"

# devops reads the same tasks/ folder through devflow-common.sh.
if ! bash -c 'source "$1"; is_archived_plan "/x/tasks/tasks-foo (done #518).md" && ! is_archived_plan "/x/tasks/tasks-foo.md"' \
  _ "$repo_root/scripts/lib/devflow-common.sh"; then
  fail "devflow-common.sh is_archived_plan tells archived plans from live ones"
fi

if (( failures )); then
  print -r -- "$failures failure(s)" >&2
  exit 1
fi
print -r -- "wt-done-archive: ok"
