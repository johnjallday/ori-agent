#!/usr/bin/env bash
# Explicit convenience targets only; normal Ori builds/tests never call this.
set -euo pipefail
[[ $# == 1 ]] || { printf 'Usage: %s build|test|cross\n' "$0" >&2; exit 2; }
case "$1" in build|test|cross) ;; *) printf 'Unsupported toolbox target: %s\n' "$1" >&2; exit 2 ;; esac
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
source "$script_dir/lib/devtools-selector.sh"
tool_root="$(ori_devtools_select herdr-devflow.sh)"
[[ -f "$tool_root/Makefile" ]] || { printf 'Selected toolbox has no Makefile: %s\n' "$tool_root" >&2; exit 2; }
export ORI_DEVTOOLS_HOME="$tool_root"
exec make -C "$tool_root" "$1"
