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

# Optional per-feature kind/model overrides are forwarded to the initial
# handoff. Omitted values stay omitted so the Go helper owns configured
# defaults and retry persistence.
> "$fixture_root/herd-calls"
wt start bridge --kind codex >/dev/null
rg -q "^handoff --feature bridge --worktree $target_root --branch feature/bridge --kind codex$" "$fixture_root/herd-calls"

cat > "$fixture_root/model-defaults.toml" <<'TOML'
[primary]
kind = "pi"
model = "openai/configured"
[roles]
TOML
> "$fixture_root/herd-calls"
HERDR_DEVFLOW_CONFIG="$fixture_root/model-defaults.toml" wt start bridge > "$fixture_root/configured-model-output" 2>&1
rg -q "Agent .*pi" "$fixture_root/configured-model-output"
rg -q "Model .*openai/configured" "$fixture_root/configured-model-output"
rg -q "^handoff --feature bridge --worktree $target_root --branch feature/bridge$" "$fixture_root/herd-calls"

> "$fixture_root/herd-calls"
HERDR_DEVFLOW_CONFIG="$fixture_root/model-defaults.toml" wt start bridge --model '[openai] gpt 5.1' > "$fixture_root/model-only-output" 2>&1
rg -q "Agent .*pi" "$fixture_root/model-only-output"
rg -q "Model .*\[openai\] gpt 5.1" "$fixture_root/model-only-output"
rg -q "^handoff --feature bridge --worktree $target_root --branch feature/bridge --model \[openai\] gpt 5.1$" "$fixture_root/herd-calls"

> "$fixture_root/herd-calls"
HERDR_DEVFLOW_CONFIG="$fixture_root/model-defaults.toml" wt start bridge --kind claude > "$fixture_root/changed-kind-output" 2>&1
rg -q "Agent .*claude" "$fixture_root/changed-kind-output"
rg -q "Model .*integration default" "$fixture_root/changed-kind-output"
rg -q "^handoff --feature bridge --worktree $target_root --branch feature/bridge --kind claude$" "$fixture_root/herd-calls"
if rg -q -- '--model' "$fixture_root/herd-calls"; then
  print -r -- "changed kind inherited the configured model" >&2
  exit 1
fi

> "$fixture_root/herd-calls"
HERDR_DEVFLOW_CONFIG="$fixture_root/model-defaults.toml" wt new scratch-model --kind pi --model 'openai/new model' > "$fixture_root/new-model-output" 2>&1
rg -q "Model .*openai/new model" "$fixture_root/new-model-output"
rg -q "^handoff --feature scratch-model --worktree $target_root --branch feature/scratch-model --kind pi --model openai/new model --no-prompt$" "$fixture_root/herd-calls"

# Record one bracketed field per argument to prove a model with spaces and shell
# syntax remains one zsh argv value rather than command text.
function wt_herd {
  local argument
  : > "$fixture_root/exact-handoff-argv"
  for argument in "$@"; do
    print -r -- "<$argument>" >> "$fixture_root/exact-handoff-argv"
  done
  return 1
}
wt start bridge --kind pi --model '[openai] x; $(touch nope)' >/dev/null
rg -Fxq '<--model>' "$fixture_root/exact-handoff-argv"
rg -Fxq '<[openai] x; $(touch nope)>' "$fixture_root/exact-handoff-argv"
[[ ! -e "$fixture_root/nope" ]]
# Restore the ordinary transcript helper for the rest of the suite.
function wt_herd {
  print -r -- "$*" >> "$fixture_root/herd-calls"
  return 1
}

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

# Declining at the gate: not one Git or Herdr call may be recorded, even when
# an exact model override was already parsed and previewed.
wt start bridge --model '[openai] decline model' <<< "n" > "$fixture_root/declined-output" 2>&1
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
if wt start bridge --model openai/model --no-herdr > /dev/null 2>&1; then
  print -r -- "wt start accepted --model together with --no-herdr" >&2
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

<!-- ori-devflow: planning-starter; do not implement until the planner replaces this file -->

- [ ] 1.1 Read the Issue and generate the real tasks.' > "$dev_root/tasks/tasks-onlytasks.md"

rm -f "$fixture_root/decline-mutations"
starter_refused_status=0
wt start onlytasks --yes > "$fixture_root/starter-refused-output" 2>&1 || starter_refused_status=$?
[[ "$starter_refused_status" == "1" ]]
rg -q "still a planning starter" "$fixture_root/starter-refused-output"
[[ ! -f "$fixture_root/decline-mutations" ]]

# Once Pi has replaced the starter with a real, detailed task list, wt
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

# A completed bundle uses the same feature-slug copy path: its trusted combined
# snapshot, effective-route PRD, and one detailed task list all move together,
# with no GitHub read or mutation during wt start.
bundle_start_feature="801-802-camera-workflow"
cat > "$dev_root/tasks/issue-$bundle_start_feature.md" <<'MD'
# Issue bundle: #801, #802

