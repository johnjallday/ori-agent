#!/usr/bin/env zsh
# Non-sourced entrypoint for `wt demo`.
#
# Why this exists
# ---------------
# `wt` is a shell FUNCTION defined in scripts/wt.sh, so it only exists in a
# shell that has sourced that file. An agent session cannot just run `wt demo`,
# and `./scripts/wt.sh demo` only prints help. The documented workaround is
#
#     zsh -c 'source ./scripts/wt.sh && wt demo 8931'
#
# which is awkward and — because it is a compound command — fragments into a
# different permission prompt almost every time. A transcript scan found
# sessions repeatedly reinventing an ad-hoc smoke-server script inside their
# scratchpad instead, at a path containing the session UUID, which can never be
# allowlisted twice AND skips wt demo's sandbox guarantees.
#
# This wrapper makes the demo server ONE stable allowlist token:
#
#     Bash(./scripts/demo.sh:*)
#
# Usage:  ./scripts/demo.sh [port]      (default 8931, Ctrl-C to stop)
#
# Isolation is inherited from `wt demo` — sandboxed HOME and ORI_DATA_DIR in a
# throwaway temp dir, server launched from inside the sandbox so the plugin
# store is isolated too. See CLAUDE.md "Smoke Testing".

set -uo pipefail

script_dir="${0:A:h}"

if [[ ! -r "$script_dir/wt.sh" ]]; then
  print -u2 "demo.sh: cannot find wt.sh next to this script ($script_dir)"
  exit 1
fi

# wt.sh defines the `wt` function; silence its load-time banner.
source "$script_dir/wt.sh" >/dev/null 2>&1

if ! typeset -f wt >/dev/null; then
  print -u2 "demo.sh: sourcing wt.sh did not define the 'wt' function"
  exit 1
fi

wt demo "$@"
