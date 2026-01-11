#!/bin/bash
# post-release-sync.sh - Sync branches after a successful release
#
# This script:
#   1. Checks if a release workflow is currently running (aborts if so)
#   2. Checks if the release was successful on GitHub
#   3. Updates local main from origin
#   4. Merges main to dev (in dev worktree)
#   5. Refreshes other worktrees (optional)
#   6. Prunes assets from old releases (keeps binaries for 5 most recent)
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

WORKTREES_ROOT="${WORKTREES_ROOT:-../worktrees}"
DEV_WORKTREE="${DEV_WORKTREE:-$WORKTREES_ROOT/ori-agent-dev}"
CLAUDE_WORKTREE="${CLAUDE_WORKTREE:-$WORKTREES_ROOT/ori-agent-claude}"
CODEX_WORKTREE="${CODEX_WORKTREE:-$WORKTREES_ROOT/ori-agent-codex}"

ensure_clean_worktree() {
  local worktree_path="$1"
  local worktree_label="$2"

  if [ ! -d "$worktree_path" ]; then
    print_error "$worktree_label worktree not found: $worktree_path"
    exit 1
  fi

  if ! git -C "$worktree_path" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    print_error "$worktree_label worktree is not a git worktree: $worktree_path"
    exit 1
  fi

  if ! git -C "$worktree_path" diff-index --quiet HEAD -- 2>/dev/null; then
    print_error "$worktree_label worktree has uncommitted changes. Please commit or stash them first."
    git -C "$worktree_path" status --short
    exit 1
  fi
}

refresh_worktree() {
  local worktree_path="$1"
  local worktree_label="$2"

  if [ ! -d "$worktree_path" ]; then
    print_status "$worktree_label worktree not found at $worktree_path; skipping"
    return
  fi

  if ! git -C "$worktree_path" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    print_warning "$worktree_label worktree is not a git worktree; skipping"
    return
  fi

  if ! git -C "$worktree_path" diff-index --quiet HEAD -- 2>/dev/null; then
    print_warning "$worktree_label worktree has uncommitted changes; skipping"
    return
  fi

  local current_branch
  current_branch=$(git -C "$worktree_path" branch --show-current)
  if [ -z "$current_branch" ]; then
    print_warning "$worktree_label worktree is in detached HEAD; skipping"
    return
  fi

  print_status "Updating $worktree_label worktree ($current_branch) with latest main..."
  git -C "$worktree_path" fetch origin "$current_branch" main || print_warning "Could not fetch $worktree_label"

  if [ "$current_branch" = "main" ]; then
    if ! git -C "$worktree_path" pull --ff-only origin main; then
      print_error "Failed to update main in $worktree_label worktree"
      exit 1
    fi
    return
  fi

  if ! git -C "$worktree_path" pull --ff-only origin "$current_branch"; then
    print_error "Failed to update $current_branch in $worktree_label worktree"
    exit 1
  fi

  if git -C "$worktree_path" merge origin/main -m "Merge main ($VERSION release) into $current_branch"; then
    if ! git -C "$worktree_path" push origin "$current_branch"; then
      print_error "Failed to push $current_branch for $worktree_label worktree"
      exit 1
    fi
  else
    print_error "Merge conflict in $worktree_label worktree ($current_branch). Resolve manually."
    exit 1
  fi
}

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

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║       Post-Release Sync                    ║"
echo "╚════════════════════════════════════════════╝"
echo ""
print_status "Version: $VERSION"
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

# Step 3: Update main from origin
print_status "Updating main from origin..."
git checkout main
git fetch origin main
git pull --ff-only origin main
print_success "Main branch updated from origin"
echo ""

# Step 4: Merge main to dev
print_status "Syncing dev with main (worktree: $DEV_WORKTREE)..."
ensure_clean_worktree "$DEV_WORKTREE" "Dev"

