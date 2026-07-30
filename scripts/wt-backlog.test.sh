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

# Every real mutation cleans terminal history before the existing scoped
# commit-and-push path runs.
wt backlog add "new retained idea" > "$fixture_root/add-output"
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

print -r -- "wt backlog tests passed"
