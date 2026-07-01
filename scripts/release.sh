#!/bin/bash

# release.sh - Unified one-command release script
#
# Combines pre-release checks, branch merging, and tag creation into a single command.
# Supports git worktrees - run from either dev or main worktree.
#
# Usage:
#   ./scripts/release.sh              # Auto-computes next version (odometer: bumps last segment)
#   ./scripts/release.sh v0.1.0       # Explicit version for an editorial bump
#   ./scripts/release.sh --pre-release  # Candidate build (GitHub pre-release, doesn't touch main)
#
# Workflow:
#   1. Validates current branch (dev or main)
#   2. Runs full pre-release checks
#   3. If on dev: pushes to origin, merges to main via origin (worktree-safe)
#   4. Updates VERSION file and commits
#   5. Creates and pushes tag (triggers GitHub Actions release)

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

# Latest clean release tag (vX.Y.Z), or empty if there are none yet.
latest_release_tag() {
  git tag --list --sort=-v:refname 2>/dev/null \
    | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1
}

# Compute the next release version from the latest release tag.
# Odometer model: bump only the last segment (v0.0.85 -> v0.0.86), never
# rolling over. Major/minor are editorial and only move when a version is
# passed explicitly. Prints nothing if there are no release tags yet.
compute_next_version() {
  local latest ver major minor patch
  latest=$(latest_release_tag)
  [ -z "$latest" ] && { echo ""; return; }
  ver="${latest#v}"
  IFS='.' read -r major minor patch <<< "$ver"
  echo "v${major}.${minor}.$((patch + 1))"
}

# Given a clean base version (vX.Y.Z), return the next -rc.N pre-release tag for
# it, auto-incrementing N past any existing candidates for that base.
next_prerelease_version() {
  local base="$1" t n maxn=0
  while IFS= read -r t; do
    [ -z "$t" ] && continue
    n="${t##*-rc.}"
    if [[ "$n" =~ ^[0-9]+$ ]] && [ "$n" -gt "$maxn" ]; then
      maxn="$n"
    fi
  done < <(git tag --list "${base}-rc.*" 2>/dev/null)
  echo "${base}-rc.$((maxn + 1))"
}

# Help function
show_help() {
  echo ""
  echo "╔════════════════════════════════════════════════════════════════╗"
  echo "║           release.sh - Unified Release Manager                 ║"
  echo "╚════════════════════════════════════════════════════════════════╝"
  echo ""
  echo -e "${BLUE}USAGE:${NC}"
  echo "  ./scripts/release.sh [version]"
  echo ""
  echo -e "${BLUE}ARGUMENTS:${NC}"
  echo "  [version]       Version to release (e.g., v0.1.0 or 0.1.0)"
  echo "                  If omitted, the next version is auto-computed from the"
  echo "                  latest release tag (odometer: bumps the last segment)."
  echo "                  Pass a version explicitly to make an editorial bump."
  echo "                  The 'v' prefix is added automatically if missing"
  echo ""
  echo -e "${BLUE}OPTIONS:${NC}"
  echo "  --help, -h      Show this help message"
  echo "  --dry-run       Run checks but don't create tag or push"
  echo "  --skip-checks   Skip pre-release checks (use with caution)"
  echo "  --pre-release   Cut a candidate build: tags dev with an -rc.N suffix,"
  echo "                  publishes a GitHub pre-release (not 'Latest'), and does"
  echo "                  NOT merge to main. Stable is the default (no flag)."
  echo ""
  echo -e "${BLUE}EXAMPLES:${NC}"
  echo "  ./scripts/release.sh                   # Auto-compute next version, release (stable)"
  echo "  ./scripts/release.sh v0.1.0            # Editorial bump to v0.1.0 (stable)"
  echo "  ./scripts/release.sh --pre-release     # Cut a candidate (v0.0.86-rc.1, not 'Latest')"
  echo "  ./scripts/release.sh --dry-run         # Auto-compute + validate, no release"
  echo ""
  echo -e "${BLUE}WORKFLOW:${NC}"
  echo "  1. Validates branch (must be on 'dev' or 'main')"
  echo "  2. Runs full pre-release checks (tests, lint, build)"
  echo "  3. If on dev: merges to main (worktree-safe via origin push)"
  echo "  4. Updates VERSION file"
  echo "  5. Creates and pushes tag (triggers GitHub Actions)"
  echo ""
  echo -e "${BLUE}WORKTREE SUPPORT:${NC}"
  echo "  Works with git worktrees - run from either dev or main worktree."
  echo "  Fast-forward merges are pushed directly to origin/main."
  echo "  Non-fast-forward merges prompt you to complete from main worktree."
  echo ""
  echo -e "${BLUE}REPLACES:${NC}"
  echo "  This script replaces the manual workflow of:"
  echo "    1. ./scripts/pre-release-check.sh [version]"
  echo "    2. git checkout main && git merge dev"
  echo "    3. ./scripts/create-release.sh [version]"
  echo ""
  exit 0
}

