#!/bin/bash

# Build the store plugin

set -e

echo "Building store plugin..."

# Generate boilerplate code from plugin.yaml
go generate

# Build the plugin as an executable (NOT as a shared library)
go build -o store main.go store_generated.go

echo "Store plugin built successfully: ./store"
