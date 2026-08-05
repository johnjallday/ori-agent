#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
lister="$script_dir/list-unit-packages.sh"

output="$(cd "$repo_root" && "$lister")"

assert_excluded() {
	local pattern="$1"
	if grep -q "$pattern" <<<"$output"; then
		printf 'Expected %s to be excluded from the unit-package selection.\n' "$pattern" >&2
		exit 1
	fi
}

assert_included() {
	local pattern="$1"
	if ! grep -q "$pattern" <<<"$output"; then
		printf 'Expected %s to be included in the unit-package selection.\n' "$pattern" >&2
		exit 1
	fi
}

# Characterizes the exact bugs this selection contract fixes: root `go list
# ./...` can traverse into vendored frontend dependencies, and a bare
# `/tests$` suffix filter matches none of the nested integration/e2e/user
# suites because they all live one or more directories below tests/.
assert_excluded 'node_modules'
assert_excluded 'ori-agent/tests/e2e$'
assert_excluded 'ori-agent/tests/integration$'
assert_excluded 'ori-agent/tests/fixtures$'
assert_excluded 'ori-agent/tests/helpers$'
assert_excluded 'ori-agent/tests/user/scenarios$'
assert_excluded 'ori-agent/tests/user/workflows$'
assert_excluded 'ori-agent/test/smoke/local-models$'

# Intended unit-test scope must survive: production packages, and the
# separate herdr-devflow tool built from this module (including the
# package that owns the Unix-socket fixtures).
assert_included 'ori-agent/internal/llm$'
assert_included 'ori-agent/cmd/server$'
assert_included 'ori-agent/tools/herdr-devflow/internal/herdr$'

printf 'list-unit-packages tests passed\n'
