# Ori Agent - Git Workflow & Release Process

## Current State
- **Current Version**: `v0.1.0` (in `VERSION` file)
- **Current Branch**: `main`
- **Last Release Tag**: `v0.0.10`
- **Working Directory**: Clean (no uncommitted changes)

## Version Embedding

The version is embedded into binaries at build time using Go's `-ldflags`:
- **VERSION file**: Contains version number (without `v` prefix): `0.1.0`
- **Build time**: Version is injected via ldflags: `-X 'github.com/johnjallday/ori-agent/internal/version.Version=0.1.0'`
- **Runtime**: Binary displays version from embedded ldflags (not from VERSION file)
- **Benefits**: Binaries are self-contained and don't need external VERSION file

---

## 📋 Release Workflow Overview

Your release process is automated with two main approaches:

### Method 1: Manual Script (Recommended for Quick Releases)
Uses `./scripts/create-release.sh`

### Method 2: Automated GitHub Actions
Triggered automatically when pushing version tags

---

## 🚀 Step-by-Step Release Process

### Pre-Release Checklist

1. **Ensure Clean State**
   ```bash
   git status  # Should show clean working directory
   ```

2. **Run Quality Checks**
   ```bash
   make fmt                    # Format code
   make vet                    # Run go vet
   go test -short ./...        # Run unit tests
   ```

3. **Update VERSION File**
   - Current: `0.0.10`
   - For next release, update to: `0.0.11` (patch), `0.1.0` (minor), or `1.0.0` (major)
   - **Important**: VERSION file contains version WITHOUT `v` prefix (e.g., `0.0.11`, not `v0.0.11`)

4. **Commit Your Changes**
   ```bash
   git add .
   git commit -m "Descriptive message about changes"
   git push origin main
   ```

---

### Option A: Automated Release Script (Easiest)

**Command:**
```bash
./scripts/create-release.sh v0.0.11
```

**What It Does:**
1. ✅ Validates version format (`vX.Y.Z`)
2. ✅ Checks for uncommitted changes
3. ✅ Verifies you're on `main` branch (or asks confirmation)
4. ✅ Pulls latest changes from origin
5. ✅ Runs all tests (`go test ./...`)
6. ✅ Updates `VERSION` file (removes `v` prefix: `v0.0.11` → `0.0.11`)
7. ✅ Creates commit: `"chore: bump version to v0.0.11"`
8. ✅ Creates git tag with annotation
9. ✅ Pushes tag to origin
10. ✅ (If `gh` CLI installed) Creates GitHub release with generated notes
11. ✅ GitHub Actions runs tests and generates release notes

**Requirements:**
- GitHub CLI (`gh`) installed (optional but recommended)
- Clean working directory
- All tests passing

---

### Option B: Manual GitHub Actions Release

**Step 1: Update VERSION File**
```bash
echo "0.0.11" > VERSION
git add VERSION
git commit -m "chore: bump version to v0.0.11"
git push origin main
```

**Step 2: Create and Push Tag**
```bash
git tag -a v0.0.11 -m "Release v0.0.11

🚀 New features:
- Feature 1
- Feature 2

🐛 Bug fixes:
- Fix 1
- Fix 2"

git push origin v0.0.11
```

**Step 3: GitHub Actions Automatically:**
1. Triggers `.github/workflows/release.yml`
2. Runs unit tests
3. Creates GitHub release
4. Auto-generates release notes

---

## 📦 What Gets Built in a Release

### GitHub Release Assets
GitHub releases contain release notes only (no pre-built binaries).

Users can build from source using the instructions in the README:
```bash
git clone https://github.com/johnjallday/ori-agent
cd ori-agent
make build
```

For local development builds, use:
- `make build` - Build server binary
- `make menubar` - Build menu bar app (macOS)
- `make plugins` - Build all plugins
- `make all` - Build everything

---

## 🔄 Continuous Integration (CI) Pipeline

Runs on every push to `main`, `dev`, or `develop`:

### Jobs:
1. **Lint** (Ubuntu)
   - `go fmt` check
   - `go vet`
   - `golangci-lint` (continue-on-error)

2. **Unit Tests** (Ubuntu + macOS)
   - Runs: `go test -v -short -race -coverprofile=coverage.txt`
   - Uploads coverage to Codecov

3. **Integration Tests** (Ubuntu)
   - Requires: `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`
   - Runs: `go test -v -run Integration`

4. **E2E Tests** (Ubuntu)
   - Builds server and plugins
   - Runs: `go test -v ./tests/e2e/...`

5. **Build** (Ubuntu + macOS + Windows)
   - Cross-platform build verification
   - Uploads build artifacts (7-day retention)

6. **Security Scan** (Ubuntu)
   - Gosec security scanner
   - SARIF upload to GitHub Code Scanning

7. **Dependency Review** (PR only)
   - Reviews dependency changes

---

## ✅ Recommended Release Workflow

### For Your Next Release (v0.0.11):

```bash
# 1. Ensure clean state
git status

# 2. Make sure you're on main and up-to-date
git checkout main
git pull origin main

# 3. Run quality checks
make fmt
make vet
go test -short ./...

# 4. Use the automated script
./scripts/create-release.sh v0.0.11

# 5. Script will:
#    - Update VERSION file (0.0.10 → 0.0.11)
#    - Commit version bump
#    - Create and push tag
#    - Create GitHub release (if gh CLI installed)

# 6. GitHub Actions will:
#    - Run unit tests
#    - Create release with auto-generated notes
```

---

## 📝 VERSION File Format

**Important:** The `VERSION` file should **NOT** include the `v` prefix:

- ✅ **Correct**: `0.0.11`
- ❌ **Incorrect**: `v0.0.11`

The `create-release.sh` script handles this conversion automatically:
- Input: `v0.0.11` (command argument)
- Stored in VERSION: `0.0.11` (without `v`)
- Git tag: `v0.0.11` (with `v`)

---

## 🎯 Quick Reference Commands

```bash
# Create release (easiest method)
./scripts/create-release.sh v0.0.11

# Manual quality checks
make fmt                    # Format code
make vet                    # Run go vet
make test-unit              # Run unit tests
make test-all               # Run all tests
make check                  # Run fmt + vet + test

# Build locally
make build                  # Build server
make menubar                # Build menu bar app
make plugins                # Build all plugins
make all                    # Build everything

# Check current version
cat VERSION
git tag --list | tail -5

# View release workflow status
gh run list --workflow=release.yml
```

---

## 🐛 Troubleshooting

### If create-release.sh fails:
1. Check you have no uncommitted changes: `git status`
2. Ensure tests pass: `go test ./...`
3. Verify VERSION file exists and is readable
4. Make sure tag doesn't already exist: `git tag -l | grep v0.0.11`

### If GitHub Actions fails:
1. Check workflow runs: `gh run list --workflow=release.yml`
2. View logs: `gh run view <run-id>`
3. Common issues:
   - Missing `GITHUB_TOKEN` (should be automatic)
   - Test failures (fix tests before creating release)

---

## 🔗 Related Files

- **Version**: `VERSION`
- **Release Script**: `scripts/create-release.sh`
- **CI Workflow**: `.github/workflows/ci.yml`
- **Release Workflow**: `.github/workflows/release.yml`
- **Makefile**: `Makefile`

---

**Summary:** Your release process is well-automated! For the next release, simply run `./scripts/create-release.sh v0.0.11`, and the script + GitHub Actions will handle everything else automatically.
