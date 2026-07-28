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
target_root="$fixture_root/feature"
mkdir -p "$dev_root/tasks" "$target_root"
print -r -- "# bridge" > "$dev_root/tasks/prd-bridge.md"
print -r -- "## Tasks" > "$dev_root/tasks/tasks-bridge.md"

source "$repo_root/scripts/wt.sh"

function wt_get_dev_worktree {
  print -r -- "$dev_root"
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
wt start bridge >/dev/null
[[ "$(<"$fixture_root/herd-calls")" == "handoff --feature bridge --worktree $target_root --branch feature/bridge" ]]

# An optional per-feature kind override is forwarded to the initial handoff;
# omitting it above leaves the configured Claude default unchanged.
> "$fixture_root/herd-calls"
wt start bridge --kind codex >/dev/null
[[ "$(<"$fixture_root/herd-calls")" == "handoff --feature bridge --worktree $target_root --branch feature/bridge --kind codex" ]]

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

# A blocked Herdr cleanup guard must stop wt done before it mutates the
# backlog, archives tasks, checks dirty state, or asks Git to remove anything.
print -r -- "## Doing" > "$dev_root/BACKLOG.md"
print -r -- "- [bridge](tasks/prd-bridge.md)" >> "$dev_root/BACKLOG.md"
print -r -- "# completed bridge tasks" > "$target_root/tasks/tasks-bridge.md"
before_backlog="$(<"$dev_root/BACKLOG.md")"

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
[[ "$(<"$dev_root/BACKLOG.md")" == "$before_backlog" ]]
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
[[ "$(<"$dev_root/BACKLOG.md")" == "$before_backlog" ]]
[[ ! -e "$fixture_root/git-calls" ]]
rg -q "cannot be verified in a non-interactive shell" "$fixture_root/done-unavailable-output"

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
[[ "$(<"$fixture_root/overview-calls")" == "overview" ]]
[[ ! -s "$fixture_root/overview-output" ]]

# Every supported option is forwarded as separate words, and a feature slug is
# never concatenated into a single argument or passed through eval.
> "$fixture_root/overview-calls"
wt status --feature downloads-janitor --json --no-color > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "overview --feature downloads-janitor --json --no-color" ]]

> "$fixture_root/overview-calls"
wt status --feature=downloads-janitor > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "overview --feature downloads-janitor" ]]

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
[[ "$(<"$fixture_root/overview-calls")" == "overview --feature a-b-c" ]]

# NO_COLOR and --no-color are both honoured, and neither is swallowed.
> "$fixture_root/overview-calls"
NO_COLOR=1 wt status > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "overview" ]]
> "$fixture_root/overview-calls"
wt status --no-color --watch > /dev/null
[[ "$(<"$fixture_root/overview-calls")" == "overview --no-color --watch" ]]

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

# The remaining Herdr-free commands mutate Git or GitHub, so they are asserted
# structurally instead of by running them: no dispatcher branch below may
# mention the bridge helper at all.
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

# --no-herdr is the supported per-invocation escape hatch (FR-33), so it has to
# be discoverable from wt help rather than only from the source.
wt help > "$fixture_root/help-output" 2>&1
rg -q -- "--no-herdr" "$fixture_root/help-output"
rg -q "bare Git worktree" "$fixture_root/help-output"

print -r -- "wt-herd.test.sh: ok"
