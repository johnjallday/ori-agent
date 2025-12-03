#!/bin/bash

# Script to migrate all log.Printf calls to structured logging

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "🔍 Finding files with log.Printf..."

# Find all Go files with log.Printf (excluding vendor, test files can be included)
FILES=$(grep -rl "log\.Printf" "$PROJECT_DIR/internal" "$PROJECT_DIR/cmd" 2>/dev/null | grep "\.go$" || true)

if [ -z "$FILES" ]; then
    echo "✅ No files with log.Printf found!"
    exit 0
fi

echo "Found $(echo "$FILES" | wc -l | tr -d ' ') files to migrate"
echo ""

# Build migration tool
echo "🔨 Building migration tool..."
cd "$SCRIPT_DIR"
go build -o migrate-logging migrate-logging.go
if [ $? -ne 0 ]; then
    echo "❌ Failed to build migration tool"
    exit 1
fi

# Migrate each file
for file in $FILES; do
    echo "Processing: $file"
    "$SCRIPT_DIR/migrate-logging" "$file"
done

echo ""
echo "🔨 Building to verify changes..."
cd "$PROJECT_DIR"
if go build -o bin/ori-agent ./cmd/server; then
    echo "✅ Build successful! All migrations completed."
else
    echo "❌ Build failed. Please review the changes."
    exit 1
fi