<!-- ori-devflow: issue-bundle-snapshot; issues=801,802 -->
MD
print -r -- "# PRD: $bundle_start_feature" > "$dev_root/tasks/prd-$bundle_start_feature.md"
print -r -- "## Tasks\n- [ ] 1.0 Shared implementation" > "$dev_root/tasks/tasks-$bundle_start_feature.md"
function gh {
  print -r -- "$*" >> "$fixture_root/bundle-start-gh-calls"
  return 91
}
wt start "$bundle_start_feature" --no-herdr --yes > "$fixture_root/bundle-start-output" 2>&1
[[ ! -f "$fixture_root/bundle-start-gh-calls" ]]
[[ -f "$target_root/tasks/issue-$bundle_start_feature.md" ]]
[[ -f "$target_root/tasks/prd-$bundle_start_feature.md" ]]
[[ -f "$target_root/tasks/tasks-$bundle_start_feature.md" ]]
rg -q 'issue-bundle-snapshot; issues=801,802' "$target_root/tasks/issue-$bundle_start_feature.md"
unfunction gh

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
if wt new adhoc --model openai/model --no-herdr > /dev/null 2>&1; then
  print -r -- "wt new accepted --model together with --no-herdr" >&2
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
  "start x --kind|Usage: wt start [feature] [--kind KIND] [--model MODEL] [--no-herdr] [--yes]"
  "start x --kind --nope|wt start --kind requires a Herdr agent kind"
  "start x --kind a --kind b|wt start accepts --kind only once"
  "start x --model|wt start --model requires one non-empty model value"
  "start x --model a --model b|wt start accepts --model only once"
  "start --bogus|Unknown wt start option: --bogus"
  "start --bogus|Usage: wt start [feature] [--kind KIND] [--model MODEL] [--no-herdr] [--yes]"
  "start one two|wt start accepts one PRD/task-list feature name (got: one and two)"
  "start x --kind codex --no-herdr|wt start --kind/--model cannot be combined with --no-herdr"
  "start x --model openai/model --no-herdr|wt start --kind/--model cannot be combined with --no-herdr"
  "new x --kind|wt new --kind requires a Herdr agent kind"
  "new x --kind|Usage: wt new <name> [--kind KIND] [--model MODEL] [--no-herdr] [--yes]"
  "new x --kind --nope|wt new --kind requires a Herdr agent kind"
  "new x --kind a --kind b|wt new accepts --kind only once"
  "new x --model|wt new --model requires one non-empty model value"
  "new x --model a --model b|wt new accepts --model only once"
  "new --bogus|Unknown wt new option: --bogus"
  "new --bogus|Usage: wt new <name> [--kind KIND] [--model MODEL] [--no-herdr] [--yes]"
  "new one two|wt new accepts one name (got: one and two)"
  "new x --kind codex --no-herdr|wt new --kind/--model cannot be combined with --no-herdr"
  "new x --model openai/model --no-herdr|wt new --kind/--model cannot be combined with --no-herdr"
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
  # unnumbered fixture has no Issue snapshot, so it must need no Issue calls.
  print -r -- "$*" >> "$fixture_root/gh-calls"
  print -r -- "42"
}

typeset -g FAKE_GIT_LOG_BODY=""
function git {
  print -r -- "$*" >> "$fixture_root/git-calls"
  if [[ "$1" == "-C" && "$3" == "log" && -n "$FAKE_GIT_LOG_BODY" ]]; then
    print -r -- "$FAKE_GIT_LOG_BODY"
  fi
  return 0
}

# wt pr reads the same trusted attachment marker as wt done. Bundles append one
# closing reference per exact member while retaining --fill; ad-hoc and legacy
# single-Issue PRs keep their previous argument shape.
pr_bundle_feature="801-802-camera-workflow"
cat > "$target_root/tasks/issue-$pr_bundle_feature.md" <<'MD'
# Issue bundle: #801, #802

<!-- ori-devflow: issue-bundle-snapshot; issues=801,802 -->

Closes #999 in untrusted body text must stay inert.
MD
FAKE_GIT_LOG_BODY="Existing --fill commit body."
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt pr "$pr_bundle_feature" > "$fixture_root/pr-bundle-output" 2>&1
rg -q "push -u origin feature/bridge" "$fixture_root/git-calls"
rg -q -- "pr create --base dev --head feature/bridge --fill --body" "$fixture_root/gh-calls"
rg -q -- '--body Existing --fill commit body\.$' "$fixture_root/gh-calls"
[[ "$(rg -c '^Closes #801$' "$fixture_root/gh-calls")" == "1" ]]
[[ "$(rg -c '^Closes #802$' "$fixture_root/gh-calls")" == "1" ]]
if rg -q 'Closes #999' "$fixture_root/gh-calls"; then
  print -r -- "wt pr trusted closing text from the Issue body" >&2
  exit 1
fi

FAKE_GIT_LOG_BODY=""
rm -f "$target_root/tasks/issue-$pr_bundle_feature.md" "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt pr bridge > /dev/null
rg -q '^pr create --base dev --head feature/bridge --fill$' "$fixture_root/gh-calls"
if rg -q -- '--body' "$fixture_root/gh-calls"; then
  print -r -- "ad-hoc wt pr unexpectedly generated a body" >&2
  exit 1
fi

pr_single_feature="292-coordinate-based-map"
cat > "$target_root/tasks/issue-$pr_single_feature.md" <<'MD'
# Issue #292: Coordinate based map

