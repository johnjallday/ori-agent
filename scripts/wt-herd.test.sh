#!/bin/zsh
# Verify wt start's Herdr handoff boundary without creating a real shared Git
# worktree or contacting a running Herdr server.
set -euo pipefail

# This suite is non-interactive by definition, and one of the behaviours it
# asserts — wt done failing closed when Herdr state cannot be verified — branches
# on whether stdin is a terminal. Leaving stdin as the caller gave it made the
# run depend on how it was invoked: from a terminal it is a TTY, and under a
# background runner it can be an open pipe that never delivers, which blocks the
# suite instead of failing it. Pin stdin closed so the assertions mean the same
# thing everywhere.
exec < /dev/null

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-wt-herd.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

dev_root="$fixture_root/dev"
target_root="$fixture_root/bridge"
mkdir -p "$dev_root/tasks" "$target_root"
print -r -- "# bridge" > "$dev_root/tasks/prd-bridge.md"
print -r -- "## Tasks" > "$dev_root/tasks/tasks-bridge.md"

source "$repo_root/scripts/wt.sh"

function wt_get_dev_worktree {
  print -r -- "$dev_root"
}

# Stubbed together: the plan phase predicts the worktree path from
# wt_new_worktree_dir and the execute phase takes it from wt_provision_worktree.
# They use the same formula in real life, so the fixture must agree too or the
# summary would appear to describe a different path than the one created.
function wt_new_worktree_dir {
  print -r -- "$fixture_root"
}

function wt_provision_worktree {
  typeset -g WT_PROVISIONED_TARGET="$target_root"
  return 0
}

function wt_herd {
  print -r -- "$*" >> "$fixture_root/herd-calls"
  return 1
}

# Opting out must copy planning artifacts and never invoke the helper.
wt start bridge --no-herdr >/dev/null
[[ -f "$target_root/tasks/prd-bridge.md" ]]
[[ -f "$target_root/tasks/tasks-bridge.md" ]]
[[ ! -f "$fixture_root/herd-calls" ]]

# A failed handoff is non-blocking: Git provisioning remains successful and
# the handoff command receives the exact feature/path/branch identity.
#
# The read-only `target` call precedes it — that is what supplies the workspace
# name in the confirmation summary — so the handoff line is matched rather than
# the whole transcript.
wt start bridge >/dev/null
rg -q "^handoff --feature bridge --worktree $target_root --branch feature/bridge$" "$fixture_root/herd-calls"

# An optional per-feature kind override is forwarded to the initial handoff;
# omitting it above leaves the configured Claude default unchanged.
> "$fixture_root/herd-calls"
wt start bridge --kind codex >/dev/null
rg -q "^handoff --feature bridge --worktree $target_root --branch feature/bridge --kind codex$" "$fixture_root/herd-calls"

# Only the summary's read-only lookup may precede a mutation. Anything else
# would mean the flow contacted Herdr before the user agreed to anything.
if rg -qv "^(target|handoff )" "$fixture_root/herd-calls"; then
  print -r -- "unexpected Herdr calls during start: $(<"$fixture_root/herd-calls")" >&2
  exit 1
fi

# FR-32: a Herdr failure is non-fatal. The worktree and its planning documents
# are what the user actually asked for, so wt start still exits 0, still leaves
# them in place, and prints a reason plus the command that resumes only the
# missing Herdr half.
> "$fixture_root/herd-calls"
degraded_status=0
wt start bridge > "$fixture_root/degraded-output" 2>&1 || degraded_status=$?
[[ "$degraded_status" == "0" ]]
[[ -f "$target_root/tasks/prd-bridge.md" ]]
[[ -f "$target_root/tasks/tasks-bridge.md" ]]
rg -q "planning documents are ready and unchanged" "$fixture_root/degraded-output"
rg -q "wt herd retry --feature 'bridge' --worktree '$target_root' --branch 'feature/bridge'" "$fixture_root/degraded-output"
rg -q "wt herd doctor" "$fixture_root/degraded-output"

# FR-38: the same guarantee against the real bridge boundary rather than the
# stub above. Pointing HERDR_BIN_PATH at a binary that does not exist is the
# hermetic form of "Herdr is not installed": the helper must classify it as
# herdr_missing, exit 1, and touch nothing in the checkout.
missing_status=0
HERDR_DEVFLOW_USE_SOURCE=1 \
HERDR_BIN_PATH="$fixture_root/no-such-herdr-binary" \
HERDR_DEVFLOW_HOME="$fixture_root/runtime" \
  bash "$repo_root/scripts/herdr-devflow.sh" handoff \
    --feature bridge --worktree "$target_root" --branch feature/bridge \
    > "$fixture_root/missing-output" 2>&1 || missing_status=$?
[[ "$missing_status" == "1" ]]
rg -q "herdr_missing" "$fixture_root/missing-output"
[[ -f "$target_root/tasks/prd-bridge.md" ]]

# FR-35: the durable kill switch is not an error. With [bridge] enabled = false
# the helper reports status: disabled and exits 0, so a wt start on a machine
# that has opted out reads as success rather than a broken handoff.
mkdir -p "$fixture_root/disabled-repo/.herdr"
cat > "$fixture_root/disabled-repo/.herdr/devflow.toml" <<'TOML'
[bridge]
schema_version = 1
enabled = false
min_herdr_version = "0.7.5"
source_id = "ori.devflow"
TOML
disabled_status=0
HERDR_DEVFLOW_USE_SOURCE=1 \
HERDR_BIN_PATH="$fixture_root/no-such-herdr-binary" \
HERDR_DEVFLOW_HOME="$fixture_root/runtime" \
  bash "$repo_root/scripts/herdr-devflow.sh" --repo-root "$fixture_root/disabled-repo" handoff \
    --feature bridge --worktree "$target_root" --branch feature/bridge \
    > "$fixture_root/disabled-output" 2>&1 || disabled_status=$?
