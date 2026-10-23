#!/usr/bin/env bash
#
# branch-server.sh — run Ori from a branch checkout against a throwaway
# workspace directory that is EMPTIED on every start.
#
# branch-server.zsh (source it from ~/.zshrc) routes a plain `go run ./cmd/server`
# here whenever the current directory is an Ori checkout other than the shared
# dev worktree (ori-agent-dev) or a checkout on the dev or main branch. Those
# keep running the server exactly as typed, against their own data.
#
# Usage:
#   branch-server.sh applies
#       Exit 0 when the current directory should use the throwaway directory,
#       1 otherwise. Prints nothing.
#   branch-server.sh start [go args...]
#       Empty the throwaway directory, seed it, and exec `go <args>` with
#       ORI_DATA_DIR pointing inside it. Without args: `go run ./cmd/server`.
#
# Environment:
#   ORI_BRANCH_WORKSPACE_ROOT  throwaway directory; falls back to
#                              WT_TEST_WORKSPACE_DIR (the folder `wt new test`
#                              resets), then $HOME/Test
#   ORI_BRANCH_ONBOARDING=1    keep the onboarding wizard instead of marking it done
#   PORT, NO_BROWSER, ...      reach the server unchanged
#
# Layout after a start:
#   <root>/            workspace directory (settings.json workspace_root)
#   <root>/.ori-data/  data dir (ORI_DATA_DIR): settings.json, app_state.json,
#                      sessions.db, vaults, agent_state. Hidden, so folder scans
#                      of the workspace directory skip it.
#
# The directory is wiped without confirmation: never point it at real data, and
# never hand it to a demo or smoke sandbox (see "Smoke Testing" in CLAUDE.md).
set -euo pipefail

ORI_MODULE="github.com/johnjallday/ori-agent"
root="${ORI_BRANCH_WORKSPACE_ROOT:-${WT_TEST_WORKSPACE_DIR:-$HOME/Test}}"

die() {
	printf 'branch-server: %s\n' "$1" >&2
	exit 2
}

usage() {
	cat <<'USAGE'
usage: branch-server.sh applies
       branch-server.sh start [go args...]   (default: run ./cmd/server)

Runs Ori against a throwaway workspace directory (ORI_BRANCH_WORKSPACE_ROOT,
else WT_TEST_WORKSPACE_DIR, else $HOME/Test) that is emptied before every
start. Set ORI_BRANCH_ONBOARDING=1 to keep the onboarding wizard.
USAGE
}

# checkout_root prints the top level of the Ori checkout containing the current
# directory, or fails quietly when the directory is not inside one.
checkout_root() {
	local top
	top="$(git rev-parse --show-toplevel 2>/dev/null)" || return 1
	[[ -f "$top/go.mod" ]] || return 1
	grep -q "^module $ORI_MODULE\$" "$top/go.mod" || return 1
	printf '%s\n' "$top"
}

# applies: not the shared dev worktree, and not a checkout on dev or main.
applies() {
	local top branch
	top="$(checkout_root)" || return 1
	[[ "$(basename "$top")" != "ori-agent-dev" ]] || return 1
	branch="$(git -C "$top" branch --show-current 2>/dev/null || true)"
	case "$branch" in
	dev | main) return 1 ;;
	esac
	return 0
}

# reject_unsafe dies when a path is clearly not a throwaway folder: anything
# outside $HOME or $TMPDIR, and the home folders people keep real work in.
reject_unsafe() {
	local path="$1" tmp base
	tmp="${TMPDIR:-/tmp}"
	tmp="${tmp%/}"
	# Both spellings of each base: as given, and with symlinks resolved (on
	# macOS $TMPDIR lives behind /var -> /private/var).
	for base in "$HOME" "$(cd "$HOME" && pwd -P)"; do
		case "$path" in
		"$base" | "$base/Ori Workspaces" | "$base/Ori Workspaces/"* | "$base/Library" | "$base/Library/"* | "$base/Projects" | "$base/Projects/"* | "$base/Desktop" | "$base/Documents" | "$base/Downloads")
			die "refusing to wipe $path"
			;;
		esac
	done
	[[ ! -e "$path/.git" ]] || die "refusing to wipe $path: it is a git repository"
	for base in "$HOME" "$(cd "$HOME" && pwd -P)" "$tmp" "$(cd "$tmp" && pwd -P)"; do
		[[ "$path" != "$base/"* ]] || return 0
	done
	die "refusing to wipe $path: not inside \$HOME or \$TMPDIR"
}

# guard_root checks the configured directory as written, creates it if needed,
# then checks it again with symlinks resolved and leaves $root canonical.
guard_root() {
	[[ "$root" == /* ]] || die "ORI_BRANCH_WORKSPACE_ROOT must be absolute: $root"
	root="$(printf '%s' "$root" | tr -s '/')"
	root="${root%/}"
	reject_unsafe "$root"
	mkdir -p "$root"
	root="$(cd "$root" && pwd -P)"
	reject_unsafe "$root"
}

# seed writes the two files that make the server treat the throwaway directory
# as an already-confirmed workspace root with onboarding behind it.
seed() {
	local data="$root/.ori-data"
	local real_state="$HOME/Library/Application Support/OriAgent/app_state.json"
	local user_name="" assistant_name=""
	mkdir -p "$data"
	jq -n --arg root "$root" \
		'{workspace_root: $root, workspace_root_confirmed: true}' >"$data/settings.json"
	[[ "${ORI_BRANCH_ONBOARDING:-0}" != "1" ]] || return 0
	if [[ -r "$real_state" ]]; then
		user_name="$(jq -r '.user_name // empty' "$real_state" 2>/dev/null || true)"
		assistant_name="$(jq -r '.assistant_name // empty' "$real_state" 2>/dev/null || true)"
	fi
	jq -n --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		--arg user "$user_name" --arg assistant "$assistant_name" \
		'{onboarding: {completed: true, completed_at: $now, current_step: 3,
		               steps_completed: ["step-welcome", "step-model", "step-done"]}}
		 + (if $user == "" then {} else {user_name: $user} end)
		 + (if $assistant == "" then {} else {assistant_name: $assistant} end)' \
		>"$data/app_state.json"
}

start() {
	local top branch port
	command -v jq >/dev/null 2>&1 || die "jq is required (brew install jq)"
	top="$(checkout_root)" || die "run this from inside an Ori checkout (module $ORI_MODULE)"
	[[ $# -gt 0 ]] || set -- run ./cmd/server
	guard_root
	branch="$(git -C "$top" branch --show-current 2>/dev/null || true)"
	[[ -n "$branch" ]] || branch="$(git -C "$top" rev-parse --short HEAD 2>/dev/null || echo detached)"

	# Wipe every entry, hidden ones included. The directory itself stays, so an
	# open Finder window and the configured path remain valid.
	find "$root" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
	seed

	port="${PORT:-8765}"
	if lsof -iTCP:"$port" -sTCP:LISTEN -P -n >/dev/null 2>&1; then
		printf 'branch-server: port %s already has a listener; Ori stops an existing ori-agent on its port\n' "$port" >&2
	fi
	printf 'branch-server: %s @ %s\n' "$branch" "$top" >&2
	printf 'branch-server: workspace directory %s (emptied)\n' "$root" >&2
	printf 'branch-server: data dir %s/.ori-data\n' "$root" >&2
	exec env ORI_DATA_DIR="$root/.ori-data" go "$@"
}

case "${1:-}" in
applies) applies ;;
start)
	shift
	start "$@"
	;;
-h | --help | help | "") usage ;;
*) die "unknown command: $1 (applies | start)" ;;
esac