<!-- ori-devflow: issue-snapshot; issue=292 -->
MD
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt pr "$pr_single_feature" > /dev/null
rg -q '^pr create --base dev --head feature/bridge --fill$' "$fixture_root/gh-calls"
if rg -q -- '--body' "$fixture_root/gh-calls"; then
  print -r -- "single-Issue wt pr changed its legacy --fill argument shape" >&2
  exit 1
fi

cat > "$target_root/tasks/issue-$pr_bundle_feature.md" <<'MD'
# Issue bundle

<!-- ori-devflow: issue-bundle-snapshot; issues=802,801 -->
MD
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
pr_failure_status=0
wt pr "$pr_bundle_feature" > "$fixture_root/pr-malformed-output" 2>&1 || pr_failure_status=$?
[[ "$pr_failure_status" == "1" ]]
[[ ! -e "$fixture_root/git-calls" && ! -e "$fixture_root/gh-calls" ]]
rg -q 'no valid generated marker on line 3' "$fixture_root/pr-malformed-output"

pr_prefix_conflict="801-802-999-camera"
cat > "$target_root/tasks/issue-$pr_prefix_conflict.md" <<'MD'
# Issue bundle

<!-- ori-devflow: issue-bundle-snapshot; issues=801,802 -->
MD
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
pr_failure_status=0
wt pr "$pr_prefix_conflict" > "$fixture_root/pr-prefix-conflict-output" 2>&1 || pr_failure_status=$?
[[ "$pr_failure_status" == "1" ]]
[[ ! -e "$fixture_root/git-calls" && ! -e "$fixture_root/gh-calls" ]]
rg -q "attached Issues 801,802 do not match feature '$pr_prefix_conflict'" "$fixture_root/pr-prefix-conflict-output"
rm -f "$target_root/tasks/issue-$pr_prefix_conflict.md"

rm -f "$target_root/tasks/issue-$pr_bundle_feature.md" "$target_root/tasks/issue-$pr_single_feature.md"

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
rg -q "pr list --head feature/bridge --base dev --state merged" "$fixture_root/gh-calls"
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

# An ad-hoc/legacy feature has no exact Issue snapshot, so it remains entirely
# outside the Issue lifecycle.
rg -q "^pr list --head feature/bridge --base dev --state merged" "$fixture_root/gh-calls"
if rg -q "^issue " "$fixture_root/gh-calls"; then
  print -r -- "wt done inferred an Issue without an attachment: $(<"$fixture_root/gh-calls")" >&2
  exit 1
fi

# Issue-backed cleanup uses the generated snapshot header as the sole explicit
# attachment, closes the primary Issue after a confirmed merged PR, and records
# the PR that delivered it.
issue_feature="292-coordinate-based-map"
issue_branch="feature/$issue_feature"
issue_snapshot="$target_root/tasks/issue-$issue_feature.md"
cat > "$issue_snapshot" <<'ISSUE'
# Issue #292: Coordinate based map

<!-- ori-devflow: issue-snapshot; issue=292 -->

## Body
Requirements supplied by the Issue.
ISSUE
print -r -- "# completed numbered tasks" > "$target_root/tasks/tasks-$issue_feature.md"

function wt_resolve_worktree_branch {
  print -r -- "$issue_branch"
}

typeset -g FAKE_MERGED_PR="77" FAKE_PR_STATUS=0 FAKE_ISSUE_STATE="OPEN"
typeset -g FAKE_ISSUE_VIEW_STATUS=0 FAKE_ISSUE_CLOSE_STATUS=0
typeset -g FAKE_PR_BODY="" FAKE_PR_VIEW_STATUS=0
typeset -gA FAKE_ISSUE_STATE_BY_NUM
typeset -ga FAKE_ISSUE_CLOSE_FAIL_NUMS
FAKE_ISSUE_CLOSE_FAIL_NUMS=()
function gh {
  print -r -- "$*" >> "$fixture_root/gh-calls"
  case "$1 $2" in
    "pr list")
      (( FAKE_PR_STATUS == 0 )) || return "$FAKE_PR_STATUS"
      print -r -- "$FAKE_MERGED_PR"
      ;;
    "pr view")
      # The (untrusted) merged-PR body wt_done_close_secondary_issues parses
      # for Closes/Fixes/Resolves references. Empty by default, so every
      # existing single-Issue lifecycle case below closes nothing extra.
      (( FAKE_PR_VIEW_STATUS == 0 )) || return "$FAKE_PR_VIEW_STATUS"
      print -r -- "$FAKE_PR_BODY"
      ;;
    "issue view")
      local num="$3"
      (( FAKE_ISSUE_VIEW_STATUS == 0 )) || return "$FAKE_ISSUE_VIEW_STATUS"
      print -r -- "${FAKE_ISSUE_STATE_BY_NUM[$num]:-$FAKE_ISSUE_STATE}"
      ;;
    "issue close")
      local num="$3"
      (( ${FAKE_ISSUE_CLOSE_FAIL_NUMS[(Ie)$num]} == 0 )) || return 1
      return "$FAKE_ISSUE_CLOSE_STATUS"
      ;;
    *)
      print -r -- "unexpected fake gh call: $*" >&2
      return 99
      ;;
  esac
}

rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$issue_feature" <<< "n" > "$fixture_root/done-issue-open-output" 2>&1
rg -q "^pr list --head $issue_branch --base dev --state merged" "$fixture_root/gh-calls"
rg -q "^issue view 292 --json state --jq .state$" "$fixture_root/gh-calls"
rg -qF "issue close 292 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
rg -q "Closed attached Issue #292" "$fixture_root/done-issue-open-output"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"

