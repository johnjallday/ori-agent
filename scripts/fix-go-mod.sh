#!/bin/bash
# fix-go-mod.sh - Automatically fixes go.mod and go.sum issues
# This script runs go mod tidy and commits the changes

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
RED='\033[0;31m'
NC='\033[0m' # No Color

echo ""
echo -e "${BLUE}🔧 Fixing Go module dependencies...${NC}"
echo ""

# Check if go.mod exists
if [ ! -f "go.mod" ]; then
    echo -e "${RED}❌ go.mod not found${NC}"
    exit 1
fi

# Save current state
echo -e "${BLUE}[INFO]${NC} Saving current go.mod and go.sum state..."
cp go.mod go.mod.backup 2>/dev/null || true
cp go.sum go.sum.backup 2>/dev/null || true

# Run go mod tidy
echo -e "${BLUE}[INFO]${NC} Running go mod tidy..."
if go mod tidy; then
    echo -e "${GREEN}✅${NC} go mod tidy completed successfully"
else
    echo -e "${RED}❌${NC} go mod tidy failed"
    # Restore backups
    mv go.mod.backup go.mod 2>/dev/null || true
    mv go.sum.backup go.sum 2>/dev/null || true
    exit 1
fi

# Check if anything changed
if git diff --quiet go.mod go.sum 2>/dev/null; then
    echo -e "${GREEN}✅${NC} No changes needed - go.mod and go.sum are already tidy"
    rm -f go.mod.backup go.sum.backup
    exit 0
fi

# Show what changed
echo ""
echo -e "${BLUE}[INFO]${NC} Changes made to go.mod and go.sum:"
echo ""
git diff --stat go.mod go.sum

echo ""
echo -e "${BLUE}[INFO]${NC} Detailed changes:"
echo ""
git diff go.mod go.sum

# Clean up backups
rm -f go.mod.backup go.sum.backup

echo ""
echo -e "${GREEN}✅ Go module dependencies fixed successfully!${NC}"
echo ""
echo -e "${BLUE}[INFO]${NC} Changes have been made to:"
echo "  - go.mod"
echo "  - go.sum"
echo ""
echo -e "${YELLOW}[ACTION REQUIRED]${NC} These files need to be committed:"
echo "  git add go.mod go.sum"
echo "  git commit -m 'chore: update go module dependencies'"
echo ""
