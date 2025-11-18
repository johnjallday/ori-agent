# Dependency Management Cheat Sheet

Quick reference for common dependency management tasks.

## Daily Operations

| Task | Command | Description |
|------|---------|-------------|
| Check updates | `make deps-check` | List available updates |
| Security scan | `make deps-vuln` | Check for vulnerabilities |
| Update patches | `make deps-update-patch` | Safe updates (1.2.3 → 1.2.4) |
| Clean up | `make deps-tidy` | Remove unused deps |
| Verify deps | `make deps-verify` | Check integrity |

## Updating

| Task | Command | When to Use |
|------|---------|-------------|
| Patch only | `make deps-update-patch` | Weekly/safe updates |
| All updates | `make deps-update` | Before releases |
| Specific package | `go get pkg@latest && go mod tidy` | One package update |
| Specific version | `go get pkg@v1.2.3 && go mod tidy` | Pin to version |
| Downgrade | `go get pkg@v1.0.0 && go mod tidy` | Rollback |

## Analysis

| Task | Command | Output |
|------|---------|--------|
| Why needed? | `make deps-why DEP=pkg` | Dependency chain |
| Visual graph | `make deps-graph` | PNG image |
| List all | `go list -m all` | All dependencies |
| List outdated | `make deps-outdated` | Formatted list |
| Direct only | `go list -m -json all \| jq '.Direct'` | Direct deps |

## Security

| Task | Command | Frequency |
|------|---------|-----------|
| Vuln scan | `make deps-vuln` | Weekly + after updates |
| Install scanner | `go install golang.org/x/vuln/cmd/govulncheck@latest` | Once |
| Check specific | `govulncheck ./internal/...` | As needed |

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Checksum mismatch | `go clean -modcache && go mod download` |
| Missing package | `go mod tidy && go mod download` |
| Ambiguous import | `go get package@version && go mod tidy` |
| Corrupted cache | `go clean -modcache` |

## CI/CD

```bash
# Pre-commit check
make deps-vuln && make test

# Before release
make deps-check
make deps-update
make test-all
make deps-vuln

# Weekly automation
make deps-vuln
make deps-check
```

## Dependabot Commands (in PR comments)

| Command | Action |
|---------|--------|
| `@dependabot rebase` | Rebase the PR |
| `@dependabot recreate` | Recreate from scratch |
| `@dependabot merge` | Auto-merge when CI passes |
| `@dependabot close` | Close without merging |
| `@dependabot ignore this dependency` | Stop updates |
| `@dependabot ignore this major version` | Skip major version |

## Quick Workflows

### Weekly Maintenance
```bash
make deps-check      # See what's available
make deps-vuln       # Check security
make deps-update-patch  # Safe updates
make test           # Verify
```

### Before Release
```bash
make deps-check      # Review updates
make deps-update     # Update all (confirm)
make test-all       # Full test suite
make deps-vuln      # Final security check
make build          # Verify build
```

### After Security Alert
```bash
make deps-vuln      # Identify vulnerability
go get pkg@fixed-version  # Update specific package
go mod tidy
make test
make deps-vuln      # Verify fix
```

### Debugging Dependencies
```bash
# Find why package is needed
make deps-why DEP=github.com/example/pkg

# See all versions
go list -m -versions github.com/example/pkg

# Check what uses it
go mod graph | grep "example/pkg"

# Test without it (dry run)
go mod edit -droprequire=github.com/example/pkg
go mod tidy  # Will re-add if needed
```

## Version Patterns

| Pattern | Example | Meaning |
|---------|---------|---------|
| `@latest` | `go get pkg@latest` | Latest release |
| `@v1.2.3` | `go get pkg@v1.2.3` | Specific version |
| `@v1` | `go get pkg@v1` | Latest v1.x.x |
| `@commit` | `go get pkg@abc123` | Specific commit |
| `@branch` | `go get pkg@main` | Latest on branch |
| `@none` | `go get pkg@none` | Remove package |

## Files to Know

| File | Purpose | Commit? |
|------|---------|---------|
| `go.mod` | Dependency list | ✅ Yes |
| `go.sum` | Checksums | ✅ Yes |
| `.github/dependabot.yml` | Auto-update config | ✅ Yes |
| `go.work` | Workspace (optional) | ❌ No |
| `vendor/` | Vendored deps (if used) | Maybe |

## Environment Variables

```bash
# Proxy configuration
export GOPROXY=https://proxy.golang.org,direct

# Private repos
export GOPRIVATE=github.com/yourorg/*

# Sum database (security)
export GOSUMDB=sum.golang.org

# Module cache location
export GOMODCACHE=$HOME/go/pkg/mod
```

## Pro Tips

1. **Always** run `go mod tidy` after manual `go get`
2. **Always** run tests after updates
3. **Never** edit `go.sum` manually
4. **Use** `deps-update-patch` for safe weekly updates
5. **Review** Dependabot PRs don't just auto-merge
6. **Pin** major versions for stability
7. **Subscribe** to security advisories for critical deps
8. **Test** updates in dev before production
