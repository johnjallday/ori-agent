#!/usr/bin/env bash
# Compatibility path only; operator authorization still belongs to the installer.
set -euo pipefail
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
source "$script_dir/lib/devtools-selector.sh"
tool_root="$(ori_devtools_select away/install-herdr-telegram.sh)"
repo_root="$(ori_devtools_anchor "$script_dir/..")"
export ORI_DEVTOOLS_HOME="$tool_root" HERDR_DEVFLOW_REPO_ROOT="$repo_root"
exec bash "$tool_root/scripts/away/install-herdr-telegram.sh" "$@"
