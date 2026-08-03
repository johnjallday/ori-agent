#!/bin/zsh
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-wt-backlog.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

dev_root="$fixture_root/dev"
remote_root="$fixture_root/origin.git"
git init -q -b dev "$dev_root"
git init -q --bare "$remote_root"
git -C "$dev_root" config user.name "Ori Backlog Test"
git -C "$dev_root" config user.email "ori-backlog@example.test"
git -C "$dev_root" remote add origin "$remote_root"

today="$(date +%F)"
boundary="$(date -v-7d +%F 2>/dev/null || date -d '7 days ago' +%F)"
old="$(date -v-8d +%F 2>/dev/null || date -d '8 days ago' +%F)"

cat > "$dev_root/BACKLOG.md" <<EOF
# Backlog

## Ideas
- $old old idea must remain

## Doing
- $old old active work must remain

## Shipped / dropped
- $today recent shipment
- $boundary boundary shipment
- $old old shipment to prune
- undated historical decision must remain
EOF

git -C "$dev_root" add BACKLOG.md
git -C "$dev_root" commit -q -m "test: seed backlog"
git -C "$dev_root" push -q -u origin dev

source "$repo_root/scripts/wt.sh"

function wt_get_dev_worktree {
  print -r -- "$dev_root"
}

# The remaining file-backed commands are `sync` and `prune`, and they exist only
# until the file lifecycle is removed. Capture now goes to GitHub, so the idea
# below is written into the fixture directly rather than through `wt backlog
# add`, which no longer touches a file at all.
print -r -- "- $today new retained idea" >> "$dev_root/BACKLOG.md"
wt backlog sync > "$fixture_root/sync-output"
rg -q -- "old idea must remain" "$dev_root/BACKLOG.md"
rg -q -- "old active work must remain" "$dev_root/BACKLOG.md"
rg -q -- "boundary shipment" "$dev_root/BACKLOG.md"
rg -q -- "undated historical decision must remain" "$dev_root/BACKLOG.md"
rg -q -- "new retained idea" "$dev_root/BACKLOG.md"
if rg -q -- "old shipment to prune" "$dev_root/BACKLOG.md"; then
  print -r -- "automatic retention left an expired shipped entry" >&2
  exit 1
fi
git --git-dir="$remote_root" show dev:BACKLOG.md > "$fixture_root/remote-backlog"
rg -q -- "new retained idea" "$fixture_root/remote-backlog"
if rg -q -- "old shipment to prune" "$fixture_root/remote-backlog"; then
  print -r -- "automatic retention was not pushed" >&2
  exit 1
fi

# Explicit pruning is idempotent and does not create an empty follow-up commit.
before_count="$(git -C "$dev_root" rev-list --count HEAD)"
wt backlog prune 7 > "$fixture_root/prune-output"
after_count="$(git -C "$dev_root" rev-list --count HEAD)"
[[ "$before_count" == "$after_count" ]]
rg -q "no BACKLOG.md changes to commit" "$fixture_root/prune-output"

# Invalid retention cannot mutate or commit anything.
if wt backlog prune never > "$fixture_root/invalid-output" 2>&1; then
  print -r -- "invalid retention was accepted" >&2
  exit 1
fi
[[ "$after_count" == "$(git -C "$dev_root" rev-list --count HEAD)" ]]

# --- GitHub-backed listing ----------------------------------------------------
#
# The backlog is GitHub Issues now, so the list assertions run against a fake
# `gh` on PATH: deterministic answers, recorded argument vectors, no network, no
# authentication, and no possibility of touching the repository's real Issues.
#
# Three linked checkouts of one throwaway repository stand in for the source
# checkout, `dev`, and a feature worktree. `wt backlog` has to answer the same
# way from all of them, because they are one repository.

gh_root="$fixture_root/github"
fake_bin="$gh_root/bin"
mkdir -p "$fake_bin"

gh_calls="$gh_root/gh-calls"
gh_issues="$gh_root/issues.json"

