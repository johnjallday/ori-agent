#!/bin/zsh
# Stable launchd entrypoint for one Away Dispatcher cycle.
set -u

script_dir="${0:A:h}"
repo_root="${script_dir:h}"
cd "$repo_root" || exit 1
source "$script_dir/wt.sh"
wt away tick
