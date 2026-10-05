#!/usr/bin/env bash
# Resolve one trusted skill; this does not load agents, install or run setup.
set -euo pipefail
[[ $# == 0 ]] || { printf 'Usage: %s\n' "$0" >&2; exit 2; }
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
source "$script_dir/lib/devtools-selector.sh"
ori_devtools_anchor "$script_dir/.." >/dev/null
tool_root="$(ori_devtools_select herdr-devflow.sh)"
skill="$tool_root/.agents/skills/setup-herdr/SKILL.md"
[[ -s "$skill" ]] || { printf 'Selected toolbox setup skill is unavailable: %s\n' "$skill" >&2; exit 2; }
printf '%s\n' "$skill"
