#!/bin/bash
# release-ready.sh — Deterministic release-readiness gate (no LLM judgment).
#
# Decides whether `dev` is ready to cut the next release, from hard signals only:
#   • there are unreleased commits on dev since the last v* tag
#   • dev's HEAD commit has green CI (all check-runs completed & successful)
#   • cadence threshold met: >=N merged PRs since the last release,
#     OR >=D days since the last release, OR FORCE_RELEASE=true
#   • the kill-switch (AUTO_RELEASE_HOLD) is not set
#
# Designed to run in GitHub Actions (.github/workflows/auto-release.yml) on a
# schedule, and locally for an ad-hoc "should I ship?" check.
# Requires: git, gh (authenticated).
#
# Outputs:
#   • Human-readable verdict to stdout and $GITHUB_STEP_SUMMARY (if set)
#   • ready / version / sha to $GITHUB_OUTPUT (if set), for the workflow to consume
#   • Always exits 0 unless a real error occurs — "not ready" is a green run, so the
#     daily schedule does not spam failure notifications when simply holding.

set -euo pipefail

# ── tunables (override via env) ───────────────────────────────────────────────
MIN_PRS="${RELEASE_MIN_PRS:-5}"          # >= this many merged PRs since last tag → ready
MIN_DAYS="${RELEASE_MIN_DAYS:-3}"        # >= this many days since last release → ready
BASE_BRANCH="${RELEASE_BASE_BRANCH:-dev}"
FORCE_RELEASE="${FORCE_RELEASE:-false}"  # bypass the cadence threshold (CI-green still required)

# ── helpers ───────────────────────────────────────────────────────────────────
emit()    { [ -n "${GITHUB_OUTPUT:-}" ] && echo "$1=$2" >> "$GITHUB_OUTPUT" || true; }
summary() { [ -n "${GITHUB_STEP_SUMMARY:-}" ] && printf '%b\n' "$1" >> "$GITHUB_STEP_SUMMARY" || true; }

hold() {  # report "not ready" and exit successfully
  echo "HOLD — $1"
  summary "### 🛑 Release held\n\n$1"
  emit ready false
  exit 0
}

for bin in git gh; do
  command -v "$bin" >/dev/null || { echo "ERROR: required tool '$bin' not found"; exit 1; }
done

# Ensure tags and the latest base ref are present (no-op if already fetched).
git fetch --quiet --tags origin "$BASE_BRANCH" 2>/dev/null || true

# ── 0. kill-switch ────────────────────────────────────────────────────────────
case "${AUTO_RELEASE_HOLD:-}" in
  1 | true | TRUE | yes | on) hold "AUTO_RELEASE_HOLD is set (manual hold)" ;;
esac

# ── 1. last release tag ───────────────────────────────────────────────────────
LAST_TAG="$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -1)"
[ -n "$LAST_TAG" ] || hold "no previous v* tag found"
LAST_DATE="$(git log -1 --format=%cI "$LAST_TAG" | cut -dT -f1)"
echo "Last release: $LAST_TAG ($LAST_DATE)"

# ── 2. something to release? (exact commit range since the tag) ───────────────
DEV_SHA="$(git rev-parse "origin/$BASE_BRANCH")"
RANGE="${LAST_TAG}..origin/${BASE_BRANCH}"
AHEAD="$(git rev-list --count "$RANGE")"
[ "$AHEAD" -gt 0 ] || hold "dev has no commits since $LAST_TAG"

# Squash-merged PRs carry "(#N)" in their subject; count those exactly in-range.
PR_LINES="$(git log "$RANGE" --format='%s' | grep -E '\(#[0-9]+\)$' || true)"
if [ -n "$PR_LINES" ]; then
  PR_COUNT="$(printf '%s\n' "$PR_LINES" | wc -l | tr -d ' ')"
  NOTES="$(printf '%s\n' "$PR_LINES" | sed 's/^/- /')"
else
  PR_COUNT=0
  NOTES="_(no PR-merge commits in range)_"
fi
echo "Commits since $LAST_TAG: $AHEAD   Merged PRs: $PR_COUNT"

# ── 3. dev CI green on HEAD ────────────────────────────────────────────────────
CI_SUMMARY="$(gh api "repos/{owner}/{repo}/commits/$DEV_SHA/check-runs" --jq '
  .check_runs as $r
  | [ ($r | length),
      ([$r[] | select(.status != "completed")] | length),
      ([$r[] | select(.conclusion as $c | ["success","skipped","neutral"] | index($c) | not)] | length)
    ] | @tsv' 2>/dev/null || true)"
[ -n "$CI_SUMMARY" ] || hold "could not read CI check-runs for ${DEV_SHA:0:7}"
read -r TOTAL PENDING FAILED <<< "$CI_SUMMARY"
[ "${TOTAL:-0}" -gt 0 ]   || hold "no CI check-runs found on dev@${DEV_SHA:0:7}"
[ "${PENDING:-0}" -eq 0 ] || hold "dev CI still running ($PENDING in progress on ${DEV_SHA:0:7})"
[ "${FAILED:-0}" -eq 0 ]  || hold "dev CI not green ($FAILED failing check-run(s) on ${DEV_SHA:0:7})"
echo "dev CI: green ($TOTAL check-runs on ${DEV_SHA:0:7})"

# ── 4. cadence ────────────────────────────────────────────────────────────────
NOW_S="$(date +%s)"
LAST_S="$(date -d "$LAST_DATE" +%s 2>/dev/null || date -j -f %Y-%m-%d "$LAST_DATE" +%s)"
DAYS_SINCE=$(( (NOW_S - LAST_S) / 86400 ))

if   [ "$FORCE_RELEASE" = "true" ];     then REASON="forced (manual dispatch), CI green"
elif [ "$PR_COUNT" -ge "$MIN_PRS" ];    then REASON="$PR_COUNT PRs since $LAST_TAG (>=$MIN_PRS)"
elif [ "$DAYS_SINCE" -ge "$MIN_DAYS" ]; then REASON="$DAYS_SINCE days since $LAST_TAG (>=$MIN_DAYS)"
else hold "cadence not met: $PR_COUNT PRs / $DAYS_SINCE days since $LAST_TAG (force with manual dispatch)"
fi

# ── 5. next version (odometer: bump patch, roll at 9; major bump stays manual) ─
IFS=. read -r MA MI PA <<< "${LAST_TAG#v}"
PA=$((PA + 1))
if [ "$PA" -gt 9 ]; then PA=0; MI=$((MI + 1)); fi
[ "$MI" -le 9 ] || hold "next version would roll a major from $LAST_TAG — bump manually"
NEXT="v${MA}.${MI}.${PA}"

echo "READY → $NEXT   ($REASON)"
emit ready true
emit version "$NEXT"
emit sha "$DEV_SHA"
summary "### ✅ Release ready: $NEXT\n\n**Why:** $REASON\n**Commit:** \`${DEV_SHA:0:7}\`\n\n$NOTES"
