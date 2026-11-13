#!/usr/bin/env bash
# Temporary (assistant-reads-projects 4.6): build a REAPER plugin candidate and
# run the parity test, which reads a corpus of project files through the
# candidate's real binary and through Ori's own reader and compares what each
# would store. Removed together with Ori's reader (task 7.1).
#
#   ./scripts/project-facts-parity.sh <clean candidate worktree> [go test flags]
set -euo pipefail

src="${1:-}"
if [[ -z "$src" ]]; then
	echo "usage: $0 <clean reaper-plugin candidate worktree> [go test flags]" >&2
	exit 2
fi
shift

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir" rev-parse --show-toplevel)"

# Builds the committed HEAD in a disposable export and verifies its checksum.
"$repo_root/scripts/reaper-demo.sh" artifact --reaper-source "$src"

revision="$(git -C "$src" rev-parse HEAD)"
work="$(mktemp -d "${TMPDIR:-/tmp}/ori-parity.XXXXXX")"
trap 'rm -rf -- "$work"' EXIT
git -C "$src" show "$revision:.ori-plugin/plugin.json" >"$work/plugin.json"

printf 'PARITY_CANDIDATE=%s\n' "$revision"
cd "$repo_root"
ORI_PROJECT_FACTS_PARITY_BIN="$repo_root/reaper-plugin-darwin-arm64" \
	ORI_PROJECT_FACTS_PARITY_MANIFEST="$work/plugin.json" \
	TZ=UTC go test -count=1 -v -run 'TestProjectFactsParity' "$@" ./internal/projectlibrary/
