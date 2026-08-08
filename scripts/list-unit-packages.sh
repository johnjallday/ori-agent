#!/usr/bin/env bash
set -euo pipefail

# Reusable Go unit-package selection contract, shared by `make test-unit` and
# CI so the two can never silently drift apart. This is the intended
# production/unit-test scope of the module and deliberately excludes:
#
#   - node_modules/... - frontend dependencies vendored into the repo tree.
#     `go list ./...` from the module root can traverse into a Go package
#     that happens to live inside a node_modules dependency (for example
#     node_modules/flatted/golang/pkg/flatted); those are not ours to test.
#   - tests/... and test/... - integration (tests/integration), end-to-end
#     (tests/e2e), user-workflow (tests/user/...), local-model smoke
#     (test/smoke/local-models), and their fixtures/helpers. Each has its own
#     explicit Make target (test-integration, test-e2e, test-user,
#     test-ollama) with its own opt-in/environment contract; none of them
#     are "unit" packages of ori-agent.
#
# Everything else - cmd/, internal/, tools/, scripts/ - is in scope,
# including tools/herdr-devflow, which is a separate tool built from this
# module but still unit-tested the same way.
#
# Usage:
#   list-unit-packages.sh              # every unit package (default)
#   list-unit-packages.sh --platform   # only the platform-conditional subset
#
# --platform narrows the list to packages whose behaviour can actually differ
# per GOOS, for CI legs that exist to cover a second platform rather than to
# re-run the whole suite. Measured 2026-08-07: the macOS leg of `Unit Tests`
# spent ~8.5 min re-running the identical 150 packages the ubuntu leg already
# covers, at 10x the per-minute rate - 88% of all macOS Actions minutes. The
# packages below are 22s of that.

usage() {
	printf 'usage: %s [--platform]\n' "$(basename "$0")" >&2
}

scope="all"
case "${1-}" in
"") ;;
--platform) scope="platform" ;;
-h | --help)
	usage
	exit 0
	;;
*)
	printf 'unknown argument: %s\n' "$1" >&2
	usage
	exit 2
	;;
esac

# Shared exclusions, applied to whichever candidate list was produced above.
filter_out_of_scope() {
	grep -v '/node_modules/' |
		grep -vE '^github\.com/johnjallday/ori-agent/(tests|test)(/|$)'
}

if [[ "$scope" == "all" ]]; then
	go list ./... | filter_out_of_scope
	exit 0
fi

# A package is platform-conditional when the toolchain had to *exclude* one of
# its files for the host GOOS - that is exactly the set with _darwin/_windows/
# _unix variants or build-tagged stubs (internal/platform, internal/location,
# internal/nativemenubar, cmd/menubar, the herdr-devflow wake stack, ...).
# Deriving it means a package that grows a platform variant later is picked up
# without anyone remembering to edit this list.
#
# Packages whose *sources* are platform-neutral but whose *tests* are gated on
# darwin have to be named explicitly - `go list` cannot see inside a
# `runtime.GOOS` check. Add one here when you write a test that skips unless it
# is running on macOS. Tests gated the other way (skip on Windows, i.e. any
# darwin-or-linux behaviour such as the path-based trash round trip in
# internal/workspace and internal/sessionhttp) do not belong here: the ubuntu
# leg already exercises that path, and the darwin-specific half of it lives in
# internal/platform, which is derived above.
darwin_gated_tests=(
	github.com/johnjallday/ori-agent/cmd/server
	github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/app
)

{
	go list -e -f '{{if or .IgnoredGoFiles .IgnoredOtherFiles}}{{.ImportPath}}{{end}}' ./...
	printf '%s\n' "${darwin_gated_tests[@]}"
} | awk 'NF' | filter_out_of_scope | sort -u
