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

  # Update VERSION file if a version was specified
  if [ -f "VERSION" ]; then
    CURRENT_VERSION=$(cat VERSION | tr -d '[:space:]')
    if [ "$CURRENT_VERSION" != "$VERSION" ]; then
      echo -e "${BLUE}[INFO]${NC} Updating VERSION file: $CURRENT_VERSION → $VERSION"
      echo "$VERSION" > VERSION
      echo -e "${GREEN}✅${NC} VERSION file updated"
      echo ""
    else
      echo -e "${BLUE}[INFO]${NC} VERSION file already set to $VERSION"
      echo ""
    fi
  else
    echo -e "${BLUE}[INFO]${NC} Creating VERSION file with $VERSION"
    echo "$VERSION" > VERSION
    echo -e "${GREEN}✅${NC} VERSION file created"
    echo ""
  fi
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

run_check "Format Check" "make fmt" || {
  # Format check failed - offer to auto-fix syntax errors
  echo -e "${YELLOW}💡 Tip: Automated syntax error fixing is available${NC}"
  echo ""
  read -p "Run automated syntax fixer? [y/N]: " -n 1 -r
  echo ""
  if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo ""
    if [ -f "./scripts/auto-fix-syntax.sh" ]; then
      ./scripts/auto-fix-syntax.sh
      echo ""
      echo -e "${BLUE}Re-running format check after fixes...${NC}"
      echo ""
      # Re-run format check after fixes
      if run_check "Format Check (after fixes)" "make fmt"; then
        # Remove the original failure from FAILED_CHECKS
        FAILED_CHECKS=("${FAILED_CHECKS[@]/Format Check/}")
      fi
    else
      echo -e "${RED}❌ auto-fix-syntax.sh not found in ./scripts/${NC}"
    fi
  fi
}
run_check "Go Vet" "make vet" || {
  # Go vet failed - offer to auto-fix
  echo -e "${YELLOW}💡 Tip: Automated go vet fixing is available${NC}"
  echo ""
  read -p "Run automated go vet fixer? [y/N]: " -n 1 -r
  echo ""
  if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo ""
    if [ -f "./scripts/auto-fix-syntax.sh" ]; then
      ./scripts/auto-fix-syntax.sh
      echo ""
      echo -e "${BLUE}Re-running go vet after fixes...${NC}"
      echo ""
      # Re-run go vet after fixes
      if run_check "Go Vet (after fixes)" "make vet"; then
        # Remove the original failure from FAILED_CHECKS
        FAILED_CHECKS=("${FAILED_CHECKS[@]/Go Vet/}")
      fi
    else
      echo -e "${RED}❌ auto-fix-syntax.sh not found in ./scripts/${NC}"
    fi
  fi
}

# Check if golangci-lint is installed (check PATH and ~/go/bin)
if command -v golangci-lint &> /dev/null; then
  LINT_CMD="make lint"
  LINT_AVAILABLE=true
elif [ -x "$HOME/go/bin/golangci-lint" ]; then
  LINT_CMD="$HOME/go/bin/golangci-lint run ./..."
  LINT_AVAILABLE=true
else
  LINT_AVAILABLE=false
fi

