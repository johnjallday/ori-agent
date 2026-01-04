#!/bin/bash
# post-release-sync.sh - Sync branches after a successful release
#
# This script:
#   1. Checks if a release workflow is currently running (aborts if so)
#   2. Checks if the release was successful on GitHub
#   3. Syncs main with the release branch (force push if needed)
#      - Release branch has all commits: features + dependabot + release fixes
#   4. Merges main to dev
#   5. Deletes the release branch (local and remote)
#
# Usage:
#   ./scripts/post-release-sync.sh <version>
#   ./scripts/post-release-sync.sh v0.0.31

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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

# Get script directory and change to project root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

# Check for version argument
VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  # Try to detect from recent releases
  print_status "No version specified, checking for recent releases..."
  LATEST_TAG=$(gh release list --limit 1 --json tagName -q '.[0].tagName' 2>/dev/null || echo "")

  if [ -n "$LATEST_TAG" ]; then
    echo ""
    read -p "Use latest release $LATEST_TAG? [Y/n]: " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Nn]$ ]]; then
      VERSION="$LATEST_TAG"
    else
      print_error "Usage: $0 <version>"
      print_error "Example: $0 v0.0.30"
      exit 1
    fi
  else
    print_error "Usage: $0 <version>"
    print_error "Example: $0 v0.0.30"
    exit 1
  fi
fi

# Ensure version starts with 'v'
if [[ ! "$VERSION" =~ ^v ]]; then
  VERSION="v$VERSION"
fi

RELEASE_BRANCH="release/$VERSION"

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║       Post-Release Sync                    ║"
echo "╚════════════════════════════════════════════╝"
echo ""
print_status "Version: $VERSION"
print_status "Release branch: $RELEASE_BRANCH"
echo ""

# Check if GitHub CLI is available
if ! command -v gh &> /dev/null; then
  print_error "GitHub CLI (gh) is required but not installed."
  print_error "Install from: https://cli.github.com/"
  exit 1
fi

# Step 0: Check if a release workflow is currently running
print_status "Checking for running release workflows..."

# Check release.yml workflow
RELEASE_IN_PROGRESS=$(gh run list --workflow=release.yml --status=in_progress --json databaseId,headBranch -q '.[0]' 2>/dev/null || echo "")

# Check scheduled-release.yml workflow
SCHEDULED_IN_PROGRESS=$(gh run list --workflow=scheduled-release.yml --status=in_progress --json databaseId,headBranch -q '.[0]' 2>/dev/null || echo "")

if [ -n "$RELEASE_IN_PROGRESS" ] || [ -n "$SCHEDULED_IN_PROGRESS" ]; then
  echo ""
  print_warning "A release workflow is currently running on GitHub!"
  echo ""

  if [ -n "$RELEASE_IN_PROGRESS" ]; then
    RUN_ID=$(echo "$RELEASE_IN_PROGRESS" | jq -r '.databaseId')
    RUN_BRANCH=$(echo "$RELEASE_IN_PROGRESS" | jq -r '.headBranch')
    print_status "  Workflow: release.yml"
    print_status "  Run ID: $RUN_ID"
    print_status "  Branch: $RUN_BRANCH"
    print_status "  View: gh run view $RUN_ID"
  fi

  if [ -n "$SCHEDULED_IN_PROGRESS" ]; then
    RUN_ID=$(echo "$SCHEDULED_IN_PROGRESS" | jq -r '.databaseId')
    RUN_BRANCH=$(echo "$SCHEDULED_IN_PROGRESS" | jq -r '.headBranch')
    print_status "  Workflow: scheduled-release.yml"
    print_status "  Run ID: $RUN_ID"
    print_status "  Branch: $RUN_BRANCH"
    print_status "  View: gh run view $RUN_ID"
  fi

  echo ""
  print_error "Please wait for the release workflow to complete before running post-release sync."
  print_status "You can watch the workflow with: gh run watch"
  echo ""
  exit 1
fi

print_success "No release workflows currently running"
echo ""

# Step 1: Check if release exists and is successful
print_status "Checking release status on GitHub..."

# Check if release exists
RELEASE_INFO=$(gh release view "$VERSION" --json tagName,isDraft,assets 2>/dev/null || echo "")

if [ -z "$RELEASE_INFO" ]; then
  print_error "Release $VERSION not found on GitHub"
  print_status "Check releases at: gh release list"
  exit 1
fi

# Check if release has assets (indicates successful build)
ASSET_COUNT=$(echo "$RELEASE_INFO" | jq '.assets | length')

