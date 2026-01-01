#!/bin/bash
# Run ori-agent server without go.work interference
GOWORK=off go run ./cmd/server "$@"