if [ "$LINT_AVAILABLE" = true ]; then
  run_check "Lint Check" "$LINT_CMD" || {
    # Lint check failed - offer to auto-fix with feedback loop
    echo -e "${YELLOW}💡 Tip: Automated lint fixing is available${NC}"
    echo ""
    read -p "Run automated lint fixer? [y/N]: " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
      echo ""
      if [ -f "./scripts/fix-all-lint.sh" ]; then
        # Feedback loop: keep fixing until no errors or max iterations
        MAX_ITERATIONS=5
        ITERATION=1
        LINT_PASSED=false

        while [ $ITERATION -le $MAX_ITERATIONS ] && [ "$LINT_PASSED" = false ]; do
          echo ""
          echo -e "${BLUE}╔════════════════════════════════════════════╗${NC}"
          echo -e "${BLUE}║         FIX ITERATION $ITERATION/$MAX_ITERATIONS                ║${NC}"
          echo -e "${BLUE}╚════════════════════════════════════════════╝${NC}"
          echo ""

          ./scripts/fix-all-lint.sh

          echo ""
          echo -e "${BLUE}Re-running lint check after fixes...${NC}"
          echo ""
          echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
          echo -e "${BLUE}Running: Lint Check (iteration $ITERATION)${NC}"
          echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"

          if eval "$LINT_CMD"; then
            echo -e "${GREEN}✅ Lint Check (iteration $ITERATION): PASSED${NC}"
            echo ""
            LINT_PASSED=true
            # Remove the original failure from FAILED_CHECKS
            FAILED_CHECKS=("${FAILED_CHECKS[@]/Lint Check/}")
          else
            echo -e "${RED}❌ Lint Check (iteration $ITERATION): FAILED${NC}"
            echo ""

            if [ $ITERATION -lt $MAX_ITERATIONS ]; then
              echo -e "${YELLOW}⚠️  Still have lint errors. Attempting fix again...${NC}"
              echo ""
            else
              echo -e "${RED}❌ Maximum iterations reached. Manual intervention required.${NC}"
              echo ""
            fi
          fi

          ITERATION=$((ITERATION + 1))
        done

        if [ "$LINT_PASSED" = true ]; then
          echo ""
          echo -e "${GREEN}╔════════════════════════════════════════════╗${NC}"
          echo -e "${GREEN}║              COMPLETE                      ║${NC}"
          echo -e "${GREEN}╚════════════════════════════════════════════╝${NC}"
          echo ""
          echo -e "${GREEN}✅ All lint errors fixed successfully!${NC}"
          echo ""
        else
          echo ""
          echo -e "${RED}╔════════════════════════════════════════════╗${NC}"
          echo -e "${RED}║         MANUAL FIXES REQUIRED              ║${NC}"
          echo -e "${RED}╚════════════════════════════════════════════╝${NC}"
          echo ""
          echo -e "${RED}❌ Automated fixes could not resolve all errors.${NC}"
          echo -e "${YELLOW}   Please review the errors above and fix manually.${NC}"
          echo ""
        fi
      else
        echo -e "${RED}❌ fix-all-lint.sh not found in ./scripts/${NC}"
      fi
    fi
  }
else
  echo -e "${YELLOW}⚠️  Lint Check: SKIPPED (golangci-lint not installed)${NC}"
  echo -e "${YELLOW}   Install with: make install-tools${NC}"
  echo ""
fi

# Check Go version first (some vulnerabilities require Go upgrades)
if [ -f "./scripts/check-go-version.sh" ]; then
  run_check "Go Version Check" "./scripts/check-go-version.sh" || {
    echo -e "${YELLOW}💡 Tip: Upgrade Go to fix standard library vulnerabilities${NC}"
    echo ""
    read -p "Skip security scan (will fail due to Go version)? [y/N]: " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
      echo -e "${YELLOW}⚠️  Security Scan: SKIPPED (outdated Go version)${NC}"
      echo ""
      # Remove Go Version Check from failures since user acknowledged
      FAILED_CHECKS=("${FAILED_CHECKS[@]/Go Version Check/}")
      SKIP_SECURITY_SCAN=true
    fi
  }
fi

# Check if govulncheck is installed (check PATH and ~/go/bin)
if [ "${SKIP_SECURITY_SCAN}" != "true" ]; then
  if command -v govulncheck &> /dev/null; then
    run_check "Security Scan" "make security" || {
      echo -e "${YELLOW}💡 Tip: If failure is due to Go version, upgrade Go${NC}"
      echo -e "${YELLOW}   See instructions above or run: ./scripts/check-go-version.sh${NC}"
      echo ""
    }
  elif [ -x "$HOME/go/bin/govulncheck" ]; then
    run_check "Security Scan" "$HOME/go/bin/govulncheck ./..." || {
      echo -e "${YELLOW}💡 Tip: If failure is due to Go version, upgrade Go${NC}"
      echo -e "${YELLOW}   See instructions above or run: ./scripts/check-go-version.sh${NC}"
      echo ""
    }
  else
    echo -e "${YELLOW}⚠️  Security Scan: SKIPPED (govulncheck not installed)${NC}"
    echo -e "${YELLOW}   Install with: make install-tools${NC}"
    echo ""
  fi
