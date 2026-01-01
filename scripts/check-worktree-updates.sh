#!/bin/bash
# check-worktree-updates.sh
# Checks agent/claude and agent/codex branches for updates that can be merged into the current branch

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color
BOLD='\033[1m'

# Get current branch
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Branches to check
BRANCHES=("agent/claude" "agent/codex")

echo -e "${BOLD}========================================${NC}"
echo -e "${BOLD}Worktree Update Checker${NC}"
echo -e "${BOLD}========================================${NC}"
echo -e "Current branch: ${CYAN}${CURRENT_BRANCH}${NC}"
echo ""

# Fetch latest from remote (optional, can be skipped with --no-fetch)
if [[ "$1" != "--no-fetch" ]]; then
    echo -e "${BLUE}Fetching latest from remote...${NC}"
    git fetch --all --quiet 2>/dev/null || true
    echo ""
fi

# Track if any updates found
UPDATES_FOUND=0

for BRANCH in "${BRANCHES[@]}"; do
    echo -e "${BOLD}----------------------------------------${NC}"
    echo -e "${BOLD}Branch: ${CYAN}${BRANCH}${NC}"
    echo -e "${BOLD}----------------------------------------${NC}"

    # Check if branch exists
    if ! git rev-parse --verify "$BRANCH" >/dev/null 2>&1; then
        echo -e "${RED}Branch does not exist locally${NC}"
        echo ""
        continue
    fi

    # Get commit counts
    AHEAD=$(git rev-list --count "${CURRENT_BRANCH}..${BRANCH}" 2>/dev/null || echo "0")
    BEHIND=$(git rev-list --count "${BRANCH}..${CURRENT_BRANCH}" 2>/dev/null || echo "0")

    echo -e "Commits ahead of ${CURRENT_BRANCH}: ${GREEN}${AHEAD}${NC}"
    echo -e "Commits behind ${CURRENT_BRANCH}: ${YELLOW}${BEHIND}${NC}"
    echo ""

    if [[ "$AHEAD" -eq 0 ]]; then
        echo -e "${GREEN}✓ No new commits to merge${NC}"
    else
        UPDATES_FOUND=1
        echo -e "${YELLOW}New commits available:${NC}"
        echo ""

        # Show commits that can be merged
        git log --oneline --no-merges "${CURRENT_BRANCH}..${BRANCH}" | head -20

        TOTAL_COMMITS=$(git rev-list --count "${CURRENT_BRANCH}..${BRANCH}")
        if [[ "$TOTAL_COMMITS" -gt 20 ]]; then
            echo -e "${YELLOW}... and $((TOTAL_COMMITS - 20)) more commits${NC}"
        fi
        echo ""

        # Check for merge conflicts using git merge-tree
        echo -e "${BLUE}Checking for potential merge conflicts...${NC}"

        MERGE_BASE=$(git merge-base "${CURRENT_BRANCH}" "${BRANCH}")

        # Use git merge-tree to detect conflicts without modifying working tree
        MERGE_RESULT=$(git merge-tree "${MERGE_BASE}" "${CURRENT_BRANCH}" "${BRANCH}" 2>&1)

        if echo "$MERGE_RESULT" | grep -q "^<<<<<<<"; then
            echo -e "${RED}⚠ Merge conflicts detected${NC}"
            echo ""

            # Extract conflicting file names from merge-tree output
            echo -e "Files with conflicts:"
            echo "$MERGE_RESULT" | grep -B1 "^<<<<<<< " | grep "^+" | sed 's/^+/  /' | while read -r line; do
                echo -e "  ${RED}${line}${NC}"
            done

            # Alternative: show files modified in both branches
            echo -e "\nFiles modified in both branches:"
            BRANCH_FILES=$(git diff --name-only "${MERGE_BASE}" "${BRANCH}")
            CURRENT_FILES=$(git diff --name-only "${MERGE_BASE}" "${CURRENT_BRANCH}")

            for file in $BRANCH_FILES; do
                if echo "$CURRENT_FILES" | grep -q "^${file}$"; then
                    echo -e "  ${YELLOW}${file}${NC}"
                fi
            done

            echo ""
            echo -e "Review changes before merging:"
            echo -e "  ${CYAN}git diff ${CURRENT_BRANCH}...${BRANCH}${NC}"
        else
            echo -e "${GREEN}✓ Clean merge possible (no conflicts detected)${NC}"
            echo ""
            echo -e "To merge, run:"
            echo -e "  ${CYAN}git merge ${BRANCH}${NC}"
            echo -e "Or cherry-pick specific commits:"
            echo -e "  ${CYAN}git cherry-pick <commit-hash>${NC}"
        fi
    fi
    echo ""
done

echo -e "${BOLD}========================================${NC}"
if [[ "$UPDATES_FOUND" -eq 0 ]]; then
    echo -e "${GREEN}All branches are up to date with ${CURRENT_BRANCH}${NC}"
else
    echo -e "${YELLOW}Updates available from one or more branches${NC}"
fi
echo -e "${BOLD}========================================${NC}"
