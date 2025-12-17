#!/bin/bash

# create-release.sh - Creates a release branch following Git Flow
#
# Usage:
#   ./scripts/create-release.sh <version>
#   ./scripts/create-release.sh v1.3.0
#   ./scripts/create-release.sh v1.3.0 --immediate   # Skip release branch, release now
#
# Git Flow Workflow:
#   1. Creates release/vX.Y.Z branch from dev
#   2. Push triggers CI validation
#   3. Scheduled release (Tuesday 10:00 UTC) or manual trigger merges to main
#
# Immediate Release (--immediate flag):
#   - Skips release branch
#   - Merges dev to main, tags, and releases via GitHub Actions

set -e

# Get the script directory and project directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Change to project directory for all operations
cd "$PROJECT_DIR" || exit 1

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
  echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
  echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
  echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
  echo -e "${RED}[ERROR]${NC} $1"
}

# Help function
show_help() {
  echo ""
  echo "╔════════════════════════════════════════════════════════════════╗"
  echo "║              create-release.sh - Release Manager               ║"
  echo "╚════════════════════════════════════════════════════════════════╝"
  echo ""
  echo -e "${BLUE}USAGE:${NC}"
  echo "  ./scripts/create-release.sh <version> [options]"
  echo ""
  echo -e "${BLUE}ARGUMENTS:${NC}"
  echo "  <version>       Version to release (e.g., v1.3.0 or 1.3.0)"
  echo "                  The 'v' prefix is added automatically if missing"
  echo ""
  echo -e "${BLUE}OPTIONS:${NC}"
  echo "  --immediate     Skip release branch, release immediately"
  echo "                  Merges dev to main, creates tag, triggers release"
  echo "  --help, -h      Show this help message"
  echo ""
  echo -e "${BLUE}EXAMPLES:${NC}"
  echo "  ./scripts/create-release.sh v1.3.0"
  echo "      Create release branch release/v1.3.0 from dev (Git Flow)"
  echo ""
  echo "  ./scripts/create-release.sh v1.3.0 --immediate"
  echo "      Skip release branch, merge dev to main and release now"
  echo ""
  echo "  ./scripts/create-release.sh 1.3.0"
  echo "      Same as v1.3.0 (v prefix added automatically)"
  echo ""
  echo -e "${BLUE}GIT FLOW WORKFLOW (default):${NC}"
  echo "  1. Creates release/vX.Y.Z branch from dev"
  echo "  2. Push triggers CI validation"
  echo "  3. Make bug fixes on release branch (no new features)"
  echo "  4. Scheduled release (Tuesday 10:00 UTC) or manual trigger"
  echo ""
  echo -e "${BLUE}IMMEDIATE RELEASE WORKFLOW (--immediate):${NC}"
  echo "  1. Merges dev to main (if needed)"
  echo "  2. Runs quick tests"
  echo "  3. Updates VERSION file"
  echo "  4. Creates and pushes tag (triggers GitHub Actions release)"
  echo "  5. Syncs release back to dev"
  echo ""
  echo -e "${BLUE}RELATED COMMANDS:${NC}"
  echo "  ./scripts/pre-release-check.sh <version>   Full validation before release"
  echo "  gh run list --workflow=release.yml         View release workflow progress"
  echo ""
  echo -e "${BLUE}MANUAL RELEASE TRIGGER:${NC}"
  echo "  After creating a release branch, you can manually trigger the release:"
  echo ""
  echo "  # Trigger release immediately"
  echo "  gh workflow run scheduled-release.yml -f release_branch=release/vX.Y.Z"
  echo ""
  echo "  # Dry run first (validate without releasing)"
  echo "  gh workflow run scheduled-release.yml -f release_branch=release/vX.Y.Z -f dry_run=true"
  echo ""
  echo "  # Force release (skip validation failures)"
  echo "  gh workflow run scheduled-release.yml -f release_branch=release/vX.Y.Z -f force_release=true"
  echo ""
  exit 0
}

# Parse arguments
IMMEDIATE=false
VERSION=""
for arg in "$@"; do
  case $arg in
    --help|-h)
      show_help
      ;;
    --immediate)
      IMMEDIATE=true
      ;;
    *)
      if [ -z "$VERSION" ]; then
        VERSION="$arg"
      fi
      ;;
  esac
done

# Check if version argument is provided
if [ -z "$VERSION" ]; then
  print_error "Usage: $0 <version> [--immediate]"
  print_error "Examples:"
  print_error "  $0 v1.3.0              # Create release branch (scheduled release)"
  print_error "  $0 v1.3.0 --immediate  # Release immediately"
  exit 1
fi

# Ensure version starts with 'v'
if [[ ! "$VERSION" =~ ^v ]]; then
  VERSION="v$VERSION"
  print_status "Added 'v' prefix: $VERSION"
fi

# Validate version format (basic check for v prefix and semantic versioning)
if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  print_error "Version must be in format vX.Y.Z (e.g., v1.0.1)"
  exit 1
fi

RELEASE_BRANCH="release/$VERSION"

if [ "$IMMEDIATE" = true ]; then
  print_status "Creating immediate release $VERSION for Ori Agent"
else
  print_status "Creating release branch $RELEASE_BRANCH for Ori Agent"
fi

# Check if we're in a git repository
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  print_error "Not in a git repository"
  exit 1
fi

# Check for uncommitted changes
if ! git diff-index --quiet HEAD --; then
  print_error "You have uncommitted changes. Please commit or stash them first."
  git status --porcelain
  exit 1