# Parse arguments
VERSION=""
DRY_RUN=false
SKIP_CHECKS=false
PRE_RELEASE=false

for arg in "$@"; do
  case $arg in
    --help|-h)
      show_help
      ;;
    --dry-run)
      DRY_RUN=true
      ;;
    --skip-checks)
      SKIP_CHECKS=true
      ;;
    --pre-release|--prerelease)
      PRE_RELEASE=true
      ;;
    *)
      if [ -z "$VERSION" ]; then
        VERSION="$arg"
      fi
      ;;
  esac
done

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║     Ori Agent Unified Release Script       ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# Read current VERSION file
CURRENT_VERSION=""
VERSION_FILE="VERSION"
if [ -f "$VERSION_FILE" ]; then
  CURRENT_VERSION=$(cat "$VERSION_FILE" | tr -d '[:space:]')
fi

# If no version argument was provided, auto-compute the next version from the
# latest release tag (odometer: bump the last segment). This makes the version
# a byproduct of shipping rather than a per-release decision. To make an
# editorial bump, pass a version explicitly (e.g. v0.1.0 or v1.0.0).
if [ -z "$VERSION" ]; then
  print_status "Fetching latest tags to compute next version..."
  git fetch --tags origin >/dev/null 2>&1 || true

  SUGGESTED_VERSION=$(compute_next_version)

  if [ -n "$SUGGESTED_VERSION" ]; then
    VERSION="$SUGGESTED_VERSION"
    print_status "Latest release tag:  ${YELLOW}$(latest_release_tag)${NC}"
    print_success "Next version (auto): ${GREEN}${VERSION}${NC}"
    print_status "Editorial bump? Re-run with a version, e.g. ${BLUE}./scripts/release.sh v0.1.0${NC}"
  else
    # No release tags yet — fall back to an interactive prompt.
    if [ -n "$CURRENT_VERSION" ]; then
      print_status "Current VERSION file: ${YELLOW}$CURRENT_VERSION${NC}"
    fi
    echo -n "Enter version to release (e.g., v0.1.0): "
    read -r VERSION

    if [ -z "$VERSION" ]; then
      print_error "No version provided. Aborting."
      exit 1
    fi
  fi
fi

# Ensure version starts with 'v'
if [[ ! "$VERSION" =~ ^v ]]; then
  VERSION="v$VERSION"
  print_status "Added 'v' prefix: $VERSION"
fi

