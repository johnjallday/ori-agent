#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

mkdir -p "$fixture/scripts" "$fixture/cmd/folder-picker/frontend/wailsjs/go/main"
cp "$repo_root/scripts/check-wails-binding-modes.sh" \
    "$fixture/scripts/check-wails-binding-modes.sh"
printf 'generated binding\n' \
    > "$fixture/cmd/folder-picker/frontend/wailsjs/go/main/App.js"
chmod 0644 "$fixture/cmd/folder-picker/frontend/wailsjs/go/main/App.js"

git -C "$fixture" init -q
git -C "$fixture" config user.email test@example.com
git -C "$fixture" config user.name "Test User"
git -C "$fixture" add .
git -C "$fixture" commit -qm fixture

(
    cd "$fixture"
    bash scripts/check-wails-binding-modes.sh >/dev/null
)

chmod 0755 "$fixture/cmd/folder-picker/frontend/wailsjs/go/main/App.js"
if (
    cd "$fixture"
    bash scripts/check-wails-binding-modes.sh >/dev/null 2>&1
); then
    echo "mode check ignored an unstaged executable binding" >&2
    exit 1
fi

chmod 0644 "$fixture/cmd/folder-picker/frontend/wailsjs/go/main/App.js"
(
    cd "$fixture"
    bash scripts/check-wails-binding-modes.sh >/dev/null
)

printf 'Wails binding mode check catches working-tree drift\n'
