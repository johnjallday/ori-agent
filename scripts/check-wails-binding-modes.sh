#!/usr/bin/env bash
set -euo pipefail

# Wails' code generator writes JS/TS binding files as executable, which is
# wrong for plain text source (not a script, never invoked directly).
# Regenerating them (./scripts/build-folder-picker.sh runs `wails build`,
# which regenerates bindings as a side effect) restores the executable bit;
# this catches that before it is committed. release.yml resets this whole
# directory (plus wailsjs/runtime/) after building for the same reason.
#
# Scans the tree rather than a fixed filename list: Wails has generated
# App.js/App.d.ts under go/main/ and models.ts directly under go/ in this
# repo, and a hardcoded list previously missed the latter.
binding_dir="cmd/folder-picker/frontend/wailsjs"

failed=0
while IFS=$'\t' read -r meta path; do
	mode="${meta%% *}"
	if [[ "$mode" != "100644" ]]; then
		printf 'Generated binding tracked as executable (mode %s): %s\n' "$mode" "$path" >&2
		failed=1
	fi
done < <(git ls-files -s -- "$binding_dir")

if [[ "$failed" -ne 0 ]]; then
	exit 1
fi

printf 'Wails binding file modes OK\n'
