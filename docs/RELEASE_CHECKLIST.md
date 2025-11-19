# Pre-Release Checklist

This checklist ensures code quality, functionality, and installer integrity before every release.

## Quick Reference

```bash
# Complete pre-release script (runs everything)
./scripts/pre-release-check.sh

# Or run manually:
make pre-release
```

---

## 1. Code Quality Checks (5 minutes)

### Format Check
```bash
make fmt
# Should output: "All files formatted correctly"
```

**What it does**: Formats all Go code with `gofmt`
**Must pass**: Yes
**Fix**: Automatically fixes formatting

### Lint Check
```bash
make lint
# Requires: golangci-lint installed (make install-tools)
```

**What it does**: Runs `golangci-lint` for code quality issues
**Must pass**: Yes
**Fix**: Follow linter suggestions in output

### Vet Check
```bash
make vet
```

**What it does**: Runs `go vet` to find suspicious code
**Must pass**: Yes
**Fix**: Address issues reported by vet

### Security Scan
```bash
make security
# Requires: govulncheck installed (make install-tools)
```

**What it does**: Scans for known vulnerabilities in dependencies
**Must pass**: Yes
**Fix**: Update vulnerable dependencies

---

## 2. Unit Tests (2 minutes)

### Run All Unit Tests
```bash
make test-unit
# OR
go test ./...
```

**What it does**: Runs all unit tests across the codebase
**Must pass**: Yes
**Fix**: Fix failing tests before proceeding

### Run with Coverage
```bash
make test-coverage
# Generates coverage report in coverage.html
```

**What it does**: Runs tests and generates coverage report
**Must pass**: Coverage > 60% (recommended)
**View report**: `open coverage.html`

### Run Specific Package Tests
```bash
# Test specific package
go test ./internal/llm/
go test ./internal/registry/
go test ./internal/pluginloader/

# Test with verbose output
go test -v ./internal/llm/
```

---

## 3. Integration Tests (3 minutes)

### LLM Integration Tests
```bash
# Requires API keys
export OPENAI_API_KEY="your-key"
export ANTHROPIC_API_KEY="your-key"

go test ./internal/llm/integration_test.go
```

**What it does**: Tests real LLM provider integrations
**Must pass**: Yes (if you have API keys)
**Skip if**: No API keys available

### Agent HTTP Integration Tests
```bash
go test ./internal/agenthttp/integration_test.go
```

**What it does**: Tests HTTP endpoints
**Must pass**: Yes

---

## 4. Build Verification (5 minutes)

### Build All Binaries
```bash
make build-all
# OR
./scripts/build.sh
```

**What it does**: Builds server, menubar, and all plugins
**Must pass**: Yes
**Output**: Binaries in `bin/`

### Cross-Platform Build Test
```bash
# Test that all platforms build
GOOS=linux GOARCH=amd64 go build -o /tmp/ori-agent-linux ./cmd/server
GOOS=darwin GOARCH=arm64 go build -o /tmp/ori-agent-macos ./cmd/server
GOOS=windows GOARCH=amd64 go build -o /tmp/ori-agent-windows.exe ./cmd/server
```

**What it does**: Verifies cross-platform compilation
**Must pass**: Yes

### Plugin Build Verification
```bash
./scripts/build-plugins.sh
ls -lh plugins/*/[plugin-name]
```

**What it does**: Builds all plugins as RPC executables
**Must pass**: Yes
**Output**: Plugin executables in `plugins/*/`

---

## 5. Installer Tests (10-15 minutes)

### Local Smoke Tests
```bash
./scripts/test-all-installers.sh
```

**What it does**:
- Builds all installers (DMG, .deb, .rpm)
- Tests installation
- Starts server
- Verifies HTTP responses
- Tests health endpoint

**Must pass**: Yes
**Time**: ~10 minutes
**Platforms tested**: macOS + Linux (via Docker)

### Windows VM Test (Optional)
```bash
# On Windows machine or VM:
# 1. Build MSI
goreleaser release --snapshot --clean --skip=publish
.\build\windows\create-msi.ps1 -Version "0.0.12-test" -Arch "amd64"

# 2. Install
msiexec /i dist\ori-agent-0.0.12-test-amd64.msi /l*v install.log

# 3. Test
& "C:\Program Files\OriAgent\bin\ori-agent.exe" --port=18765

# 4. Verify
Invoke-WebRequest -Uri "http://localhost:18765/api/health"
```

