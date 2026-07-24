#!/usr/bin/env bash
# Thin developer entrypoint for the checked-in helper. `wt herd setup` copies a
# compiled helper and plugin files into a stable user-local runtime directory.
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
binary="${HERDR_DEVFLOW_BINARY:-$repo_root/bin/herdr-devflow}"

if [[ "${HERDR_DEVFLOW_USE_SOURCE:-}" != "1" && -x "$binary" ]]; then
  exec "$binary" --repo-root "$repo_root" "$@"
fi

cd "$repo_root"
exec go run ./tools/herdr-devflow/cmd/herdr-devflow -- --repo-root "$repo_root" "$@"