[[ "$disabled_status" == "0" ]]
rg -q "disabled" "$fixture_root/disabled-output"

# Local secrets are gitignored, so a fresh worktree has no .env and anything
# needing a key fails in a way that looks like a config bug. The real
# wt_provision_worktree copies the dev worktree's; here we check the summary
# tells the user it will, and only when there is one to copy.
> "$fixture_root/herd-calls"
wt start bridge > "$fixture_root/no-env-output" 2>&1
if rg -q "\.env copied" "$fixture_root/no-env-output"; then
  print -r -- "the plan promised to copy a .env that does not exist" >&2
  exit 1
fi
print -r -- "SECRET=x" > "$dev_root/.env"
wt start bridge > "$fixture_root/env-output" 2>&1
rg -q "\.env copied" "$fixture_root/env-output"

# FR-18: the confirmation summary must name everything that is about to happen,
# so nothing lands that the user did not see first.
> "$fixture_root/herd-calls"
wt start bridge > "$fixture_root/plan-output" 2>&1
rg -q "Feature .*bridge" "$fixture_root/plan-output"
rg -q "Branch .*feature/bridge" "$fixture_root/plan-output"
rg -q "Worktree .*$target_root" "$fixture_root/plan-output"
rg -q "PRD .*prd-bridge.md" "$fixture_root/plan-output"
rg -q "Task list .*tasks-bridge.md" "$fixture_root/plan-output"
rg -q "Agent .*claude" "$fixture_root/plan-output"
rg -q "Herdr tab" "$fixture_root/plan-output"
# Irreversible steps are marked, not buried in prose.
rg -q "new branch" "$fixture_root/plan-output"
rg -q "not undone by declining later" "$fixture_root/plan-output"

# FR-19/FR-20 are one mechanism seen from two sides: the flow only prompts when
# there is a terminal, and declining mutates nothing at all. The suite has no
# terminal, so interactivity is forced and stdin supplied per command.
function wt_plan_is_interactive { return 0 }

function wt_provision_worktree {
  print -r -- "provision" >> "$fixture_root/decline-mutations"
  typeset -g WT_PROVISIONED_TARGET="$target_root"
  return 0
}

# `target` is the summary's read-only lookup and runs before the gate by design,
# so it is answered rather than recorded. Everything else is a mutation and any
# occurrence of one before a yes is a failure.
function wt_herd {
  if [[ "$1" == "target" ]]; then
    print -r -- "ready\tw1\tdemo-workspace"
    return 0
  fi
  print -r -- "$*" >> "$fixture_root/decline-mutations"
  return 0
}

# Declining at the gate: not one Git or Herdr call may be recorded.
wt start bridge <<< "n" > "$fixture_root/declined-output" 2>&1
rg -q "Nothing was changed" "$fixture_root/declined-output"
if [[ -f "$fixture_root/decline-mutations" ]]; then
  print -r -- "declining the confirmation still mutated: $(<"$fixture_root/decline-mutations")" >&2
  exit 1
fi

# Answering anything other than yes is a decline, including an empty line.
wt start bridge <<< "" > /dev/null 2>&1
[[ ! -f "$fixture_root/decline-mutations" ]]

# Accepting runs the whole plan, and the Herdr handoff receives the exact
# feature/path/branch identity the summary showed.
wt start bridge <<< "y" > "$fixture_root/accepted-output" 2>&1
rg -q "provision" "$fixture_root/decline-mutations"
rg -q "handoff --feature bridge --worktree $target_root --branch feature/bridge" "$fixture_root/decline-mutations"

# --yes is the escape hatch for someone who already knows: no prompt, same plan.
rm -f "$fixture_root/decline-mutations"
wt start bridge --yes > "$fixture_root/yes-output" 2>&1
rg -q "provision" "$fixture_root/decline-mutations"
rg -q "Feature .*bridge" "$fixture_root/yes-output"

# FR-21: the existing flags keep working through the new flow, including their
# mutual exclusion, which is still rejected before anything is planned.
rm -f "$fixture_root/decline-mutations"
wt start bridge --no-herdr --yes > /dev/null 2>&1
rg -q "provision" "$fixture_root/decline-mutations"
if rg -q "handoff" "$fixture_root/decline-mutations"; then
  print -r -- "--no-herdr still handed off to Herdr" >&2
  exit 1
fi
rm -f "$fixture_root/decline-mutations"
wt start bridge --kind codex --yes > /dev/null 2>&1
rg -q "handoff --feature bridge --worktree $target_root --branch feature/bridge --kind codex" "$fixture_root/decline-mutations"
rm -f "$fixture_root/decline-mutations"
if wt start bridge --kind codex --no-herdr > /dev/null 2>&1; then
  print -r -- "wt start accepted --kind together with --no-herdr" >&2
  exit 1
fi
[[ ! -f "$fixture_root/decline-mutations" ]]

# FR-15: a PRD with no task list becomes a decision instead of a note that
# scrolls past. Cancelling there is also a decline: nothing may be mutated.
print -r -- "# planless" > "$dev_root/tasks/prd-planless.md"
rm -f "$fixture_root/decline-mutations"
wt start planless <<< "q" > "$fixture_root/planless-output" 2>&1
rg -q "No task list for planless" "$fixture_root/planless-output"
[[ ! -f "$fixture_root/decline-mutations" ]]

# Choosing to generate one writes a starter checklist whose first task is to
# replace it, so the agent's bootstrap prompt has something real to read.
printf 'g\ny\n' | wt start planless > "$fixture_root/generate-output" 2>&1
rg -q "will be created as the agent's first task" "$fixture_root/generate-output"
[[ -f "$target_root/tasks/tasks-planless.md" ]]
rg -q "replace this file with a real task" "$target_root/tasks/tasks-planless.md"
rg -q "^- \[ \] 1\.1 " "$target_root/tasks/tasks-planless.md"