**Must pass**: Yes (if Windows available)
**Skip if**: No Windows machine (CI/CD will test)

---

## 6. Manual Functional Tests (10 minutes)

### Server Functionality
```bash
# 1. Start server
./bin/ori-agent

# 2. Open browser
open http://localhost:8765

# 3. Test core features:
# ✓ Create an agent
# ✓ Add a plugin to the agent
# ✓ Send a chat message
# ✓ Verify agent responds
# ✓ Check plugin works
# ✓ Verify UI renders correctly
```

**Must pass**: Core workflows work
**Time**: ~5 minutes

### Plugin System Test
```bash
# 1. Upload a plugin
curl -X POST http://localhost:8765/api/plugins/upload \
  -F "file=@plugins/math/math"

# 2. List plugins
curl http://localhost:8765/api/plugins

# 3. Configure plugin for agent
# (via UI or API)

# 4. Test plugin in chat
# Ask agent: "What is 2 + 2?"
```

**Must pass**: Plugins load and work
**Time**: ~3 minutes

### Multi-Agent Test (Optional)
```bash
# 1. Create multiple agents
# 2. Test agent isolation (different plugins)
# 3. Test workspace collaboration
# 4. Test orchestration
```

**Must pass**: Agents isolated correctly
**Time**: ~5 minutes
**Skip if**: Not using multi-agent features

---

## 7. Documentation Check (5 minutes)

### Update Version Numbers
```bash
# 1. Update VERSION file
echo "0.0.12" > VERSION

# 2. Update CHANGELOG.md
# Add new version section with changes
```

### Verify Documentation
```bash
# Check that key docs are up to date:
cat README.md | grep -i "version"
cat docs/INSTALLATION_MACOS.md | head -20
cat docs/INSTALLATION_WINDOWS.md | head -20
cat docs/INSTALLATION_LINUX.md | head -20
```

**Must verify**:
- README reflects current features
- Installation guides are current
- No broken links
- Version numbers correct

### Generate Release Notes
```bash
# Use git log to create release notes
git log v0.0.11..HEAD --oneline --no-merges

# Or use GitHub's auto-generate feature
gh release create v0.0.12 --generate-notes --draft
```

---

## 8. Dependency Check (2 minutes)

### Update Dependencies
```bash
# Check for outdated dependencies
go list -u -m all

# Update dependencies
go get -u ./...
go mod tidy

# Re-run tests after updating
make test-unit
```

**Must pass**: All tests pass after updates
**When**: Before every release

### Verify go.mod and go.sum
```bash
# Verify consistency
go mod verify
```

**Must pass**: Yes

---

## 9. Git Workflow (2 minutes)

### Ensure Clean State
```bash
git status
# Should show: "nothing to commit, working tree clean"
```

**Must pass**: Yes
**Fix**: Commit or stash changes

### Verify Branch
```bash
git branch --show-current
# Should be: main (or release branch)
```

**Must pass**: On correct branch
**Fix**: Switch to main or create release branch

### Check Commits
```bash
git log --oneline -10
# Verify commit messages are descriptive
```

**Must verify**: Commit messages follow conventions

---

## 10. CI/CD Verification (15 minutes)

### Run CI Tests
```bash
# Push to develop branch first
git push origin develop

# Check CI status
gh run list --branch develop

# Wait for all checks to pass
gh run watch
```

**Must pass**: All CI checks pass
**Includes**:
- Unit tests
- Smoke tests (all platforms)
- Linting
- Security scans

### Manual Workflow Trigger
```bash
# Trigger smoke tests manually
gh workflow run smoke-tests.yml

# Watch results
gh run watch
```

**Must pass**: All smoke tests pass on all platforms

---

## Complete Pre-Release Checklist

Use this checklist before tagging any release:

