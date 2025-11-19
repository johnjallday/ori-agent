# Release Workflow Guide

This document describes the two-branch release workflow for Ori Agent.

## Overview

```
dev branch (development) ──test──> main branch (releases)
     ↑                                       ↓
     └───────────auto-switch back────────────┘
```

## Branch Strategy

| Branch | Purpose | Usage |
|--------|---------|-------|
| **`dev`** | Daily development, testing, features | 95% of your time - your "home" |
| **`main`** | Stable releases, production snapshots | 5% of time - only for releases |

**Golden Rule:** Test everything on `dev` first, then merge to `main` for release.

---

## Daily Development Workflow

### 1. Work on Features (dev branch)

```bash
# Always start from dev
git checkout dev

# Create feature branch
git checkout -b feature/my-feature

# Work, test, commit
go test ./...
git add .
git commit -m "feat: add my feature"

# Merge back to dev
git checkout dev
git merge feature/my-feature
git push origin dev

# Delete feature branch
git branch -d feature/my-feature
```

**Repeat this process for all features, fixes, and improvements.**

---

## Release Workflow (3 Steps)

### Prerequisites
- All features merged to `dev`
- Code tested locally
- Ready to publish a new version

### Step 1: Run Pre-Release Checks (on `dev`)

```bash
# Make sure you're on dev
git checkout dev

# Run all quality checks on dev BEFORE merging
./scripts/pre-release-check.sh v0.5.0
```

**What it checks:**
- ✅ Code formatting (go fmt)
- ✅ Go vet
- ✅ Linting
- ✅ Security scan
- ✅ All tests pass
- ✅ Builds succeed
- ✅ Dependencies clean
- ✅ Git status clean

**If checks pass on dev:**
```
✅ All checks passed!
dev branch is ready to release v0.5.0

Next steps:
  1. ./scripts/prepare-release.sh
     (Merges dev → main)

  2. ./scripts/create-release.sh v0.5.0
     (Creates release from main)

💡 Tip: All checks passed on dev, safe to merge!
```

**If checks fail:**
- Fix issues on `dev`
- Re-run checks until all pass
- **Do NOT merge to main until checks pass**

---

### Step 2: Merge to Main

```bash
# Merges tested code from dev → main
./scripts/prepare-release.sh
```

**What it does:**
1. Asks: "Have you run pre-release checks?"
2. Shows commits that will be merged
3. Merges `dev` → `main`
4. Pushes to `origin/main`

**Output:**
```
⚠️  Have you run pre-release checks on dev branch?
Best practice: Run './scripts/pre-release-check.sh' on dev BEFORE merging

Have you run pre-release checks and all tests passed? (y/N): y

There are 5 commit(s) in dev that are not in main:
* 76b7399 feat: auto-switch back to dev
* c9e7112 feat: add two-branch workflow
...

Merge these commits to main? (y/N): y

[SUCCESS] Successfully merged dev into main
[SUCCESS] Release preparation complete!
```

---

### Step 3: Create Release

```bash
# Creates tag, builds installers, publishes to GitHub
./scripts/create-release.sh v0.5.0
```

**What it does:**
1. Validates you're on `main`
2. Validates `dev` is merged
3. Creates git tag
4. Runs GoReleaser (builds binaries, installers)
5. Creates GitHub release
6. **Auto-switches you back to `dev`**

**Output:**
```
[INFO] Creating release v0.5.0 for Ori Agent
[SUCCESS] All platform installers built successfully
[SUCCESS] GitHub release created and installers uploaded
[SUCCESS] Release v0.5.0 created successfully!

[INFO] Switching back to dev branch for continued development...
[SUCCESS] Now on dev branch - ready for next features!

💡 Tip: main branch now represents the released version (v0.5.0)
💡 Tip: Continue your daily work on dev branch
```

**You're automatically back on `dev` - ready to start building v0.6.0!**

---

## Complete Example