# AR26-AR29: wt start also accepts a feature that has ONLY a task list — no
# PRD at all — the exact shape `wt plan` produces for size:quick/planned
# Issues. A task list still carrying the wt-plan starter marker is refused
# (AR27); a real one is accepted, rendered honestly as task-list-sized
# (AR28), and its Issue snapshot is copied independently of everything else
# (AR29).
print -r -- '# Tasks: onlytasks

<!-- ori-devflow: planning-starter; do not implement until Codex replaces this file -->

- [ ] 1.1 Read the Issue and generate the real tasks.' > "$dev_root/tasks/tasks-onlytasks.md"

rm -f "$fixture_root/decline-mutations"
starter_refused_status=0
wt start onlytasks --yes > "$fixture_root/starter-refused-output" 2>&1 || starter_refused_status=$?
[[ "$starter_refused_status" == "1" ]]
rg -q "still a planning starter" "$fixture_root/starter-refused-output"
[[ ! -f "$fixture_root/decline-mutations" ]]

# Once Codex has replaced the starter with a real, detailed task list, wt
# start accepts it with no PRD at all.
print -r -- '# Tasks: onlytasks

- [ ] 1.1 Real work, no PRD needed.' > "$dev_root/tasks/tasks-onlytasks.md"
print -r -- '# Issue #555: onlytasks

<!-- ori-devflow: issue-snapshot; issue=555 -->

body' > "$dev_root/tasks/issue-onlytasks.md"

rm -f "$fixture_root/decline-mutations"
wt start onlytasks --yes > "$fixture_root/tasklist-only-output" 2>&1
rg -q "PRD .*none \(task-list-sized\)" "$fixture_root/tasklist-only-output"
if rg -q "PRD .*none \(ad-hoc\)" "$fixture_root/tasklist-only-output"; then
  print -r -- "a task-list-only feature was rendered as ad-hoc" >&2
  exit 1
fi
rg -q "Issue snapshot .*issue-onlytasks.md" "$fixture_root/tasklist-only-output"
rg -q "provision" "$fixture_root/decline-mutations"
rg -q "handoff --feature onlytasks --worktree $target_root --branch feature/onlytasks" "$fixture_root/decline-mutations"
[[ -f "$target_root/tasks/tasks-onlytasks.md" ]]
[[ -f "$target_root/tasks/issue-onlytasks.md" ]]
[[ ! -f "$target_root/tasks/prd-onlytasks.md" ]]
rg -q "Real work, no PRD needed" "$target_root/tasks/tasks-onlytasks.md"

# Named resolution accepts "onlytasks", "tasks-onlytasks", and
# "tasks-onlytasks.md" interchangeably — each reaches the same confirmation
# summary instead of "No PRD or task list found". Declining costs nothing,
# so no worktree needs to be re-provisioned between these checks.
for alias_name in tasks-onlytasks tasks-onlytasks.md; do
  alias_output="$fixture_root/alias-output"
  wt start "$alias_name" <<< "n" > "$alias_output" 2>&1
  if rg -q "No PRD or task list found" "$alias_output"; then
    print -r -- "wt start did not resolve the alias '$alias_name'" >&2
    exit 1
  fi
  rg -q "Feature .*onlytasks" "$alias_output"
done

# Guided (bare) wt start lists a task-list-only feature exactly once,
# distinguished from PRD-driven ones, without duplicating it.
wt start <<< "q" > "$fixture_root/guided-listing-output" 2>&1
rg -q "tasks-onlytasks.md" "$fixture_root/guided-listing-output"
rg -q "task-list-sized, no PRD" "$fixture_root/guided-listing-output"
onlytasks_rows="$(rg -c "tasks-onlytasks.md" "$fixture_root/guided-listing-output")"
[[ "$onlytasks_rows" == "1" ]]

# FR-22/23: wt new runs the same flow. The only differences allowed are the
# planning documents and the bootstrap prompt.
rm -f "$fixture_root/decline-mutations"
wt new adhoc --yes > "$fixture_root/new-output" 2>&1
rg -q "PRD .*none \(ad-hoc\)" "$fixture_root/new-output"
rg -q "no prompt" "$fixture_root/new-output"
rg -q "^handoff --feature adhoc --worktree $target_root --branch feature/adhoc --no-prompt$" "$fixture_root/decline-mutations"

# FR-25: creating a worktree records nothing anywhere else. Starting work makes
# no commit and no push — not for an ad-hoc worktree, and not for a planned one.
if rg -q "commit|push" "$fixture_root/decline-mutations"; then
  print -r -- "wt new committed or pushed something: $(<"$fixture_root/decline-mutations")" >&2
  exit 1
fi
# And no planning documents are invented for it.
[[ ! -f "$target_root/tasks/prd-adhoc.md" ]]
[[ ! -f "$target_root/tasks/tasks-adhoc.md" ]]

# FR-24: <type>/<name> still yields that branch, with the directory and feature
# taken from the last segment.
rm -f "$fixture_root/decline-mutations"
wt new fix/adhoc --yes > "$fixture_root/prefix-output" 2>&1
rg -q "Branch .*fix/adhoc" "$fixture_root/prefix-output"
rg -q "^handoff --feature adhoc --worktree $target_root --branch fix/adhoc --no-prompt$" "$fixture_root/decline-mutations"

# FR-34: --no-herdr covers ad-hoc starts too, and still produces the worktree.
rm -f "$fixture_root/decline-mutations"
wt new adhoc --no-herdr --yes > /dev/null 2>&1
rg -q "provision" "$fixture_root/decline-mutations"
if rg -q "handoff" "$fixture_root/decline-mutations"; then
  print -r -- "wt new --no-herdr still handed off to Herdr" >&2
  exit 1
fi
rm -f "$fixture_root/decline-mutations"
if wt new adhoc --kind codex --no-herdr > /dev/null 2>&1; then
  print -r -- "wt new accepted --kind together with --no-herdr" >&2
  exit 1
fi
[[ ! -f "$fixture_root/decline-mutations" ]]

