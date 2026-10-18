#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The gosec pin in ci-local.sh must be the one CI's action is pinned to, or
# the local gate scans with a different analyzer set than the PR gate.
script_pin="$(sed -n 's/^GOSEC_VERSION="\([0-9.]*\)"$/\1/p' "$repo_root/scripts/ci-local.sh")"
ci_pin="$(grep -o 'pinned action/image: gosec [0-9.]*' "$repo_root/.github/workflows/ci.yml" | head -n1 | grep -o '[0-9.]*$')"
if [[ -z "$script_pin" || -z "$ci_pin" || "$script_pin" != "$ci_pin" ]]; then
  echo "gosec pin drift: scripts/ci-local.sh has '$script_pin', .github/workflows/ci.yml has '$ci_pin'" >&2
  exit 1
fi

# The README gate is selected from the paths the README scenes photograph.
# shellcheck source=scripts/ci-local.sh
source "$repo_root/scripts/ci-local.sh"
if ! ci_local_readme_needed "internal/web/static/js/modules/home.js"; then
  echo "a Home module change did not select the README gate" >&2
  exit 1
fi
if ! ci_local_readme_needed "docs/readme-screenshots.json"; then
  echo "a manifest change did not select the README gate" >&2
  exit 1
fi
if ci_local_readme_needed "internal/folderdigest/scan.go" "scripts/smoke.sh"; then
  echo "a backend-only change selected the README gate" >&2
  exit 1
fi
if ci_local_readme_needed; then
  echo "no changes selected the README gate" >&2
  exit 1
fi

# A dry run prints the plan in CI's order and executes nothing.
full="$(cd "$repo_root" && bash scripts/ci-local.sh --dry-run --no-fetch --readme)"
grep -q 'ci-local: gates: gofmt vet wails-modes lint-new unit-tests devtools test-js lint-js format-js character-assets gosec-new readme$' <<<"$full" || {
  echo "unexpected full plan:" >&2
  printf '%s\n' "$full" >&2
  exit 1
}
grep -q 'ci-local: dry run, nothing executed' <<<"$full"

quick="$(cd "$repo_root" && bash scripts/ci-local.sh --dry-run --no-fetch --quick)"
grep -q 'ci-local: gates: gofmt vet wails-modes lint-new test-unit devtools test-js lint-js format-js character-assets$' <<<"$quick" || {
  echo "unexpected quick plan:" >&2
  printf '%s\n' "$quick" >&2
  exit 1
}
grep -q 'readme gate skipped (--quick)' <<<"$quick"

skipped="$(cd "$repo_root" && bash scripts/ci-local.sh --dry-run --no-fetch --no-readme)"
grep -q 'readme gate skipped (--no-readme)' <<<"$skipped"
grep -q 'ci-local: gates: gofmt vet wails-modes lint-new unit-tests devtools test-js lint-js format-js character-assets gosec-new$' <<<"$skipped"

if (cd "$repo_root" && bash scripts/ci-local.sh --dry-run --bogus >/dev/null 2>&1); then
  echo "an unknown flag was accepted" >&2
  exit 1
fi

# The full gate delegates to CI's canonical coverage-enabled runner, rather
# than duplicating flags that can drift. Inherited hosted scope must not shard it.
unit_sandbox="$(mktemp -d "${TMPDIR:-/tmp}/ori-ci-unit-contract.XXXXXX")"
trap 'rm -rf "$unit_sandbox"' EXIT
mkdir "$unit_sandbox/scripts"
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' \
  '[[ $# == 0 && -z "${RUNNER_OS:-}${ORI_UNIT_SHARD:-}${ORI_UNIT_SHARD_COUNT:-}" ]]' \
  'printf "canonical full unit runner invoked\\n"' > "$unit_sandbox/scripts/run-unit-tests.sh"
chmod 700 "$unit_sandbox/scripts/run-unit-tests.sh"
unit_result="$(
  cd "$unit_sandbox"
  export RUNNER_OS=macOS ORI_UNIT_SHARD=2 ORI_UNIT_SHARD_COUNT=4
  ci_local_run_gate unit-tests origin/dev
)"
if [[ "$unit_result" != 'canonical full unit runner invoked' ]]; then
  echo "full unit gate did not invoke the unsharded canonical runner" >&2
  exit 1
fi

printf 'ci-local pins gosec to CI, selects the README gate, plans in CI order, and uses the full canonical unit runner\n'
