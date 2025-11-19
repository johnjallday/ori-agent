#!/bin/bash

# prepare-release.sh - Prepares the main branch for release by merging dev
# Usage: ./scripts/prepare-release.sh
# This automates the branch workflow for the two-branch strategy (dev → main)

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

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║     Ori Agent Release Preparation         ║"
echo "╚════════════════════════════════════════════╝"
echo ""
print_status "This script will merge dev → main for release"
echo ""

# Check if we're in a git repository
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  print_error "Not in a git repository"
  exit 1
fi

# Check if dev branch exists
if ! git show-ref --verify --quiet refs/heads/dev; then
  print_error "dev branch does not exist"
  print_error "Create it with: git checkout -b dev"
  exit 1
fi

# Check for uncommitted changes
if ! git diff-index --quiet HEAD --; then
  print_error "You have uncommitted changes. Please commit or stash them first."
  echo ""
  git status --short
  exit 1
fi

CURRENT_BRANCH=$(git branch --show-current)
print_status "Current branch: $CURRENT_BRANCH"
echo ""

# Switch to dev and pull latest
print_status "Switching to dev branch..."
git checkout dev

print_status "Pulling latest from origin/dev..."
git pull origin dev

# Remind user to run pre-release checks first
print_warning "⚠️  Have you run pre-release checks on dev branch?"
print_status "Best practice: Run './scripts/pre-release-check.sh' on dev BEFORE merging"
echo ""
read -p "Have you run pre-release checks and all tests passed? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
  print_warning "Merge cancelled"
  echo ""
  print_status "Run pre-release checks first:"
  print_status "  ./scripts/pre-release-check.sh"
  echo ""
  exit 0
fi

echo ""

# Show what commits will be merged to main
DEV_COMMITS=$(git rev-list main..dev --count 2>/dev/null || echo "0")
if [ "$DEV_COMMITS" -eq 0 ]; then
  print_success "dev is already merged into main - nothing to do!"
  print_status "Switching back to main..."
  git checkout main
  exit 0
fi

echo ""
print_status "There are $DEV_COMMITS commit(s) in dev that are not in main:"
echo ""
git log --oneline --graph main..dev | head -20
echo ""

read -p "Merge these commits to main? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
  print_warning "Merge cancelled"
  exit 0
fi

# Switch to main
print_status "Switching to main branch..."
git checkout main

# Pull latest main
print_status "Pulling latest from origin/main..."
git pull origin main

# Merge dev into main
print_status "Merging dev into main..."
if git merge dev --no-edit; then
  print_success "Successfully merged dev into main"
else
  print_error "Merge failed - please resolve conflicts manually"
  print_error "After resolving conflicts:"
  print_error "  git add <resolved-files>"
  print_error "  git commit"
  print_error "  git push origin main"
  exit 1
fi

# Push to remote
print_status "Pushing main to origin..."
git push origin main

print_success "Release preparation complete!"
echo ""
print_status "main branch now contains all commits from dev"
print_status "You can now run the pre-release checks:"
echo ""
echo "  ./scripts/pre-release-check.sh v0.X.X"
echo ""