# FR-19 applies to wt new as well: declining creates nothing.
wt new adhoc <<< "n" > /dev/null 2>&1
[[ ! -f "$fixture_root/decline-mutations" ]]

# start and new share one flag parser but not one voice. Each command names
# itself, prints its own usage line, and calls its positional what it calls it —
# the wording is the user-visible surface, so a shared parser must carry it
# rather than flatten it. Every rejection below also has to happen before
# anything is created.
rm -f "$fixture_root/decline-mutations"
typeset -a parser_cases
parser_cases=(
  "start x --kind|wt start --kind requires a Herdr agent kind"
  "start x --kind|Usage: wt start [feature] [--kind KIND] [--no-herdr] [--yes]"
  "start x --kind --nope|wt start --kind requires a Herdr agent kind"
  "start x --kind a --kind b|wt start accepts --kind only once"
  "start --bogus|Unknown wt start option: --bogus"
  "start --bogus|Usage: wt start [feature] [--kind KIND] [--no-herdr] [--yes]"
  "start one two|wt start accepts one PRD/task-list feature name (got: one and two)"
  "start x --kind codex --no-herdr|wt start --kind cannot be combined with --no-herdr"
  "new x --kind|wt new --kind requires a Herdr agent kind"
  "new x --kind|Usage: wt new <name> [--kind KIND] [--no-herdr] [--yes]"
  "new x --kind --nope|wt new --kind requires a Herdr agent kind"
  "new x --kind a --kind b|wt new accepts --kind only once"
  "new --bogus|Unknown wt new option: --bogus"
  "new --bogus|Usage: wt new <name> [--kind KIND] [--no-herdr] [--yes]"
  "new one two|wt new accepts one name (got: one and two)"
  "new x --kind codex --no-herdr|wt new --kind cannot be combined with --no-herdr"
)
for parser_case in "${parser_cases[@]}"; do
  parser_args="${parser_case%%|*}"
  parser_expected="${parser_case#*|}"
  parser_status=0
  wt ${=parser_args} > "$fixture_root/parser-output" 2>&1 < /dev/null || parser_status=$?
  if [[ "$parser_status" == "0" ]]; then
    print -r -- "wt $parser_args was accepted; it must be rejected" >&2
    exit 1
  fi
  if ! rg -qF -- "$parser_expected" "$fixture_root/parser-output"; then
    print -r -- "wt $parser_args did not say '$parser_expected': $(<"$fixture_root/parser-output")" >&2
    exit 1
  fi
done
if [[ -f "$fixture_root/decline-mutations" ]]; then
  print -r -- "a rejected invocation still mutated something" >&2
  exit 1
fi

# A name the bridge could never adopt is rejected before the branch exists,
# rather than after a worktree has been created that handoff will always refuse.
for bad_name in "bad name" "-leading-dash" "has/slash/but//empty"; do
  if wt new "$bad_name" > /dev/null 2>&1; then
    print -r -- "wt new accepted an invalid feature name: $bad_name" >&2
    exit 1
  fi
done
[[ ! -f "$fixture_root/decline-mutations" ]]

# Restore the suite's non-interactive stance and stubs for the sections below.
unfunction wt_plan_is_interactive
function wt_provision_worktree {
  typeset -g WT_PROVISIONED_TARGET="$target_root"
  return 0
}
function wt_herd {
  print -r -- "$*" >> "$fixture_root/herd-calls"
  return 1
}
rm -f "$target_root/tasks/tasks-planless.md" "$fixture_root/decline-mutations"
print -r -- "## Tasks" > "$dev_root/tasks/tasks-bridge.md"

# A blocked Herdr cleanup guard must stop wt done before it archives tasks,
# checks dirty state, or asks Git to remove anything.
print -r -- "# completed bridge tasks" > "$target_root/tasks/tasks-bridge.md"

function wt_resolve_worktree_path {
  print -r -- "$target_root"
}

function wt_resolve_worktree_branch {
  print -r -- "feature/bridge"
}

function wt_is_protected_worktree {
  return 1
}

function wt_herd_cleanup_preflight {
  print -r -- "$*" >> "$fixture_root/cleanup-calls"
  return 20
}

function gh {
  # Records the question and answers with a merged pull-request number. The
  # recording is what proves cleanup asks GitHub nothing else.
  print -r -- "$*" >> "$fixture_root/gh-calls"
  print -r -- "42"
}

function git {
  print -r -- "$*" >> "$fixture_root/git-calls"
  return 0
}

if wt done bridge > "$fixture_root/done-output" 2>&1; then
  print -r -- "Expected blocked Herdr cleanup to stop wt done." >&2
  exit 1
fi
[[ "$(<"$fixture_root/cleanup-calls")" == "$target_root 0" ]]
[[ -f "$target_root/tasks/tasks-bridge.md" ]]
[[ ! -e "$fixture_root/git-calls" ]]
rg -q "Herdr work is still active in this worktree" "$fixture_root/done-output"

# A disconnected/unknown Herdr result also fails closed in this non-interactive
# fixture, even when a caller supplied the explicit override flag.
function wt_herd_cleanup_preflight {
  print -r -- "$*" >> "$fixture_root/cleanup-calls"
  return 21
}

if wt done bridge --herdr-override > "$fixture_root/done-unavailable-output" 2>&1; then
  print -r -- "Expected unverified Herdr cleanup to fail closed without a terminal." >&2
  exit 1
fi
[[ ! -e "$fixture_root/git-calls" ]]
rg -q "cannot be verified in a non-interactive shell" "$fixture_root/done-unavailable-output"

# The blocked paths above prove what wt done refuses to do. This is the other
# half: what it must still do when nothing blocks it. These are the effects the
# user actually asked for, and no later change to backlog bookkeeping may cost
# any of them.
function wt_herd_cleanup_preflight {
  print -r -- "$*" >> "$fixture_root/cleanup-calls"
  return 0
}

