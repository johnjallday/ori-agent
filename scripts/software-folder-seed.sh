#!/usr/bin/env bash
#
# software-folder-seed.sh — put disposable software-project folders where a
# sandboxed demo server's folder chips can reach them.
#
# Usage:
#   ./scripts/software-folder-seed.sh <sandbox-dir> [--ori]
#
# <sandbox-dir> is the demo server's HOME (the directory passed to
# scripts/demo-server.sh, or the SANDBOX= line it prints). A sandboxed server
# has the native folder dialog switched off, so folders are shown through the
# Documents, Desktop and Downloads chips, which resolve under its HOME:
#
#   Documents/ori-agent   (--ori) this repository's tracked files at HEAD, laid
#                         out as a linked Git worktree: one recognized project
#   Documents/widget      (without --ori) one checkout holding a Go module and a
#                         Node package: one recognized project
#   Desktop/api, site     two separate checkouts: a choice of project
#   Desktop/ori-agent-hotfix  (--ori) a sibling worktree of the same repository
#                         as Documents/ori-agent: another folder, another project
#   Downloads/scratchpad  a Node package with no Git: an unrelated project
#
# The fixtures come from internal/testutil/softwarefixture, the same trees the
# Go tests use. All Git metadata is inert text written by hand: no git command
# creates a repository, a worktree or a clone, and nothing is fetched. The one
# git command run is `git archive HEAD`, which only reads this repository.
#
# The script refuses any directory that is not under $TMPDIR, so it can never
# write into a real home folder. It prints the seeded paths as KEY=path lines.

set -euo pipefail

sandbox="${1:-}"
with_ori=0
if [[ "${2:-}" == "--ori" ]]; then with_ori=1; fi
if [[ -z "$sandbox" || "$sandbox" == --* ]]; then
	echo "usage: $(basename "$0") <sandbox-dir> [--ori]" >&2
	exit 2
fi

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
	echo "must run inside the Ori checkout" >&2
	exit 2
}

tmp_root="${TMPDIR:-/tmp}"
tmp_root="$(cd "${tmp_root%/}" && pwd -P)"
mkdir -p "$sandbox"
sandbox="$(cd "$sandbox" && pwd -P)"
case "$sandbox/" in
"$tmp_root"/*/) ;;
*)
	echo "refusing to seed $sandbox: it is not under \$TMPDIR ($tmp_root)" >&2
	exit 2
	;;
esac
if [[ -n "${HOME:-}" && "$sandbox" == "$(cd "$HOME" && pwd -P)" ]]; then
	echo "refusing to seed the real home folder" >&2
	exit 2
fi

staging="$sandbox/Staging/fixtures"
if [[ -e "$staging" ]]; then
	echo "$staging already exists; seed a fresh sandbox" >&2
	exit 2
fi
mkdir -p "$sandbox/Documents" "$sandbox/Desktop" "$sandbox/Downloads" "$sandbox/Staging"

(cd "$repo_root" && go run ./internal/testutil/softwarefixture/cmd/softwarefixture "$staging" >/dev/null)

mv "$staging/client-work/api" "$sandbox/Desktop/api"
mv "$staging/client-work/site" "$sandbox/Desktop/site"
mv "$staging/scratchpad" "$sandbox/Downloads/scratchpad"
echo "DESKTOP_API=$sandbox/Desktop/api"
echo "DESKTOP_SITE=$sandbox/Desktop/site"
echo "DOWNLOADS_SCRATCHPAD=$sandbox/Downloads/scratchpad"

if [[ "$with_ori" == "1" ]]; then
	checkout="$sandbox/Documents/ori-agent"
	main_git="$sandbox/Staging/ori-main/.git"
	admin="$main_git/worktrees/ori-agent"
	mkdir -p "$checkout" "$admin"
	git -C "$repo_root" archive HEAD | tar -x -C "$checkout"
	# A linked worktree: its .git is a file naming a directory in the main
	# checkout's Git directory, which points back at it and at the shared one.
	printf 'gitdir: %s\n' "$admin" >"$checkout/.git"
	printf 'ref: refs/heads/feature/demo\n' >"$admin/HEAD"
	printf '../..\n' >"$admin/commondir"
	printf '%s\n' "$checkout/.git" >"$admin/gitdir"
	printf 'ref: refs/heads/dev\n' >"$main_git/HEAD"
	printf '[core]\n\trepositoryformatversion = 0\n[remote "origin"]\n\turl = https://github.com/johnjallday/ori-agent.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n' >"$main_git/config"
	echo "DOCUMENTS_ORI=$checkout"

	# A sibling worktree of the same repository, on another branch, beside the
	# two unrelated checkouts: the same remote and history, a different folder.
	sibling="$sandbox/Desktop/ori-agent-hotfix"
	sibling_admin="$main_git/worktrees/ori-agent-hotfix"
	mkdir -p "$sibling" "$sibling_admin"
	git -C "$repo_root" archive HEAD | tar -x -C "$sibling"
	printf 'gitdir: %s\n' "$sibling_admin" >"$sibling/.git"
	printf 'ref: refs/heads/hotfix/demo\n' >"$sibling_admin/HEAD"
	printf '../..\n' >"$sibling_admin/commondir"
	printf '%s\n' "$sibling/.git" >"$sibling_admin/gitdir"
	echo "DESKTOP_ORI_SIBLING=$sibling"
else
	mv "$staging/widget" "$sandbox/Documents/widget"
	echo "DOCUMENTS_WIDGET=$sandbox/Documents/widget"
fi
