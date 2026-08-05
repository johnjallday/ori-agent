#!/bin/zsh
set -euo pipefail

# ./scripts/issue.sh reads and writes GitHub Issues. There is no backlog file,
# so there is nothing here about dates, retention windows, or scoped commits:
# this suite checks the command, and it checks that the command touches no Git
# state at all.
#
# The board half of the split — ./scripts/backlog.sh, which reads the linked
# project's Ready column — is covered by Go tests against a stubbed runner. What
# is only testable here is the shell boundary: how the helper is found, and
# whether an argument survives crossing it intact.

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-wt-backlog.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

source "$repo_root/scripts/wt.sh"

# The file-backed helpers are gone, not merely unreferenced.
for removed_function in wt_backlog_file wt_backlog_prune wt_backlog_commit_push \
  wt_backlog_add_idea wt_backlog_ensure_doing wt_backlog_retire wt_backlog_render \
  wt_backlog_cutoff_date; do
  if typeset -f "$removed_function" > /dev/null 2>&1; then
    print -r -- "$removed_function still exists" >&2
    exit 1
  fi
done
if rg -q "WT_BACKLOG_RETENTION_DAYS" "$repo_root/scripts/wt.sh"; then
  print -r -- "the backlog retention setting still exists" >&2
  exit 1
fi
if [[ -e "$repo_root/BACKLOG.md" ]]; then
  print -r -- "BACKLOG.md is still present" >&2
  exit 1
fi

# The entrypoint is an executable that anyone — including an agent that cannot
# source a zsh function — can call as a single token.
if [[ ! -x "$repo_root/scripts/issue.sh" ]]; then
  print -r -- "scripts/backlog.sh is not executable" >&2
  exit 1
fi

# --- GitHub-backed listing ----------------------------------------------------
#
# The backlog is GitHub Issues now, so the list assertions run against a fake
# `gh` on PATH: deterministic answers, recorded argument vectors, no network, no
# authentication, and no possibility of touching the repository's real Issues.
#
# Three linked checkouts of one throwaway repository stand in for the source
# checkout, `dev`, and a feature worktree. The backlog has to answer the same
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

# One compiled helper, reused by every checkout. scripts/backlog.sh prefers this
# over rebuilding from source on each invocation.
go build -o "$gh_root/herdr-devflow" ./tools/herdr-devflow/cmd/herdr-devflow

gh_source="$gh_root/source"
git init -q -b main "$gh_source"
git -C "$gh_source" config user.name "Ori Backlog Test"
git -C "$gh_source" config user.email "ori-backlog@example.test"
mkdir -p "$gh_source/scripts/lib"
cp "$repo_root/scripts/issue.sh" "$gh_source/scripts/issue.sh"
cp "$repo_root/scripts/backlog.sh" "$gh_source/scripts/backlog.sh"
# Both entrypoints source their repo-root and helper resolution from one shared
# file, so a fixture seeding only the entrypoint would fail before reaching
# anything this suite is about.
cp "$repo_root/scripts/lib/devflow-bootstrap.sh" "$gh_source/scripts/lib/devflow-bootstrap.sh"
git -C "$gh_source" add scripts/issue.sh scripts/backlog.sh scripts/lib/devflow-bootstrap.sh
git -C "$gh_source" commit -q -m "test: seed the Issue and board entrypoints"
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

# The removed file commands say what replaced them rather than failing as an
# unknown word, because somebody with the old habit deserves an answer.
: > "$gh_calls"
for removed_command in sync prune; do
  removed_status=0
  ( cd "$gh_root/feature-checkout" && ./scripts/issue.sh "$removed_command" ) \
    > "$fixture_root/removed-$removed_command" 2>&1 || removed_status=$?
  if [[ "$removed_status" != "2" ]]; then
    print -r -- "backlog.sh $removed_command exited $removed_status, want 2" >&2
    exit 1
  fi
  rg -q -- "was removed" "$fixture_root/removed-$removed_command"
done

