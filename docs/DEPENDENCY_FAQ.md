# Dependency Management FAQ

Common questions and answers about managing dependencies in Ori Agent.

## General Questions

### Q: Why does `make deps-check` show so many updates but `make deps-update` doesn't install them all?

**A:** Go dependencies come in two types:

1. **Direct dependencies** - Packages your code directly imports
   - These are in the `require (...)` section of `go.mod`
   - You can update these with `make deps-update`

2. **Indirect dependencies** - Packages used by your dependencies
   - These have `// indirect` comments in `go.mod`
   - Controlled by the packages that use them
   - Can't be updated directly (by design)

**Example:**
```
Your code uses: github.com/anthropics/anthropic-sdk-go v1.18.0
   └─ Which uses: cloud.google.com/go/auth v0.7.2

You can update: anthropic-sdk-go to latest
You can't update: cloud.google.com/go/auth directly (it's indirect)
```

**Solution:**
- Run `make deps-check-direct` to see only direct dependencies
- Run `make deps-summary` for a clear breakdown

### Q: How do I know which dependencies I can safely update?

**A:** Use this workflow:

```bash
# 1. See what's available
make deps-summary

# 2. Check only your direct dependencies
make deps-check-direct

# 3. Update safely (patches only)
make deps-update-patch

# 4. Or update to latest compatible versions
make deps-update

# 5. Always test after updates
make test

# 6. Check for security issues
make deps-vuln
```

### Q: What's the difference between `deps-update` and `deps-update-patch`?

**A:**

| Command | Updates | Risk | Example |
|---------|---------|------|---------|
| `make deps-update-patch` | Bug fixes only | Very Low | v1.2.3 → v1.2.4 |
| `make deps-update` | New features + fixes | Low | v1.2.3 → v1.3.0 |

**Recommendation:**
- Use `deps-update-patch` weekly
- Use `deps-update` monthly or before releases

### Q: Why are some packages still showing as outdated after I run `make deps-update`?

**A:** Several reasons:

1. **Indirect dependencies** (most common)
   - They're controlled by other packages
   - Will update when the parent package updates

2. **Version constraints**
   - Another package requires an older version
   - Use `make deps-why DEP=package-name` to investigate

3. **Major version updates**
   - `go get -u` doesn't update to new major versions (breaking changes)
   - Requires manual update: `go get package@v2`

4. **Module replacement**
   - Check for `replace` directives in `go.mod`

## Security Questions

### Q: How do I check for security vulnerabilities?

**A:**
```bash
# Quick check
make deps-vuln

# Or directly
govulncheck ./...
```

This scans your code and dependencies for known vulnerabilities.

### Q: What do I do if a vulnerability is found?

**A:**

1. **Read the output** - govulncheck shows:
   - Which package is vulnerable
   - What version fixes it
   - Whether your code actually uses the vulnerable function

2. **Update the package**:
   ```bash
   go get package-name@fixed-version
   go mod tidy
   ```

3. **Test**:
   ```bash
   make test
   make deps-vuln  # Verify fix
   ```

4. **If no fix available**:
   - Check if you actually use the vulnerable code
   - Look for alternative packages
   - File an issue with the package maintainer

### Q: How often should I run security scans?

**A:**
- **Minimum**: Weekly
- **Recommended**: Before every release
- **Best**: Automated in CI/CD (on every push)

## Update Strategy Questions

### Q: What's the recommended update schedule?

**A:**

| Frequency | Command | Purpose |
|-----------|---------|---------|
| **Weekly** | `make deps-vuln` | Security check |
| **Weekly** | `make deps-update-patch` | Safe bug fixes |
| **Monthly** | `make deps-update` | Feature updates |
| **Before Release** | Full update + test | Latest stable versions |

### Q: How do I update just one specific package?

**A:**

```bash
# Update to latest version
go get github.com/example/package@latest
go mod tidy

# Update to specific version
go get github.com/example/package@v1.2.3
go mod tidy

# Always test after
make test
```

### Q: Can I downgrade a package if an update breaks something?

**A:** Yes!

```bash
# Find previous versions
go list -m -versions github.com/example/package

# Downgrade to specific version
go get github.com/example/package@v1.2.3
go mod tidy

# Test
make test
```

## Dependabot Questions

### Q: How does Dependabot work?

**A:**
- **Schedule**: Checks every Monday at 9:00 AM
- **Process**:
  1. Scans your `go.mod` for updates
  2. Creates pull requests for each update
  3. Groups minor/patch updates together
  4. Runs your CI tests
- **Configuration**: `.github/dependabot.yml`

### Q: Should I auto-merge Dependabot PRs?

**A:** It depends:

✅ **Safe to auto-merge:**
- Patch updates (v1.2.3 → v1.2.4)
- Dependencies with good test coverage
- Non-critical packages

❌ **Review carefully:**
- Minor updates (v1.2.3 → v1.3.0)
- Major updates (v1.x.x → v2.0.0)
- Critical packages (auth, crypto, etc.)

**Pro tip:** Enable auto-merge for patch updates only:
```yaml
# In .github/dependabot.yml
groups:
  patch-updates:
    patterns: ["*"]
    update-types: ["patch"]
```

### Q: How do I control what Dependabot updates?

**A:** Edit `.github/dependabot.yml`:

```yaml
# Ignore specific packages
ignore:
  - dependency-name: "github.com/example/package"
    # Ignore all updates
  - dependency-name: "github.com/another/package"
    versions: ["2.x"]  # Ignore v2.x updates only

# Limit number of PRs
open-pull-requests-limit: 5

# Change schedule
schedule:
  interval: "monthly"  # Instead of weekly
```