# Rerunning after a partial cleanup, or closing manually first, is idempotent:
# CLOSED is accepted without another close write.
FAKE_ISSUE_STATE="CLOSED"
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$issue_feature" <<< "n" > "$fixture_root/done-issue-closed-output" 2>&1
rg -q "issue view 292" "$fixture_root/gh-calls"
if rg -q "^issue close " "$fixture_root/gh-calls"; then
  print -r -- "wt done tried to close an already-closed Issue" >&2
  exit 1
fi
rg -q "already closed; leaving it unchanged" "$fixture_root/done-issue-closed-output"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"

# #378: after the primary Issue closes, wt done additionally closes every
# OPEN Issue the confirmed merged PR body names with Closes/Fixes/Resolves —
# mixed case, duplicated, and with the primary itself repeated in the body
# (which must never be closed a second time as a "secondary").
FAKE_ISSUE_STATE="OPEN"
FAKE_ISSUE_STATE_BY_NUM=(999 OPEN 1000 OPEN)
FAKE_PR_BODY="Closes #999. Also FIXES #1000 and resolves #1000 again. Resolves #292 too."
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$issue_feature" <<< "n" > "$fixture_root/done-secondary-mixed-output" 2>&1
rg -q "^pr view 77 --json body --jq .body$" "$fixture_root/gh-calls"
rg -qF "issue close 292 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
rg -qF "issue close 999 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
rg -qF "issue close 1000 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
[[ "$(rg -c '^issue close 1000 ' "$fixture_root/gh-calls")" == "1" ]]
[[ "$(rg -c '^issue close 292 ' "$fixture_root/gh-calls")" == "1" ]]
rg -q "Closed secondary Issue #999" "$fixture_root/done-secondary-mixed-output"
rg -q "Closed secondary Issue #1000" "$fixture_root/done-secondary-mixed-output"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"

# A secondary Issue that is already CLOSED is left unchanged, same as the
# primary; a still-OPEN secondary in the same PR body still closes.
FAKE_ISSUE_STATE_BY_NUM=(999 CLOSED 1000 OPEN)
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$issue_feature" <<< "n" > "$fixture_root/done-secondary-mixed-states-output" 2>&1
if rg -q "^issue close 999 " "$fixture_root/gh-calls"; then
  print -r -- "wt done tried to close an already-closed secondary Issue" >&2
  exit 1
fi
rg -qF "issue close 1000 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"

# A merged PR body with no closing references closes nothing beyond the
# primary — the additive contract never invents Issue authority.
FAKE_PR_BODY="Nothing to see here."
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$issue_feature" <<< "n" > "$fixture_root/done-secondary-none-output" 2>&1
[[ "$(rg -c '^issue (view|close) 292 ' "$fixture_root/gh-calls")" == "2" ]]
if rg -q '^issue (view|close) (999|1000) ' "$fixture_root/gh-calls"; then
  print -r -- "wt done invented a secondary Issue with no references in the PR body" >&2
  exit 1
fi
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"

# A malicious-looking PR body is parsed with a fixed regex only, never
# evaluated: shell metacharacters and command substitutions inside it must
# never execute, and only genuine #N references are extracted.
FAKE_ISSUE_STATE_BY_NUM=(999 OPEN 1000 OPEN)
FAKE_PR_BODY='Closes #999; $(rm -rf /); `id`; resolves #1000 && echo pwned'
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls" "$fixture_root/pwned"
wt done "$issue_feature" <<< "n" > "$fixture_root/done-secondary-hostile-output" 2>&1
[[ ! -e "$fixture_root/pwned" ]]
rg -qF "issue close 999 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
rg -qF "issue close 1000 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"

# A PR-body read failure is a visible nonfatal warning: it never undoes the
# already-successful primary close, and cleanup still proceeds.
FAKE_PR_VIEW_STATUS=5
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$issue_feature" <<< "n" > "$fixture_root/done-secondary-pr-body-failed-output" 2>&1
rg -qF "issue close 292 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
rg -q "Could not read PR #77 body; skipping any secondary Issues it may mention" "$fixture_root/done-secondary-pr-body-failed-output"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"
FAKE_PR_VIEW_STATUS=0

# A secondary Issue's own state/close failure is fatal, preserving the
# worktree for retry exactly like a primary failure would.
FAKE_ISSUE_STATE_BY_NUM=(999 OPEN)
FAKE_PR_BODY="Closes #999."
FAKE_ISSUE_CLOSE_FAIL_NUMS=(999)
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
issue_failure_status=0
wt done "$issue_feature" > "$fixture_root/done-secondary-close-failed-output" 2>&1 || issue_failure_status=$?
[[ "$issue_failure_status" == "1" ]]
if rg -q "worktree remove" "$fixture_root/git-calls"; then
  print -r -- "wt done removed the worktree after a secondary Issue close failed" >&2
  exit 1
fi
rg -qF "issue close 292 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
rg -q "Could not close secondary Issue #999; worktree preserved" "$fixture_root/done-secondary-close-failed-output"
FAKE_ISSUE_CLOSE_FAIL_NUMS=()