rm -f "$fixture_root/git-calls" "$fixture_root/cleanup-calls" "$fixture_root/gh-calls"
print -r -- "# completed bridge tasks" > "$target_root/tasks/tasks-bridge.md"
done_status=0
# One answer, for the "delete the remote branch too?" prompt.
wt done bridge <<< "n" > "$fixture_root/done-success-output" 2>&1 || done_status=$?
[[ "$done_status" == "0" ]]

# The merged pull request is looked up for this exact branch, so cleanup is not
# discarding unmerged work.
rg -q "pr list --head feature/bridge --state merged" "$fixture_root/gh-calls"
# Herdr safety is still consulted, and it ran before anything was removed.
[[ "$(<"$fixture_root/cleanup-calls")" == "$target_root 0" ]]
# The completed task list is archived back to dev, which is the record of what
# was done.
[[ -f "$dev_root/tasks/tasks-bridge.md" ]]
rg -q "completed bridge tasks" "$dev_root/tasks/tasks-bridge.md"
# The worktree and its local branch are removed, exactly and by name.
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"
rg -q "branch -D feature/bridge" "$fixture_root/git-calls"
# Declining the remote-branch prompt leaves the remote branch alone.
if rg -q "push origin --delete" "$fixture_root/git-calls"; then
  print -r -- "wt done deleted a remote branch that was not confirmed" >&2
  exit 1
fi
# dev is brought up to date by rebasing, never by resetting: unpushed dev
# commits belong to whoever made them.
rg -q "rebase origin/dev" "$fixture_root/git-calls"
if rg -q "reset --hard" "$fixture_root/git-calls"; then
  print -r -- "wt done hard-reset the dev worktree" >&2
  exit 1
fi

# Starting and finishing a feature writes nothing to Git except the branch and
# worktree themselves. The old flow pushed a backlog commit at each end, which
# moved dev and left a day-old feature branch behind it. Both ends are checked
# against the same recording, because either one reappearing would bring the
# whole problem back.
for lifecycle_call in "$fixture_root/git-calls" "$fixture_root/decline-mutations"; do
  [[ -e "$lifecycle_call" ]] || continue
  if rg -q "docs\(backlog\)" "$lifecycle_call"; then
    print -r -- "a docs(backlog) commit was created: $(<"$lifecycle_call")" >&2
    exit 1
  fi
  if rg -q "BACKLOG.md" "$lifecycle_call"; then
    print -r -- "a backlog file was touched: $(<"$lifecycle_call")" >&2
    exit 1
  fi
done
if rg -q "commit" "$fixture_root/git-calls"; then
  print -r -- "wt done created a commit: $(<"$fixture_root/git-calls")" >&2
  exit 1
fi
if [[ -e "$dev_root/BACKLOG.md" ]]; then
  print -r -- "the lifecycle recreated a backlog file" >&2
  exit 1
fi

# Nor does either end touch an Issue. Linking delivery to Issue state is a
# contract this version deliberately does not have, so the only thing cleanup
# may ask GitHub is which pull request merged.
rg -q "^pr list --head feature/bridge --state merged" "$fixture_root/gh-calls"
if rg -qv "^pr list " "$fixture_root/gh-calls"; then
  print -r -- "wt done asked GitHub something beyond the merged-PR lookup: $(<"$fixture_root/gh-calls")" >&2
  exit 1
fi

# The whole lifecycle just ran for a feature named `bridge` — a slug with no
# Issue number in it. New work is named `<issue-number>-<slug>` now, but nothing
# renames what already exists, and a legacy identity must keep working end to
# end: planning documents, branch, worktree, handoff, and cleanup.
rg -q "^handoff --feature bridge --worktree $target_root --branch feature/bridge" "$fixture_root/herd-calls"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"
[[ -f "$dev_root/tasks/prd-bridge.md" ]]
[[ -f "$dev_root/tasks/tasks-bridge.md" ]]

# And an issue-number-first slug is just as ordinary: the number is part of the
# name, not a special case the flow has to understand.
> "$fixture_root/herd-calls"
print -r -- "# numbered PRD" > "$dev_root/tasks/prd-292-coordinate-based-map.md"
print -r -- "## Tasks" > "$dev_root/tasks/tasks-292-coordinate-based-map.md"
wt start 292-coordinate-based-map --yes > "$fixture_root/numbered-output" 2>&1
rg -q "Feature .*292-coordinate-based-map" "$fixture_root/numbered-output"
rg -q "Branch .*feature/292-coordinate-based-map" "$fixture_root/numbered-output"
rg -q "^handoff --feature 292-coordinate-based-map --worktree $target_root --branch feature/292-coordinate-based-map" \
  "$fixture_root/herd-calls"
[[ -f "$target_root/tasks/prd-292-coordinate-based-map.md" ]]

# The cleanup addition must not perturb the existing read-only worktree views.
function wt_load_merged_set {
  return 0
}

function wt_load_worktrees {
  typeset -ga WT_PATHS WT_BRANCHES
  WT_PATHS=("$dev_root" "$target_root")
  WT_BRANCHES=("dev" "feature/bridge")
}

function wt_branch_status {
  print -r -- "0 0 [clean]"
}

wt ls > "$fixture_root/list-output"
wt status --worktrees > "$fixture_root/status-output"
rg -q "worktree list" "$fixture_root/git-calls"
rg -q "bridge" "$fixture_root/status-output"

# The Git-only table must survive the dispatcher change unchanged, including
# its header columns, so existing habits and scripts keep working.
rg -q "WORKTREE" "$fixture_root/status-output"
rg -q "AHEAD" "$fixture_root/status-output"
rg -q "Compared against" "$fixture_root/status-output"

# Bare `wt status` is now the feature overview and must delegate to the helper
# rather than rendering the worktree table itself.
function wt_herd {
  print -r -- "$*" >> "$fixture_root/overview-calls"
  return 0
}

wt status > "$fixture_root/overview-output"
[[ "$(<"$fixture_root/overview-calls")" == "feature-overview" ]]
[[ ! -s "$fixture_root/overview-output" ]]