```
Pre-Release Checklist for v0.0.12
==================================

Code Quality:
[ ] make fmt - Code formatted
[ ] make lint - No linting errors
[ ] make vet - No suspicious code
[ ] make security - No vulnerabilities

Testing:
[ ] make test-unit - All unit tests pass
[ ] Integration tests pass (with API keys)
[ ] ./scripts/test-all-installers.sh - Smoke tests pass
[ ] Manual functional tests complete

Build:
[ ] make build-all - All binaries build
[ ] ./scripts/build-plugins.sh - All plugins build
[ ] Cross-platform builds work

Documentation:
[ ] VERSION file updated
[ ] CHANGELOG.md updated
[ ] Release notes drafted
[ ] Documentation reviewed

Dependencies:
[ ] go mod tidy executed
[ ] go mod verify passes
[ ] Dependencies updated (if needed)

Git:
[ ] Working tree clean
[ ] On main branch
[ ] All changes committed
[ ] Commit messages descriptive

CI/CD:
[ ] Pushed to develop, all checks pass
[ ] Smoke tests pass on all platforms
[ ] No failing workflows

Final Steps:
[ ] Tag release: git tag v0.0.12
[ ] Push tag: git push origin v0.0.12
[ ] Monitor release workflow
[ ] Verify installers uploaded to GitHub Releases
```

---

## Automated Pre-Release Script

For convenience, run all checks with one command:

```bash
# Create and run pre-release script
./scripts/pre-release-check.sh v0.0.12
```

This script will:
1. Run all code quality checks
2. Run all tests
3. Build all binaries
4. Run smoke tests
5. Generate checklist summary
6. Exit with error if anything fails

---

## Release Process

Once all checks pass, you have two options for creating a release:

### Option A: Automated Script (Recommended)

Use the automated release script that handles everything:

```bash
# Run the automated release script
./scripts/create-release.sh v0.0.12

# The script will:
# 1. Validate version format (vX.Y.Z)
# 2. Check for uncommitted changes
# 3. Verify you're on main branch
# 4. Pull latest changes
# 5. Run all tests
# 6. Update VERSION file (removes 'v' prefix: v0.0.12 → 0.0.12)
# 7. Create commit: "chore: bump version to v0.0.12"
# 8. Create and push git tag
# 9. Create GitHub release (if gh CLI installed)
```

**Note**: The VERSION file should contain the version WITHOUT the 'v' prefix (e.g., `0.0.12`, not `v0.0.12`). The script handles this conversion automatically.

### Option B: Manual Process

If you prefer manual control:

```bash
# 1. Update VERSION file (no 'v' prefix)
echo "0.0.12" > VERSION
git add VERSION
git commit -m "chore: bump version to v0.0.12"
git push origin main

# 2. Tag the release
git tag v0.0.12
git push origin v0.0.12

# 3. Monitor GitHub Actions
gh run watch

# 4. Wait for release workflow to complete
# - Builds all installers
# - Runs smoke tests
# - Uploads to GitHub Releases

# 5. Verify release
gh release view v0.0.12

# 6. Download and test installers
gh release download v0.0.12
```

---

## Common Issues

### "Tests fail on CI but pass locally"
- **Cause**: Environment differences
- **Fix**: Check for hardcoded paths, ensure tests are isolated

### "Smoke tests timeout"
- **Cause**: Server takes too long to start
- **Fix**: Check server logs, increase timeout in smoke test script

### "Linter fails on code that compiled"
- **Cause**: Linter has stricter rules
- **Fix**: Follow linter suggestions, improve code quality

### "Security scan finds vulnerabilities"
- **Cause**: Outdated dependencies
- **Fix**: Update dependencies, re-test

### "Cross-platform build fails"
- **Cause**: Platform-specific code without build tags
- **Fix**: Use build tags or conditional compilation

---

## Rollback Procedure

If issues are found after release:

```bash
# 1. Delete the tag
git tag -d v0.0.12
git push origin :refs/tags/v0.0.12

# 2. Delete the GitHub release
gh release delete v0.0.12

# 3. Fix issues
# ... make fixes ...

# 4. Re-run checklist
./scripts/pre-release-check.sh v0.0.12

# 5. Re-tag and release
git tag v0.0.12
git push origin v0.0.12
```

---

## Quick Commands Summary

```bash
# Code quality (1 min)
make fmt && make lint && make vet && make security

# Tests (3 min)
make test-unit

# Build (5 min)
make build-all

# Smoke tests (10 min)
./scripts/test-all-installers.sh

# Complete check (20 min)
./scripts/pre-release-check.sh v0.0.12

# Release (1 min)
git tag v0.0.12 && git push origin v0.0.12
```

---

**Last Updated**: November 18, 2025
**Recommended**: Run full checklist before every release
**Minimum**: Run code quality + tests before every commit
