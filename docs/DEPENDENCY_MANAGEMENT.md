# Dependency Management Guide

This guide covers all the tools and workflows for managing dependencies in Ori Agent.

## Table of Contents
- [Quick Start](#quick-start)
- [Security Scanning](#security-scanning)
- [Updating Dependencies](#updating-dependencies)
- [Automated Updates](#automated-updates)
- [Advanced Usage](#advanced-usage)
- [Troubleshooting](#troubleshooting)

## Quick Start

### Check for Updates
```bash
make deps-check
```
Shows all dependencies with available updates in format: `current [latest]`

### Run Security Scan
```bash
make deps-vuln
```
Checks for known vulnerabilities using `govulncheck`.

### Update Dependencies
```bash
# Safer: Patch versions only (1.2.3 → 1.2.4)
make deps-update-patch

# Full update: Minor and patch (1.2.3 → 1.3.0)
make deps-update  # Interactive confirmation required
```

## Security Scanning

### govulncheck

Official Go vulnerability scanner that checks your code and dependencies for known security issues.

**Installation:**
```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
# Or use: make install-tools
```

**Usage:**
```bash
# Via Makefile
make deps-vuln

# Direct command
govulncheck ./...

# Check specific package
govulncheck ./internal/llm/...
```

**What it checks:**
- Direct dependencies
- Transitive dependencies
- Only reports vulnerabilities in code you actually use
- Provides fix recommendations

**Example output:**
```
Scanning your code and 247 packages across 89 dependent modules for known vulnerabilities...

No vulnerabilities found.
```

## Updating Dependencies

### Semantic Versioning

Go uses semantic versioning (semver):
- **MAJOR.MINOR.PATCH** (e.g., v1.2.3)
- **Patch** (1.2.3 → 1.2.4): Bug fixes, safe
- **Minor** (1.2.3 → 1.3.0): New features, backward compatible
- **Major** (1.2.3 → 2.0.0): Breaking changes

### Update Strategies

#### 1. Patch Updates (Safest)
```bash
make deps-update-patch
```
- Updates: Bug fixes only
- Risk: Very low
- Recommended: Run frequently (weekly)

#### 2. Minor & Patch Updates
```bash
make deps-update
```
- Updates: New features + bug fixes
- Risk: Low (should be backward compatible)
- Recommended: Monthly or before releases
- **Interactive**: Asks for confirmation

#### 3. Specific Package Update
```bash
go get github.com/example/package@latest
go mod tidy
```

#### 4. Update to Specific Version
```bash
go get github.com/example/package@v1.2.3
go mod tidy
```

### After Updating

Always run these after updates:
```bash
# Clean up dependencies
make deps-tidy

# Run tests
make test

# Check for vulnerabilities
make deps-vuln
```

## Automated Updates

### Dependabot (GitHub)

Dependabot automatically creates pull requests for dependency updates.

**Configuration:** `.github/dependabot.yml`

**Schedule:**
- **When**: Every Monday at 9:00 AM
- **What**: Checks all Go modules and GitHub Actions
- **How**: Creates PRs with updates grouped by type

**Grouping Strategy:**
- Minor and patch updates grouped together
- Major updates separate (require manual review)

**PR Labels:**
- `dependencies` - All dependency updates
- `go` - Go module updates
- `github-actions` - GitHub Actions updates

**Reviewing Dependabot PRs:**
1. Check the changelog link in PR description
2. Review breaking changes (if any)
3. Ensure CI passes
4. Merge if tests pass

**Commands in PR comments:**
```
@dependabot rebase      # Rebase the PR
@dependabot recreate    # Recreate the PR
@dependabot merge       # Merge when CI passes
@dependabot close       # Close without merging
@dependabot ignore this dependency  # Stop updates for this package
```

### Alternative: Renovate Bot

For more advanced automation, consider [Renovate](https://github.com/renovatebot/renovate):
- Better monorepo support
- More configuration options
- Supports more package managers
- Free for open source

## Advanced Usage

### Dependency Analysis

#### Why is a dependency needed?
```bash
make deps-why DEP=github.com/hashicorp/go-plugin
```

Shows the dependency chain:
```
github.com/johnjallday/ori-agent
github.com/johnjallday/ori-agent/internal/pluginloader
github.com/hashicorp/go-plugin
```

#### Visualize dependency graph
```bash
# Requires graphviz: brew install graphviz
make deps-graph
```
Generates `deps-graph.png` with visual representation.

#### Show outdated dependencies
```bash
make deps-outdated
```
More readable format than `deps-check`.

### Dependency Verification

#### Verify integrity
```bash
make deps-verify
```
Checks that dependencies match expected checksums in `go.sum`.

#### Clean up go.mod
```bash
make deps-tidy
```
- Removes unused dependencies
- Adds missing dependencies
- Updates `go.sum`

### CI/CD Integration

#### GitHub Actions Example

```yaml
name: Security Scan
on:
  push:
    branches: [main]
  pull_request:
  schedule:
    - cron: '0 0 * * 1'  # Weekly on Monday

jobs:
  scan:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'

      - name: Install govulncheck
        run: go install golang.org/x/vuln/cmd/govulncheck@latest

      - name: Run vulnerability scan
        run: govulncheck ./...

      - name: Check for outdated dependencies
        run: go list -m -u all
```

## Troubleshooting

### Common Issues

#### "no required module provides package"
```bash
# Solution: Clean and re-download
go mod tidy
go mod download
```

#### "checksum mismatch"
```bash
# Solution: Clear module cache
go clean -modcache
go mod download
```

#### "ambiguous import"
```bash
# Solution: Specify exact version
go get package@version
go mod tidy
```

#### Dependabot not creating PRs
1. Check `.github/dependabot.yml` is valid
2. Ensure GitHub Actions are enabled
3. Check repository settings → Code security
4. Verify you have admin access

### Manual Dependency Investigation

#### List all dependencies
```bash
go list -m all
```

#### List direct dependencies only
```bash
go list -m -json all | jq 'select(.Main != true and .Indirect != true)'
```

#### Find unused dependencies
```bash
go mod tidy -v
```

#### Check module compatibility
```bash
go mod graph | grep "module-name"
```

## Best Practices

1. **Regular Updates**
   - Run `make deps-vuln` weekly
   - Update patches monthly
   - Update minor versions quarterly

2. **Before Releases**
   - Update all dependencies: `make deps-update`
   - Run full test suite: `make test-all`
   - Check for vulnerabilities: `make deps-vuln`
   - Verify builds: `make build`

3. **Security First**
   - Always scan after updates: `make deps-vuln`
   - Review Dependabot PRs promptly
   - Subscribe to security advisories for critical dependencies

4. **Testing**
   - Run tests after any update
   - Use `make test-coverage` to ensure coverage stays high
   - Test in staging before production

5. **Version Pinning**
   - Pin major versions in `go.mod` for stability
   - Use `// indirect` comments for transitive deps
   - Document reasons for version constraints

## Resources

- [Go Modules Reference](https://go.dev/ref/mod)
- [govulncheck Documentation](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
- [Dependabot Documentation](https://docs.github.com/en/code-security/dependabot)
- [Semantic Versioning](https://semver.org/)
- [Go Vulnerability Database](https://vuln.go.dev/)