# Bundle cleanup treats every trusted member as attached, deduplicates those
# numbers from PR-body references, and preserves the worktree after a partial
# member failure so a retry can leave already-closed members untouched.
bundle_done_feature="801-802-803-camera-workflow"
bundle_done_snapshot="$target_root/tasks/issue-$bundle_done_feature.md"
cat > "$bundle_done_snapshot" <<'MD'
# Issue bundle: #801, #802, #803

<!-- ori-devflow: issue-bundle-snapshot; issues=801,802,803 -->

Issue body says Closes #444 but is not attachment authority.
MD
print -r -- "# completed bundle tasks" > "$target_root/tasks/tasks-$bundle_done_feature.md"
issue_branch="feature/$bundle_done_feature"
FAKE_ISSUE_STATE="OPEN"
FAKE_ISSUE_STATE_BY_NUM=(801 OPEN 802 OPEN 803 OPEN 999 OPEN)
FAKE_PR_BODY="Closes #801. Fixes #802 again. Resolves #999."
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$bundle_done_feature" <<< "n" > "$fixture_root/done-bundle-all-open-output" 2>&1
for attached in 801 802 803; do
  rg -qF "issue close $attached --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
  [[ "$(rg -c "^issue close $attached " "$fixture_root/gh-calls")" == "1" ]]
done
rg -qF "issue close 999 --reason completed --comment Delivered by PR #77." "$fixture_root/gh-calls"
if rg -q '^issue (view|close) 444 ' "$fixture_root/gh-calls"; then
  print -r -- "wt done trusted an Issue-body closing reference" >&2
  exit 1
fi
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"

FAKE_PR_BODY=""
FAKE_ISSUE_STATE_BY_NUM=(801 CLOSED 802 OPEN 803 CLOSED)
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$bundle_done_feature" <<< "n" > "$fixture_root/done-bundle-mixed-output" 2>&1
if rg -q '^issue close (801|803) ' "$fixture_root/gh-calls"; then
  print -r -- "wt done reclosed a closed bundle member" >&2
  exit 1
fi
rg -q '^issue close 802 ' "$fixture_root/gh-calls"

FAKE_ISSUE_STATE_BY_NUM=(801 OPEN 802 OPEN 803 OPEN)
FAKE_ISSUE_CLOSE_FAIL_NUMS=(802)
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
bundle_failure_status=0
wt done "$bundle_done_feature" > "$fixture_root/done-bundle-partial-failure-output" 2>&1 || bundle_failure_status=$?
[[ "$bundle_failure_status" == "1" ]]
rg -q '^issue close 801 ' "$fixture_root/gh-calls"
rg -q '^issue close 802 ' "$fixture_root/gh-calls"
if rg -q 'worktree remove' "$fixture_root/git-calls"; then
  print -r -- "wt done removed the worktree after a bundle member failed" >&2
  exit 1
fi
rg -q 'Could not close attached Issue #802; worktree preserved' "$fixture_root/done-bundle-partial-failure-output"

# Safe retry: #801 is now closed, so only the remaining open members mutate.
FAKE_ISSUE_CLOSE_FAIL_NUMS=()
FAKE_ISSUE_STATE_BY_NUM=(801 CLOSED 802 OPEN 803 OPEN)
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$bundle_done_feature" <<< "n" > "$fixture_root/done-bundle-retry-output" 2>&1
if rg -q '^issue close 801 ' "$fixture_root/gh-calls"; then
  print -r -- "bundle retry reclosed the member completed before failure" >&2
  exit 1
fi
rg -q '^issue close 802 ' "$fixture_root/gh-calls"
rg -q '^issue close 803 ' "$fixture_root/gh-calls"
rg -q 'worktree remove' "$fixture_root/git-calls"

FAKE_MERGED_PR=""
FAKE_ISSUE_STATE_BY_NUM=(801 OPEN 802 OPEN 803 OPEN)
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
printf 'y\nn\n' | wt done "$bundle_done_feature" > "$fixture_root/done-bundle-no-merged-output" 2>&1
if rg -q '^issue ' "$fixture_root/gh-calls"; then
  print -r -- "wt done touched bundle members without a merged PR" >&2
  exit 1
fi
rg -q 'Attached Issues 801,802,803 were not changed because no merged PR was confirmed' "$fixture_root/done-bundle-no-merged-output"
FAKE_MERGED_PR="77"

# --keep-issue-open bypasses malformed bundle attachment parsing and all
# attached/secondary Issue mutations intentionally.
cat > "$bundle_done_snapshot" <<'MD'
# Issue bundle

<!-- ori-devflow: issue-bundle-snapshot; issues=803,801 -->
MD
FAKE_PR_BODY="Closes #999"
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$bundle_done_feature" --keep-issue-open <<< "n" > "$fixture_root/done-bundle-keep-output" 2>&1
if rg -q '^issue ' "$fixture_root/gh-calls"; then
  print -r -- "--keep-issue-open mutated a bundle or secondary Issue" >&2
  exit 1
fi
rg -q 'worktree remove' "$fixture_root/git-calls"

rm -f "$bundle_done_snapshot" "$target_root/tasks/tasks-$bundle_done_feature.md"
issue_branch="feature/$issue_feature"

# Restore the secondary-issue fakes to their no-op defaults for the rest of
# this lifecycle fixture.
FAKE_PR_BODY=""
FAKE_ISSUE_STATE_BY_NUM=()

