#!/bin/bash
# Human entry point: dispatch the RC lifecycle, never tag or merge local work.
set -euo pipefail

usage() {
  printf '%s\n' \
    'Usage:' \
    '  ./scripts/release.sh candidate [--force] [--yes]' \
    '  ./scripts/release.sh candidate vX.Y.Z [--yes]  # next RC after branch fixes' \
    '  ./scripts/release.sh promote vX.Y.Z-rc.N [--yes]' \
    '' \
    'candidate prepares a frozen release branch; dev stays open.' \
    'promote confirms you tested that exact RC and requests stable publication.' \
    'Commands dispatch GitHub Actions on main; they do not release local changes.' \
    '--yes explicitly confirms the selected action in non-interactive use.' \
    'See docs/RELEASE_CHECKLIST.md for testing, promotion and merge-back.'
}

command="${1:-candidate}"
if [[ "$command" == --help || "$command" == -h ]]; then usage; exit 0; fi
# Keep the old candidate spelling, but no direct-stable/skip-checks escape hatch.
[[ "$command" != --pre-release && "$command" != --prerelease ]] || command=candidate
[[ $# -eq 0 ]] || shift
version=""
force=false
assume_yes=false
for arg in "$@"; do
  case "$arg" in
    --yes) assume_yes=true ;;
    --force) force=true ;;
    -*) usage >&2; exit 2 ;;
    *) [[ -z "$version" ]] || { usage >&2; exit 2; }; version="$arg" ;;
  esac
done
case "$command" in
  candidate)
    if [[ -n "$version" ]] && ! [[ "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
      usage >&2; exit 2
    fi
    if [[ -n "$version" && "$force" == true ]]; then usage >&2; exit 2; fi
    workflow=auto-release.yml
    args=(-f "force=$force" -f "candidate=$version")
    printf 'Prepare candidate: %s (force cadence: %s). No stable publication.\n' "${version:-new batch}" "$force"
    ;;
  promote)
    if [[ "$force" == true ]] || ! [[ "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.[1-9][0-9]*$ ]]; then
      usage >&2; exit 2
    fi
    workflow=promote-release.yml
    args=(-f "rc_tag=$version" -f confirm_tested=true)
    printf 'Confirm you completed and reviewed the test report for %s and approve publishing it as stable.\n' "$version"
    ;;
  *) usage >&2; exit 2 ;;
esac
if [[ "$assume_yes" != true ]]; then
  [[ -t 0 ]] || { echo 'Refusing without a terminal; pass --yes to confirm.' >&2; exit 2; }
  read -r -p 'Dispatch this action? [y/N]: ' answer
  [[ "$answer" == y || "$answer" == Y ]] || { echo 'Cancelled.'; exit 0; }
fi
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."
gh workflow run "$workflow" --ref main "${args[@]}"
printf 'Dispatched. Inspect with: gh run list --workflow %s\n' "$workflow"