CURRENT_BRANCH=$(git -C "$DEV_WORKTREE" branch --show-current)
if [ "$CURRENT_BRANCH" != "dev" ]; then
  print_warning "Dev worktree is on '$CURRENT_BRANCH'; checking out dev..."
  git -C "$DEV_WORKTREE" checkout dev
fi

print_status "Fetching main and dev in dev worktree..."
git -C "$DEV_WORKTREE" fetch origin dev main

# Check if dev needs updating
DEV_CONTAINS_MAIN=$(git -C "$DEV_WORKTREE" merge-base --is-ancestor origin/main dev && echo "yes" || echo "no")

if [ "$DEV_CONTAINS_MAIN" = "yes" ]; then
  print_status "Dev already contains all main commits"
  git -C "$DEV_WORKTREE" pull origin dev
else
  print_status "Merging main to dev..."
  git -C "$DEV_WORKTREE" pull origin dev

  if git -C "$DEV_WORKTREE" merge origin/main -m "Merge main ($VERSION release) back to dev"; then
    git -C "$DEV_WORKTREE" push origin dev
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

# Step 5: Refresh other worktrees
print_status "Refreshing other worktrees..."
refresh_worktree "$CLAUDE_WORKTREE" "Claude"
refresh_worktree "$CODEX_WORKTREE" "Codex"
echo ""

# Step 6: Prune assets from old releases (keep binaries only for 5 most recent)
RELEASES_WITH_ASSETS=5
print_status "Pruning assets from old releases (keeping binaries for $RELEASES_WITH_ASSETS most recent)..."

# Get all releases sorted by creation date (newest first)
ALL_RELEASES=$(gh release list --json tagName --limit 100 -q '.[].tagName' 2>/dev/null || echo "")

if [ -n "$ALL_RELEASES" ]; then
  RELEASE_COUNT=$(echo "$ALL_RELEASES" | wc -l | tr -d ' ')

  if [ "$RELEASE_COUNT" -gt "$RELEASES_WITH_ASSETS" ]; then
    # Get older releases to prune (skip the first N releases which are the newest)
    RELEASES_TO_PRUNE=$(echo "$ALL_RELEASES" | tail -n +$((RELEASES_WITH_ASSETS + 1)))
    PRUNE_COUNT=$(echo "$RELEASES_TO_PRUNE" | wc -l | tr -d ' ')

    print_status "Found $RELEASE_COUNT releases, pruning assets from $PRUNE_COUNT older releases..."

    while IFS= read -r release_tag; do
      if [ -n "$release_tag" ]; then
        # Get assets for this release
        ASSETS=$(gh release view "$release_tag" --json assets -q '.assets[].name' 2>/dev/null || echo "")

        if [ -n "$ASSETS" ]; then
          print_status "  Pruning assets from: $release_tag"
          while IFS= read -r asset_name; do
            if [ -n "$asset_name" ]; then
              if gh release delete-asset "$release_tag" "$asset_name" --yes 2>/dev/null; then
                print_success "    Deleted: $asset_name"
              else
                print_warning "    Failed to delete: $asset_name"
              fi
            fi
          done <<< "$ASSETS"
        else
          print_status "  $release_tag: no assets to prune"
        fi
      fi
    done <<< "$RELEASES_TO_PRUNE"

    print_success "Pruned assets from old releases"
  else
    print_status "Only $RELEASE_COUNT releases exist, no pruning needed"
  fi
else
  print_warning "Could not fetch release list"
fi
echo ""

# Summary
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
print_success "Post-release sync complete for $VERSION!"
echo ""
echo "  ✓ Release verified on GitHub ($ASSET_COUNT assets)"
echo "  ✓ Main branch updated from origin"
echo "  ✓ Dev branch synced with main"
echo "  ✓ Old release assets pruned (binaries kept for $RELEASES_WITH_ASSETS most recent)"
echo ""
print_status "All branches now at: $(git rev-parse --short main)"
print_status "Current branch: $(git branch --show-current)"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