# Every supported option is forwarded as separate words, and a feature slug is
# never concatenated into a single argument or passed through eval.
> "$fixture_root/overview-calls"
wt status --feature downloads-janitor --json --no-color > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "feature-overview --feature downloads-janitor --json --no-color" ]]

> "$fixture_root/overview-calls"
wt status --feature=downloads-janitor > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "feature-overview --feature downloads-janitor" ]]

# The helper's exit status is the command's exit status: an incomplete
# snapshot must not be reported to a script as success.
function wt_herd {
  return 1
}
if wt status > /dev/null 2>&1; then
  print -r -- "wt status swallowed a nonzero helper exit status" >&2
  exit 1
fi

# Invalid combinations are rejected in the shell, before the helper runs.
function wt_herd {
  print -r -- "helper must not run for invalid arguments" >> "$fixture_root/invalid-calls"
  return 0
}

for invalid_args in "--bogus" "--feature" "--worktrees --json"; do
  if wt status ${=invalid_args} > /dev/null 2>&1; then
    print -r -- "wt status accepted invalid arguments: $invalid_args" >&2
    exit 1
  fi
done
[[ ! -f "$fixture_root/invalid-calls" ]]

# Restore the recording helper: the invalid-argument block above replaced it.
function wt_herd {
  print -r -- "$*" >> "$fixture_root/overview-calls"
  return 0
}

# Unusual but valid paths must survive quoting: the dispatcher forwards
# arguments as separate words and never evaluates them.
> "$fixture_root/overview-calls"
wt status --feature "a-b-c" > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "feature-overview --feature a-b-c" ]]

# NO_COLOR and --no-color are both honoured, and neither is swallowed.
> "$fixture_root/overview-calls"
NO_COLOR=1 wt status > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "feature-overview" ]]
> "$fixture_root/overview-calls"
wt status --no-color --watch > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "feature-overview --no-color --watch" ]]

# The exit status distinguishes a usage error (2) from an incomplete
# snapshot (1) from success (0). Scripts branch on these, so each is asserted
# without letting `set -e` abort on the deliberate failures.
function wt_status_exit_code {
  local code=0
  wt status "$@" > /dev/null 2>&1 || code=$?
  print -r -- "$code"
}

function wt_herd { return 0 }
[[ "$(wt_status_exit_code)" == "0" ]]

function wt_herd { return 1 }
[[ "$(wt_status_exit_code)" == "1" ]]

[[ "$(wt_status_exit_code --bogus)" == "2" ]]

# --worktrees keeps working with no helper at all: it is pure Git.
function wt_herd {
  print -r -- "the legacy table must not call the helper" >> "$fixture_root/legacy-calls"
  return 0
}
wt status --worktrees > /dev/null
[[ ! -f "$fixture_root/legacy-calls" ]]

# The REPL dispatches the same picker command as one-shot `wt herd go`; the
# helper keeps stdin/stdout attached so its numbered prompt remains interactive.
function wt_herd {
  print -r -- "$*" >> "$fixture_root/repl-herd-calls"
  return 0
}
printf 'herd go\nq\n' | wt repl > /dev/null
[[ "$(<"$fixture_root/repl-herd-calls")" == "go" ]]

# FR-36: the Git-and-GitHub half of wt must stay Herdr-free. Adding tabs and
# routing wt new through the shared flow must not quietly make Herdr a
# prerequisite for reading, navigating, or shipping.
function wt_herd {
  print -r -- "$*" >> "$fixture_root/herdr-free-calls"
  return 0
}
wt ls > /dev/null
wt help > /dev/null
[[ ! -f "$fixture_root/herdr-free-calls" ]]

# --- wt plan --issue <N> [--yes] -------------------------------------------
#
# Shell-wiring layer: argument validation and the exact call handed to the
# bridge, with wt_herd stubbed so no Go binary or GitHub read is needed. AR1:
# a malformed, duplicate, missing, or unknown argument must fail before the
# bridge is ever reached.
function wt_get_dev_worktree { print -r -- "$dev_root" }
function wt_herd {
  print -r -- "$*" >> "$fixture_root/plan-calls"
  return 0
}

rm -f "$fixture_root/plan-calls"
plan_status=0
for bad_plan_args in "" "--issue" "--issue 0" "--issue -5" "--issue abc" \
                     "--issue 1 --issue 2" "--issue 1 --bogus" "--yes"; do
  plan_status=0
  wt plan ${=bad_plan_args} > /dev/null 2>&1 || plan_status=$?
  if [[ "$plan_status" != "1" ]]; then
    print -r -- "wt plan '$bad_plan_args' exited $plan_status, want 1" >&2
    exit 1
  fi
done
if [[ -f "$fixture_root/plan-calls" ]]; then
  print -r -- "a rejected wt plan invocation still reached the bridge: $(<"$fixture_root/plan-calls")" >&2
  exit 1
fi

# A valid invocation resolves the dev worktree via the same lookup wt start
# uses, and forwards exactly one call with arguments as separate words —
# never through eval, so a title or label containing shell metacharacters is
# data the helper reads, not syntax this shell runs.
wt plan --issue 342 > /dev/null
[[ "$(<"$fixture_root/plan-calls")" == "issue-plan --issue 342 --worktree $dev_root" ]]

rm -f "$fixture_root/plan-calls"
wt plan --issue 342 --yes > /dev/null
[[ "$(<"$fixture_root/plan-calls")" == "issue-plan --issue 342 --worktree $dev_root --yes" ]]

rm -f "$fixture_root/plan-calls"
wt help > "$fixture_root/plan-help-output" 2>&1
rg -q -- "wt plan --issue" "$fixture_root/plan-help-output"
[[ ! -f "$fixture_root/plan-calls" ]]