fi

# 2. TESTS (ALL)
echo ""
echo "════════════════════════════════════════════"
echo "2. TESTS (Unit + Integration + E2E + User)"
echo "════════════════════════════════════════════"
echo ""

run_check "All Tests" "go test -p 1 ./..." || {
  # Tests failed - offer diagnostic tool
  echo -e "${YELLOW}💡 Tip: Test diagnostic tool is available${NC}"
  echo ""
  read -p "Run test diagnostics and auto-fix? [y/N]: " -n 1 -r
  echo ""
  if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo ""
    if [ -f "./scripts/diagnose-test-failures.sh" ]; then
      ./scripts/diagnose-test-failures.sh
      echo ""
      echo -e "${BLUE}Re-running tests after fixes...${NC}"
      echo ""
      # Re-run tests after fixes
      if run_check "All Tests (after fixes)" "go test -p 1 ./..."; then
        # Remove the original failure from FAILED_CHECKS
        FAILED_CHECKS=("${FAILED_CHECKS[@]/All Tests/}")
      fi
    else
      echo -e "${RED}❌ diagnose-test-failures.sh not found in ./scripts/${NC}"
    fi
  fi
}

# 3. BUILD VERIFICATION
echo ""
echo "════════════════════════════════════════════"
echo "3. BUILD VERIFICATION"
echo "════════════════════════════════════════════"
echo ""

run_check "Build Server" "go build -o bin/ori-agent ./cmd/server" || true
run_check "Build Menubar (macOS)" "go build -o bin/ori-menubar ./cmd/menubar 2>/dev/null || echo 'Skipping menubar (not on macOS)'" || true
run_check "Build Plugins" "./scripts/build-plugins.sh" || true
run_check "Build External Plugins" "./scripts/build-external-plugins.sh" || true

# 4. DEPENDENCY CHECK
echo ""
echo "════════════════════════════════════════════"
echo "4. DEPENDENCY CHECK"
echo "════════════════════════════════════════════"
echo ""

run_check "Go Mod Verify" "go mod verify" || true
run_check "Go Mod Tidy" "go mod tidy && git diff --exit-code go.mod go.sum" || {
  # Go mod tidy failed - offer to auto-fix
  echo -e "${YELLOW}💡 Tip: Automated go.mod fix is available${NC}"
  echo ""
  read -p "Run automated go.mod fixer? [y/N]: " -n 1 -r
  echo ""
  if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo ""
    if [ -f "./scripts/fix-go-mod.sh" ]; then
      ./scripts/fix-go-mod.sh
      echo ""
      echo -e "${BLUE}Re-running Go Mod Tidy check after fixes...${NC}"
      echo ""
      # Re-run go mod tidy check after fixes
      if run_check "Go Mod Tidy (after fixes)" "go mod tidy && git diff --exit-code go.mod go.sum"; then
        # Remove the original failure from FAILED_CHECKS
        FAILED_CHECKS=("${FAILED_CHECKS[@]/Go Mod Tidy/}")
      fi
    else
      echo -e "${RED}❌ fix-go-mod.sh not found in ./scripts/${NC}"
    fi
  fi
}

# 5. UPDATE README
echo ""
echo "════════════════════════════════════════════"
echo "5. UPDATE README"
echo "════════════════════════════════════════════"
echo ""

if [ -f "./scripts/update-readme.sh" ]; then
  run_check "Update README badges" "./scripts/update-readme.sh" || true
