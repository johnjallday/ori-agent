#!/bin/sh
# This wrapper is copied with the manifest into a stable user-local runtime
# directory by `wt herd setup`. It never points Herdr at a removable worktree.
set -eu

plugin_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
runtime_root=$(CDPATH= cd -- "$plugin_dir/.." && pwd)
helper="$runtime_root/bin/herdr-devflow"

if [ ! -x "$helper" ]; then
  echo "Ori Devflow helper is missing. Run: wt herd setup" >&2
  exit 1
fi

# The installed plugin must operate only from its stable user-local runtime;
# a Herdr pane can outlive the Git worktree that originally installed it.
exec "$helper" --home "$runtime_root" "$@"
