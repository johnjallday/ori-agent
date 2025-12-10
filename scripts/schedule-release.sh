#!/bin/bash

# schedule-release.sh - Schedule a release branch for a future date
#
# Usage:
#   ./scripts/schedule-release.sh 2d          # Release in 2 days
#   ./scripts/schedule-release.sh 1w          # Release in 1 week
#   ./scripts/schedule-release.sh 2024-12-25  # Release on specific date
#
# This creates a RELEASE_DATE file on the release branch.
# The scheduled-release workflow checks daily and releases when the date arrives.

set -e

# Get the script directory and project directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_DIR" || exit 1

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_status() { echo -e "${BLUE}[INFO]${NC} $1"; }
print_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARNING]${NC} $1"; }
print_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Check argument
WHEN="$1"
if [ -z "$WHEN" ]; then
  echo "Usage: $0 <when>"
  echo ""
  echo "Examples:"
  echo "  $0 2d           # Release in 2 days"
  echo "  $0 1w           # Release in 1 week"
  echo "  $0 3d           # Release in 3 days"
  echo "  $0 2024-12-25   # Release on specific date (YYYY-MM-DD)"
  echo "  $0 tomorrow     # Release tomorrow"
  echo "  $0 now          # Release immediately (triggers workflow)"
  exit 1
fi

# Check we're on a release branch
CURRENT_BRANCH=$(git branch --show-current)
if [[ ! "$CURRENT_BRANCH" =~ ^release/ ]]; then
  print_error "Must be on a release branch (release/vX.Y.Z)"
  print_error "Current branch: $CURRENT_BRANCH"
  echo ""
  print_status "Create a release branch first:"
  echo "  ./scripts/create-release.sh v1.3.0"
  exit 1
fi

VERSION="${CURRENT_BRANCH#release/}"

# Handle "now" - immediate release
if [ "$WHEN" = "now" ]; then
  print_status "Triggering immediate release for $CURRENT_BRANCH..."

  # Check if gh is available
  if ! command -v gh >/dev/null 2>&1; then
    print_error "GitHub CLI (gh) not found. Install it or trigger manually:"
    echo "  gh workflow run scheduled-release.yml -f release_branch=$CURRENT_BRANCH"
    exit 1
  fi

  gh workflow run scheduled-release.yml -f release_branch="$CURRENT_BRANCH"
  print_success "Release triggered!"
  echo ""
  print_status "Monitor progress:"
  echo "  gh run list --workflow=scheduled-release.yml"
  exit 0
fi

# Calculate release date
if [ "$WHEN" = "tomorrow" ]; then
  RELEASE_DATE=$(date -v+1d +%Y-%m-%d 2>/dev/null || date -d "+1 day" +%Y-%m-%d)
elif [[ "$WHEN" =~ ^[0-9]+d$ ]]; then
  # N days from now
  DAYS="${WHEN%d}"
  RELEASE_DATE=$(date -v+${DAYS}d +%Y-%m-%d 2>/dev/null || date -d "+$DAYS days" +%Y-%m-%d)
elif [[ "$WHEN" =~ ^[0-9]+w$ ]]; then
  # N weeks from now
  WEEKS="${WHEN%w}"
  DAYS=$((WEEKS * 7))
  RELEASE_DATE=$(date -v+${DAYS}d +%Y-%m-%d 2>/dev/null || date -d "+$DAYS days" +%Y-%m-%d)
elif [[ "$WHEN" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
  # Specific date
  RELEASE_DATE="$WHEN"
else
  print_error "Invalid date format: $WHEN"
  echo ""
  echo "Valid formats:"
  echo "  2d, 3d, 5d    - Days from now"
  echo "  1w, 2w        - Weeks from now"
  echo "  2024-12-25    - Specific date (YYYY-MM-DD)"
  echo "  tomorrow      - Tomorrow"
  echo "  now           - Immediate release"
  exit 1
fi

# Validate the date is in the future
TODAY=$(date +%Y-%m-%d)
if [[ "$RELEASE_DATE" < "$TODAY" ]]; then
  print_error "Release date ($RELEASE_DATE) is in the past!"
  exit 1
fi

print_status "Scheduling release $VERSION for $RELEASE_DATE"

# Create RELEASE_DATE file
echo "$RELEASE_DATE" > RELEASE_DATE
git add RELEASE_DATE
git commit -m "chore: schedule release for $RELEASE_DATE"

# Push to remote
print_status "Pushing schedule to remote..."
git push

print_success "Release scheduled!"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "  Version:      $VERSION"
echo "  Release date: $RELEASE_DATE"
echo "  Branch:       $CURRENT_BRANCH"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
print_status "The workflow runs daily at 10:00 UTC and will release on $RELEASE_DATE"
echo ""
print_status "To change the schedule:"
echo "  Edit RELEASE_DATE file and push"
echo ""
print_status "To release immediately instead:"
echo "  ./scripts/schedule-release.sh now"
echo ""
print_status "To cancel the scheduled release:"
echo "  git rm RELEASE_DATE && git commit -m 'Cancel scheduled release' && git push"
echo ""
print_status "You can now switch back to dev and continue working:"
echo "  git switch dev"
