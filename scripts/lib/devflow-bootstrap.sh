# shellcheck shell=bash
# Locates this repository and the herdr-devflow helper that serves its CLI
# entry points. Sourced — never executed — by the thin scripts in
# scripts/devops/, each of which then execs the helper with its own subcommand.
#
# One copy exists because the callers must never disagree about which binary
# they run: a stale helper answering one script and a fresh one answering
# another is the kind of split-brain that presents as an unrelated bug
# somewhere else entirely.
#
# On success the caller has:
#   repo_root  absolute path to this checkout
#   helper     path to an executable helper, or empty when none was found
#              (the caller falls back to `go run`)
#
# The caller is responsible for `set -euo pipefail`; sourcing does not impose
# shell options on a script that may want its own.

# The name the caller reports errors under, so a failure says which command
# the user actually typed rather than naming this file.
devflow_script_name="${devflow_script_name:-$(basename "${BASH_SOURCE[1]:-devflow}")}"

# `git rev-parse` answers per worktree while every checkout shares one
# repository, so this works from the source checkout, dev, or any feature
# worktree.
if ! repo_root="$(git rev-parse --show-toplevel 2>/dev/null)"; then
  printf '%s must run inside a Git checkout of this repository\n' "$devflow_script_name" >&2
  exit 2
fi

# The helper the installer copies to a user-local runtime root. Derived exactly
# as tools/herdr-devflow/internal/worktree/context.go derives HelperPath, so the
# shell and the Go code can never disagree about where the binary lives.
devflow_installed_helper() {
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

# First hit wins: an explicit override, then a binary built into this checkout,
# then the installed runtime binary. Compiling is the last resort, because
# reading a backlog should not rebuild a Go program.
#
# The checkout's own bin/ outranks the installed helper deliberately. Only one
# kind of person has both — somebody working on the helper — and for them the
# global install is by definition the older of the two. Preferring it means a
# freshly built subcommand reports itself as unknown, which reads as a bug in
# the feature rather than a stale binary answering for it.
#
# This does not reintroduce the cost that put the installed helper first: that
# concern is about the `go run` fallback at the end of this chain, and a binary
# already sitting in bin/ compiles nothing.
helper=""
devflow_installed="$(devflow_installed_helper || true)"
if [[ -n "${HERDR_DEVFLOW_BINARY:-}" && -x "${HERDR_DEVFLOW_BINARY}" ]]; then
  helper="$HERDR_DEVFLOW_BINARY"
elif [[ -x "$repo_root/bin/herdr-devflow" ]]; then
  helper="$repo_root/bin/herdr-devflow"
elif [[ -n "$devflow_installed" && -x "$devflow_installed" ]]; then
  helper="$devflow_installed"
fi
unset devflow_installed

# devflow_exec runs one subcommand against the helper and replaces this shell,
# so the helper's exit code and signals propagate without translation:
# 0 the operation completed, 1 GitHub could not answer, 2 the invocation was
# wrong.
devflow_exec() {
  if [[ -n "$helper" ]]; then
    exec "$helper" --repo-root "$repo_root" "$@"
  fi
  cd "$repo_root" || exit 1
  # Once the package path is supplied, remaining values are program arguments.
  # Do not insert a standalone `--`: Go passes that token through to the helper.
  exec go run ./tools/herdr-devflow/cmd/herdr-devflow --repo-root "$repo_root" "$@"
}
