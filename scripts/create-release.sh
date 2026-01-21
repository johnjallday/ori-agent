#!/bin/bash

# create-release.sh - Creates a release tag on main branch
#
# Usage:
#   ./scripts/create-release.sh [version]
#   ./scripts/create-release.sh v1.3.0
#   ./scripts/create-release.sh          # Prompts for version interactively
#
# Prerequisites (handled by release-manager agent or pre-release-check.sh):
#   - Pre-release checks passed
#   - VERSION file updated
#   - Dev branch merged to main
#   - Main branch pushed
#
# This script only:
#   1. Verifies main branch is ready
#   2. Creates and pushes the release tag (triggers GitHub Actions)

set -e

# Get the script directory and project directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Support running from any worktree - find main worktree
MAIN_WORKTREE=""
if git worktree list 2>/dev/null | grep -q "\[main\]"; then
  MAIN_WORKTREE=$(git worktree list | grep "\[main\]" | awk '{print $1}')
fi

# If we found main worktree, use it; otherwise use current directory
if [ -n "$MAIN_WORKTREE" ]; then
  PROJECT_DIR="$MAIN_WORKTREE"
fi

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
  echo "║              create-release.sh - Release Tag Creator           ║"
  echo "╚════════════════════════════════════════════════════════════════╝"
  echo ""
  echo -e "${BLUE}USAGE:${NC}"
  echo "  ./scripts/create-release.sh [version]"
  echo ""
  echo -e "${BLUE}ARGUMENTS:${NC}"
  echo "  [version]       Version to release (e.g., v1.3.0 or 1.3.0)"
  echo "                  If omitted, reads from VERSION file"
  echo "                  The 'v' prefix is added automatically if missing"
  echo ""
  echo -e "${BLUE}OPTIONS:${NC}"
  echo "  --help, -h      Show this help message"
  echo ""
  echo -e "${BLUE}EXAMPLES:${NC}"
  echo "  ./scripts/create-release.sh           # Uses version from VERSION file"
  echo "  ./scripts/create-release.sh v1.3.0"
  echo "  ./scripts/create-release.sh 1.3.0"
  echo ""
  echo -e "${BLUE}PREREQUISITES:${NC}"
  echo "  Run these first (or use release-manager agent):"
  echo "  1. ./scripts/pre-release-check.sh <version>  # Validates and updates VERSION"
  echo "  2. Push dev branch"
  echo "  3. Merge dev to main"
  echo "  4. Push main branch"
  echo ""
  echo -e "${BLUE}WHAT THIS SCRIPT DOES:${NC}"
  echo "  1. Verifies main branch is clean and up-to-date"
  echo "  2. Creates annotated tag"
  echo "  3. Pushes tag (triggers GitHub Actions release workflow)"
  echo ""
  echo -e "${BLUE}WORKTREE SUPPORT:${NC}"
  echo "  This script can be run from any worktree - it automatically"
  echo "  finds and operates on the main worktree."
  echo ""
  echo -e "${BLUE}RELATED COMMANDS:${NC}"
  echo "  gh run list --workflow=release.yml         View release workflow progress"
  echo ""
  exit 0
}

# Parse arguments
VERSION=""
for arg in "$@"; do
  case $arg in
    --help|-h)
      show_help
      ;;
    *)
      if [ -z "$VERSION" ]; then
        VERSION="$arg"
      fi
      ;;
  esac
done

# Read current VERSION file
VERSION_FILE="VERSION"
CURRENT_VERSION=""
if [ -f "$VERSION_FILE" ]; then
  CURRENT_VERSION=$(cat "$VERSION_FILE" | tr -d '[:space:]')
fi

# If no version argument provided, use VERSION file
if [ -z "$VERSION" ]; then
  if [ -n "$CURRENT_VERSION" ]; then
    VERSION="$CURRENT_VERSION"
    print_status "Using version from VERSION file: ${GREEN}$VERSION${NC}"
  else
    print_error "No version provided and VERSION file not found."
    print_status "Usage: ./scripts/create-release.sh v1.3.0"
    exit 1
  fi
fi

# Ensure version starts with 'v'
if [[ ! "$VERSION" =~ ^v ]]; then
  VERSION="v$VERSION"
  print_status "Added 'v' prefix: $VERSION"
fi

# Validate version format
if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  print_error "Version must be in format vX.Y.Z (e.g., v1.0.1)"
  exit 1
fi

# Check if we're in a git repository
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  print_error "Not in a git repository"
  exit 1
fi

# Check current branch (should be main)
CURRENT_BRANCH=$(git branch --show-current)
if [ "$CURRENT_BRANCH" != "main" ]; then
  print_status "Operating on main worktree: $PROJECT_DIR"
fi

# Check for uncommitted changes
if ! git diff-index --quiet HEAD --; then
  print_error "Main branch has uncommitted changes."
  print_status "Run pre-release-check.sh first to commit all changes."
  git status --porcelain
  exit 1
fi

# Check if tag already exists
if git tag -l | grep -q "^$VERSION$"; then
  print_error "Tag $VERSION already exists"
  exit 1
fi

# Verify VERSION file matches
if [ "$VERSION" != "$CURRENT_VERSION" ]; then
  print_warning "VERSION file ($CURRENT_VERSION) doesn't match requested version ($VERSION)"
  echo -n "Continue anyway? [y/N]: "
  read -r CONFIRM
  if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
    print_status "Release cancelled."
    exit 0
  fi
fi

# Confirm with user
echo ""
print_status "Ready to create release tag: ${GREEN}$VERSION${NC}"
print_status "Working directory: $PROJECT_DIR"
echo ""
echo -n "Create and push tag $VERSION? [y/N]: "
read -r CONFIRM

if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
  print_status "Release cancelled."
  exit 0
fi

# Create and push tag (triggers release.yml workflow)
echo ""
print_status "Creating annotated tag $VERSION..."
git tag -a "$VERSION" -m "Release $VERSION"

print_status "Pushing tag to origin..."
git push origin "$VERSION"

echo ""
print_success "Release $VERSION triggered!"
echo ""
print_status "The release workflow is now running on GitHub Actions."
print_status "View progress:"
echo "  gh run list --workflow=release.yml"
echo "  gh run watch"
echo ""