## Troubleshooting Questions

### Q: I get "checksum mismatch" errors

**A:**
```bash
# Clear the module cache
go clean -modcache

# Re-download everything
go mod download

# Verify
go mod verify
```

### Q: `go mod tidy` keeps adding/removing the same package

**A:** This usually means:
- A dependency is imported but not used
- Or imported in test files only

**Solution:**
```bash
# Check where it's used
go mod why package-name

# If not needed, remove the import
# Then run:
go mod tidy
```

### Q: How do I fix "ambiguous import" errors?

**A:**
```bash
# Specify exact version
go get github.com/example/package@v1.2.3
go mod tidy
```

### Q: Dependencies are slow to download

**A:**

1. **Use a proxy** (faster, cached):
   ```bash
   export GOPROXY=https://proxy.golang.org,direct
   ```

2. **Check your connection**:
   ```bash
   go env GOPROXY
   ```

3. **For private repos**:
   ```bash
   export GOPRIVATE=github.com/yourorg/*
   ```

## Advanced Questions

### Q: How do I see the dependency tree/graph?

**A:**

```bash
# Text format
go mod graph

# Visual (requires graphviz)
make deps-graph
# Opens: deps-graph.png
```

### Q: Why is a specific package required?

**A:**
```bash
# Show dependency chain
make deps-why DEP=github.com/example/package

# Or directly
go mod why github.com/example/package
```

### Q: How do I vendor dependencies?

**A:**
```bash
# Copy all dependencies to vendor/
go mod vendor

# Build using vendor/
go build -mod=vendor

# Update vendor/
go mod vendor
```

**Note:** Ori Agent doesn't use vendoring by default.

### Q: Can I use different versions of the same package?

**A:** Only for major versions:

```go
import (
    v1 "github.com/example/package"      // v1.x.x
    v2 "github.com/example/package/v2"   // v2.x.x
)
```

You cannot use v1.2.0 and v1.3.0 at the same time (by design).

### Q: How do I update the Go version itself?

**A:**

```bash
# In go.mod, change:
go 1.25.3

# To:
go 1.26.0

# Then:
go mod tidy
```

**Note:** Ensure Go 1.26 is installed on your system first.

## CI/CD Questions

### Q: How do I add dependency checks to CI?

**A:** Example GitHub Actions workflow:

```yaml
name: Dependency Check
on:
  push:
    branches: [main]
  pull_request:
  schedule:
    - cron: '0 0 * * 1'  # Weekly

jobs:
  security:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'

      - name: Check for vulnerabilities
        run: |
          go install golang.org/x/vuln/cmd/govulncheck@latest
          govulncheck ./...

      - name: Check for outdated deps
        run: |
          go list -m -u all | grep '\[' || echo "All up to date"
```

### Q: Should I commit `go.sum`?

**A:** **YES, always!**

- `go.sum` ensures reproducible builds
- Contains cryptographic checksums
- Required for security verification
- Should be in version control

### Q: Should I commit the `vendor/` directory?

**A:** It depends:

✅ **Yes, if:**
- You need reproducible builds without network access
- Your CI doesn't have internet access
- You're publishing a final binary

❌ **No, if:**
- You have reliable CI/CD with internet
- You want smaller repository size
- You're building a library (not a binary)

**Ori Agent recommendation:** Don't vendor (we don't currently use it).

## Best Practices

### Q: What's the ideal dependency update workflow?

**A:**

```bash
# 1. Weekly security check
make deps-vuln

# 2. Check what's available
make deps-summary

# 3. Safe updates
make deps-update-patch

# 4. Test everything
make test-all

# 5. Before releases, do full update
make deps-update
make test-all
make deps-vuln
```

### Q: How do I keep dependencies minimal?

**A:**

1. **Regular cleanup**:
   ```bash
   go mod tidy  # Removes unused
   ```

2. **Avoid unnecessary imports**:
   ```bash
   # Check what imports a package
   go mod why package-name
   ```

3. **Use standard library when possible**

4. **Review dependency size**:
   ```bash
   go list -m all | wc -l  # Count dependencies
   ```

### Q: What should I do before a major release?

**A:**

**Pre-release checklist:**
```bash
# 1. Update dependencies
make deps-check
make deps-update

# 2. Full test suite
make test-all

# 3. Security scan
make deps-vuln

# 4. Verify builds
make build

# 5. Check for outdated direct deps
make deps-check-direct

# 6. Document changes in CHANGELOG
```

## Getting Help

### Q: Where can I learn more?

**A:**

- **Ori Agent docs**:
  - `docs/DEPENDENCY_MANAGEMENT.md` - Full guide
  - `docs/DEPENDENCY_CHEATSHEET.md` - Quick reference

- **Official Go docs**:
  - [Go Modules Reference](https://go.dev/ref/mod)
  - [Go Module Commands](https://go.dev/ref/mod#go-commands)

- **Tools**:
  - [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
  - [Dependabot](https://docs.github.com/en/code-security/dependabot)

### Q: How do I get help with a specific issue?

**A:**

1. **Check the error message** - Often includes the solution
2. **Run diagnostics**:
   ```bash
   go mod verify   # Check integrity
   go mod why pkg  # Check why needed
   go list -m all  # List all deps
   ```
3. **Check this FAQ**
4. **Search issues**: [github.com/johnjallday/ori-agent/issues](https://github.com/johnjallday/ori-agent/issues)
5. **File an issue** with:
   - Error message
   - Output of `go version`
   - Output of `go env`
   - Relevant `go.mod` section