# No lifecycle-mutation command exists. GitHub owns Issue state; inventing a
# local one is what this feature removed.
for absent_command in promote doing ship drop close select project; do
  absent_status=0
  ( cd "$gh_root/feature-checkout" && ./scripts/issue.sh "$absent_command" ) \
    > /dev/null 2>&1 || absent_status=$?
  if [[ "$absent_status" != "2" ]]; then
    print -r -- "backlog.sh $absent_command exited $absent_status, want 2" >&2
    exit 1
  fi
done
if [[ -s "$gh_calls" ]]; then
  print -r -- "a removed or absent command still queried GitHub: $(<"$gh_calls")" >&2
  exit 1
fi

# Every supported list spelling is one path, from every checkout.
for checkout in "${gh_checkouts[@]}"; do
  name="${checkout:t}"
  : > "$gh_calls"
  ( cd "$checkout" && ./scripts/issue.sh ) > "$gh_root/$name-bare"
  ( cd "$checkout" && ./scripts/issue.sh list ) > "$gh_root/$name-named"
  ( cd "$checkout" && ./scripts/issue.sh ls ) > "$gh_root/$name-alias"
  if ! cmp -s "$gh_root/$name-bare" "$gh_root/$name-named" \
    || ! cmp -s "$gh_root/$name-bare" "$gh_root/$name-alias"; then
    print -r -- "the list spellings disagreed in $name" >&2
    exit 1
  fi
  rg -q -- "Ori Issues" "$gh_root/$name-bare"
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
  ( cd "$checkout" && ./scripts/issue.sh --json ) | rg -v "observed_at" > "$gh_root/$name-json"
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
( cd "$gh_root/feature-checkout" && ./scripts/issue.sh ) > /dev/null
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
( cd "$gh_root/feature-checkout" && ./scripts/issue.sh --all ) > "$gh_root/all-output"
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
  ( cd "$gh_root/feature-checkout" && ./scripts/issue.sh ${=invalid_args} ) \
    > "$gh_root/invalid-output" 2>&1 || invalid_status=$?
  if [[ "$invalid_status" != "2" ]]; then
    print -r -- "backlog.sh $invalid_args exited $invalid_status, want 2" >&2
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
( export GH_FAIL=1; cd "$gh_root/feature-checkout" && ./scripts/issue.sh ) \
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
( cd "$gh_root/feature-checkout" && ./scripts/issue.sh view 292 ) > "$gh_root/view-output"
rg -q -- "issue view 292 --repo johnjallday/ori-agent" "$gh_calls"
rg -q -- "#292" "$gh_root/view-output"
rg -q -- "Coordinate based map" "$gh_root/view-output"
rg -q -- "pick a projection" "$gh_root/view-output"
# Reading an Issue is reading: no browser, no editor, no attachment fetch.
for interactive_flag in "--web" "--editor" "--comments"; do
  if rg -q -- "$interactive_flag" "$gh_calls"; then
    print -r -- "backlog.sh view used $interactive_flag" >&2
    exit 1
  fi
done

# `add` creates one Issue and reports it, and the title reaches gh as exactly
# one argument no matter what it contains. A shell that re-split this would
# create an Issue named after its first word — or run the rest.
hostile_title='$(touch /tmp/ori-backlog-should-not-exist); rm -rf . && echo "pwned" | cat'
: > "$gh_calls"
( cd "$gh_root/feature-checkout" && ./scripts/issue.sh add "$hostile_title" --body "a body with 'quotes' and \$vars" ) \
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
    print -r -- "backlog.sh add sent $unsupported_option" >&2
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
( export GH_FAIL=1; cd "$gh_root/feature-checkout" && ./scripts/issue.sh add "an idea that will not land" ) \
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
  ( cd "$gh_root/feature-checkout" && ./scripts/issue.sh ${=invalid_args} ) > /dev/null 2>&1 || invalid_status=$?
  if [[ "$invalid_status" != "2" ]]; then
    print -r -- "backlog.sh $invalid_args exited $invalid_status, want 2" >&2
    exit 1
  fi
done
if [[ -s "$gh_calls" ]]; then
  print -r -- "an invalid view/add invocation still called gh: $(<"$gh_calls")" >&2
  exit 1
fi

# --- every command, every JSON form, from every checkout ----------------------
#
# One repository read from three linked checkouts must answer identically. The
# list case is asserted above; this repeats it for view and add, and for the
# machine contract of all three, because a difference between checkouts would
# mean the command was reading something other than the repository it is in.

