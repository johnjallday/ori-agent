#!/bin/zsh
# Verify what `wt demo` hands the server about your Codex login.
#
# A demo sandbox replaces HOME, which hides ~/.codex, so Ori's codex provider
# (the Codex CLI) never registers. wt_demo_codex_env passes your real Codex
# home through by default and drops it entirely under ORI_DEMO_NO_CODEX=1.
# The env line is exercised for real, because an `env` option placed after an
# assignment would be run as the command instead.
set -euo pipefail

exec < /dev/null

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
source "$repo_root/scripts/lib/devtools-project.zsh"

fixture="$(mktemp -d "${TMPDIR:-/tmp}/ori-wt-demo-codex.XXXXXX")"
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/home/.codex" "$fixture/custom-codex" "$fixture/bare-home" "$fixture/sandbox"

# What the demo server would see for CODEX_HOME, launched the way wt demo
# launches it (with and without the ORI_DEMO_OPEN option in front).
function server_codex_home {
  local open="${1:-0}"
  if [[ "$open" == 1 ]]; then
    env -u NO_BROWSER "${WT_DEMO_CODEX_ENV[@]}" HOME="$fixture/sandbox" \
      sh -c 'printf "%s|%s" "${CODEX_HOME-<unset>}" "$HOME"'
  else
    env "${WT_DEMO_CODEX_ENV[@]}" HOME="$fixture/sandbox" NO_BROWSER=1 \
      sh -c 'printf "%s|%s" "${CODEX_HOME-<unset>}" "$HOME"'
  fi
}

# --- default: your Codex home reaches the server ----------------------------

unset CODEX_HOME ORI_DEMO_NO_CODEX
HOME="$fixture/home" wt_demo_codex_env
[[ "${WT_DEMO_CODEX_ENV[*]}" == "CODEX_HOME=$fixture/home/.codex" ]]
[[ "$WT_DEMO_CODEX_NOTE" == *"$fixture/home/.codex"* ]]
[[ "$(server_codex_home 0)" == "$fixture/home/.codex|$fixture/sandbox" ]]
[[ "$(server_codex_home 1)" == "$fixture/home/.codex|$fixture/sandbox" ]]

# --- an explicit CODEX_HOME wins ---------------------------------------------

CODEX_HOME="$fixture/custom-codex" HOME="$fixture/home" wt_demo_codex_env
[[ "${WT_DEMO_CODEX_ENV[*]}" == "CODEX_HOME=$fixture/custom-codex" ]]

# --- no Codex login: nothing is passed, and nothing is said -----------------

HOME="$fixture/bare-home" wt_demo_codex_env
(( ${#WT_DEMO_CODEX_ENV[@]} == 0 ))
[[ -z "$WT_DEMO_CODEX_NOTE" ]]
[[ "$(server_codex_home 0)" == "<unset>|$fixture/sandbox" ]]
[[ "$(server_codex_home 1)" == "<unset>|$fixture/sandbox" ]]

# --- ORI_DEMO_NO_CODEX=1 isolates, even from an inherited CODEX_HOME --------

export CODEX_HOME="$fixture/custom-codex"
ORI_DEMO_NO_CODEX=1 HOME="$fixture/home" wt_demo_codex_env
[[ "${WT_DEMO_CODEX_ENV[*]}" == "-u CODEX_HOME" ]]
[[ "$WT_DEMO_CODEX_NOTE" == *"off"* ]]
[[ "$(server_codex_home 0)" == "<unset>|$fixture/sandbox" ]]
[[ "$(server_codex_home 1)" == "<unset>|$fixture/sandbox" ]]
unset CODEX_HOME

print "wt-demo-codex: ok"
