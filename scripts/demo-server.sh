#!/usr/bin/env bash
#
# demo-server.sh — build the current worktree and serve it from an ISOLATED
# demo sandbox, exactly like `wt demo`, but as a plain script so an agent can
# run it as a tracked background process (see "Smoke Testing" in CLAUDE.md).
#
# Usage:
#   ./scripts/demo-server.sh [--rev REV] [--open] [--native-picker] [--managed-restart] [port] [sandbox_dir]
#
# --rev REV serves a build of another commit instead of the working tree, so a
# failing browser test can be checked against its baseline (for example the
# branch's merge base) without creating a Git worktree. The commit is exported
# with `git archive` and built once under $TMPDIR/ori-rev-<sha>/.
#
# Demo servers do not open a browser by default. Pass --open or set
# ORI_DEMO_OPEN=1 to opt in. --native-picker separately opts in to desktop
# dialogs for explicit file/folder picker demos; it never opens a browser.
#
# Both HOME and ORI_DATA_DIR are redirected into the sandbox, and the server is
# started from INSIDE it so the plugin store is isolated too. Nothing is ever
# written under the real $HOME.
#
# The sandbox path is printed on the first line as `SANDBOX=<path>` so the
# caller can clean it up with a single `rm -rf` of a temp path. The sandbox is
# never removed for you, but it records this worktree in `.ori-demo-owner`, so
# `wt done` offers to remove it with the worktree once no server runs from it.

set -euo pipefail

rev=""
open_browser="${ORI_DEMO_OPEN:-0}"
native_picker=0
managed_restart="${ORI_DEMO_MANAGED_RESTART:-0}"
while [[ "${1:-}" == --* ]]; do
	case "$1" in
	--rev)
		rev="${2:-}"
		[[ -n "$rev" ]] || {
			echo "--rev needs a commit" >&2
			exit 2
		}
		shift 2
		;;
	--open)
		open_browser=1
		shift
		;;
	--native-picker)
		native_picker=1
		shift
		;;
	--managed-restart)
		managed_restart=1
		shift
		;;
	*)
		echo "unknown option: $1" >&2
		exit 2
		;;
	esac
done

port="${1:-8931}"
sandbox="${2:-}"

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
	echo "must run inside a git worktree" >&2
	exit 2
}
cd "$repo_root"

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"

if [[ -n "$rev" ]]; then
	sha="$(git rev-parse --verify --quiet "${rev}^{commit}" || true)"
	[[ -n "$sha" ]] || {
		echo "unknown commit: $rev" >&2
		exit 2
	}
	build_root="$tmp_root/ori-rev-${sha:0:12}"
	binary="$build_root/ori-agent"
	if [[ ! -x "$binary" ]]; then
		mkdir -p "$build_root/src"
		git archive "$sha" | tar -x -C "$build_root/src"
		(cd "$build_root/src" && go build -o "$binary" ./cmd/server)
	fi
	label="rev ${sha:0:12}"
else
	go build -o bin/ori-agent ./cmd/server
	binary="$repo_root/bin/ori-agent"
	label="$(git branch --show-current)"
fi

if [[ -z "$sandbox" ]]; then
	sandbox="$(mktemp -d "$tmp_root/ori-demo.XXXXXX")"
fi
mkdir -p "$sandbox"
# A sandbox reused from another run keeps the worktree it first recorded.
owner_record="$sandbox/.ori-demo-owner"
if [[ ! -e "$owner_record" && ! -L "$owner_record" ]]; then
	printf 'worktree=%s\n' "$(pwd -P)" >"$owner_record"
fi

echo "SANDBOX=$sandbox"
echo "BRANCH=$label"
echo "URL=http://localhost:$port"

export ORI_DEMO_REQUIRE_FREE_PORT=1
cd "$sandbox"
# A search-first demo needs HOME and app records in different physical roots:
# never make the search validator special-case a folder inside ORI_DATA_DIR.
# Leave WORKSPACE_DIR unset in this mode (it is explicit setup consent).
demo_home="$sandbox"
demo_data="$sandbox"
if [[ "${ORI_DEMO_SPLIT_PROFILE:-0}" == 1 ]]; then
	[[ -z "${WORKSPACE_DIR:-}" && ! -L home && ! -L data ]] || { echo 'split profile requires isolated home/data and no WORKSPACE_DIR' >&2; exit 2; }
	demo_home="$(pwd -P)/home"
	demo_data="$(pwd -P)/data"
	mkdir -m 700 -p "$demo_home"
fi
desktop_off=1
if [[ "$native_picker" == "1" ]]; then desktop_off=0; fi
reset_launch_args=()
if [[ "$managed_restart" == "1" ]]; then reset_launch_args=(-managed-restart); fi
if [[ "$open_browser" == "1" ]]; then
	exec env -u NO_BROWSER HOME="$demo_home" XDG_CONFIG_HOME="$demo_home/.config" ORI_DATA_DIR="$demo_data" PORT="$port" ORI_NO_DESKTOP_OPEN="$desktop_off" "$binary" ${reset_launch_args[@]+"${reset_launch_args[@]}"}
fi
exec env HOME="$demo_home" XDG_CONFIG_HOME="$demo_home/.config" ORI_DATA_DIR="$demo_data" PORT="$port" NO_BROWSER=1 ORI_NO_DESKTOP_OPEN="$desktop_off" "$binary" ${reset_launch_args[@]+"${reset_launch_args[@]}"}