fi

# Check if tag already exists
if git tag -l | grep -q "^$VERSION$"; then
  print_error "Tag $VERSION already exists"
  exit 1
fi

# Check current branch
CURRENT_BRANCH=$(git branch --show-current)

if [ "$IMMEDIATE" = true ]; then
  # ============================================
  # IMMEDIATE RELEASE MODE
  # ============================================

  # For immediate release, we need to be on main or merge dev to main
  if [ "$CURRENT_BRANCH" != "main" ] && [ "$CURRENT_BRANCH" != "dev" ]; then
    print_error "For immediate release, must be on 'main' or 'dev' branch"
    print_error "Current branch: '$CURRENT_BRANCH'"
    exit 1
  fi

  # If on dev, switch to main and merge
  if [ "$CURRENT_BRANCH" = "dev" ]; then
    print_status "Switching to main and merging dev..."
    git switch main
    git pull origin main
    git merge dev --no-ff -m "Merge dev for release $VERSION"
  else
    # Already on main, just pull
    print_status "Pulling latest changes..."
    git pull origin main

    # Check that dev is merged
    if git show-ref --verify --quiet refs/heads/dev; then
      DEV_COMMITS=$(git rev-list main..dev --count 2>/dev/null || echo "0")
      if [ "$DEV_COMMITS" -gt 0 ]; then
        print_warning "dev branch has $DEV_COMMITS commit(s) not merged to main"
        read -p "Merge dev to main now? (Y/n): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Nn]$ ]]; then
          git merge dev --no-ff -m "Merge dev for release $VERSION"
        fi
      fi
    fi
  fi

  # Run pre-release checks
  print_status "Running pre-release checks..."
  if [ -f "./scripts/pre-release-check.sh" ]; then
    print_warning "Consider running './scripts/pre-release-check.sh $VERSION' for full validation"
  fi

  # Run quick tests
  print_status "Running quick tests..."
  go test -short ./... || {
    print_error "Tests failed. Fix issues before releasing."
    exit 1
  }

  # Update VERSION file
  VERSION_FILE="VERSION"
  print_status "Updating VERSION file..."
  echo "$VERSION" >"$VERSION_FILE"
  if ! git diff --quiet "$VERSION_FILE" 2>/dev/null; then
    git add "$VERSION_FILE"
    git commit -m "chore: bump version to $VERSION"
  fi

  # Push main
  print_status "Pushing main branch..."
  git push origin main

  # Create and push tag (triggers release.yml workflow)
  print_status "Creating and pushing tag $VERSION..."
  git tag -a "$VERSION" -m "Release $VERSION"
  git push origin "$VERSION"

  print_success "Release $VERSION triggered!"
  echo ""
  print_status "The release workflow is now running on GitHub Actions."
  print_status "View progress: gh run list --workflow=release.yml"
  echo ""

  # Merge back to dev
  if git show-ref --verify --quiet refs/heads/dev; then
    print_status "Syncing release back to dev..."
    git switch dev
    git pull origin dev
    git merge main --no-ff -m "Merge main ($VERSION) back to dev"
    git push origin dev
    print_success "dev branch updated with release"
  fi

else
  # ============================================
  # RELEASE BRANCH MODE (Git Flow)
  # ============================================

  # Must be on dev branch for release branch creation
  if [ "$CURRENT_BRANCH" != "dev" ]; then
    print_warning "Not on dev branch (currently on $CURRENT_BRANCH)"
    read -p "Switch to dev? [Y/n] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Nn]$ ]]; then
      git switch dev
    else
      print_error "Must be on dev branch to create a release branch"
      exit 1
    fi
  fi

  # Pull latest dev
  print_status "Pulling latest changes from dev..."
  git pull origin dev

  # Check if release branch already exists
  if git show-ref --verify --quiet "refs/heads/$RELEASE_BRANCH" || \
     git show-ref --verify --quiet "refs/remotes/origin/$RELEASE_BRANCH"; then
    print_error "Branch $RELEASE_BRANCH already exists."
    exit 1
  fi

  # Update VERSION file on release branch
  VERSION_FILE="VERSION"

  # Create release branch
  print_status "Creating branch $RELEASE_BRANCH..."
  git switch -c "$RELEASE_BRANCH"

  # Update VERSION file
  print_status "Updating VERSION file..."
  echo "$VERSION" >"$VERSION_FILE"
  git add "$VERSION_FILE"
  git commit -m "chore: bump version to $VERSION"

  # Push to origin (triggers CI)
  print_status "Pushing $RELEASE_BRANCH to origin..."
  git push -u origin "$RELEASE_BRANCH"

  print_success "Release branch created: $RELEASE_BRANCH"
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  print_status "Next steps:"
  echo "  1. Make any final fixes on this branch (bug fixes only, no new features)"
  echo "  2. Push changes: git push"
  echo "  3. Wait for scheduled release (Tuesday 10:00 UTC)"
  echo "     OR manually trigger the release workflow"
  echo ""
  print_status "To trigger release immediately:"
  echo "  gh workflow run scheduled-release.yml -f release_branch=$RELEASE_BRANCH"
  echo ""
  print_status "To do a dry run first:"
  echo "  gh workflow run scheduled-release.yml -f release_branch=$RELEASE_BRANCH -f dry_run=true"
  echo ""
  print_status "To continue development on dev:"
  echo "  git switch dev"
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
fi
