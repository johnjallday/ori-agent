#!/bin/bash
# update-readme.sh - Automatically updates version badges in README.md
# This script updates the version and Go version badges based on VERSION file and go.mod

set -e

# Get the script directory and project directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Change to project directory
cd "$PROJECT_DIR" || exit 1

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo ""
echo -e "${BLUE}📝 Updating README.md with latest versions...${NC}"
echo ""

# Check if README.md exists
if [ ! -f "README.md" ]; then
    echo -e "${YELLOW}⚠️  README.md not found${NC}"
    exit 1
fi

# Read current version from VERSION file
if [ -f "VERSION" ]; then
    VERSION=$(cat VERSION | tr -d '[:space:]')
    echo -e "${BLUE}[INFO]${NC} Found version: $VERSION"
else
    echo -e "${YELLOW}⚠️  VERSION file not found, skipping version update${NC}"
    VERSION=""
fi

# Get installed Go version (e.g., 1.25.5)
if command -v go &> /dev/null; then
    GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
    echo -e "${BLUE}[INFO]${NC} Found Go version: $GO_VERSION"
else
    echo -e "${YELLOW}⚠️  Go not installed, skipping Go version update${NC}"
    GO_VERSION=""
fi

# Update version badge if VERSION exists
if [ -n "$VERSION" ]; then
    # Match the Version badge URL anywhere in README; works regardless of line layout
    perl -i -pe 's|!\[Version\]\(https://img\.shields\.io/badge/Version-[^)]+\)|![Version](https://img.shields.io/badge/Version-'"$VERSION"'-blue)|' README.md
    echo -e "${GREEN}✅${NC} Updated version badge to $VERSION"
fi

# Update Go version badge if GO_VERSION exists
if [ -n "$GO_VERSION" ]; then
    perl -i -pe 's|!\[Go\]\(https://img\.shields\.io/badge/Go-[^)]+\)|![Go](https://img.shields.io/badge/Go-'"$GO_VERSION"'-00add8)|' README.md
    echo -e "${GREEN}✅${NC} Updated Go version badge to $GO_VERSION"
fi

echo ""
echo -e "${GREEN}✅ README.md updated successfully!${NC}"
echo ""

# Show the updated badges
echo -e "${BLUE}Updated badges:${NC}"
grep -E "!\[Version\]\(https://img\.shields\.io/badge/Version-" README.md
grep -E "!\[Go\]\(https://img\.shields\.io/badge/Go-" README.md
echo ""
