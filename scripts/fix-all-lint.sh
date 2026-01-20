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

# Use --max-same-issues 0 to show ALL errors, not just first 3 of each type
$GOLANGCI_LINT run ./... --fix --max-same-issues 0 --max-issues-per-linter 0 || true

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

if $GOLANGCI_LINT run ./... --max-same-issues 0 --max-issues-per-linter 0 > "$TEMP_ERRORS" 2>&1; then
  echo -e "${GREEN}✅ All lint issues resolved by auto-fix!${NC}"
  echo ""
  exit 0
fi

# Step 3: Show remaining issues
echo -e "${YELLOW}Some issues require AI assistance:${NC}"
echo ""
cat "$TEMP_ERRORS"
echo ""

# Step 2.5: Attempt automatic fixes for common orihttp errcheck patterns
# Enable with: FIX_ORIHTTP_ERRCHECK=1 ./scripts/fix-all-lint.sh
if [ "${FIX_ORIHTTP_ERRCHECK:-}" = "1" ] && [ -f "./scripts/fix-orihttp-errcheck.go" ]; then
  if rg -q "errcheck" "$TEMP_ERRORS" && rg -q "orihttp\\.Respond" "$TEMP_ERRORS"; then
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}Step 2.5: Fixing orihttp errcheck patterns${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""

    mapfile -t ERR_FILES < <(rg -o "^[^:]+\\.go" "$TEMP_ERRORS" | sort -u)
    if [ ${#ERR_FILES[@]} -gt 0 ]; then
      go run ./scripts/fix-orihttp-errcheck.go -w "${ERR_FILES[@]}" || true
      gofmt -w "${ERR_FILES[@]}" || true
    fi

    echo ""
    echo "Re-running lint after orihttp errcheck fix..."
    if $GOLANGCI_LINT run ./... --max-same-issues 0 --max-issues-per-linter 0 > "$TEMP_ERRORS" 2>&1; then
      echo -e "${GREEN}✅ All lint issues resolved!${NC}"
      echo ""
      exit 0
    fi

    echo ""
    echo -e "${YELLOW}Some issues remain after orihttp errcheck fix:${NC}"
    echo ""
    cat "$TEMP_ERRORS"
    echo ""
  fi
fi

# Step 4: AI-powered fixing (automatic)
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 3: AI-Powered Fix${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Create prompt for OpenCode
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
# -p runs non-interactively, --permission-mode acceptEdits allows file edits without prompting
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
  if $GOLANGCI_LINT run ./... --max-same-issues 0 --max-issues-per-linter 0; then
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

: <<'OPENCODE_DISABLED'
echo -e "${BLUE}Launching OpenCode to fix issues...${NC}"
echo ""

# Check if opencode CLI is available
if ! command -v opencode &> /dev/null; then
  echo -e "${RED}❌ OpenCode CLI not found${NC}"
  echo -e "${YELLOW}Please install OpenCode from: https://opencode.ai${NC}"
  exit 1
fi

# Launch OpenCode with the prompt
if opencode run "$(cat "$PROMPT_FILE")"; then
  echo ""
  echo -e "${GREEN}✓ OpenCode finished${NC}"
  echo ""

  # Step 5: Verify fixes
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo -e "${BLUE}Step 4: Verifying fixes${NC}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo ""

  echo "Running lint check..."
  if $GOLANGCI_LINT run ./... --max-same-issues 0 --max-issues-per-linter 0; then
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
  echo -e "${RED}❌ OpenCode execution failed${NC}"
  exit 1
fi
OPENCODE_DISABLED

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║              COMPLETE                      ║"
echo "╚════════════════════════════════════════════╝"
echo ""