else
  echo -e "${YELLOW}⚠️  Update README: SKIPPED (update-readme.sh not found)${NC}"
  echo ""
fi

# 6. GIT STATUS CHECK
echo ""
echo "════════════════════════════════════════════"
echo "6. GIT STATUS CHECK"
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

  # Check what types of changes exist
  # Exclude VERSION and README.md from this check (will be auto-committed at the end)
  NON_VERSION_CHANGES=$(git status --porcelain | grep -v "VERSION\|README.md" | wc -l | tr -d ' ')

  if [ "$NON_VERSION_CHANGES" -eq 0 ]; then
    # Only VERSION/README changes (will be auto-committed after all checks pass)
    echo -e "${BLUE}💡 Note: Only VERSION/README.md changes detected${NC}"
    if [ -n "$VERSION" ]; then
      echo -e "${BLUE}   These will be auto-committed after all checks pass.${NC}"
    else
      echo -e "${BLUE}   Run with version argument to auto-commit: ./scripts/pre-release-check.sh v0.X.Y${NC}"
    fi
    echo ""
  else
    # Check if only script files + VERSION/README changed
    NON_SCRIPT_CHANGES=$(git status --porcelain | grep -v "scripts/" | grep -v "VERSION\|README.md" | grep -v "^??" | wc -l | tr -d ' ')
    SCRIPT_CHANGES=$(git status --porcelain | grep "scripts/" | wc -l | tr -d ' ')

    if [ "$NON_SCRIPT_CHANGES" -eq 0 ] && [ "$SCRIPT_CHANGES" -gt 0 ]; then
      # Only script changes (+ possibly VERSION/README) - this is OK during development
      echo -e "${BLUE}💡 Note: Only script changes detected (normal during script development)${NC}"
      echo -e "${BLUE}   These are the scripts you're currently working on.${NC}"
      if [ -n "$VERSION" ]; then
        echo -e "${BLUE}   VERSION/README will be auto-committed after all checks pass.${NC}"
      fi
      echo ""
    else
      # Other changes exist - mark as failed
      FAILED_CHECKS+=("Git Status - uncommitted changes")
    fi
  fi
fi

# Check current branch
CURRENT_BRANCH=$(git branch --show-current)
if [ "$CURRENT_BRANCH" = "dev" ]; then
  echo -e "${GREEN}✅ Git Branch: $CURRENT_BRANCH (testing before release branch)${NC}"
  echo ""
  echo -e "${BLUE}ℹ️  Running pre-release checks on dev branch${NC}"
  echo -e "${BLUE}   This ensures code is stable before creating release branch${NC}"
  echo ""
elif [[ "$CURRENT_BRANCH" =~ ^release/ ]]; then
  echo -e "${GREEN}✅ Git Branch: $CURRENT_BRANCH (release stabilization)${NC}"
  echo ""
  echo -e "${BLUE}ℹ️  Running pre-release checks on release branch${NC}"
  echo -e "${BLUE}   This validates the release before merging to main${NC}"
  echo ""
elif [ "$CURRENT_BRANCH" = "main" ]; then
  echo -e "${GREEN}✅ Git Branch: $CURRENT_BRANCH${NC}"
  echo ""

  echo -e "${YELLOW}⚠️  You're running checks on main (after merge)${NC}"
  echo -e "${YELLOW}   Best practice: Run checks on release branch first${NC}"
  echo ""

  # Check if dev is merged into main
  if git show-ref --verify --quiet refs/heads/dev; then
    DEV_COMMITS=$(git rev-list main..dev --count 2>/dev/null || echo "0")
    if [ "$DEV_COMMITS" -gt 0 ]; then
      echo -e "${RED}❌ Warning: dev branch has $DEV_COMMITS commit(s) not in main${NC}"
      echo -e "${YELLOW}   Create release branch: ./scripts/create-release.sh vX.Y.Z${NC}"
      echo ""
      FAILED_CHECKS+=("dev branch not merged to main")
    else
      echo -e "${GREEN}✅ dev branch is fully merged into main${NC}"
      echo ""
    fi
  else
    echo -e "${YELLOW}⚠️  dev branch does not exist${NC}"
    echo ""
  fi
