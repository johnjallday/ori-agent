#!/bin/bash

# Dolphin Agent Desktop Build Script
# Builds the Wails desktop application with proper tags

set -e

echo "🖥️  Building Dolphin Agent Desktop..."
echo ""

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Sync frontend assets first
echo "${BLUE}Syncing frontend assets...${NC}"
./sync-frontend.sh

# Build for current platform using go build with wails tags
echo ""
echo "${BLUE}Building for current platform...${NC}"
cd cmd/wails
CGO_LDFLAGS="-framework UniformTypeIdentifiers" go build -tags desktop -ldflags="-s -w" -o ../../build/bin/dolphin-desktop .
cd ../..

if [ $? -eq 0 ]; then
    echo ""
    echo "${GREEN}✅ Build successful!${NC}"
    echo "📦 Binary: ./build/bin/dolphin-desktop"
    echo ""
    echo "Usage:"
    echo "  ./build/bin/dolphin-desktop    - Run desktop app"
    echo ""
    echo "Or run in development mode:"
    echo "  CGO_LDFLAGS=\"-framework UniformTypeIdentifiers\" go run -tags dev cmd/wails/main.go"
    echo ""
else
    echo "❌ Build failed!"
    exit 1
fi
