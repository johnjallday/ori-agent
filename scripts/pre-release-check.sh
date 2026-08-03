#!/bin/bash
# Pre-release checklist automation
# Runs all quality checks, tests, and builds before release
#
# Usage:
#   ./scripts/pre-release-check.sh [version]
#   ./scripts/pre-release-check.sh v0.0.47
#   ./scripts/pre-release-check.sh              # No version bump, just checks
#
# Options:
#   --fix         Auto-fix lint errors (up to 5 iterations)
#   --no-smoke    Skip smoke tests (faster)
#   --ci          CI mode: no interactive prompts, fail fast
#   --verbose     Show full output for all commands
#
# Exit codes:
#   0 - All checks passed
#   1 - One or more checks failed

set -e
set -o pipefail

# ════════════════════════════════════════════════════════════════════════════
# CONFIGURATION
# ════════════════════════════════════════════════════════════════════════════

VERSION=""
AUTO_FIX=false
SKIP_SMOKE=false
CI_MODE=false
VERBOSE=false

# Parse arguments
for arg in "$@"; do
  case $arg in
    --fix)
      AUTO_FIX=true
      ;;
    --no-smoke)
      SKIP_SMOKE=true
      ;;
    --ci)
      CI_MODE=true
      ;;
    --verbose)
      VERBOSE=true
      ;;
    --help|-h)
      echo "Usage: ./scripts/pre-release-check.sh [version] [options]"
      echo ""
      echo "Options:"
      echo "  --fix         Auto-fix lint errors (up to 5 iterations)"
      echo "  --no-smoke    Skip smoke tests (faster)"
      echo "  --ci          CI mode: no interactive prompts, fail fast"
      echo "  --verbose     Show full output for all commands"
      echo ""
      echo "Examples:"
      echo "  ./scripts/pre-release-check.sh v0.0.47"
      echo "  ./scripts/pre-release-check.sh --fix"
      echo "  ./scripts/pre-release-check.sh v0.0.47 --fix --no-smoke"
      exit 0
      ;;
    -*)
      echo "Unknown option: $arg"
      echo "Use --help for usage information"
      exit 1
      ;;
    *)
      if [ -z "$VERSION" ]; then
        VERSION="$arg"
      fi
      ;;
  esac
done

cd "$(dirname "$0")/.."

# ════════════════════════════════════════════════════════════════════════════
# OUTPUT HELPERS
# ════════════════════════════════════════════════════════════════════════════

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

FAILED_CHECKS=()
SKIPPED_CHECKS=()
PASSED_CHECKS=()
FAILED_OUTPUTS_DIR=$(mktemp -d)

cleanup() {
  rm -rf "$FAILED_OUTPUTS_DIR"
}
trap cleanup EXIT

print_header() {
  echo ""
  echo -e "${CYAN}════════════════════════════════════════════${NC}"
  echo -e "${CYAN}$1${NC}"
  echo -e "${CYAN}════════════════════════════════════════════${NC}"
  echo ""
}