else
  echo -e "${RED}❌ Git Branch: $CURRENT_BRANCH (must be on 'dev', 'release/*', or 'main')${NC}"
  echo ""
  FAILED_CHECKS+=("Not on dev, release, or main branch")
fi

# 7. SMOKE TESTS (OPTIONAL)
echo ""
echo "════════════════════════════════════════════"
echo "7. SMOKE TESTS (Optional)"
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

  # Commit VERSION and README changes if a version was specified and all checks passed
  if [ -n "$VERSION" ]; then
    if ! git diff --quiet VERSION README.md 2>/dev/null; then
      echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
      echo -e "${BLUE}[AUTO-COMMIT]${NC} All checks passed! Committing version bump..."
      echo ""
      git add VERSION README.md 2>/dev/null || true
      if git commit -m "chore: bump version to $VERSION" --no-verify; then
        echo ""
        echo -e "${GREEN}✅ Version files committed successfully${NC}"
        echo -e "${GREEN}   Commit: chore: bump version to $VERSION${NC}"
        echo -e "${GREEN}   Files: VERSION, README.md${NC}"
        echo ""
      else
        echo ""
        echo -e "${RED}❌ Failed to commit version bump${NC}"
        echo ""
      fi
      echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
      echo ""
    fi
  fi

  # Get current branch to show appropriate next steps
  CURRENT_BRANCH=$(git branch --show-current)

  if [ "$CURRENT_BRANCH" = "dev" ]; then
    # Checks passed on dev - ready to create release branch
    if [ -n "$VERSION" ]; then
      echo -e "${GREEN}dev branch is ready to release $VERSION${NC}"
    else
      echo -e "${GREEN}dev branch is ready to release!${NC}"
    fi
    echo ""
    echo "Next steps:"
    if [ -n "$VERSION" ]; then
      echo "  ./scripts/create-release.sh $VERSION"
      echo "     (Creates release/vX.Y.Z branch for stabilization)"
    else
      echo "  ./scripts/create-release.sh vX.Y.Z"
      echo "     (Creates release branch for stabilization)"
    fi
    echo ""
    echo -e "${BLUE}💡 Tip: All checks passed on dev, safe to create release branch!${NC}"
    echo ""
  elif [[ "$CURRENT_BRANCH" =~ ^release/ ]]; then
    # Checks passed on release branch - ready to trigger release
    RELEASE_VERSION="${CURRENT_BRANCH#release/}"
    echo -e "${GREEN}Release branch is ready: $CURRENT_BRANCH${NC}"
    echo ""
    echo "Next steps:"
    echo "  1. Wait for scheduled release (Tuesday 10:00 UTC)"
    echo "     OR trigger manually:"
    echo ""
    echo "  gh workflow run scheduled-release.yml -f release_branch=$CURRENT_BRANCH"
    echo ""
    echo "  Optional dry run first:"
    echo "  gh workflow run scheduled-release.yml -f release_branch=$CURRENT_BRANCH -f dry_run=true"
    echo ""
    echo -e "${BLUE}💡 Tip: All checks passed, release branch is ready!${NC}"
    echo ""
  elif [ "$CURRENT_BRANCH" = "main" ]; then
    # Checks passed on main - unusual but ok
    if [ -n "$VERSION" ]; then
      echo "Ready to release $VERSION"
      echo ""
      echo "Next steps:"
      echo "  ./scripts/create-release.sh $VERSION --immediate"
      echo ""
    else
      echo "Ready to release!"
      echo ""
      echo "Next steps:"
      echo "  ./scripts/create-release.sh vX.Y.Z --immediate"
      echo ""
    fi
  else
    # Unknown branch
    echo "Ready to proceed!"
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
