#!/bin/bash
# Cross-platform build checker
# Catches build errors that only appear on other platforms (Linux, Windows)
# Run this locally before pushing to catch CI failures early

set -e

cd "$(dirname "$0")/.."

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║    Cross-Platform Build Checker            ║"
echo "╚════════════════════════════════════════════╝"
echo ""

FAILED=0

# Platforms to check (matches CI matrix)
PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "darwin/amd64"
  "darwin/arm64"
)

echo -e "${BLUE}Checking builds for ${#PLATFORMS[@]} platforms...${NC}"
echo ""

for platform in "${PLATFORMS[@]}"; do
  OS="${platform%/*}"
  ARCH="${platform#*/}"

  echo -n "  Building for $OS/$ARCH... "

  # Build with CGO disabled (matches CI)
  if CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH" go build -o /dev/null ./cmd/server 2>&1; then
    echo -e "${GREEN}OK${NC}"
  else
    echo -e "${RED}FAILED${NC}"
    FAILED=1

    # Show the actual error
    echo ""
    echo -e "${RED}Error details:${NC}"
    CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH" go build -o /dev/null ./cmd/server 2>&1 || true
    echo ""
  fi
done

echo ""

# Also check go vet for each platform
# Note: go vet ./... only checks our code, not dependencies
echo -e "${BLUE}Running go vet for each platform...${NC}"
echo ""

for platform in "${PLATFORMS[@]}"; do
  OS="${platform%/*}"
  ARCH="${platform#*/}"

  echo -n "  Vetting for $OS/$ARCH... "

  # Capture output and filter out dependency errors
  VET_OUTPUT=$(GOOS="$OS" GOARCH="$ARCH" go vet ./... 2>&1 || true)

  # Filter to only show errors from our code (github.com/johnjallday/ori-agent)
  OUR_ERRORS=$(echo "$VET_OUTPUT" | grep -E "^#? ?github\.com/johnjallday/ori-agent" || true)

  if [ -z "$OUR_ERRORS" ]; then
    echo -e "${GREEN}OK${NC}"
  else
    echo -e "${RED}FAILED${NC}"
    FAILED=1

    echo ""
    echo -e "${RED}Error details:${NC}"
    echo "$OUR_ERRORS"
    echo ""
  fi
done

echo ""

if [ $FAILED -eq 0 ]; then
  echo -e "${GREEN}✅ All platforms build successfully!${NC}"
  exit 0
else
  echo -e "${RED}❌ Some platforms failed to build${NC}"
  echo ""
  echo "Common issues:"
  echo "  - Build constraints (//go:build) missing or wrong"
  echo "  - Platform-specific code referenced from shared files"
  echo "  - Unused imports in platform-specific files"
  echo ""
  exit 1
fi
