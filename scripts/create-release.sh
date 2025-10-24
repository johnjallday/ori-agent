#!/bin/bash

# create-release.sh - Creates a new release for Dolphin Agent
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

print_status "Creating release $VERSION for Dolphin Agent"

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

# Make sure we're on main branch
CURRENT_BRANCH=$(git branch --show-current)
if [ "$CURRENT_BRANCH" != "main" ]; then
  print_warning "You're on branch '$CURRENT_BRANCH', not 'main'"
  read -p "Continue anyway? (y/N): " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    print_error "Release cancelled"
    exit 1
  fi
fi

# Pull latest changes
print_status "Pulling latest changes..."
git pull origin "$CURRENT_BRANCH"

# Run tests
print_status "Running tests..."
if ! go test ./...; then
  print_error "Tests failed. Fix them before creating a release."
  exit 1
fi

# Build cross-platform release binaries
print_status "Building cross-platform release binaries..."
if ! ./scripts/build-release.sh; then
  print_error "Cross-platform build failed. Fix build errors before creating a release."
  exit 1
fi

# Update version in VERSION file
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

# Check if GitHub CLI is available for creating release
if command -v gh >/dev/null 2>&1; then
  print_status "Creating GitHub release..."

  # Generate release notes
  PREV_TAG=$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")

  if [ -n "$PREV_TAG" ]; then
    print_status "Generating release notes since $PREV_TAG..."
    RELEASE_NOTES="## Changes since $PREV_TAG

$(git log $PREV_TAG..HEAD --oneline --pretty=format:"- %s" | head -20)

---
🤖 Release created automatically with create-release.sh"
  else
    RELEASE_NOTES="## Release $VERSION

🤖 Release created automatically with create-release.sh"
  fi

  # Upload release assets
  print_status "Uploading release binaries..."
  UPLOAD_SUCCESS=true

  # Upload all platform binaries from dist/
  if [ -d "dist" ]; then
    for asset in dist/*.tar.gz dist/*.zip; do
      if [ -f "$asset" ]; then
        print_status "Uploading $(basename "$asset")..."
        if ! gh release upload "$VERSION" "$asset"; then
          print_error "Failed to upload $(basename "$asset")"
          UPLOAD_SUCCESS=false
        fi
      fi
    done
  else
    print_error "dist/ directory not found. Build may have failed."
    UPLOAD_SUCCESS=false
  fi

  # Create the release
  if gh release create "$VERSION" \
    --title "Dolphin Agent $VERSION" \
    --notes "$RELEASE_NOTES"; then
    print_success "GitHub release created successfully!"

    if [ "$UPLOAD_SUCCESS" = true ]; then
      print_success "All release binaries uploaded successfully!"
    else
      print_warning "Some binaries failed to upload. Check the release page."
    fi

    print_status "View release at: $(gh repo view --web --json url -q .url)/releases/tag/$VERSION"
  else
    print_error "Failed to create GitHub release"
    print_status "Tag has been created and pushed. You can create the release manually at:"
    print_status "https://github.com/johnjallday/ori-agent/releases/new?tag=$VERSION"
  fi
else
  print_warning "GitHub CLI (gh) not found. Tag created but no GitHub release."
  print_status "Create release manually at:"
  print_status "https://github.com/johnjallday/ori-agent/releases/new?tag=$VERSION"
fi

print_success "Release $VERSION created successfully!"
print_status "Next steps:"
print_status "  1. Review the release on GitHub"
print_status "  2. Add any additional release notes if needed"
print_status "  3. Update documentation if necessary"
