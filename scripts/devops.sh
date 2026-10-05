#!/usr/bin/env bash
# Compatibility entrypoint: application context here, workflow source outside Ori.
_ori_scripts="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)" || { return 2 2>/dev/null || exit 2; }
source "$_ori_scripts/lib/devtools-selector.sh" || { return 2 2>/dev/null || exit 2; }
_ori_tool="$(ori_devtools_select devops.sh)" || { return 2 2>/dev/null || exit 2; }
_ori_target="$(ori_devtools_anchor "$_ori_scripts/..")" || { return 2 2>/dev/null || exit 2; }
export ORI_DEVTOOLS_HOME="$_ori_tool" HERDR_DEVFLOW_REPO_ROOT="$_ori_target"
if [[ "${DEVOPS_SOURCE_ONLY:-}" == 1 ]]; then
  source "$_ori_tool/scripts/devops.sh"
else
  exec bash "$_ori_tool/scripts/devops.sh" "$@"
fi