cat > "$gh_issues" <<'JSON'
[
  {"number":293,"title":"Skin Makeover","author":{"login":"johnjallday"},"labels":[],
   "url":"https://github.com/johnjallday/ori-agent/issues/293",
   "createdAt":"2026-08-02T23:37:08Z","updatedAt":"2026-08-02T23:37:08Z"},
  {"number":292,"title":"Coordinate based map","author":{"login":"johnjallday"},
   "labels":[{"name":"idea"}],
   "url":"https://github.com/johnjallday/ori-agent/issues/292",
   "createdAt":"2026-08-02T23:06:49Z","updatedAt":"2026-08-02T23:06:49Z"}
]
JSON

cat > "$gh_root/issue-292.json" <<'JSON'
{"number":292,"title":"Coordinate based map","author":{"login":"johnjallday"},
 "labels":[{"name":"idea"}],"url":"https://github.com/johnjallday/ori-agent/issues/292",
 "createdAt":"2026-08-02T23:06:49Z","updatedAt":"2026-08-02T23:10:00Z",
 "state":"OPEN","stateReason":"","closedAt":"",
 "body":"Show stations at real coordinates.\n\n- [ ] pick a projection"}
JSON

cat > "$fake_bin/gh" <<'SH'
#!/bin/sh
# Deterministic stand-in for the GitHub CLI. It records how it was called —
# every argument on its own line, so a title containing spaces or shell
# metacharacters can be checked exactly as it arrived — and answers from a
# fixture, so the suite tests Ori's behavior rather than GitHub's availability.
{
  printf 'CALL %s\n' "$*"
  for argument in "$@"; do
    printf 'ARG %s\n' "$argument"
  done
} >> "$GH_CALLS"
case "$1 $2" in
  "repo view")
    printf '%s' '{"name":"ori-agent","owner":{"login":"johnjallday"}}'
    ;;
  "issue list")
    if [ -n "${GH_FAIL:-}" ]; then
      printf 'HTTP 404: Not Found (https://api.github.com/repos/o/r/issues)\n' >&2
      exit 1
    fi
    cat "$GH_ISSUES"
    ;;
  "issue view")
    cat "$GH_DETAIL"
    ;;
  "issue create")
    if [ -n "${GH_FAIL:-}" ]; then
      printf 'HTTP 403: Resource not accessible by integration\n' >&2
      exit 1
    fi
    printf 'https://github.com/johnjallday/ori-agent/issues/294\n'
    ;;
  *)
    printf 'the fake gh was asked something the command should not ask: %s\n' "$*" >&2
    exit 99
    ;;
esac
SH
chmod +x "$fake_bin/gh"

# One compiled helper, reused by every checkout. scripts/herdr-devflow.sh
# prefers this over rebuilding from source on each invocation.
go build -o "$gh_root/herdr-devflow" ./tools/herdr-devflow/cmd/herdr-devflow

gh_source="$gh_root/source"
git init -q -b main "$gh_source"
git -C "$gh_source" config user.name "Ori Backlog Test"
git -C "$gh_source" config user.email "ori-backlog@example.test"
mkdir -p "$gh_source/scripts"
cp "$repo_root/scripts/herdr-devflow.sh" "$gh_source/scripts/herdr-devflow.sh"
git -C "$gh_source" add scripts/herdr-devflow.sh
git -C "$gh_source" commit -q -m "test: seed devflow helper"
git -C "$gh_source" worktree add -q -b dev "$gh_root/dev-checkout"
git -C "$gh_source" worktree add -q -b feature/backlog "$gh_root/feature-checkout"

export PATH="$fake_bin:$PATH"
export HERDR_DEVFLOW_BINARY="$gh_root/herdr-devflow"
export HERDR_DEVFLOW_HOME="$gh_root/runtime"
export GH_CALLS="$gh_calls"
export GH_ISSUES="$gh_issues"
export GH_DETAIL="$gh_root/issue-292.json"

typeset -a gh_checkouts
gh_checkouts=("$gh_source" "$gh_root/dev-checkout" "$gh_root/feature-checkout")

