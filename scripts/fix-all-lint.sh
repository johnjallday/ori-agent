#!/bin/bash
# Automated Lint Fixing Workflow
# Combines golangci-lint auto-fix with AI-powered fixes for complex issues
# Usage: ./scripts/fix-all-lint.sh

set -e
cd "$(dirname "$0")/.."

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║     Automated Lint Fixing Workflow        ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# Check if golangci-lint is installed
GOLANGCI_LINT=""
if command -v golangci-lint &> /dev/null; then
  GOLANGCI_LINT="golangci-lint"
elif [ -x "$HOME/go/bin/golangci-lint" ]; then
  GOLANGCI_LINT="$HOME/go/bin/golangci-lint"
else
  echo -e "${RED}❌ golangci-lint not found${NC}"
  echo -e "${YELLOW}Install with: make install-tools${NC}"
  exit 1
fi

# Step 1: Auto-fix what we can
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 1: Running golangci-lint auto-fix${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

$GOLANGCI_LINT run ./... --fix || true

echo ""
echo -e "${GREEN}✓ Auto-fix complete${NC}"
echo ""

# Step 2: Check if issues remain
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 2: Checking for remaining issues${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Create temp file for errors
TEMP_ERRORS=$(mktemp)
trap "rm -f $TEMP_ERRORS" EXIT

if $GOLANGCI_LINT run ./... > "$TEMP_ERRORS" 2>&1; then
  echo -e "${GREEN}✅ All lint issues resolved by auto-fix!${NC}"
  echo ""
  exit 0
fi

# Step 3: Show remaining issues
echo -e "${YELLOW}Some issues require AI assistance:${NC}"
echo ""
cat "$TEMP_ERRORS"
echo ""

# Step 4: Offer AI-powered fixing
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 3: AI-Powered Fix${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

read -p "Use Claude Code to fix remaining issues? [y/N]: " -n 1 -r
echo ""

if [[ ! $REPLY =~ ^[Yy]$ ]]; then
  echo -e "${YELLOW}Manual review required.${NC}"
  echo ""
  echo "Remaining errors saved to: $TEMP_ERRORS"
  echo "To fix manually, review the errors above and run:"
  echo "  golangci-lint run ./... --fix"
  echo ""
  # Don't delete temp file if user wants to review
  trap - EXIT
  exit 1
fi

# Create prompt for Claude
PROMPT_FILE=$(mktemp)
trap "rm -f $TEMP_ERRORS $PROMPT_FILE" EXIT

cat > "$PROMPT_FILE" <<EOF
Fix all golangci-lint errors in the ori-agent project.

Here are the remaining lint errors after auto-fix:

$(cat "$TEMP_ERRORS")

Please:
1. Read each file that has errors
2. Fix all lint violations following Go best practices
3. Common fixes needed:
   - Remove unused variables and imports
   - Add proper error handling
   - Fix ineffectual assignments
   - Address style violations (gofmt, goimports)
   - Fix any shadowed variables
   - Add missing comments for exported functions/types
4. After making fixes, run: go test ./... -short
5. Verify the fixes don't break tests
6. Summarize what was fixed

IMPORTANT: Only fix the specific issues mentioned. Don't refactor or add features.
EOF

echo -e "${BLUE}Launching Claude Code to fix issues...${NC}"
echo ""

# Check if claude CLI is available
if ! command -v claude &> /dev/null; then
  echo -e "${RED}❌ Claude Code CLI not found${NC}"
  echo -e "${YELLOW}Please install Claude Code from: https://claude.ai/code${NC}"
  exit 1
fi

# Launch Claude Code with the prompt
if claude -p "$(cat "$PROMPT_FILE")" --permission-mode acceptEdits; then
  echo ""
  echo -e "${GREEN}✓ Claude Code finished${NC}"
  echo ""

  # Step 5: Verify fixes
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo -e "${BLUE}Step 4: Verifying fixes${NC}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo ""

  echo "Running lint check..."
  if $GOLANGCI_LINT run ./...; then
    echo ""
    echo -e "${GREEN}✅ All lint issues resolved!${NC}"
    echo ""
  else
    echo ""
    echo -e "${YELLOW}⚠️  Some issues may remain. Review above.${NC}"
    echo ""
  fi

  echo "Running tests to verify nothing broke..."
  if go test ./... -short; then
    echo ""
    echo -e "${GREEN}✅ Tests passed!${NC}"
    echo ""
  else
    echo ""
    echo -e "${RED}❌ Tests failed after fixes${NC}"
    echo -e "${YELLOW}You may need to review the changes${NC}"
    echo ""
  fi
else
  echo ""
  echo -e "${RED}❌ Claude Code execution failed${NC}"
  exit 1
fi

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║              COMPLETE                      ║"
echo "╚════════════════════════════════════════════╝"
echo ""