print_section() {
  echo ""
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo -e "${BLUE}$1${NC}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

# Run a check and track results
# Usage: run_check "Check Name" "command to run" [allow_failure]
run_check() {
  local name=$1
  local command=$2
  local allow_failure=${3:-false}
  local output_file
  output_file=$(mktemp)

  print_section "Running: $name"

  local start_time=$(date +%s)

  if [ "$VERBOSE" = true ]; then
    if eval "$command" 2>&1 | tee "$output_file"; then
      local end_time=$(date +%s)
      local duration=$((end_time - start_time))
      echo -e "${GREEN}✅ $name: PASSED${NC} (${duration}s)"
      PASSED_CHECKS+=("$name")
      rm -f "$output_file"
      return 0
    fi
  else
    if eval "$command" > "$output_file" 2>&1; then
      local end_time=$(date +%s)
      local duration=$((end_time - start_time))
      echo -e "${GREEN}✅ $name: PASSED${NC} (${duration}s)"
      PASSED_CHECKS+=("$name")
      rm -f "$output_file"
      return 0
    fi
  fi

  # Command failed
  local end_time=$(date +%s)
  local duration=$((end_time - start_time))

  if [ "$allow_failure" = true ]; then
    echo -e "${YELLOW}⚠️  $name: FAILED (non-blocking)${NC} (${duration}s)"
    tail -20 "$output_file"
    rm -f "$output_file"
    return 0
  else
    echo -e "${RED}❌ $name: FAILED${NC} (${duration}s)"
    echo ""
    tail -30 "$output_file"
    FAILED_CHECKS+=("$name")
    local safe_name
    safe_name=$(echo "$name" | tr ' /' '__')
    tail -50 "$output_file" > "$FAILED_OUTPUTS_DIR/$safe_name"
    rm -f "$output_file"
    return 1
  fi
}

skip_check() {
  local name=$1
  local reason=$2
  echo -e "${YELLOW}⏭️  $name: SKIPPED${NC} ($reason)"
  SKIPPED_CHECKS+=("$name: $reason")
}

# ════════════════════════════════════════════════════════════════════════════
# MAIN SCRIPT
# ════════════════════════════════════════════════════════════════════════════

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║     Ori Agent Pre-Release Checker          ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# Show configuration
echo -e "Configuration:"
echo -e "  Version:    ${VERSION:-"(no version bump)"}"
echo -e "  Auto-fix:   ${AUTO_FIX}"
echo -e "  Skip smoke: ${SKIP_SMOKE}"
echo -e "  CI mode:    ${CI_MODE}"
echo ""

# ════════════════════════════════════════════════════════════════════════════
# STEP 0: PREREQUISITES
# ════════════════════════════════════════════════════════════════════════════

print_header "0. PREREQUISITES"

# Check git status first
CURRENT_BRANCH=$(git branch --show-current)
echo -e "Current branch: ${BOLD}$CURRENT_BRANCH${NC}"

if [ "$CURRENT_BRANCH" != "dev" ] && [ "$CURRENT_BRANCH" != "main" ] && [[ ! "$CURRENT_BRANCH" =~ ^release/ ]]; then
  echo -e "${RED}❌ Must be on 'dev', 'main', or 'release/*' branch${NC}"
  FAILED_CHECKS+=("Branch check")
fi

# Show uncommitted changes (warning only)
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo -e "${YELLOW}⚠️  Uncommitted changes detected${NC}"
  git status --short
  echo ""
else
  echo -e "${GREEN}✅ Working directory clean${NC}"
fi

# Update VERSION file if specified
if [ -n "$VERSION" ]; then
  # Ensure version starts with 'v'
  if [[ ! "$VERSION" =~ ^v ]]; then
    VERSION="v$VERSION"
  fi

  # Validate format
  if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo -e "${RED}❌ Version must be in format vX.Y.Z (e.g., v1.0.1)${NC}"
    exit 1
  fi

  # Check if tag already exists
  if git tag -l | grep -q "^$VERSION$"; then
    echo -e "${RED}❌ Tag $VERSION already exists${NC}"
    exit 1
  fi

  # Update VERSION file
  if [ -f "VERSION" ]; then
    CURRENT_VERSION=$(cat VERSION | tr -d '[:space:]')
    if [ "$CURRENT_VERSION" != "$VERSION" ]; then
      echo -e "${BLUE}Updating VERSION file: $CURRENT_VERSION → $VERSION${NC}"
      echo "$VERSION" > VERSION
    else
      echo -e "VERSION file already set to $VERSION"
    fi
  else
    echo -e "${BLUE}Creating VERSION file with $VERSION${NC}"
    echo "$VERSION" > VERSION
  fi
fi

# ════════════════════════════════════════════════════════════════════════════
# STEP 1: DEPENDABOT PR MERGE
# ════════════════════════════════════════════════════════════════════════════

print_header "1. DEPENDABOT PR MERGE"

if [ -f "./scripts/merge-dependabot.sh" ]; then
  run_check "Merge Dependabot PRs" "./scripts/merge-dependabot.sh" true
else
  skip_check "Merge Dependabot PRs" "script not found"
fi

# ════════════════════════════════════════════════════════════════════════════
# STEP 2: CODE QUALITY CHECKS
# ════════════════════════════════════════════════════════════════════════════

print_header "2. CODE QUALITY CHECKS"

# Format check
run_check "Format Check (gofmt)" "test -z \"\$(gofmt -l . 2>&1 | grep -v '^vendor/' | head -20)\"" || {
  echo ""
  echo "Files needing formatting:"
  gofmt -l . 2>&1 | grep -v '^vendor/' | head -10
  echo ""
  if [ "$AUTO_FIX" = true ]; then
    echo "Auto-fixing with gofmt..."
    gofmt -w .
    echo -e "${GREEN}✅ Format issues fixed${NC}"
    NEW_FAILED=()
    for item in "${FAILED_CHECKS[@]}"; do
      [[ "$item" == "Format Check (gofmt)" ]] && continue
      NEW_FAILED+=("$item")
    done
    FAILED_CHECKS=("${NEW_FAILED[@]}")
    PASSED_CHECKS+=("Format Check (gofmt) [fixed]")
  fi
}

# Go vet
run_check "Go Vet" "go vet ./..." || true

# Generated Wails bindings must stay regular files. Wails' generator writes
# them executable; regenerating locally and committing without noticing
# reintroduces that.
run_check "Wails Binding Modes" "./scripts/check-wails-binding-modes.sh" || true

# Lint check (golangci-lint). Ratcheted against origin/dev, matching the CI
# gate: only issues introduced since dev fail this check. The full,
# pre-existing legacy baseline (tracked separately, not required to reach
# zero for this delivery) is visible via `make lint` but does not block here.
if command -v golangci-lint &> /dev/null; then
  LINT_CMD="golangci-lint run --new-from-merge-base=origin/dev ./..."
elif [ -x "$HOME/go/bin/golangci-lint" ]; then
  LINT_CMD="$HOME/go/bin/golangci-lint run --new-from-merge-base=origin/dev ./..."
else
  LINT_CMD=""
fi

if [ -n "$LINT_CMD" ]; then
  if ! run_check "Lint Check" "$LINT_CMD"; then
    if [ "$AUTO_FIX" = true ] && [ -f "./scripts/fix-all-lint.sh" ]; then
      echo ""
      echo "Auto-fixing lint errors..."
      MAX_ITERATIONS=5
      ITERATION=1
      LINT_PASSED=false

      while [ $ITERATION -le $MAX_ITERATIONS ] && [ "$LINT_PASSED" = false ]; do
        echo -e "${BLUE}Fix iteration $ITERATION/$MAX_ITERATIONS${NC}"
        ./scripts/fix-all-lint.sh > /dev/null 2>&1 || true

        if eval "$LINT_CMD" > /dev/null 2>&1; then
          LINT_PASSED=true
          echo -e "${GREEN}✅ Lint errors fixed after $ITERATION iteration(s)${NC}"
          NEW_FAILED=()
          for item in "${FAILED_CHECKS[@]}"; do
            [[ "$item" == "Lint Check" ]] && continue
            NEW_FAILED+=("$item")
          done
          FAILED_CHECKS=("${NEW_FAILED[@]}")
          PASSED_CHECKS+=("Lint Check [fixed]")
        fi
        ITERATION=$((ITERATION + 1))
      done

      if [ "$LINT_PASSED" = false ]; then
        echo -e "${RED}❌ Could not auto-fix all lint errors${NC}"
      fi
    fi
  fi
else
  skip_check "Lint Check" "golangci-lint not installed"
fi

# Go version check
if [ -f "./scripts/check-go-version.sh" ]; then
  run_check "Go Version Check" "./scripts/check-go-version.sh" true
fi

# Security scan (govulncheck)
if command -v govulncheck &> /dev/null; then
  run_check "Security Scan" "govulncheck ./..." || true
elif [ -x "$HOME/go/bin/govulncheck" ]; then
  run_check "Security Scan" "$HOME/go/bin/govulncheck ./..." || true
else
  skip_check "Security Scan" "govulncheck not installed"
fi

# ESLint check (frontend)
if [ -f "package.json" ]; then
  ESLINT_SCRIPT=""
  if grep -q '"lint"[[:space:]]*:' package.json 2>/dev/null; then
    ESLINT_SCRIPT="lint"
  elif grep -q '"lint:js"[[:space:]]*:' package.json 2>/dev/null; then
    ESLINT_SCRIPT="lint:js"
  fi

  if [ -z "$ESLINT_SCRIPT" ]; then
    skip_check "ESLint Check" "no lint script in package.json"
  elif ! command -v npm &> /dev/null; then
    echo -e "${RED}❌ ESLint Check: FAILED${NC} (npm not installed)"
    FAILED_CHECKS+=("ESLint Check")
  else
    NODE_DEPS_READY=true
    if [ ! -d "node_modules" ]; then
      if ! run_check "Install Node Dependencies" "npm ci --no-audit --no-fund"; then
        NODE_DEPS_READY=false
      fi
    fi

    if [ "$NODE_DEPS_READY" = true ]; then
      run_check "ESLint Check" "npm run $ESLINT_SCRIPT" || true
    else
      skip_check "ESLint Check" "node dependencies failed to install"
    fi
  fi
else
  skip_check "ESLint Check" "package.json not found"
fi

# ════════════════════════════════════════════════════════════════════════════
# STEP 3: TESTS
# ════════════════════════════════════════════════════════════════════════════

print_header "3. TESTS"

# Run all tests with race detector
# Note: -p 1 runs packages sequentially to avoid race conditions in shared state
TEST_CMD="./scripts/run-test-command.sh go test -p 1 -race -timeout 5m ./..."

if ! run_check "All Tests (unit + integration)" "$TEST_CMD"; then
  echo ""
  echo -e "${YELLOW}💡 Test failures must be fixed manually before release.${NC}"
  echo ""
  echo "Useful commands:"
  echo "  go test -v ./internal/path/to/failing/package/..."
  echo "  go test -v -run TestSpecificTest ./..."
  echo ""

  if [ -f "./scripts/diagnose-test-failures.sh" ]; then
    echo "Run diagnostics:"
    echo "  ./scripts/diagnose-test-failures.sh"
    echo ""
  fi
fi

# ════════════════════════════════════════════════════════════════════════════
# STEP 4: BUILD VERIFICATION
# ════════════════════════════════════════════════════════════════════════════

print_header "4. BUILD VERIFICATION"

# Build server
run_check "Build Server" "go build -o bin/ori-agent ./cmd/server" || true

# Build menubar (macOS only)
if [ "$(uname)" = "Darwin" ]; then
  run_check "Build Menubar (macOS)" "go build -o bin/ori-menubar ./cmd/menubar" || true
else
  skip_check "Build Menubar" "not on macOS"
fi

# Build folder picker (Wails app) — mirrors CI release workflow
if [ -f "./scripts/build-folder-picker.sh" ]; then
  if command -v wails &> /dev/null || [ -x "$HOME/go/bin/wails" ]; then
    # Save git state before build
    PRE_BUILD_STATUS=$(git status --porcelain)

    run_check "Build Folder Picker" "./scripts/build-folder-picker.sh" || true

    # Check if the build dirtied the tree (this is what breaks GoReleaser in CI)
    POST_BUILD_STATUS=$(git status --porcelain)
    if [ "$PRE_BUILD_STATUS" != "$POST_BUILD_STATUS" ]; then
      DIRTY_FILES=$(diff <(echo "$PRE_BUILD_STATUS") <(echo "$POST_BUILD_STATUS") | grep '^>' | sed 's/^> //')
      echo -e "${YELLOW}⚠️  Folder picker build modified files:${NC}"
      echo "$DIRTY_FILES"
      echo ""
      echo -e "${YELLOW}   These files must be reset in .github/workflows/release.yml${NC}"
      echo -e "${YELLOW}   before GoReleaser runs, or CI will fail with 'git is in a dirty state'.${NC}"
      echo ""

      # Verify the release workflow has the reset step
      if grep -q "Reset Wails-generated file changes" .github/workflows/release.yml 2>/dev/null; then
        echo -e "${GREEN}✅ Release workflow has Wails reset step${NC}"
        # Reset the files locally too
        git checkout -- . 2>/dev/null || true
      else
        echo -e "${RED}❌ Release workflow is MISSING the Wails reset step — CI will fail${NC}"
        FAILED_CHECKS+=("CI Dirty Tree Protection")
      fi
    fi
  else
    skip_check "Build Folder Picker" "wails CLI not installed"
  fi
else
  skip_check "Build Folder Picker" "build script not found"
fi

# Cross-platform builds
if [ -f "./scripts/check-cross-platform.sh" ]; then
  run_check "Cross-Platform Builds" "./scripts/check-cross-platform.sh" || true
else
  skip_check "Cross-Platform Builds" "script not found"
fi

# Sync plugin dependencies
if [ -f "./scripts/sync-plugin-deps.sh" ]; then
  run_check "Sync Plugin Dependencies" "./scripts/sync-plugin-deps.sh" || true
fi

# Build plugins
if [ -f "./scripts/build-plugins.sh" ]; then
  run_check "Build Plugins" "./scripts/build-plugins.sh" || true
fi

# ════════════════════════════════════════════════════════════════════════════
# STEP 5: DEPENDENCY CHECK
# ════════════════════════════════════════════════════════════════════════════

print_header "5. DEPENDENCY CHECK"

run_check "Go Mod Verify" "go mod verify" || true

# Check if go.mod/go.sum need tidying
GO_MOD_CHECK="go mod tidy && git diff --exit-code go.mod go.sum"
if ! run_check "Go Mod Tidy" "$GO_MOD_CHECK"; then
  echo ""
  echo "go.mod/go.sum were modified by 'go mod tidy'."
  echo "Changes will be committed automatically if all checks pass."
  echo ""
  # Don't treat this as a failure - we'll commit the changes
  # Rebuild array without the "Go Mod Tidy" entry (string substitution leaves empty elements)
  NEW_FAILED=()
  for item in "${FAILED_CHECKS[@]}"; do
    [[ "$item" == "Go Mod Tidy" ]] && continue
    NEW_FAILED+=("$item")
  done
  FAILED_CHECKS=("${NEW_FAILED[@]}")
fi

# ════════════════════════════════════════════════════════════════════════════
# STEP 6: README UPDATE
# ════════════════════════════════════════════════════════════════════════════

print_header "6. README UPDATE"

# This validates the existing product screenshot contract before the separate
# release badge updater intentionally edits README.md.
if [ -f "./scripts/readme/manifest.mjs" ]; then
  run_check "README screenshot contract" "make readme-check" || true
else
  skip_check "README screenshot contract" "README contract helper not found"
fi

if [ -f "./scripts/update-readme.sh" ]; then
  run_check "Update README badges" "./scripts/update-readme.sh" true
else
  skip_check "Update README badges" "script not found"
fi

# ════════════════════════════════════════════════════════════════════════════
# STEP 7: GIT STATUS CHECK
# ════════════════════════════════════════════════════════════════════════════

print_header "7. GIT STATUS CHECK"

# Final branch check
if [ "$CURRENT_BRANCH" = "dev" ]; then
  echo -e "${GREEN}✅ Branch: $CURRENT_BRANCH (pre-release testing)${NC}"
elif [[ "$CURRENT_BRANCH" =~ ^release/ ]]; then
  echo -e "${GREEN}✅ Branch: $CURRENT_BRANCH (release stabilization)${NC}"
elif [ "$CURRENT_BRANCH" = "main" ]; then
  echo -e "${GREEN}✅ Branch: $CURRENT_BRANCH${NC}"
else
  echo -e "${RED}❌ Branch: $CURRENT_BRANCH (expected dev, main, or release/*)${NC}"
fi

# Show modified files
if [ -n "$(git status --porcelain)" ]; then
  echo ""
  echo "Modified files (will be auto-committed if checks pass):"
  git status --short
fi

# ════════════════════════════════════════════════════════════════════════════
# STEP 8: SMOKE TESTS
# ════════════════════════════════════════════════════════════════════════════

print_header "8. SMOKE TESTS"

if [ "$SKIP_SMOKE" = true ]; then
  skip_check "Smoke Tests" "--no-smoke flag"
elif [ -f "./scripts/test-all-installers.sh" ]; then
  run_check "Smoke Tests" "./scripts/test-all-installers.sh" true
else
  skip_check "Smoke Tests" "script not found"
fi

# ════════════════════════════════════════════════════════════════════════════
# SUMMARY
# ════════════════════════════════════════════════════════════════════════════

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║              SUMMARY                       ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# Show results
echo -e "${GREEN}Passed: ${#PASSED_CHECKS[@]}${NC}"
for check in "${PASSED_CHECKS[@]}"; do
  echo -e "  ${GREEN}✅${NC} $check"
done

if [ ${#SKIPPED_CHECKS[@]} -gt 0 ]; then
  echo ""
  echo -e "${YELLOW}Skipped: ${#SKIPPED_CHECKS[@]}${NC}"
  for check in "${SKIPPED_CHECKS[@]}"; do
    echo -e "  ${YELLOW}⏭️${NC}  $check"
  done
fi

if [ ${#FAILED_CHECKS[@]} -gt 0 ]; then
  echo ""
  echo -e "${RED}Failed: ${#FAILED_CHECKS[@]}${NC}"
  for check in "${FAILED_CHECKS[@]}"; do
    echo -e "  ${RED}❌${NC} $check"
  done
fi

echo ""

# Handle success/failure
if [ ${#FAILED_CHECKS[@]} -eq 0 ]; then
  echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo -e "${GREEN}  ✅ ALL CHECKS PASSED${NC}"
  echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo ""

  # Auto-commit changes
  COMMITTED=false

  # Commit go.mod/go.sum if changed
  if ! git diff --quiet go.mod go.sum 2>/dev/null; then
    echo "Committing go.mod/go.sum changes..."
    git add go.mod go.sum 2>/dev/null || true
    git commit -m "chore: tidy go module dependencies" --no-verify 2>/dev/null && COMMITTED=true
  fi

  # Commit VERSION and README if changed
  if [ -n "$VERSION" ] && ! git diff --quiet VERSION README.md 2>/dev/null; then
    echo "Committing version bump..."
    git add VERSION README.md 2>/dev/null || true
    git commit -m "chore: bump version to $VERSION" --no-verify 2>/dev/null && COMMITTED=true
  fi

  # Commit any remaining changes (lint fixes, etc.)
  if [ -n "$(git status --porcelain)" ]; then
    echo "Committing remaining fixes..."
    git add -A 2>/dev/null || true
    git commit -m "chore: apply pre-release fixes" --no-verify 2>/dev/null && COMMITTED=true
  fi

  if [ "$COMMITTED" = true ]; then
    echo -e "${GREEN}✅ Changes committed${NC}"
    echo ""
  fi

  # Show next steps
  echo "Next steps:"
  if [ "$CURRENT_BRANCH" = "dev" ]; then
    echo "  1. Push dev branch:     git push origin dev"
    echo "  2. Merge to main:       ./scripts/release.sh ${VERSION:-vX.Y.Z}"
    echo ""
    echo "Or run full release:"
    echo "  ./scripts/release.sh ${VERSION:-vX.Y.Z}"
  elif [ "$CURRENT_BRANCH" = "main" ]; then
    echo "  1. Create tag:          git tag ${VERSION:-vX.Y.Z}"
    echo "  2. Push tag:            git push origin ${VERSION:-vX.Y.Z}"
  fi
  echo ""

  exit 0
else
  echo -e "${RED}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo -e "${RED}  ❌ ${#FAILED_CHECKS[@]} CHECK(S) FAILED${NC}"
  echo -e "${RED}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo ""

  echo "Please fix the issues above before releasing."
  echo ""

  # Show detailed error output
  if [ "$(ls -A "$FAILED_OUTPUTS_DIR" 2>/dev/null)" ]; then
    echo "Error details:"
    echo ""
    for check in "${FAILED_CHECKS[@]}"; do
      safe_name=$(echo "$check" | tr ' /' '__')
      if [ -f "$FAILED_OUTPUTS_DIR/$safe_name" ]; then
        echo -e "${RED}━━━ $check ━━━${NC}"
        cat "$FAILED_OUTPUTS_DIR/$safe_name"
        echo ""
      fi
    done
  fi

  echo "Tips:"
  echo "  - Run with --fix to auto-fix lint errors"
  echo "  - Run with --verbose to see full command output"
  echo "  - Run with --no-smoke to skip slow smoke tests"
  echo ""

  exit 1
fi