# For a pre-release (candidate) build, append an auto-incrementing -rc.N suffix
# to the base version (unless the caller already supplied their own suffix).
# goreleaser's `prerelease: auto` marks any suffixed tag as a GitHub
# pre-release, so it never becomes the "Latest" (stable) pin and the in-app
# updater skips it by default.
if [ "$PRE_RELEASE" = true ] && [[ "$VERSION" != *-* ]]; then
  if [[ $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    VERSION="$(next_prerelease_version "$VERSION")"
    print_status "Pre-release build: ${YELLOW}${VERSION}${NC} (won't move the stable pin)"
  fi
fi

# Validate version format (an optional -suffix marks a pre-release)
if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  print_error "Version must be vX.Y.Z or vX.Y.Z-suffix (e.g., v1.0.1 or v1.0.1-rc.1)"
  exit 1
fi

# Check if we're in a git repository
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  print_error "Not in a git repository"
  exit 1
fi

# Check if tag already exists
if git tag -l | grep -q "^$VERSION$"; then
  print_error "Tag $VERSION already exists"
  exit 1
fi

# Get current branch
CURRENT_BRANCH=$(git branch --show-current)
STARTED_ON_DEV=false

# Validate branch
if [ "$CURRENT_BRANCH" = "dev" ]; then
  STARTED_ON_DEV=true
  print_status "Current branch: ${YELLOW}dev${NC}"
  print_status "Will merge to main after checks pass"
elif [ "$CURRENT_BRANCH" = "main" ]; then
  print_status "Current branch: ${GREEN}main${NC}"
else
  print_error "Must be on 'dev' or 'main' branch to release"
  print_error "Current branch: '$CURRENT_BRANCH'"
  exit 1
fi

# Show release summary
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "  Release: ${GREEN}$VERSION${NC}"
if [ -n "$CURRENT_VERSION" ] && [ "$VERSION" != "$CURRENT_VERSION" ]; then
  echo -e "  Current: ${YELLOW}$CURRENT_VERSION${NC} → ${GREEN}$VERSION${NC}"
fi
echo -e "  Branch:  ${BLUE}$CURRENT_BRANCH${NC}"
if [ "$PRE_RELEASE" = true ]; then
  echo -e "  Channel: ${YELLOW}pre-release${NC} (candidate — won't become 'Latest')"
else
  echo -e "  Channel: ${GREEN}stable${NC} (GitHub 'Latest' — the pin the updater follows)"
fi
if [ "$DRY_RUN" = true ]; then
  echo -e "  Mode:    ${YELLOW}DRY RUN${NC} (no tag/push)"
fi
if [ "$SKIP_CHECKS" = true ]; then
  echo -e "  Checks:  ${YELLOW}SKIPPED${NC}"
fi
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

echo -n "Proceed with release? [y/N]: "
read -r CONFIRM
if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
  print_status "Release cancelled."
  exit 0
fi

# ════════════════════════════════════════════════════════════════════
# STEP 1: Pre-release checks
# ════════════════════════════════════════════════════════════════════

if [ "$SKIP_CHECKS" = false ]; then
  echo ""
  echo "════════════════════════════════════════════"
  echo "STEP 1: Running pre-release checks"
  echo "════════════════════════════════════════════"
  echo ""

  if [ -f "./scripts/pre-release-check.sh" ]; then
    # Run pre-release checks (it handles auto-commits)
    if ! ./scripts/pre-release-check.sh "$VERSION" --no-smoke; then
      print_error "Pre-release checks failed. Fix issues before releasing."
      exit 1
    fi
  else
    print_warning "pre-release-check.sh not found, running basic checks..."

    # Fallback: basic checks
    print_status "Running tests..."
    go test -short ./... || {
      print_error "Tests failed."
      exit 1
    }

    print_status "Building..."
    go build -o bin/ori-agent ./cmd/server || {
      print_error "Build failed."
      exit 1
    }
  fi

  print_success "Pre-release checks passed"
else
  print_warning "Skipping pre-release checks (--skip-checks)"
fi

# ════════════════════════════════════════════════════════════════════
# STEP 2: Merge dev to main (if started on dev)
# ════════════════════════════════════════════════════════════════════

if [ "$STARTED_ON_DEV" = true ] && [ "$PRE_RELEASE" = true ]; then
  echo ""
  echo "════════════════════════════════════════════"
  echo "STEP 2: Pre-release — tag dev (no merge to main)"
  echo "════════════════════════════════════════════"
  echo ""
  print_status "Pushing dev so the tag points at an origin commit..."
  git push origin dev

elif [ "$STARTED_ON_DEV" = true ]; then
  echo ""
  echo "════════════════════════════════════════════"
  echo "STEP 2: Merging dev to main"
  echo "════════════════════════════════════════════"
  echo ""

  # Fetch latest
  print_status "Fetching latest changes..."
  git fetch origin

  # Check if main is checked out in another worktree
  MAIN_WORKTREE=$(git worktree list | grep '\[main\]' | awk '{print $1}')
  USING_WORKTREES=false

  if [ -n "$MAIN_WORKTREE" ] && [ "$MAIN_WORKTREE" != "$PROJECT_DIR" ]; then
    USING_WORKTREES=true
    print_status "Detected worktree setup"
    print_status "Main worktree: ${BLUE}$MAIN_WORKTREE${NC}"
  fi

  if [ "$USING_WORKTREES" = true ]; then
    # Worktree mode: push dev, then work from main worktree
    print_status "Pushing dev branch to origin..."
    git push origin dev

    # Check if fast-forward merge is possible
    if git merge-base --is-ancestor origin/main HEAD; then
      # Fast-forward possible - we can push directly to main
      print_status "Fast-forward merge possible, updating main via origin..."
      git push origin dev:main
      print_success "Main branch updated via origin"
    else
      # Need actual merge - must be done from main worktree
      echo ""
      print_warning "Merge required (not fast-forward)"
      echo ""
      echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
      echo "  Complete release from main worktree:"
      echo ""
      echo "    cd $MAIN_WORKTREE"
      echo "    git pull origin main"
      echo "    git merge origin/dev --no-edit"
      echo "    ./scripts/release.sh $VERSION --skip-checks"
      echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
      echo ""
      exit 0
    fi

    # Continue tagging from dev worktree (main is updated on origin)
    print_status "Fetching updated main..."
    git fetch origin main:main 2>/dev/null || true

  else
    # Standard mode: switch branches
    print_status "Switching to main branch..."
    git switch main

    # Pull latest main
    print_status "Pulling latest main..."
    git pull origin main

    # Merge dev into main
    print_status "Merging dev into main..."
    if ! git merge dev --no-edit -m "Merge dev for release $VERSION"; then
      print_error "Merge conflict! Resolve manually and re-run."
      exit 1
    fi

    print_success "Merged dev into main"
  fi
fi

# ════════════════════════════════════════════════════════════════════
# STEP 3: Update VERSION file (if needed)
# ════════════════════════════════════════════════════════════════════

echo ""
echo "════════════════════════════════════════════"
echo "STEP 3: Updating VERSION file"
echo "════════════════════════════════════════════"
echo ""

# Check if VERSION needs updating
CURRENT_VERSION_NOW=$(cat "$VERSION_FILE" 2>/dev/null | tr -d '[:space:]')
if [ "$PRE_RELEASE" = true ]; then
  print_status "Pre-release: leaving the VERSION file and main branch untouched"
elif [ "$CURRENT_VERSION_NOW" != "$VERSION" ]; then
  print_status "Updating VERSION: $CURRENT_VERSION_NOW → $VERSION"
  echo "$VERSION" > "$VERSION_FILE"
  git add "$VERSION_FILE"
  git commit -m "chore: bump version to $VERSION" --no-verify

  # If using worktrees and on dev, push version update to main
  if [ "$STARTED_ON_DEV" = true ] && [ "${USING_WORKTREES:-false}" = true ]; then
    print_status "Pushing version update to main (worktree mode)..."
    git push origin dev:main
  fi

  print_success "VERSION file updated"
else
  print_status "VERSION file already set to $VERSION"
fi

# ════════════════════════════════════════════════════════════════════
# STEP 4: Create and push tag
# ════════════════════════════════════════════════════════════════════

echo ""
echo "════════════════════════════════════════════"
echo "STEP 4: Creating release tag"
echo "════════════════════════════════════════════"
echo ""

if [ "$DRY_RUN" = true ]; then
  print_warning "DRY RUN: Would create tag $VERSION and push"
  if [ "$PRE_RELEASE" = true ]; then
    print_warning "DRY RUN: Pre-release — would NOT touch main"
  else
    print_warning "DRY RUN: Would push main branch"
  fi
  echo ""
  print_success "Dry run complete - no changes pushed"
  exit 0
fi

# Push main branch (skip for pre-releases and when worktree mode already pushed)
if [ "$PRE_RELEASE" = false ] && [ "${USING_WORKTREES:-false}" = false ]; then
  print_status "Pushing main branch..."
  git push origin main
fi

# Create annotated tag on the right commit
if [ "$STARTED_ON_DEV" = true ] && [ "${USING_WORKTREES:-false}" = true ]; then
  # Worktree mode: tag origin/main (which has our changes)
  print_status "Creating tag $VERSION on origin/main..."
  git tag -a "$VERSION" origin/main -m "Release $VERSION"
else
  # Standard mode: tag current HEAD (which is main)
  print_status "Creating tag $VERSION..."
  git tag -a "$VERSION" -m "Release $VERSION"
fi

# Push tag (triggers GitHub Actions release workflow)
print_status "Pushing tag $VERSION..."
git push origin "$VERSION"

# ════════════════════════════════════════════════════════════════════
# STEP 5: Post-release
# ════════════════════════════════════════════════════════════════════

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║           RELEASE COMPLETE                 ║"
echo "╚════════════════════════════════════════════╝"
echo ""

print_success "Release $VERSION triggered!"
echo ""
if [ "$PRE_RELEASE" = true ]; then
  print_status "This is a ${YELLOW}pre-release${NC} — GitHub won't mark it 'Latest' and the updater skips it."
  print_status "To promote to stable, cut a normal release: ${BLUE}./scripts/release.sh${NC}"
fi
print_status "GitHub Actions is now building the release."
print_status "View progress: ${BLUE}gh run list --workflow=release.yml${NC}"
echo ""

# Offer to sync dev branch (stable releases only; pre-releases never touch main)
if [ "$STARTED_ON_DEV" = true ] && [ "$PRE_RELEASE" = false ]; then
  if [ "${USING_WORKTREES:-false}" = true ]; then
    # Worktree mode: already on dev, just pull/merge
    echo -n "Sync dev with main? [Y/n]: "
    read -r SYNC_DEV
    if [[ ! "$SYNC_DEV" =~ ^[Nn]$ ]]; then
      git fetch origin main
      git merge origin/main --no-edit
      git push origin dev
      print_success "dev branch synced with main"
    fi
  else
    # Standard mode: switch back to dev
    echo -n "Switch back to dev and sync with main? [Y/n]: "
    read -r SYNC_DEV
    if [[ ! "$SYNC_DEV" =~ ^[Nn]$ ]]; then
      git switch dev
      git merge main --no-edit
      git push origin dev
      print_success "dev branch synced with main"
    fi
  fi
fi

echo ""
print_status "Done!"
