#!/bin/zsh
# Stable launchd path: target comes from this Ori checkout, not launchd's CWD.
set -eu
script_dir="${0:A:h}"
source "$script_dir/lib/devtools-selector.sh"
tool_root="$(ori_devtools_select away-tick.sh)"
repo_root="$(ori_devtools_anchor "${script_dir:h}")"
export ORI_DEVTOOLS_HOME="$tool_root" HERDR_DEVFLOW_REPO_ROOT="$repo_root"
exec zsh -f "$tool_root/scripts/away-tick.sh" "$@"
