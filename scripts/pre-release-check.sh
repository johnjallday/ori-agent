#!/bin/bash
# Pre-release checklist automation
# Runs all quality checks, tests, and builds before release
# Usage: ./scripts/pre-release-check.sh [version]

set -e

VERSION=${1:-""}
FAILED_CHECKS=()

cd "$(dirname "$0")/.."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║     Ori Agent Pre-Release Checker         ║"
echo "╚════════════════════════════════════════════╝"
echo ""

if [ -n "$VERSION" ]; then
  echo "Target version: $VERSION"
  echo ""
fi

# Function to run check and track failures
run_check() {
  local name=$1
  local command=$2

  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo -e "${BLUE}Running: $name${NC}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"

  if eval "$command"; then
    echo -e "${GREEN}✅ $name: PASSED${NC}"
    echo ""
    return 0
  else
    echo -e "${RED}❌ $name: FAILED${NC}"
    echo ""
    FAILED_CHECKS+=("$name")
    return 1
  fi
}

# 1. CODE QUALITY CHECKS
echo ""
echo "════════════════════════════════════════════"
echo "1. CODE QUALITY CHECKS"
echo "════════════════════════════════════════════"
echo ""

run_check "Format Check" "make fmt" || true
run_check "Go Vet" "make vet" || true

# Check if golangci-lint is installed
if command -v golangci-lint &> /dev/null; then
  run_check "Lint Check" "make lint" || true
else
  echo -e "${YELLOW}⚠️  Lint Check: SKIPPED (golangci-lint not installed)${NC}"
  echo -e "${YELLOW}   Install with: make install-tools${NC}"
  echo ""
fi

# Check if govulncheck is installed
if command -v govulncheck &> /dev/null; then
  run_check "Security Scan" "make security" || true
else
  echo -e "${YELLOW}⚠️  Security Scan: SKIPPED (govulncheck not installed)${NC}"
  echo -e "${YELLOW}   Install with: make install-tools${NC}"
  echo ""
fi

# 2. UNIT TESTS
echo ""
echo "════════════════════════════════════════════"
echo "2. UNIT TESTS"
echo "════════════════════════════════════════════"
echo ""

run_check "Unit Tests" "make test-unit" || true

# 3. BUILD VERIFICATION
echo ""
echo "════════════════════════════════════════════"
echo "3. BUILD VERIFICATION"
echo "════════════════════════════════════════════"
echo ""

run_check "Build Server" "go build -o bin/ori-agent ./cmd/server" || true
run_check "Build Menubar (macOS)" "go build -o bin/ori-menubar ./cmd/menubar 2>/dev/null || echo 'Skipping menubar (not on macOS)'" || true
run_check "Build Plugins" "./scripts/build-plugins.sh" || true

# 4. DEPENDENCY CHECK
echo ""
echo "════════════════════════════════════════════"
echo "4. DEPENDENCY CHECK"
echo "════════════════════════════════════════════"
echo ""

run_check "Go Mod Verify" "go mod verify" || true
run_check "Go Mod Tidy" "go mod tidy && git diff --exit-code go.mod go.sum" || true

# 5. GIT STATUS CHECK
echo ""
echo "════════════════════════════════════════════"
echo "5. GIT STATUS CHECK"
echo "════════════════════════════════════════════"
echo ""

# Check for uncommitted changes
if git diff --quiet && git diff --cached --quiet; then
  echo -e "${GREEN}✅ Git Status: Clean${NC}"
  echo ""
else
  echo -e "${YELLOW}⚠️  Git Status: Uncommitted changes${NC}"
  echo ""
  echo "Modified files:"
  git status --short
  echo ""
  FAILED_CHECKS+=("Git Status - uncommitted changes")
fi

# Check current branch
CURRENT_BRANCH=$(git branch --show-current)
if [ "$CURRENT_BRANCH" = "main" ] || [ "$CURRENT_BRANCH" = "develop" ]; then
  echo -e "${GREEN}✅ Git Branch: $CURRENT_BRANCH${NC}"
  echo ""
else
  echo -e "${YELLOW}⚠️  Git Branch: $CURRENT_BRANCH (not main or develop)${NC}"
  echo ""
fi

# 6. SMOKE TESTS (OPTIONAL)
echo ""
echo "════════════════════════════════════════════"
echo "6. SMOKE TESTS (Optional)"
echo "════════════════════════════════════════════"
echo ""

read -p "Run smoke tests? (takes ~10 minutes) [y/N]: " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
  run_check "Smoke Tests" "./scripts/test-all-installers.sh" || true
else
  echo -e "${YELLOW}⚠️  Smoke Tests: SKIPPED (user choice)${NC}"
  echo ""
fi

# 7. SUMMARY
echo ""
echo "╔════════════════════════════════════════════╗"
echo "║           SUMMARY                          ║"
echo "╚════════════════════════════════════════════╝"
echo ""

if [ ${#FAILED_CHECKS[@]} -eq 0 ]; then
  echo -e "${GREEN}✅ All checks passed!${NC}"
  echo ""

  if [ -n "$VERSION" ]; then
    echo "Ready to release $VERSION"
    echo ""
    echo "Next steps:"
    echo "  1. git tag $VERSION"
    echo "  2. git push origin $VERSION"
    echo "  3. Monitor GitHub Actions: gh run watch"
    echo ""
  else
    echo "Ready to release!"
    echo ""
    echo "Next steps:"
    echo "  1. Update VERSION file"
    echo "  2. git tag v0.0.X"
    echo "  3. git push origin v0.0.X"
    echo ""
  fi

  exit 0
else
  echo -e "${RED}❌ ${#FAILED_CHECKS[@]} check(s) failed:${NC}"
  echo ""
  for check in "${FAILED_CHECKS[@]}"; do
    echo -e "${RED}  • $check${NC}"
  done
  echo ""
  echo "Please fix the issues above before releasing."
  echo ""
  exit 1
fi
