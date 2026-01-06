#!/bin/bash
# Merge Dependabot PRs with green checks (requires gh CLI)
# Usage: ./scripts/merge-dependabot.sh

set -e
set -o pipefail

cd "$(dirname "$0")/.."

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI not found; skipping Dependabot merge."
  exit 0
fi

if ! gh auth status -h github.com >/dev/null 2>&1; then
  echo "gh CLI not authenticated; run 'gh auth login' to enable Dependabot merge."
  exit 0
fi

PR_NUMBERS=()
while IFS= read -r PR_NUMBER; do
  if [ -n "$PR_NUMBER" ]; then
    PR_NUMBERS+=("$PR_NUMBER")
  fi
done < <(
  gh pr list --state open --search "author:app/dependabot" --json number,isDraft \
    --jq '.[] | select(.isDraft == false) | .number'
)

if [ ${#PR_NUMBERS[@]} -eq 0 ]; then
  echo "No open Dependabot PRs to merge."
  exit 0
fi

MERGED_COUNT=0
SKIPPED_COUNT=0
FAILED_COUNT=0

for PR_NUMBER in "${PR_NUMBERS[@]}"; do
  IFS=$'\t' read -r PR_TITLE PR_MERGEABLE PR_MERGE_STATE PR_CHECK_STATE PR_AUTHOR <<<"$(
    gh pr view "$PR_NUMBER" --json title,mergeable,mergeStateStatus,statusCheckRollup,author \
      --jq '[.title, .mergeable, .mergeStateStatus, (.statusCheckRollup.state // "UNKNOWN"), .author.login] | @tsv'
  )"

  case "$PR_AUTHOR" in
    "dependabot[bot]"|"dependabot-preview[bot]"|"app/dependabot")
      ;;
    *)
      echo "Skipping #$PR_NUMBER ($PR_TITLE): not a Dependabot PR ($PR_AUTHOR)."
      SKIPPED_COUNT=$((SKIPPED_COUNT + 1))
      continue
      ;;
  esac

  if [ "$PR_MERGEABLE" != "MERGEABLE" ] || [ "$PR_MERGE_STATE" != "CLEAN" ] || [ "$PR_CHECK_STATE" != "SUCCESS" ]; then
    echo "Skipping #$PR_NUMBER ($PR_TITLE): mergeable=$PR_MERGEABLE merge_state=$PR_MERGE_STATE checks=$PR_CHECK_STATE."
    SKIPPED_COUNT=$((SKIPPED_COUNT + 1))
    continue
  fi

  echo "Merging #$PR_NUMBER ($PR_TITLE)..."
  if gh pr merge "$PR_NUMBER" --squash --delete-branch --yes; then
    MERGED_COUNT=$((MERGED_COUNT + 1))
  else
    echo "Failed to merge #$PR_NUMBER."
    FAILED_COUNT=$((FAILED_COUNT + 1))
  fi
done

echo "Dependabot merge summary: merged=$MERGED_COUNT skipped=$SKIPPED_COUNT failed=$FAILED_COUNT"

if [ "$FAILED_COUNT" -gt 0 ]; then
  exit 1
fi