for checkout in "${gh_checkouts[@]}"; do
  name="${checkout:t}"
  ( cd "$checkout" && ./scripts/issue.sh view 292 --json ) > "$gh_root/$name-view-json"
  ( cd "$checkout" && ./scripts/issue.sh add "one idea" --json ) > "$gh_root/$name-add-json"
  ( cd "$checkout" && ./scripts/issue.sh --all --json ) | rg -v "observed_at" > "$gh_root/$name-all-json"
done
for form in view-json add-json all-json; do
  if ! cmp -s "$gh_root/source-$form" "$gh_root/dev-checkout-$form" \
    || ! cmp -s "$gh_root/source-$form" "$gh_root/feature-checkout-$form"; then
    print -r -- "the $form output differed between checkouts of one repository" >&2
    exit 1
  fi
done
rg -q '"schema_version": 1' "$gh_root/source-view-json"
rg -q '"schema_version": 1' "$gh_root/source-add-json"
rg -q '"body"' "$gh_root/source-view-json"
rg -q '"state": "open"' "$gh_root/source-add-json"
rg -q '"author_scope": "all"' "$gh_root/source-all-json"

# --- hostile content and the failure matrix -----------------------------------
#
# Remote text is rendered into a terminal and user text is sent to a subprocess.
# Neither may become something the shell or the terminal acts on.

cat > "$gh_root/hostile-issues.json" <<'JSON'
[
  {"number":9001,"title":"title with \u001b[31mescape and \u202ereordering",
   "author":{"login":"a\u0000b"},"labels":[{"name":"lab\u0007el"}],
   "url":"https://github.com/johnjallday/ori-agent/issues/9001",
   "createdAt":"2026-08-01T10:00:00Z","updatedAt":"2026-08-01T10:00:00Z"}
]
JSON
( export GH_ISSUES="$gh_root/hostile-issues.json"; cd "$gh_root/feature-checkout" && ./scripts/issue.sh ) \
  > "$gh_root/hostile-output"
rg -q -- "9001" "$gh_root/hostile-output"
if rg -q -- $'\033' "$gh_root/hostile-output"; then
  print -r -- "a remote escape sequence reached the terminal" >&2
  exit 1
fi
if rg -q -- $'\u202e' "$gh_root/hostile-output"; then
  print -r -- "a remote reordering character reached the terminal" >&2
  exit 1
fi

# Every classified failure exits 1, explains itself, and offers a recovery. The
# working fake is kept aside so the loop below can swap it out and back.
cp "$fake_bin/gh" "$fake_bin/gh-working"
cat > "$fake_bin/gh-failing" <<'SH'
#!/bin/sh
printf '%s\n' "$GH_FAILURE_TEXT" >&2
exit 1
SH
chmod +x "$fake_bin/gh-failing"
for failure_text in \
  "gh: To get started with GitHub CLI, please run: gh auth login" \
  "HTTP 403: API rate limit exceeded for user ID 1" \
  "HTTP 403: Resource not accessible by integration" \
  "HTTP 404: Not Found" \
  "dial tcp: lookup api.github.com: no such host" \
  "unrecognized catastrophe"; do
  failure_status=0
  ( export GH_FAILURE_TEXT="$failure_text"
    cp "$fake_bin/gh-failing" "$fake_bin/gh"
    cd "$gh_root/feature-checkout" && ./scripts/issue.sh ) \
    > "$gh_root/failure-output" 2>&1 || failure_status=$?
  if [[ "$failure_status" != "1" ]]; then
    print -r -- "'$failure_text' exited $failure_status, want 1" >&2
    exit 1
  fi
  rg -q -- "Recovery:" "$gh_root/failure-output"
  # The raw CLI text — which is where a token would be — is never echoed.
  if rg -qF -- "$failure_text" "$gh_root/failure-output"; then
    print -r -- "raw gh output was echoed for: $failure_text" >&2
    exit 1
  fi
done
cp "$fake_bin/gh-working" "$fake_bin/gh"

# A working GitHub after a run of failures is an ordinary listing again: no
# state was carried forward, because there is no state to carry.
( cd "$gh_root/feature-checkout" && ./scripts/issue.sh ) > "$gh_root/recovered-output"
rg -q -- "2 open Issues" "$gh_root/recovered-output"

