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
go list ./... | grep -v '/node_modules/' | grep -vE '^github\.com/johnjallday/ori-agent/(tests|test)(/|$)'
