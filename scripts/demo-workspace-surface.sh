#!/bin/zsh
# Launch the isolated Ori demo with the non-REAPER Workspace Surface protocol
# fixture attached to every disposable workspace in that sandbox.
set -euo pipefail

script_dir="${0:A:h}"
repo_root="${script_dir:h}"
export ORI_WORKSPACE_SURFACE_DEMO=1
export ORI_WORKSPACE_SURFACE_DEMO_ROOT="$repo_root/internal/workspacesurfacedemo/testdata/plugin"
exec "$script_dir/demo.sh" "$@"