# --- how the helper is found --------------------------------------------------
#
# Resolution order is the whole reason this entrypoint exists: an already-built
# helper is preferred over compiling one. Each candidate below is a stub that
# names itself, so the chosen path is observable and the argument vector it
# receives is checked at the same time. The fixture repository has no Go module,
# so a fall-through to `go run` cannot be mistaken for success.

resolution_root="$gh_root/resolution"
mkdir -p "$resolution_root"
cat > "$resolution_root/explicit" <<'SH'
#!/bin/sh
printf 'EXPLICIT %s\n' "$*"
SH
cat > "$resolution_root/installed" <<'SH'
#!/bin/sh
printf 'INSTALLED %s\n' "$*"
SH
cat > "$resolution_root/repo-bin" <<'SH'
#!/bin/sh
printf 'REPO-BIN %s\n' "$*"
SH
chmod +x "$resolution_root/explicit" "$resolution_root/installed" "$resolution_root/repo-bin"

# $HERDR_DEVFLOW_BINARY wins over every later candidate, including `go run`, and
# the arguments cross the boundary verbatim: one parser owns the invocation.
resolution_output="$( cd "$gh_root/feature-checkout" \
  && HERDR_DEVFLOW_BINARY="$resolution_root/explicit" ./scripts/issue.sh list --json )"
if ! print -r -- "$resolution_output" \
  | rg -q '^EXPLICIT --repo-root .*/feature-checkout issue list --json$'; then
  print -r -- "HERDR_DEVFLOW_BINARY was not preferred, or the arguments were rewritten: $resolution_output" >&2
  exit 1
fi

# The installed runtime binary comes next: $HERDR_DEVFLOW_HOME is the runtime
# root and the helper sits at <root>/bin/herdr-devflow, exactly where the Go code
# puts it. A checkout-local bin/ exists here to prove it does not win.
mkdir -p "$gh_root/installed-runtime/bin" "$gh_root/feature-checkout/bin"
cp "$resolution_root/installed" "$gh_root/installed-runtime/bin/herdr-devflow"
cp "$resolution_root/repo-bin" "$gh_root/feature-checkout/bin/herdr-devflow"
resolution_output="$( cd "$gh_root/feature-checkout" \
  && env -u HERDR_DEVFLOW_BINARY HERDR_DEVFLOW_HOME="$gh_root/installed-runtime" ./scripts/issue.sh list )"
if ! print -r -- "$resolution_output" | rg -q '^INSTALLED '; then
  print -r -- "the installed runtime binary was not preferred: $resolution_output" >&2
  exit 1
fi

# A checkout-local bin/herdr-devflow is the third candidate.
resolution_output="$( cd "$gh_root/feature-checkout" \
  && env -u HERDR_DEVFLOW_BINARY HERDR_DEVFLOW_HOME="$gh_root/nothing-installed" ./scripts/issue.sh list )"
if ! print -r -- "$resolution_output" | rg -q '^REPO-BIN '; then
  print -r -- "\$repo_root/bin/herdr-devflow was not used: $resolution_output" >&2
  exit 1
fi

# Without $HERDR_DEVFLOW_HOME the runtime root comes from the platform's user
# config directory. Getting the Linux spelling wrong degrades silently to a
# recompile instead of failing, so a faked `uname` exercises that branch here:
# $XDG_CONFIG_HOME/herdr/ori-devflow/bin, matching Go's os.UserConfigDir().
linux_bin="$gh_root/linux-uname"
mkdir -p "$linux_bin" "$gh_root/xdg/herdr/ori-devflow/bin"
cat > "$linux_bin/uname" <<'SH'
#!/bin/sh
printf 'Linux\n'
SH
chmod +x "$linux_bin/uname"
cp "$resolution_root/installed" "$gh_root/xdg/herdr/ori-devflow/bin/herdr-devflow"
resolution_output="$( cd "$gh_root/feature-checkout" \
  && PATH="$linux_bin:$PATH" XDG_CONFIG_HOME="$gh_root/xdg" \
     env -u HERDR_DEVFLOW_BINARY -u HERDR_DEVFLOW_HOME ./scripts/issue.sh list )"