# --- wt plan against the real bridge ----------------------------------------
#
# A fully disposable repository, separate from this checkout: the helper's
# worktree validation requires --worktree to be a linked worktree of the same
# repository as --repo-root, and this checkout's own "dev" branch is already
# checked out elsewhere on this machine, so a second checkout of literal
# branch "dev" has to live in its own repository entirely. The Go binary is
# invoked directly (the same --repo-root override the pre-existing FR-38/
# disabled-bridge tests above already use), bypassing wt plan's shell layer,
# which the block above already proved constructs the right call.
# An earlier wt done fixture shadows `git` with a call-recording stub for the
# rest of this script (see the `function git` block above); restore the real
# binary before creating a genuine repository below.
unfunction git 2>/dev/null || true

issue_repo="$fixture_root/issue-plan-repo"
issue_dev="$fixture_root/issue-plan-dev"
mkdir -p "$issue_repo"
git init -q -b main "$issue_repo"
print -r -- "fixture" > "$issue_repo/README.md"
git -C "$issue_repo" add README.md
git -C "$issue_repo" -c user.name="Ori Test" -c user.email="ori@example.test" commit -q -m fixture
git -C "$issue_repo" worktree add -q -b dev "$issue_dev" main
mkdir -p "$issue_repo/.herdr"
cp "$repo_root/.herdr/devflow.toml" "$issue_repo/.herdr/devflow.toml"

# The hostile Issue body lives in its own file rather than embedded in the
# fake gh script below: this heredoc is quoted, so the backticks, `$(...)`,
# quotes, and tab it contains are written to disk completely inert, with no
# shell escaping to get wrong at any layer.
fake_gh_body_file="$fixture_root/fake-gh-body.txt"
cat > "$fake_gh_body_file" <<'HOSTILE_BODY'
line one
line two with `code` and $(rm -rf /) and "quotes" and --leading-dash
	tabbed line
HOSTILE_BODY

issue_gh_bin="$fixture_root/issue-plan-bin"
mkdir -p "$issue_gh_bin"
cat > "$issue_gh_bin/gh" <<'FAKE_GH'
#!/usr/bin/env bash
# Fake gh for the wt plan end-to-end fixture: answers exactly the one
# `gh issue view <n> --json ...` invocation issue-plan makes. Every field is
# built from plain, unquoted environment values and escaped for JSON here,
# so the outer fixture never has to hand-embed escaped JSON literals.
json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\t'/\\t}"
  s="${s//$'\r'/\\r}"
  printf '%s' "$s"
}
if [[ "$1" == "issue" && "$2" == "view" ]]; then
  number="$3"
  body_raw="line one"
  if [[ -n "${FAKE_GH_BODY_FILE:-}" && -f "$FAKE_GH_BODY_FILE" ]]; then
    body_raw="$(cat "$FAKE_GH_BODY_FILE")"
  fi
  title="$(json_escape "${FAKE_GH_TITLE:-Ready issue codex planning}")"
  body="$(json_escape "$body_raw")"
  state="${FAKE_GH_STATE:-OPEN}"

  labels_json=""
  old_ifs="$IFS"
  IFS=','
  for label in ${FAKE_GH_LABELS:-backlog,size:planned}; do
    [[ -n "$label" ]] || continue
    [[ -n "$labels_json" ]] && labels_json="$labels_json,"
    labels_json="$labels_json{\"name\":\"$(json_escape "$label")\"}"
  done
  IFS="$old_ifs"

  comments_json=""
  if [[ "${FAKE_GH_NO_COMMENTS:-}" != "1" ]]; then
    comment_body="a hostile comment with \`code\`"
    comments_json="{\"author\":{\"login\":\"johnjallday\"},\"body\":\"$(json_escape "$comment_body")\",\"createdAt\":\"2026-08-12T14:09:12Z\"}"
  fi

  printf '{"number":%s,"title":"%s","body":"%s","url":"https://example.invalid/issues/%s","state":"%s","labels":[%s],"comments":[%s]}\n' \
    "$number" "$title" "$body" "$number" "$state" "$labels_json" "$comments_json"
  exit 0
fi
echo "fake gh: unhandled invocation: $*" >&2
exit 1
FAKE_GH
chmod +x "$issue_gh_bin/gh"

function issue_plan_direct {
  local issue="$1" extra_arg="${2:-}"
  local -a call
  call=(--repo-root "$issue_repo" issue-plan --issue "$issue" --worktree "$issue_dev" --yes)
  [[ -n "$extra_arg" ]] && call+=("$extra_arg")
  HERDR_DEVFLOW_USE_SOURCE=1 \
  PATH="$issue_gh_bin:$PATH" \
  HERDR_DEVFLOW_HOME="$fixture_root/issue-plan-runtime" \
  HERDR_BIN_PATH="$fixture_root/no-such-herdr" \
  FAKE_GH_BODY_FILE="$fake_gh_body_file" \
    bash "$repo_root/scripts/herdr-devflow.sh" "${call[@]}"
}

# Happy path: one fresh read, eligibility passes, the snapshot and starter
# checklist are written atomically, hostile Issue content survives verbatim
# as inert text, the starter's first item requests parent tasks and "Go",
# and — since no Herdr is reachable here — the command still exits 0 with the
# planning files intact (AR8-AR13, AR22-AR24).
happy_status=0
issue_plan_direct 934 > "$fixture_root/issue-plan-happy" 2>&1 || happy_status=$?
if [[ "$happy_status" != "0" ]]; then
  print -r -- "issue-plan happy path exited $happy_status: $(<"$fixture_root/issue-plan-happy")" >&2
  exit 1
fi
rg -q "Herdr executable was not found" "$fixture_root/issue-plan-happy"
snapshot_path="$issue_dev/tasks/issue-934-ready-issue-codex-planning.md"
starter_path="$issue_dev/tasks/tasks-934-ready-issue-codex-planning.md"
[[ -f "$snapshot_path" ]]
[[ -f "$starter_path" ]]
rg -q "ori-devflow: issue-snapshot; issue=934" "$snapshot_path"
rg -Fq '`code`' "$snapshot_path"
rg -Fq '$(rm -rf /)' "$snapshot_path"
rg -Fq -- '--leading-dash' "$snapshot_path"
rg -q "ori-devflow: planning-starter" "$starter_path"
rg -q '1\.1 Read `AGENTS.md`' "$starter_path"
rg -q 'Wait for "Go"' "$starter_path"
rg -q 'Do not start implementing' "$starter_path"