# A failed Issue read or close aborts before the worktree is removed, so the
# exact same wt done command can safely be retried.
FAKE_ISSUE_STATE="OPEN"
FAKE_ISSUE_VIEW_STATUS=8
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
issue_failure_status=0
wt done "$issue_feature" > "$fixture_root/done-issue-view-failed-output" 2>&1 || issue_failure_status=$?
[[ "$issue_failure_status" == "1" ]]
if rg -q "worktree remove" "$fixture_root/git-calls"; then
  print -r -- "wt done removed the worktree after the Issue read failed" >&2
  exit 1
fi
rg -q "worktree preserved" "$fixture_root/done-issue-view-failed-output"

FAKE_ISSUE_VIEW_STATUS=0
FAKE_ISSUE_CLOSE_STATUS=7
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
issue_failure_status=0
wt done "$issue_feature" > "$fixture_root/done-issue-close-failed-output" 2>&1 || issue_failure_status=$?
[[ "$issue_failure_status" == "1" ]]
if rg -q "worktree remove" "$fixture_root/git-calls"; then
  print -r -- "wt done removed the worktree after Issue closure failed" >&2
  exit 1
fi
rg -q "Could not close attached Issue #292" "$fixture_root/done-issue-close-failed-output"
FAKE_ISSUE_CLOSE_STATUS=0

# A failed merged-PR lookup is also fatal for attached work; otherwise a network
# outage could silently turn delivery into local cleanup only.
FAKE_PR_STATUS=9
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
issue_failure_status=0
wt done "$issue_feature" > "$fixture_root/done-pr-read-failed-output" 2>&1 || issue_failure_status=$?
[[ "$issue_failure_status" == "1" ]]
[[ ! -e "$fixture_root/git-calls" ]]
if rg -q "^issue " "$fixture_root/gh-calls"; then
  print -r -- "wt done touched the Issue without confirming the merged PR" >&2
  exit 1
fi
rg -q "worktree preserved" "$fixture_root/done-pr-read-failed-output"
FAKE_PR_STATUS=0

# A successful query that confirms there is no merged PR keeps the existing
# explicit override path, but never closes the Issue on that unsafe path.
FAKE_MERGED_PR=""
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
printf 'y\nn\n' | wt done "$issue_feature" > "$fixture_root/done-no-merged-pr-output" 2>&1
if rg -q "^issue " "$fixture_root/gh-calls"; then
  print -r -- "wt done touched the Issue without a merged PR" >&2
  exit 1
fi
rg -q "Issue #292 was not changed because no merged PR was confirmed" "$fixture_root/done-no-merged-pr-output"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"
FAKE_MERGED_PR="77"

# Only the fixed generated header is trusted. A marker copied into the
# untrusted Issue body cannot redirect cleanup, and an identity mismatch fails
# before either GitHub or Git is mutated.
cat > "$issue_snapshot" <<'ISSUE'
# Issue #292: Coordinate based map

not-a-generated-marker

## Body
<!-- ori-devflow: issue-snapshot; issue=292 -->
ISSUE
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
invalid_snapshot_status=0
wt done "$issue_feature" > "$fixture_root/done-invalid-snapshot-output" 2>&1 || invalid_snapshot_status=$?
[[ "$invalid_snapshot_status" == "1" ]]
[[ ! -e "$fixture_root/gh-calls" ]]
[[ ! -e "$fixture_root/git-calls" ]]
rg -q "no valid generated marker on line 3" "$fixture_root/done-invalid-snapshot-output"

cat > "$issue_snapshot" <<'ISSUE'
# Issue #999: Wrong feature

<!-- ori-devflow: issue-snapshot; issue=999 -->
ISSUE
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
invalid_snapshot_status=0
wt done "$issue_feature" > "$fixture_root/done-mismatched-snapshot-output" 2>&1 || invalid_snapshot_status=$?
[[ "$invalid_snapshot_status" == "1" ]]
[[ ! -e "$fixture_root/gh-calls" ]]
[[ ! -e "$fixture_root/git-calls" ]]
rg -q "Issue #999 does not match feature '$issue_feature'" "$fixture_root/done-mismatched-snapshot-output"

# --keep-issue-open is the deliberate recovery/exception path. It bypasses even
# a malformed attachment, performs no Issue read or write, and keeps cleanup.
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done "$issue_feature" --keep-issue-open <<< "n" > "$fixture_root/done-keep-issue-output" 2>&1
if rg -q "^issue " "$fixture_root/gh-calls"; then
  print -r -- "--keep-issue-open still touched the Issue" >&2
  exit 1
fi
rg -q "Keeping the attached Issue open" "$fixture_root/done-keep-issue-output"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"

# Restore the original fixture identities for the remaining lifecycle checks.
rm -f "$issue_snapshot" "$target_root/tasks/tasks-$issue_feature.md"
function wt_resolve_worktree_branch {
  print -r -- "feature/bridge"
}
function gh {
  print -r -- "$*" >> "$fixture_root/gh-calls"
  print -r -- "42"
}

