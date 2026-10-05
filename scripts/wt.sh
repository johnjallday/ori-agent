#!/bin/zsh
# Sourced compatibility entrypoint. Functions, not a subprocess, own navigation.
# Selection is lazy so an unavailable tool cannot terminate a login shell.
source "${${(%):-%x}:A:h}/lib/devtools-selector.sh" || return 2
unalias wt 2>/dev/null || true
function wt {
  local selected
  selected="$(ori_devtools_select wt.sh)" || return $?
  # The companion initializes safety defaults in function bodies for restored
  # shell snapshots and resolves the current Ori target on every invocation.
  source "$selected/scripts/wt.sh" || return $?
  wt "$@"
}
