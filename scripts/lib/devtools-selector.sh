#!/bin/sh
# Functions only; shared by Bash 3.2 entrypoints and sourced zsh. No downloads,
# PATH search, source-time target cache, or fallback to embedded implementations.
ori_devtools_error() { printf 'Ori devtools: %s\n' "$*" >&2; }

ori_devtools_directory() {
  case "$1" in /*) ;; *) ori_devtools_error "expected an absolute directory: $1"; return 2 ;; esac
  case "$1" in *[[:cntrl:]]*) ori_devtools_error 'directory contains control characters'; return 2 ;; esac
  (CDPATH= cd -P -- "$1" 2>/dev/null && pwd -P) || {
    ori_devtools_error "unavailable directory: $1; select a tested checkout with ORI_DEVTOOLS_HOME"
    return 2
  }
}

ori_devtools_select() {
  local selected
  selected="$(ori_devtools_directory "${ORI_DEVTOOLS_HOME-/Users/jjdev/Projects/ori/devtools}")" || return $?
  if [ ! -f "$selected/devtools-contract" ] ||
     [ "$(wc -c < "$selected/devtools-contract" | tr -d ' ')" != 16 ] ||
     [ "$(< "$selected/devtools-contract")" != ori-devtools-v1 ] ||
     ! grep -Eq '^module ori-devtools\.local[[:space:]]*$' "$selected/go.mod" 2>/dev/null; then
    ori_devtools_error "incompatible source: $selected; select a tested ori-devtools-v1 checkout"
    return 2
  fi
  # Entrypoints are fixed by the wrapper, never a caller-supplied file name.
  if [ ! -f "$selected/scripts/$1" ]; then
    ori_devtools_error "selected source lacks scripts/$1: $selected"
    return 2
  fi
  printf '%s\n' "$selected"
}

ori_devtools_anchor() {
  local root git_root explicit
  root="$(ori_devtools_directory "$1")" || return $?
  git_root="$(git -C "$root" rev-parse --show-toplevel 2>/dev/null)" || {
    ori_devtools_error "not a Git checkout: $root"; return 2;
  }
  git_root="$(ori_devtools_directory "$git_root")" || return $?
  if [ "$root" != "$git_root" ] ||
     ! grep -Eq '^module github\.com/johnjallday/ori-agent[[:space:]]*$' "$root/go.mod" 2>/dev/null; then
    ori_devtools_error "not an Ori root: $root"; return 2
  fi
  if [ "${HERDR_DEVFLOW_REPO_ROOT+x}" = x ]; then
    explicit="$(ori_devtools_directory "$HERDR_DEVFLOW_REPO_ROOT")" || return $?
    if [ "$explicit" != "$root" ]; then
      ori_devtools_error 'explicit target conflicts with this Ori wrapper; invoke the companion directly to target another checkout'
      return 2
    fi
  fi
  printf '%s\n' "$root"
}
