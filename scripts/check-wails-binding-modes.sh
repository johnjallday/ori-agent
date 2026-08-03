#!/usr/bin/env bash
set -euo pipefail

# Wails' code generator writes these JS/TS binding files as executable, which
# is wrong for plain text source (not a script, never invoked directly).
# Regenerating them (./scripts/build-folder-picker.sh runs `wails build`,
# which regenerates bindings as a side effect) restores the executable bit;
# this catches that before it is committed. release.yml resets both this
# directory and wailsjs/runtime/ after building for the same reason.
files=(
	cmd/folder-picker/frontend/wailsjs/go/main/App.js
	cmd/folder-picker/frontend/wailsjs/go/main/App.d.ts
)

failed=0
for file in "${files[@]}"; do
	mode="$(git ls-files -s -- "$file" | awk '{print $1}')"
	if [[ -z "$mode" ]]; then
		printf 'Expected generated binding is not tracked: %s\n' "$file" >&2
		failed=1
	elif [[ "$mode" != "100644" ]]; then
		printf 'Generated binding tracked as executable (mode %s): %s\n' "$mode" "$file" >&2
		failed=1
	fi
done

if [[ "$failed" -ne 0 ]]; then
	exit 1
fi

printf 'Wails binding file modes OK\n'