```bash
# ============================================
# PHASE 1: Development (on dev branch)
# ============================================

git checkout dev

# Build multiple features
git checkout -b feature/plugin-marketplace
# ... work, test ...
git checkout dev && git merge feature/plugin-marketplace

git checkout -b feature/api-improvements
# ... work, test ...
git checkout dev && git merge feature/api-improvements

git checkout -b fix/memory-leak
# ... work, test ...
git checkout dev && git merge fix/memory-leak

git push origin dev

# dev now has all features, tested and ready!

# ============================================
# PHASE 2: Release (Friday afternoon)
# ============================================

# Step 1: Test on dev (2-5 minutes)
./scripts/pre-release-check.sh v0.5.0
# ✅ All checks passed!

# Step 2: Merge to main (30 seconds)
./scripts/prepare-release.sh
# ✅ dev merged to main

# Step 3: Release (5-10 minutes)
./scripts/create-release.sh v0.5.0
# 🚀 v0.5.0 released!
# ✅ Auto-switched back to dev

# ============================================
# PHASE 3: Continue Development (immediately)
# ============================================

# You're already on dev!
git checkout -b feature/next-thing
# ... start v0.6.0 features ...
```

---

## Why This Workflow?

### ✅ Benefits

1. **`dev` stays clean**
   - Only merge tested code
   - If pre-release checks fail, fix on `dev` without polluting `main`

2. **`main` represents releases**
   - Every commit on `main` is a release
   - Clean history: v0.1.0 → v0.2.0 → v0.3.0
   - Easy to see what's in production

3. **Test before merge**
   - Catch issues on `dev` before they reach `main`
   - `main` only gets proven-stable code

4. **Automatic return to dev**
   - No manual branch switching
   - Immediately ready for next features

---

## Troubleshooting

### "I'm on the wrong branch"

```bash
# Always go back to dev for daily work
git checkout dev
```

### "Pre-release checks failed on dev"

```bash
# Fix issues on dev
# ... make fixes ...
git add .
git commit -m "fix: resolve test failures"

# Re-run checks
./scripts/pre-release-check.sh v0.5.0

# Only proceed when checks pass
```

### "I merged to main but tests should have failed"

```bash
# If main has bad code, you can:

# Option 1: Fix forward (recommended)
git checkout main
# ... fix the issue ...
git add .
git commit -m "fix: resolve issue"
git push origin main

# Option 2: Revert (if serious)
git checkout main
git revert HEAD
git push origin main
```

### "I forgot to test on dev first"

```bash
# If you already merged to main:
# Run checks on main (better than nothing)
./scripts/pre-release-check.sh v0.5.0

# If checks fail, fix on main or revert
```

---

## Quick Reference

```bash
# Daily work (dev)
git checkout dev
git checkout -b feature/name
# ... work ...
git checkout dev && git merge feature/name
git push origin dev

# Release (3 commands)
./scripts/pre-release-check.sh v0.X.Y    # On dev
./scripts/prepare-release.sh              # Merge to main
./scripts/create-release.sh v0.X.Y        # Publish

# Auto-switched back to dev - continue working!
```

---

## Branch States Over Time

```
Week 1-3: Work on dev
dev:  ●──●──●──●──●──●──●──●──●──●──●
main: ────────────────────────────────  (unchanged)

Week 4: Release
dev:  ●──●──●──●──●──●──●──●──●──●──●
                                    ↓ (merge)
main: ──────────────────────────────●  (v0.5.0)
                                    ↓ (auto-switch)
dev:  ●──●──●──●──●──●──●──●──●──●──● ← You are here

Week 5: Continue on dev
dev:  ●──●──●──●──●──●──●──●──●──●──●──●──● (v0.6.0 work)
main: ──────────────────────────────●────── (still v0.5.0)
```

---

## Summary

1. **Live on `dev`** - All daily work happens here
2. **Test on `dev`** - Run pre-release checks before merging
3. **Merge to `main`** - Only when checks pass
4. **Release from `main`** - Creates tag, builds, publishes
5. **Back to `dev`** - Automatic, ready for next version

**Remember:** `dev` = work, `main` = releases 🚀
