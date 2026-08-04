#!/usr/bin/env bash
# This repository's backlog: its open GitHub Issues. A plain executable rather
# than a shell function, so an agent can call it as one stable token instead of
# sourcing a zsh file first.
#
# Every subcommand — list, view, add, their flags, and any spelling this shell
# has never heard of — goes to the Go helper, which owns the query, the bounds,
# the sanitization, the JSON contract, and the rejections. One parser decides
# what an invocation means; splitting that between the shell and the helper is
# how a quoted title ends up as two arguments.
set -euo pipefail

# `git rev-parse` answers per worktree while every checkout shares one
# repository, so this works from the source checkout, dev, or any feature
# worktree.
if ! repo_root="$(git rev-parse --show-toplevel 2>/dev/null)"; then
  printf 'backlog.sh must run inside a Git checkout of this repository\n' >&2
  exit 2
fi

# The helper the installer copies to a user-local runtime root. Derived exactly
# as tools/herdr-devflow/internal/worktree/context.go derives HelperPath, so the
# shell and the Go code can never disagree about where the binary lives.
installed_helper() {
  local root
  if [[ "${HERDR_DEVFLOW_HOME:-}" =~ [^[:space:]] ]]; then
    root="$HERDR_DEVFLOW_HOME"
  elif [[ "$(uname -s)" == "Darwin" ]]; then
    [[ -n "${HOME:-}" ]] || return 1
    root="$HOME/Library/Application Support/herdr/ori-devflow"
  elif [[ -n "${XDG_CONFIG_HOME:-}" ]]; then
    root="$XDG_CONFIG_HOME/herdr/ori-devflow"
  else
    [[ -n "${HOME:-}" ]] || return 1
    root="$HOME/.config/herdr/ori-devflow"
  fi
  printf '%s/bin/herdr-devflow' "$root"
}

# First hit wins: an explicit override, then the installed runtime binary, then
# a binary built into this checkout. Compiling is the last resort, because
# reading the backlog should not rebuild a Go program.
helper=""
installed="$(installed_helper || true)"
if [[ -n "${HERDR_DEVFLOW_BINARY:-}" && -x "${HERDR_DEVFLOW_BINARY}" ]]; then
  helper="$HERDR_DEVFLOW_BINARY"
elif [[ -n "$installed" && -x "$installed" ]]; then
  helper="$installed"
elif [[ -x "$repo_root/bin/herdr-devflow" ]]; then
  helper="$repo_root/bin/herdr-devflow"
fi

# exec so the helper replaces this shell: its exit code and signals propagate
# without translation. 0 the operation completed, 1 GitHub could not answer,
# 2 the invocation was wrong.
if [[ -n "$helper" ]]; then
  exec "$helper" --repo-root "$repo_root" backlog "$@"
fi

cd "$repo_root"
# Once the package path is supplied, remaining values are program arguments. Do
# not insert a standalone `--`: Go passes that token through to the helper.
exec go run ./tools/herdr-devflow/cmd/herdr-devflow --repo-root "$repo_root" backlog "$@"