# The whole lifecycle just ran for a feature named `bridge` — a slug with no
# Issue number in it. New work is named `<issue-number>-<slug>` now, but nothing
# renames what already exists, and a legacy identity must keep working end to
# end: planning documents, branch, worktree, handoff, and cleanup.
rg -q "^handoff --feature bridge --worktree $target_root --branch feature/bridge" "$fixture_root/herd-calls"
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"
[[ -f "$dev_root/tasks/prd-bridge.md" ]]
[[ -f "$dev_root/tasks/tasks-bridge.md" ]]

# A number-first slug remains ordinary during start. Even during cleanup, the
# number alone is not an attachment: without the exact snapshot, no Issue call
# is allowed.
> "$fixture_root/herd-calls"
rm -f "$dev_root/tasks/issue-292-coordinate-based-map.md"
print -r -- "# numbered PRD" > "$dev_root/tasks/prd-292-coordinate-based-map.md"
print -r -- "## Tasks" > "$dev_root/tasks/tasks-292-coordinate-based-map.md"
wt start 292-coordinate-based-map --yes > "$fixture_root/numbered-output" 2>&1
rg -q "Feature .*292-coordinate-based-map" "$fixture_root/numbered-output"
rg -q "Branch .*feature/292-coordinate-based-map" "$fixture_root/numbered-output"
rg -q "^handoff --feature 292-coordinate-based-map --worktree $target_root --branch feature/292-coordinate-based-map" \
  "$fixture_root/herd-calls"
[[ -f "$target_root/tasks/prd-292-coordinate-based-map.md" ]]
[[ ! -f "$target_root/tasks/issue-292-coordinate-based-map.md" ]]

function wt_resolve_worktree_branch {
  print -r -- "feature/292-coordinate-based-map"
}
rm -f "$fixture_root/git-calls" "$fixture_root/gh-calls"
wt done 292-coordinate-based-map <<< "n" > "$fixture_root/done-number-only-output" 2>&1
if rg -q "^issue " "$fixture_root/gh-calls"; then
  print -r -- "wt done inferred Issue #292 from the slug alone" >&2
  exit 1
fi
rg -q "worktree remove $target_root --force" "$fixture_root/git-calls"
function wt_resolve_worktree_branch {
  print -r -- "feature/bridge"
}

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

# --all is the escape hatch that restores full history in the human table; it
# forwards like every other flag and composes with --json unchanged.
> "$fixture_root/overview-calls"
wt status --all > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "feature-overview --all" ]]

> "$fixture_root/overview-calls"
wt status --all --json > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "feature-overview --all --json" ]]

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

for invalid_args in "--bogus" "--feature" "--worktrees --json" "--worktrees --all"; do
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

# --- wt plan --issue <N> [--issue <N> ...] [--yes] -------------------------
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
                     "--issue 1 --issue 1" "--issue 1 --bogus" "--yes"; do
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
wt plan --issue 202 --issue 101 --yes > /dev/null
[[ "$(<"$fixture_root/plan-calls")" == "issue-plan --issue 202 --issue 101 --worktree $dev_root --yes" ]]

# Record one line per argument to prove repeated Issues remain separate zsh
# array elements rather than one flattened shell string.
function wt_herd {
  local argument
  : > "$fixture_root/plan-argv"
  for argument in "$@"; do
    print -r -- "$argument" >> "$fixture_root/plan-argv"
  done
}
wt plan --issue 202 --issue 101 --yes > /dev/null
[[ "$(<"$fixture_root/plan-argv")" == $'issue-plan\n--issue\n202\n--issue\n101\n--worktree\n'"$dev_root"$'\n--yes' ]]

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
  default_title="Ready issue codex planning"
  default_labels="backlog,size:planned"
  case "$number" in
    201) default_title="Camera"; default_labels="backlog,size:quick" ;;
    202) default_title="Workflow"; default_labels="backlog,size:prd" ;;
  esac
  title="$(json_escape "${FAKE_GH_TITLE:-$default_title}")"
  body="$(json_escape "$body_raw")"
  state="${FAKE_GH_STATE:-OPEN}"

  labels_json=""
  old_ifs="$IFS"
  IFS=','
  for label in ${FAKE_GH_LABELS:-$default_labels}; do
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

