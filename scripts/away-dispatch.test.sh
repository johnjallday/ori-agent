#!/bin/zsh
set -euo pipefail

exec < /dev/null

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ori-away-dispatch.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

dev_root="$fixture_root/dev"
mkdir -p "$dev_root/tasks"
source "$repo_root/scripts/wt.sh"

function wt_get_dev_worktree {
  print -r -- "$dev_root"
}

output="$(wt away status)"
[[ "$output" == "Away dispatcher: disarmed" ]]

# A disarmed tick is silent and exits before the queue authorization boundary.
mkdir "$dev_root/tasks/away-queue.md"
output="$(wt away tick)"
[[ -z "$output" ]]

output="$(wt away arm)"
[[ "$output" == "Away dispatcher armed." ]]
[[ -f "$dev_root/tasks/.away-armed" ]]
[[ "$(stat -f '%Lp' "$dev_root/tasks/.away-armed")" == "600" ]]

output="$(wt away arm)"
[[ "$output" == "Away dispatcher is already armed." ]]
[[ "$(wt away status)" == "Away dispatcher: armed" ]]

output="$(wt away disarm)"
[[ "$output" == "Away dispatcher disarmed. Running agents were not interrupted." ]]
[[ ! -e "$dev_root/tasks/.away-armed" ]]

output="$(wt away disarm)"
[[ "$output" == "Away dispatcher is already disarmed." ]]

if wt away > "$fixture_root/no-command.out" 2>&1; then
  print -r -- "wt away without a subcommand succeeded" >&2
  exit 1
fi
rg -q '^Usage: wt away <command>$' "$fixture_root/no-command.out"

if wt away launch > "$fixture_root/unknown.out" 2>&1; then
  print -r -- "wt away accepted an unknown subcommand" >&2
  exit 1
fi
rg -q '^Unknown wt away command: launch$' "$fixture_root/unknown.out"

if wt away tick --force > "$fixture_root/flag.out" 2>&1; then
  print -r -- "wt away tick accepted an unknown flag" >&2
  exit 1
fi
rg -q '^wt away tick accepts no arguments$' "$fixture_root/flag.out"

print -r -- "away-dispatch.test.sh: OK"