# Idempotent rerun: neither file's content changes, and the command still
# reports readiness rather than erroring on an already-planned Issue.
before_snapshot_sum="$(shasum -a 256 "$snapshot_path")"
before_starter_sum="$(shasum -a 256 "$starter_path")"
rerun_status=0
issue_plan_direct 934 > "$fixture_root/issue-plan-rerun" 2>&1 || rerun_status=$?
[[ "$rerun_status" == "0" ]]
rg -q "resumed" "$fixture_root/issue-plan-rerun"
[[ "$(shasum -a 256 "$snapshot_path")" == "$before_snapshot_sum" ]]
[[ "$(shasum -a 256 "$starter_path")" == "$before_starter_sum" ]]

# Ineligible Issues fail closed and create nothing: closed, approved,
# missing size, and duplicate size, each its own Issue number so a rejected
# case can never be confused with the happy path's artifacts.
ineligible_status=0
FAKE_GH_STATE="CLOSED" issue_plan_direct 101 > "$fixture_root/issue-plan-closed" 2>&1 || ineligible_status=$?
[[ "$ineligible_status" == "1" ]]
[[ ! -e "$issue_dev/tasks/issue-101-ready-issue-codex-planning.md" ]]

ineligible_status=0
FAKE_GH_LABELS="backlog,approved,size:planned" \
  issue_plan_direct 102 > "$fixture_root/issue-plan-approved" 2>&1 || ineligible_status=$?
[[ "$ineligible_status" == "1" ]]
[[ ! -e "$issue_dev/tasks/issue-102-ready-issue-codex-planning.md" ]]

ineligible_status=0
FAKE_GH_LABELS="backlog" \
  issue_plan_direct 103 > "$fixture_root/issue-plan-no-size" 2>&1 || ineligible_status=$?
[[ "$ineligible_status" == "1" ]]
[[ ! -e "$issue_dev/tasks/issue-103-ready-issue-codex-planning.md" ]]

ineligible_status=0
FAKE_GH_LABELS="backlog,size:quick,size:prd" \
  issue_plan_direct 104 > "$fixture_root/issue-plan-dup-size" 2>&1 || ineligible_status=$?
[[ "$ineligible_status" == "1" ]]
[[ ! -e "$issue_dev/tasks/issue-104-ready-issue-codex-planning.md" ]]
if [[ -e "$issue_dev/tasks" ]] && [[ "$(ls "$issue_dev/tasks" | wc -l | tr -d ' ')" != "2" ]]; then
  print -r -- "an ineligible Issue left files behind: $(ls "$issue_dev/tasks")" >&2
  exit 1
fi

# size:prd routes to a PRD-first starter that asks clarifying questions
# before generating tasks, rather than the tasks-first starter above.
FAKE_GH_LABELS="backlog,size:prd" issue_plan_direct 105 > /dev/null
prd_starter="$issue_dev/tasks/tasks-105-ready-issue-codex-planning.md"
[[ -f "$prd_starter" ]]
rg -q 'Ask only the 3-5' "$prd_starter"
rg -q 'tasks/prd-105-ready-issue-codex-planning.md' "$prd_starter"

# The remaining Herdr-free commands mutate Git or GitHub, so they are asserted
# structurally instead of by running them: no dispatcher branch below may
# reach for the bridge.
function wt_case_branch_body {
  awk -v want="  $1)" '
    $0 == want { inside = 1; next }
    inside && $0 == "    ;;" { inside = 0 }
    inside { print }
  ' "$repo_root/scripts/wt.sh"
}
for herdr_free_command in pr merge demo backlog cd ls; do
  body="$(wt_case_branch_body "$herdr_free_command")"
  if [[ -z "$body" ]]; then
    print -r -- "could not read the '$herdr_free_command' dispatcher branch" >&2
    exit 1
  fi
  if print -r -- "$body" | rg -q "wt_herd"; then
    print -r -- "wt $herdr_free_command reaches for the Herdr bridge; it must stay Herdr-free" >&2
    exit 1
  fi
done

# Reading GitHub Issues must never need a running Herdr. The one DevOps
# entrypoint calls `gh` directly and the former helper bootstrap is gone.
devops_entrypoint="$repo_root/scripts/devops.sh"
if [[ ! -x "$devops_entrypoint" ]]; then
  print -r -- "scripts/devops.sh is missing or not executable" >&2
  exit 1
fi
if ! rg -q 'gh "\$\{args\[@\]\}"' "$devops_entrypoint"; then
  print -r -- "scripts/devops.sh no longer invokes gh directly" >&2
  exit 1
fi
if rg -q 'wt_herd|herdr-devflow|devflow_exec|devflow-bootstrap' "$devops_entrypoint"; then
  print -r -- "scripts/devops.sh reaches for the Herdr bridge" >&2
  exit 1
fi
if [[ -e "$repo_root/scripts/lib/devflow-bootstrap.sh" ]]; then
  print -r -- "the retired DevOps-to-Herdr bootstrap still exists" >&2
  exit 1
fi
for retired_entrypoint in issue backlog ready; do
  if [[ -e "$repo_root/scripts/devops/$retired_entrypoint.sh" ]]; then
    print -r -- "the retired scripts/devops/$retired_entrypoint.sh still exists" >&2
    exit 1
  fi
done

# --no-herdr is the supported per-invocation escape hatch (FR-33), so it has to
# be discoverable from wt help rather than only from the source.
wt help > "$fixture_root/help-output" 2>&1
rg -q -- "--no-herdr" "$fixture_root/help-output"
rg -q "bare Git worktree" "$fixture_root/help-output"

print -r -- "wt-herd.test.sh: ok"
