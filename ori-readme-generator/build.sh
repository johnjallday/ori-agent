#!/bin/bash
set -e

echo "Building ori-readme-generator plugin..."

# Ensure dependencies
go mod tidy

# Build the plugin
go build -o readme-generator main.go

echo "✓ Plugin built successfully: readme-generator"
echo ""
echo "To install:"
echo "  1. Copy to ori-agent: cp readme-generator ../uploaded_plugins/"
echo "  2. Restart ori-agent"
echo ""
