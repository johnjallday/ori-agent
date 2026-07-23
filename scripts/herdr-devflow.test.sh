#!/usr/bin/env bash
# Lightweight shell coverage for the thin entrypoint. Deeper behavior lives in
# Go tests with an injected Herdr command runner.
set -euo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

bash -n "$repo_root/scripts/herdr-devflow.sh"
go test ./tools/herdr-devflow/...
