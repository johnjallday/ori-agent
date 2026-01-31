#!/bin/bash
# Run ori-agent server using the local workspace, even if GOWORK is globally set to off.
GOWORK="$(cd "$(dirname "$0")" && pwd)/go.work" go run ./cmd/server "$@"
