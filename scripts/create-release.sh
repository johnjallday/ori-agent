#!/bin/bash

# create-release.sh - Creates a release from main branch (GitHub Flow)
#
# Usage:
#   ./scripts/create-release.sh <version>
#   ./scripts/create-release.sh v1.3.0
#
# Workflow:
#   1. Ensures you're on main branch with clean working tree
#   2. Runs quick tests
#   3. Updates VERSION file
#   4. Creates and pushes tag (triggers GitHub Actions release)

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
  echo "  ./scripts/create-release.sh <version>"
  echo ""
  echo -e "${BLUE}ARGUMENTS:${NC}"
  echo "  <version>       Version to release (e.g., v1.3.0 or 1.3.0)"
  echo "                  The 'v' prefix is added automatically if missing"
  echo ""
  echo -e "${BLUE}OPTIONS:${NC}"
  echo "  --help, -h      Show this help message"
  echo ""
  echo -e "${BLUE}EXAMPLES:${NC}"
  echo "  ./scripts/create-release.sh v1.3.0"
  echo "  ./scripts/create-release.sh 1.3.0"
  echo ""
  echo -e "${BLUE}WORKFLOW:${NC}"
  echo "  1. Ensures you're on main with clean working tree"
  echo "  2. Pulls latest changes"
  echo "  3. Runs quick tests"
  echo "  4. Updates VERSION file"
  echo "  5. Creates and pushes tag (triggers GitHub Actions release)"
  echo ""
  echo -e "${BLUE}RELATED COMMANDS:${NC}"
  echo "  ./scripts/pre-release-check.sh <version>   Full validation before release"
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

# Check if version argument is provided
if [ -z "$VERSION" ]; then
  print_error "Usage: $0 <version>"
  print_error "Example: $0 v1.3.0"
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

print_status "Creating release $VERSION for Ori Agent"

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

# Must be on main branch
if [ "$CURRENT_BRANCH" != "main" ]; then
  print_error "Must be on 'main' branch to create a release"
  print_error "Current branch: '$CURRENT_BRANCH'"
  echo ""
  print_status "Switch to main first:"
  echo "  cd /path/to/main/worktree"
  echo "  # or: git switch main"
  exit 1
fi

# Pull latest changes
print_status "Pulling latest changes..."
git pull origin main

# Run pre-release checks reminder
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