if ! print -r -- "$resolution_output" | rg -q '^INSTALLED '; then
  print -r -- "the Linux user-config derivation did not find the installed helper: $resolution_output" >&2
  exit 1
fi
rm -f "$gh_root/feature-checkout/bin/herdr-devflow"
rmdir "$gh_root/feature-checkout/bin"

# Outside a Git checkout the entrypoint says so in one line rather than guessing
# at a repository.
outside_root="$gh_root/outside"
mkdir -p "$outside_root"
outside_status=0
outside_output="$( cd "$outside_root" && "$gh_source/scripts/backlog.sh" list 2>&1 )" || outside_status=$?
if [[ "$outside_status" == "0" ]]; then
  print -r -- "backlog.sh succeeded outside a Git checkout" >&2
  exit 1
fi
if [[ "$(print -r -- "$outside_output" | wc -l | tr -d ' ')" != "1" ]]; then
  print -r -- "the outside-a-checkout failure was not one line: $outside_output" >&2
  exit 1
fi
rg -q -- "Git checkout" <<< "$outside_output"

# --- what `wt` says about the commands that left it ---------------------------
#
# Both are signposts, not aliases. A generic unknown-command usage dump would
# leave a reader guessing where the command went, and forwarding would keep the
# old spelling alive indefinitely — so each names its replacement and fails.

signpost_status=0
wt backlog > "$fixture_root/backlog-signpost" 2>&1 || signpost_status=$?
if [[ "$signpost_status" == "0" ]]; then
  print -r -- "wt backlog succeeded; it must fail and point at the script" >&2
  exit 1
fi
if [[ "$(<"$fixture_root/backlog-signpost")" != "wt backlog moved to ./scripts/backlog.sh" ]]; then
  print -r -- "wt backlog said: $(<"$fixture_root/backlog-signpost")" >&2
  exit 1
fi

signpost_status=0
wt merge > "$fixture_root/merge-signpost" 2>&1 || signpost_status=$?
if [[ "$signpost_status" == "0" ]]; then
  print -r -- "wt merge succeeded; it must fail and name the workflow that replaced it" >&2
  exit 1
fi
if [[ "$(<"$fixture_root/merge-signpost")" != "wt merge was removed — use wt pr, then wt done after the PR merges" ]]; then
  print -r -- "wt merge said: $(<"$fixture_root/merge-signpost")" >&2
  exit 1
fi

# Deleting the merge arm must not take its helpers with it: every one of these
# is shared with go, rm, and status, which are still live commands.
for shared_helper in wt_get_dev_worktree wt_load_worktrees wt_color_init \
  wt_compute_widths wt_render_header wt_branch_status wt_render_row; do
  if ! typeset -f "$shared_helper" > /dev/null 2>&1; then
    print -r -- "$shared_helper was deleted; go, rm, and status share it" >&2
    exit 1
  fi
done

# The rest of the dispatcher is unaffected by the backlog leaving it.
wt help > "$fixture_root/help-output" 2>&1
rg -q "wt status" "$fixture_root/help-output"

# --- the board entrypoint, at the shell boundary ------------------------------
#
# backlog.sh's behaviour is covered by Go tests; what only exists out here is
# that the retired Issue spellings reach a rejection through the same shell,
# and that reaching one costs no GitHub call.

: > "$gh_calls"
for retired_args in "list" "view 292" "add a-title" "--all"; do
  retired_status=0
  ( cd "$gh_root/feature-checkout" && ./scripts/backlog.sh ${=retired_args} ) \
    > "$gh_root/retired-output" 2>&1 || retired_status=$?
  if [[ "$retired_status" != "2" ]]; then
    print -r -- "backlog.sh $retired_args exited $retired_status, want 2" >&2
    exit 1
  fi
  # Each one was a working command days ago, so the rejection names where it
  # went rather than claiming it never existed.
  rg -q -- "./scripts/issue.sh" "$gh_root/retired-output"
done
if [[ -s "$gh_calls" ]]; then
  print -r -- "a retired backlog.sh invocation still queried GitHub: $(<"$gh_calls")" >&2
  exit 1
fi

print -r -- "issue.sh tests passed"