# Every supported list spelling is one path, from every checkout.
for checkout in "${gh_checkouts[@]}"; do
  name="${checkout:t}"
  : > "$gh_calls"
  ( cd "$checkout" && wt backlog ) > "$gh_root/$name-bare"
  ( cd "$checkout" && wt backlog list ) > "$gh_root/$name-named"
  ( cd "$checkout" && wt backlog ls ) > "$gh_root/$name-alias"
  if ! cmp -s "$gh_root/$name-bare" "$gh_root/$name-named" \
    || ! cmp -s "$gh_root/$name-bare" "$gh_root/$name-alias"; then
    print -r -- "the list spellings disagreed in $name" >&2
    exit 1
  fi
  rg -q -- "Ori backlog" "$gh_root/$name-bare"
  rg -q -- "#293" "$gh_root/$name-bare"
  rg -q -- "Coordinate based map" "$gh_root/$name-bare"
  # Redirected output is data, not decoration: no escape sequences may reach a
  # pipe, a file, or another program.
  if rg -q -- $'\033' "$gh_root/$name-bare"; then
    print -r -- "redirected output carried ANSI escapes in $name" >&2
    exit 1
  fi
done

# The three checkouts are one repository, so they must name one repository and
# return one set of Issues. Only the observation time may differ.
for checkout in "${gh_checkouts[@]}"; do
  name="${checkout:t}"
  ( cd "$checkout" && wt backlog --json ) | rg -v "observed_at" > "$gh_root/$name-json"
done
if ! cmp -s "$gh_root/source-json" "$gh_root/dev-checkout-json" \
  || ! cmp -s "$gh_root/source-json" "$gh_root/feature-checkout-json"; then
  print -r -- "the same repository produced different backlogs from different checkouts" >&2
  exit 1
fi
rg -q '"schema_version": 1' "$gh_root/source-json"
rg -q '"repository": "johnjallday/ori-agent"' "$gh_root/source-json"
rg -q '"author_scope": "me"' "$gh_root/source-json"
rg -q '"state": "open"' "$gh_root/source-json"
rg -q '"truncated": false' "$gh_root/source-json"
# The list is for choosing work, not for reading descriptions.
if rg -q '"body"' "$gh_root/source-json"; then
  print -r -- "the list JSON carried Issue bodies" >&2
  exit 1
fi

# The default listing asks GitHub for this repository's open Issues by the
# authenticated user, and nothing else.
: > "$gh_calls"
( cd "$gh_root/feature-checkout" && wt backlog ) > /dev/null
rg -q -- "repo view --json owner,name" "$gh_calls"
rg -q -- "issue list --repo johnjallday/ori-agent --state open" "$gh_calls"
rg -q -- "--author @me" "$gh_calls"
for unsupported_filter in "--label" "--milestone" "--assignee" "--search" "--web"; do
  if rg -q -- "$unsupported_filter" "$gh_calls"; then
    print -r -- "the default listing filtered on $unsupported_filter" >&2
    exit 1
  fi
done

# --all removes the author filter and nothing else.
: > "$gh_calls"
( cd "$gh_root/feature-checkout" && wt backlog --all ) > "$gh_root/all-output"
rg -q -- "issue list --repo johnjallday/ori-agent --state open" "$gh_calls"
rg -q -- "by all authors" "$gh_root/all-output"
if rg -q -- "--author" "$gh_calls"; then
  print -r -- "--all kept an author filter" >&2
  exit 1
fi

# Listing is a read: no commit, no working-tree change, in any checkout.
for checkout in "${gh_checkouts[@]}"; do
  [[ "$(git -C "$checkout" rev-list --count HEAD)" == "1" ]]
  [[ -z "$(git -C "$checkout" status --porcelain)" ]]
done

# An unsupported invocation is a usage error (exit 2) decided before any query.
: > "$gh_calls"
for invalid_args in "definitely-not-a-subcommand" "--nope" "list extra"; do
  invalid_status=0
  ( cd "$gh_root/feature-checkout" && wt backlog ${=invalid_args} ) \
    > "$gh_root/invalid-output" 2>&1 || invalid_status=$?
  if [[ "$invalid_status" != "2" ]]; then
    print -r -- "wt backlog $invalid_args exited $invalid_status, want 2" >&2
    exit 1
  fi
done
if [[ -s "$gh_calls" ]]; then
  print -r -- "an invalid invocation still queried GitHub: $(<"$gh_calls")" >&2
  exit 1
fi

# A failed query is a failure, not an empty backlog.
: > "$gh_calls"
failed_status=0
( export GH_FAIL=1; cd "$gh_root/feature-checkout" && wt backlog ) \
  > "$gh_root/failed-output" 2>&1 || failed_status=$?
