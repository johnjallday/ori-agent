#!/bin/bash

# create-release.sh - Creates a new release for Ori Agent
# Usage: ./scripts/create-release.sh <version>
# Example: ./scripts/create-release.sh v1.0.1

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

# Check if version argument is provided
if [ $# -eq 0 ]; then
  print_error "Usage: $0 <version>"
  print_error "Example: $0 v1.0.1"
  exit 1
fi

VERSION=$1

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

# ENFORCE main branch for releases
CURRENT_BRANCH=$(git branch --show-current)
if [ "$CURRENT_BRANCH" != "main" ]; then
  print_error "Releases must be created from 'main' branch"
  print_error "Current branch: '$CURRENT_BRANCH'"
  echo ""
  print_error "To release:"
  print_error "  1. ./scripts/prepare-release.sh"
  print_error "  2. ./scripts/pre-release-check.sh $VERSION"
  print_error "  3. ./scripts/create-release.sh $VERSION"
  exit 1
fi

# Check that dev is merged into main
if git show-ref --verify --quiet refs/heads/dev; then
  print_status "Checking if dev branch is merged..."
  DEV_COMMITS=$(git rev-list main..dev --count 2>/dev/null || echo "0")
  if [ "$DEV_COMMITS" -gt 0 ]; then
    print_warning "dev branch has $DEV_COMMITS commit(s) not merged to main"
    print_warning "You should merge dev to main before releasing:"
    print_warning "  git merge dev"
    print_warning "Or run: ./scripts/prepare-release.sh"
    echo ""
    read -p "Continue anyway? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
      print_error "Release cancelled"
      exit 1
    fi
  else
    print_success "dev branch is fully merged into main"
  fi
fi

# Pull latest changes
print_status "Pulling latest changes..."
git pull origin "$CURRENT_BRANCH"

# Check that pre-release checks were run
print_status "Verifying pre-release checks were completed..."
print_warning "Make sure you ran './scripts/pre-release-check.sh' before releasing!"
read -p "Have you run pre-release-check.sh and all checks passed? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
  print_error "Please run './scripts/pre-release-check.sh' first to validate the release."
  exit 1
fi

# Update version in VERSION file (do this before creating tag)
VERSION_FILE="VERSION"
if [ -f "$VERSION_FILE" ]; then
  print_status "Updating version in VERSION file..."
  echo "$VERSION" >"$VERSION_FILE"

  if git diff --quiet "$VERSION_FILE"; then
    print_warning "Version in VERSION file was not updated (already correct)"
  else
    print_status "Updated version in VERSION file"
    git add "$VERSION_FILE"
    git commit -m "chore: bump version to $VERSION"
  fi
else
  print_status "Creating VERSION file..."
  echo "$VERSION" >"$VERSION_FILE"
  git add "$VERSION_FILE"
  git commit -m "chore: create VERSION file with version $VERSION"
fi

# Create git tag
print_status "Creating git tag $VERSION..."
git tag -a "$VERSION" -m "Release $VERSION

🚀 Generated with create-release.sh"

# Push the tag
print_status "Pushing tag to origin..."
git push origin "$VERSION"

# Build multi-platform installers and create GitHub release with GoReleaser
print_status "Building installers for all platforms (macOS, Windows, Linux)..."

# Check if GoReleaser is installed
if ! command -v goreleaser >/dev/null 2>&1; then
  print_error "GoReleaser not found. Install it with:"
  print_error "  brew install goreleaser"
  print_error "  or visit: https://goreleaser.com/install/"
  exit 1
fi

# Run GoReleaser to build all installers and create GitHub release
print_status "Running GoReleaser (this will build binaries, create installers, and publish to GitHub)..."
if goreleaser release --clean; then
  print_success "All platform installers built successfully"
  print_success "GitHub release created and installers uploaded"

  # Show what was built
  print_status "Built installers:"
  ls -lh dist/*.dmg dist/*.deb dist/*.rpm 2>/dev/null || echo "  (See dist/ directory for all artifacts)"

  # Verify and show release info
  if command -v gh >/dev/null 2>&1; then
    if gh release view "$VERSION" >/dev/null 2>&1; then
      ASSET_COUNT=$(gh release view "$VERSION" --json assets --jq '.assets | length')
      print_success "Uploaded $ASSET_COUNT file(s) as release assets"
      print_status "View release at: $(gh repo view --web --json url -q .url)/releases/tag/$VERSION"
    fi
  fi
else
  print_error "GoReleaser failed. Check the output above for errors."
  print_warning "The git tag has been pushed. You may need to delete it if you want to retry:"
  print_warning "  git tag -d $VERSION"
  print_warning "  git push origin :refs/tags/$VERSION"
  exit 1
fi

print_success "Release $VERSION created successfully!"
echo ""

# Switch back to dev branch for continued development
if git show-ref --verify --quiet refs/heads/dev; then
  print_status "Switching back to dev branch for continued development..."
  git switch dev
  print_success "Now on dev branch - ready for next features!"
  echo ""
fi

print_status "Next steps:"
print_status "  1. Review the release on GitHub"
print_status "  2. Add any additional release notes if needed"
print_status "  3. Continue development on dev branch"
echo ""
print_status "💡 Tip: main branch now represents the released version ($VERSION)"
print_status "💡 Tip: Continue your daily work on dev branch"
