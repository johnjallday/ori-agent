#!/usr/bin/env bash
# Source-path compatibility only. Installed root helpers remain self-contained;
# the companion installer copies its real helper, never this repository shim.
set -euo pipefail
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
source "$script_dir/lib/devtools-selector.sh"
tool_root="$(ori_devtools_select away/pmset-helper.sh)"
repo_root="$(ori_devtools_anchor "$script_dir/..")"
export ORI_DEVTOOLS_HOME="$tool_root" HERDR_DEVFLOW_REPO_ROOT="$repo_root"
exec bash "$tool_root/scripts/away/pmset-helper.sh" "$@"