[[ "$failed_status" == "1" ]]
if rg -q -- "0 open Issues" "$gh_root/failed-output"; then
  print -r -- "a failed query was rendered as an empty backlog" >&2
  exit 1
fi
rg -q -- "Recovery:" "$gh_root/failed-output"

# --- view and add through the same shell boundary -----------------------------

# `view` reads one Issue in full, including the body the listing withholds.
: > "$gh_calls"
( cd "$gh_root/feature-checkout" && wt backlog view 292 ) > "$gh_root/view-output"
rg -q -- "issue view 292 --repo johnjallday/ori-agent" "$gh_calls"
rg -q -- "#292" "$gh_root/view-output"
rg -q -- "Coordinate based map" "$gh_root/view-output"
rg -q -- "pick a projection" "$gh_root/view-output"
# Reading an Issue is reading: no browser, no editor, no attachment fetch.
for interactive_flag in "--web" "--editor" "--comments"; do
  if rg -q -- "$interactive_flag" "$gh_calls"; then
    print -r -- "wt backlog view used $interactive_flag" >&2
    exit 1
  fi
done

# `add` creates one Issue and reports it, and the title reaches gh as exactly
# one argument no matter what it contains. A shell that re-split this would
# create an Issue named after its first word — or run the rest.
hostile_title='$(touch /tmp/ori-backlog-should-not-exist); rm -rf . && echo "pwned" | cat'
: > "$gh_calls"
( cd "$gh_root/feature-checkout" && wt backlog add "$hostile_title" --body "a body with 'quotes' and \$vars" ) \
  > "$gh_root/add-output"
rg -q -- "issue create --repo johnjallday/ori-agent" "$gh_calls"
rg -q -- "#294" "$gh_root/add-output"
if ! rg -qF -- "ARG $hostile_title" "$gh_calls"; then
  print -r -- "the title did not reach gh as a single argument: $(<"$gh_calls")" >&2
  exit 1
fi
if [[ -e /tmp/ori-backlog-should-not-exist ]]; then
  print -r -- "the title was executed by a shell" >&2
  exit 1
fi
# Nothing else is set on a captured idea, and no editor or browser is opened.
for unsupported_option in "--label" "--assignee" "--milestone" "--project" \
  "--template" "--editor" "--web" "--body-file"; do
  if rg -q -- "$unsupported_option" "$gh_calls"; then
    print -r -- "wt backlog add sent $unsupported_option" >&2
    exit 1
  fi
done

# The checkouts stay clean: capture writes to GitHub, never to Git.
for checkout in "${gh_checkouts[@]}"; do
  [[ "$(git -C "$checkout" rev-list --count HEAD)" == "1" ]]
  [[ -z "$(git -C "$checkout" status --porcelain)" ]]
done

# A failed creation is a failure, and it leaves nothing local behind.
: > "$gh_calls"
add_failure_status=0
( export GH_FAIL=1; cd "$gh_root/feature-checkout" && wt backlog add "an idea that will not land" ) \
  > "$gh_root/add-failed-output" 2>&1 || add_failure_status=$?
[[ "$add_failure_status" == "1" ]]
if rg -q -- "#" "$gh_root/add-failed-output"; then
  print -r -- "a failed creation reported an Issue number" >&2
  exit 1
fi
[[ -z "$(git -C "$gh_root/feature-checkout" status --porcelain)" ]]

# Argument mistakes on view and add are rejected before any request.
: > "$gh_calls"
for invalid_args in "view" "view 0" "view 292 293" "add" "add title --all" "view 292 --all"; do
  invalid_status=0
  ( cd "$gh_root/feature-checkout" && wt backlog ${=invalid_args} ) > /dev/null 2>&1 || invalid_status=$?
  if [[ "$invalid_status" != "2" ]]; then
    print -r -- "wt backlog $invalid_args exited $invalid_status, want 2" >&2
    exit 1
  fi
done
if [[ -s "$gh_calls" ]]; then
  print -r -- "an invalid view/add invocation still called gh: $(<"$gh_calls")" >&2
  exit 1
fi

# Help documents the command, and the rest of the dispatcher is unaffected.
wt help > "$fixture_root/help-output" 2>&1
rg -q "wt backlog" "$fixture_root/help-output"
rg -q "wt status" "$fixture_root/help-output"

print -r -- "wt backlog tests passed"
