# Release Quick Reference

**TL;DR:** Test on `dev` first, merge to `main` only when tests pass, release from `main`.

---

## Daily Development (95% of time)

```bash
# Always work on dev
git switch dev

# Create feature
git switch -c feature/my-feature
# ... code, test, commit ...

# Merge to dev
git switch dev
git merge feature/my-feature
git push origin dev
```

**Repeat for all features.** Stay on `dev`!

---

## Release Process (3 Commands)

### 1️⃣ Test on `dev` FIRST

```bash
# While on dev branch
git switch dev

# Run all quality checks
./scripts/pre-release-check.sh v0.5.0
```

**Output if successful:**
```
✅ All checks passed!
dev branch is ready to release v0.5.0

Next steps:
  1. ./scripts/prepare-release.sh
  2. ./scripts/create-release.sh v0.5.0

💡 Tip: All checks passed on dev, safe to merge!
```

**If checks fail:** Fix on `dev`, then re-run. Do NOT merge until all pass.

---

### 2️⃣ Merge to `main`

```bash
# Merges tested code from dev → main
./scripts/prepare-release.sh
```

**Output:**
```
⚠️  Have you run pre-release checks on dev branch?
Have you run pre-release checks and all tests passed? (y/N): y

There are 5 commit(s) in dev that are not in main:
* 8464463 feat: improve release workflow
* 76b7399 feat: auto-switch back to dev
...

Merge these commits to main? (y/N): y

✅ Successfully merged dev into main
```

---

### 3️⃣ Release from `main`

```bash
# Creates tag, builds installers, publishes
./scripts/create-release.sh v0.5.0
```

**Output:**
```
✅ All platform installers built successfully
✅ GitHub release created and installers uploaded
✅ Release v0.5.0 created successfully!

[INFO] Switching back to dev branch...
✅ Now on dev branch - ready for next features!

💡 Tip: main represents v0.5.0 (released)
💡 Tip: Continue daily work on dev
```

**You're automatically back on `dev`** - ready for v0.6.0!

---

## Complete Example

```bash
# ============================================
# Monday-Thursday: Build features on dev
# ============================================
git switch dev
git switch -c feature/plugin-store
# ... work ...
git switch dev && git merge feature/plugin-store

git switch -c feature/api-v2
# ... work ...
git switch dev && git merge feature/api-v2

git push origin dev

# ============================================
# Friday: Release
# ============================================

# Step 1: Test on dev (before merge) ⭐
git switch dev
./scripts/pre-release-check.sh v0.5.0
# ✅ All checks passed on dev!

# Step 2: Merge to main
./scripts/prepare-release.sh
# ✅ dev → main merged

# Step 3: Release
./scripts/create-release.sh v0.5.0
# 🚀 v0.5.0 released!
# ✅ Auto-switched back to dev

# ============================================
# Continue working (already on dev!)
# ============================================
git switch -c feature/next-thing
```

---

## Visual Flow

```
┌──────────────────────────────────┐
│  DEV BRANCH (your home)          │
│  ● Features                      │
│  ● Bug fixes                     │
│  ● Testing                       │
│  ● Daily commits                 │
└──────────────────────────────────┘
              ↓
        (Ready to release?)
              ↓
┌──────────────────────────────────┐
│  1. Test on dev                  │
│     pre-release-check.sh         │
│     ✅ All pass                  │
└──────────────────────────────────┘
              ↓
┌──────────────────────────────────┐
│  2. Merge dev → main             │
│     prepare-release.sh           │
└──────────────────────────────────┘
              ↓
┌──────────────────────────────────┐
│  3. Release from main            │
│     create-release.sh            │
│     → Auto-switch back to dev    │
└──────────────────────────────────┘
              ↓
        Back on dev!
    Continue development
```

---

## Branch Purpose

| Branch | Purpose | When |
|--------|---------|------|
| **`dev`** | Daily work, features, testing | **Always** (95% of time) |
| **`main`** | Releases only | Release day (5% of time) |

**Golden Rule:** Test on `dev` → Merge to `main` → Release → Back to `dev`

---

## Troubleshooting

### "Tests failed on dev"
```bash
# Fix on dev
git add .
git commit -m "fix: resolve test failures"

# Re-run checks
./scripts/pre-release-check.sh v0.5.0

# Only proceed when ✅
```

### "I'm on the wrong branch"
```bash
# Always return to dev
git switch dev
```

### "I forgot which step I'm on"
```bash
# Check current branch
git branch --show-current

# If on dev: Run pre-release checks
# If on main: You probably just merged (run create-release.sh)
```

---

## One-Liner Cheat Sheet

```bash
# Release in 3 commands:
./scripts/pre-release-check.sh v0.X.Y && ./scripts/prepare-release.sh && ./scripts/create-release.sh v0.X.Y
```

---

## Documentation

- **Release guide:** `RELEASE_QUICKSTART.md` (this file)
- **Scripts:**
  - `scripts/pre-release-check.sh` - Validate code quality
  - `scripts/prepare-release.sh` - Merge dev → main
  - `scripts/create-release.sh` - Publish release

---

## Remember

✅ **DO:**
- Work on `dev` daily
- Test on `dev` before merging
- Merge to `main` only when tests pass
- Release from `main`

❌ **DON'T:**
- Work on `main` directly
- Merge untested code to `main`
- Skip pre-release checks
- Forget to switch back to `dev` (automatic now!)

---

**Questions?** See the scripts in `scripts/` or run them with `--help`.