function issue_bundle_plan_direct {
  local -a call
  call=(--repo-root "$issue_repo" issue-plan --issue 202 --issue 201 --worktree "$issue_dev" --yes)
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
rg -q '1\.1 Read the canonical planning skill' "$starter_path"
rg -q '\.agents/skills/task-planning/SKILL\.md' "$starter_path"
rg -q 'planning-only mode' "$starter_path"
if rg -q 'Wait for "Go"|vertical slices|Permission sweep' "$starter_path"; then
  print -r -- "the starter duplicated workflow from the canonical planning skill" >&2
  exit 1
fi

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

# Repeated --issue values cross the real shell/Go boundary as one canonical
# bundle. Input order is presentation-only, the highest size route wins, and
# Herdr degradation prints a recovery command containing every member.
bundle_status=0
issue_bundle_plan_direct > "$fixture_root/issue-plan-bundle" 2>&1 || bundle_status=$?
if [[ "$bundle_status" != "0" ]]; then
  print -r -- "issue-plan bundle exited $bundle_status: $(<"$fixture_root/issue-plan-bundle")" >&2
  exit 1
fi
bundle_slug="201-202-camera-workflow"
bundle_snapshot="$issue_dev/tasks/issue-$bundle_slug.md"
bundle_starter="$issue_dev/tasks/tasks-$bundle_slug.md"
[[ -f "$bundle_snapshot" && -f "$bundle_starter" ]]
rg -q 'Issue bundle  #201, #202' "$fixture_root/issue-plan-bundle"
rg -q 'Size +size:prd' "$fixture_root/issue-plan-bundle"
rg -q 'wt plan --issue 201 --issue 202' "$fixture_root/issue-plan-bundle"
rg -q 'ori-devflow: issue-bundle-snapshot; issues=201,202' "$bundle_snapshot"
rg -q 'Compatibility: human-confirmed' "$bundle_snapshot"
rg -q 'Effective size route: `size:prd`' "$bundle_starter"
rg -Fq '$(rm -rf /)' "$bundle_snapshot"

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
if [[ -e "$issue_dev/tasks" ]] && [[ "$(ls "$issue_dev/tasks" | wc -l | tr -d ' ')" != "4" ]]; then
  print -r -- "an ineligible Issue left files behind: $(ls "$issue_dev/tasks")" >&2
  exit 1
fi

# size:prd routes to the canonical skill's PRD-first workflow and names the
# expected output path, rather than restating that workflow in the starter.
FAKE_GH_LABELS="backlog,size:prd" issue_plan_direct 105 > /dev/null
prd_starter="$issue_dev/tasks/tasks-105-ready-issue-codex-planning.md"
[[ -f "$prd_starter" ]]
rg -q 'canonical planning skill' "$prd_starter"
rg -q 'planning-only mode' "$prd_starter"
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
# entrypoint calls `gh` directly. Its sole Go-helper boundary is the local
# config agent-defaults command, which makes no Herdr call.
devops_entrypoint="$repo_root/scripts/devops.sh"
if [[ ! -x "$devops_entrypoint" ]]; then
  print -r -- "scripts/devops.sh is missing or not executable" >&2
  exit 1
fi
if ! rg -q 'gh "\$\{args\[@\]\}"' "$devops_entrypoint"; then
  print -r -- "scripts/devops.sh no longer invokes gh directly" >&2
  exit 1
fi
if rg -q 'wt_herd|devflow_exec|devflow-bootstrap' "$devops_entrypoint"; then
  print -r -- "scripts/devops.sh reaches for the Herdr runtime bridge" >&2
  exit 1
fi
if [[ "$(rg -c 'bash "\$script_dir/herdr-devflow\.sh" "\$@"' "$devops_entrypoint" || true)" != "1" ]] || \
   ! rg -q 'agent_defaults_helper config agent-defaults' "$devops_entrypoint"; then
  print -r -- "scripts/devops.sh local helper boundary is not limited to config agent-defaults" >&2
  exit 1
fi
# The picker has exactly three bash-to-zsh bridges: `s` delegates one-Issue
# planning, `b` delegates bundle planning, and `i` delegates implementation.
# All source this checkout's wt entrypoint and pass validated values as separate
# arguments. wt remains the
# owner of confirmation, files, worktree creation, and Herdr/Pi handoff.
devops_code="$(rg -v '^\s*#' "$devops_entrypoint")"
if ! print -r -- "$devops_code" | rg -Fq "zsh -c 'source \"\$1\" && wt plan --issue \"\$2\"' devops-plan \"\$script_dir/wt.sh\" \"\$issue_number\""; then
  print -r -- "scripts/devops.sh does not launch wt plan through the constrained zsh bridge" >&2
  exit 1
fi
if ! print -r -- "$devops_code" | rg -Fq "zsh -c 'source \"\$1\" || exit; shift; typeset -a plan_args; plan_args=(); for issue in \"\$@\"; do plan_args+=(--issue \"\$issue\"); done; wt plan \"\${plan_args[@]}\"'"; then
  print -r -- "scripts/devops.sh does not launch bundle planning through the constrained positional-argument bridge" >&2
  exit 1
fi
if ! print -r -- "$devops_code" | rg -Fq "zsh -c 'source \"\$1\" && if [[ \"\$3\" == no-herdr ]]; then wt start \"\$2\" --no-herdr; else wt start \"\$2\" --kind \"\$3\"; fi' devops-start \"\$script_dir/wt.sh\" \"\$feature\" \"\$mode\""; then
  print -r -- "scripts/devops.sh does not launch wt start through the constrained zsh bridge" >&2
  exit 1
fi
if [[ "$(print -r -- "$devops_code" | rg -c '^\s*zsh -c ' || true)" != "3" ]]; then
  print -r -- "scripts/devops.sh must contain only the three constrained wt plan/bundle/start zsh bridges" >&2
  exit 1
fi
if print -r -- "$devops_code" | rg -q '\beval\b|\$\(\s*wt\s|^\s*(source\s+.*wt\.sh|wt\s+(plan|start))'; then
  print -r -- "scripts/devops.sh evaluates, sources, or invokes wt outside the constrained zsh bridges" >&2
  exit 1
fi
# A clipboard dependency was explicitly ruled out: it is per-platform, fails
# silently in a pipe, and makes the printed command unverifiable.
if rg -q 'pbcopy|xclip|xsel|wl-copy|clip\.exe' "$devops_entrypoint"; then
  print -r -- "scripts/devops.sh grew a clipboard dependency" >&2
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