if [ "$ASSET_COUNT" -eq 0 ]; then
  print_warning "Release $VERSION exists but has no assets yet"
  print_status "The build might still be in progress..."
  echo ""

  # Check for in-progress workflow
  IN_PROGRESS=$(gh run list --workflow=scheduled-release.yml --status=in_progress --json databaseId -q '.[0].databaseId' 2>/dev/null || echo "")

  if [ -n "$IN_PROGRESS" ]; then
    print_status "Found in-progress workflow: $IN_PROGRESS"
    read -p "Wait for it to complete? [Y/n]: " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Nn]$ ]]; then
      print_status "Waiting for workflow to complete..."
      gh run watch "$IN_PROGRESS"

      # Re-check assets
      RELEASE_INFO=$(gh release view "$VERSION" --json tagName,isDraft,assets 2>/dev/null || echo "")
      ASSET_COUNT=$(echo "$RELEASE_INFO" | jq '.assets | length')
    fi
  fi

  if [ "$ASSET_COUNT" -eq 0 ]; then
    print_error "Release still has no assets. Aborting."
    exit 1
  fi
fi

print_success "Release $VERSION found with $ASSET_COUNT assets"
echo ""

# Step 2: Check for uncommitted changes
if ! git diff-index --quiet HEAD -- 2>/dev/null; then
  print_error "You have uncommitted changes. Please commit or stash them first."
  git status --short
  exit 1
fi

# Step 3: Sync main with release branch
# The release branch contains all commits (features + dependabot + release fixes)
# Main needs to be updated to match the release branch
print_status "Fetching release branch..."
git fetch origin "$RELEASE_BRANCH"

print_status "Checking if main needs to be synced with release branch..."
git checkout main

# Check if release branch has commits not in main
COMMITS_AHEAD=$(git rev-list --count origin/main..origin/"$RELEASE_BRANCH" 2>/dev/null || echo "0")

if [ "$COMMITS_AHEAD" -gt 0 ]; then
  print_status "Release branch has $COMMITS_AHEAD commits not in main"
  print_status "Resetting main to match release branch..."
  git reset --hard origin/"$RELEASE_BRANCH"

  print_status "Force pushing main to origin..."
  if git push --force-with-lease origin main; then
    print_success "Main branch synced with release branch"
  else
    print_error "Failed to push main. You may need to resolve this manually."
    exit 1
  fi
else
  print_status "Main is already up-to-date with release branch"
  git pull origin main
fi
echo ""

# Step 4: Merge main to dev
print_status "Syncing dev with main..."
git checkout dev
git fetch origin dev

# Check if dev needs updating
MAIN_COMMIT=$(git rev-parse main)
DEV_CONTAINS_MAIN=$(git merge-base --is-ancestor main dev && echo "yes" || echo "no")

if [ "$DEV_CONTAINS_MAIN" = "yes" ]; then
  print_status "Dev already contains all main commits"
  git pull origin dev
else
  print_status "Merging main to dev..."
  git pull origin dev

  if git merge main -m "Merge main ($VERSION release) back to dev"; then
    git push origin dev
    print_success "Dev branch updated with $VERSION"
  else
    print_error "Merge conflict! Please resolve manually:"
    print_error "  1. Resolve conflicts"
    print_error "  2. git add ."
    print_error "  3. git commit"
    print_error "  4. git push origin dev"
    exit 1
  fi
fi
echo ""

# Step 5: Delete release branch
print_status "Cleaning up release branch..."

# Delete remote branch
if git ls-remote --exit-code --heads origin "$RELEASE_BRANCH" &>/dev/null; then
  print_status "Deleting remote branch: origin/$RELEASE_BRANCH"
  git push origin --delete "$RELEASE_BRANCH" || print_warning "Could not delete remote branch"
else
  print_status "Remote branch already deleted"
fi

# Delete local branch
if git show-ref --verify --quiet "refs/heads/$RELEASE_BRANCH"; then
  print_status "Deleting local branch: $RELEASE_BRANCH"
  git branch -D "$RELEASE_BRANCH" || print_warning "Could not delete local branch"
else
  print_status "Local branch already deleted"
fi

print_success "Release branch cleaned up"
echo ""

# Summary
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
print_success "Post-release sync complete for $VERSION!"
echo ""
echo "  ✓ Release verified on GitHub ($ASSET_COUNT assets)"
echo "  ✓ Main branch synced with release branch"
echo "  ✓ Dev branch synced with main"
echo "  ✓ Release branch deleted"
echo ""
print_status "All branches now at: $(git rev-parse --short main)"
print_status "Current branch: $(git branch --show-current)"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
