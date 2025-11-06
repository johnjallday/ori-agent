# Testing System Setup Summary

This document summarizes the comprehensive testing system that has been set up for ori-agent.

## What Was Added

### 1. Makefile (`Makefile`)

A comprehensive Makefile with 25+ commands for:
- **Build targets**: `build`, `plugins`, `all`, `clean`
- **Run targets**: `run`, `run-dev`
- **Test targets**: `test`, `test-unit`, `test-integration`, `test-e2e`, `test-all`, `test-coverage`, `test-watch`
- **Code quality**: `fmt`, `vet`, `lint`, `check`
- **Docker**: `docker-build`, `docker-run`
- **Development**: `dev-setup`, `install-tools`

**Usage**:
```bash
make help           # Show all available commands
make test-unit      # Run fast unit tests
make test-all       # Run everything
make check          # Run all code quality checks
```

### 2. Test Utilities (`internal/testutil/testutil.go`)

Shared testing utilities including:
- `TestServer` - HTTP test server wrapper
- `WaitForServer` - Server readiness checking
- `AssertStatusCode`, `AssertJSONResponse` - Common assertions
- `CreateTempFile`, `CreateTempDir` - File system helpers
- `SkipIfNoAPIKey` - Conditional test skipping
- `MakeRequest`, `ReadJSONResponse` - HTTP testing helpers

**Usage**:
```go
import "github.com/johnjallday/ori-agent/internal/testutil"

func TestEndpoint(t *testing.T) {
    server := testutil.NewTestServer(t, handler)
    defer server.Cleanup()

    rec := server.Get("/api/health")
    testutil.AssertStatusCode(t, http.StatusOK, rec.Code)
}
```

### 3. Integration Tests (`tests/integration/api_test.go`)

HTTP API integration tests covering:
- Health endpoint
- Agent CRUD operations (create, list, delete)
- Plugin listing
- Settings management
- Chat endpoint (error cases)

Tests use mock handlers and can be extended to use real server.

**Run**:
```bash
make test-integration
```

### 4. E2E Tests (`tests/e2e/`)

End-to-end tests that:
- Start actual server binary
- Test full request/response cycle
- Verify plugin loading
- Test concurrent requests
- Validate complete workflows

**Files**:
- `server_test.go` - Server lifecycle, endpoints, agent operations
- `plugin_test.go` - Plugin loading and health checks

**Run**:
```bash
OPENAI_API_KEY=your-key make test-e2e
```

### 5. CI/CD Workflows (`.github/workflows/`)

Automated testing on GitHub Actions:

**`ci.yml`** - Main CI pipeline with:
- **Lint**: Code formatting and static analysis
- **Unit Tests**: Fast tests on Linux and macOS
- **Integration Tests**: API integration tests (when secrets configured)
- **E2E Tests**: Full system tests
- **Build**: Cross-platform builds (Linux, macOS, Windows)
- **Security**: Gosec security scanning
- **Dependency Review**: Automated dependency checks on PRs

**`release.yml`** - Release automation:
- Triggered on version tags (v*.*.*)
- Builds for multiple platforms
- Creates GitHub releases with binaries

**Required Secrets** (optional, for integration/E2E tests):
- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`

### 6. Documentation

**`TESTING.md`** - Comprehensive testing guide:
- Overview of test types
- Quick start guide
- Running tests (Make, Go commands)
- Writing tests (patterns, best practices)
- CI/CD integration
- Troubleshooting

**`TEST_CHEATSHEET.md`** - Quick reference:
- Common commands
- Test selection
- Coverage generation
- Debugging
- Common issues
- Workflow examples

**`TESTING_SETUP_SUMMARY.md`** - This file

## Project Structure

```
ori-agent/
├── Makefile                          # Build and test automation
├── README.md                         # Main project documentation
├── TESTING.md                        # Comprehensive testing guide (main)
│
├── docs/                             # Organized documentation
│   ├── README.md                     # Documentation index
│   ├── api/
│   │   └── API_REFERENCE.md          # HTTP API documentation
│   └── testing/
│       ├── TEST_CHEATSHEET.md        # Quick testing commands
│       └── TESTING_SETUP_SUMMARY.md  # This summary
│
├── .github/
│   └── workflows/
│       ├── ci.yml                    # CI pipeline
│       └── release.yml               # Release automation
│
├── internal/
│   ├── testutil/
│   │   └── testutil.go               # Shared test utilities
│   └── llm/
│       ├── *_test.go                 # Unit tests
│       └── integration_test.go       # LLM integration tests
│
├── tests/
│   ├── integration/
│   │   └── api_test.go               # HTTP API tests
│   └── e2e/
│       ├── server_test.go            # Server E2E tests
│       └── plugin_test.go            # Plugin E2E tests
│
└── manual_tests/
    ├── test_rpc_plugin.go            # Manual RPC plugin test
    ├── test_rpc_system.go            # Manual RPC system test
    └── test_converted_plugins.go     # Manual converted plugins test
```

## Quick Start

### First Time Setup

```bash
# 1. Install dependencies
make deps

# 2. Build everything
make all

# 3. Run unit tests (no API key needed)
make test-unit

# 4. Set API key for integration/E2E tests
export OPENAI_API_KEY="sk-..."

# 5. Run all tests
make test-all

# 6. Check code quality
make check
```

### Daily Development Workflow

```bash
# 1. Make code changes
vim internal/llm/provider.go

# 2. Run unit tests (fast feedback)
make test-unit

# 3. Test specific package
go test -v ./internal/llm/

# 4. Check coverage
make test-coverage
open coverage/coverage.html

# 5. Run pre-commit checks
make check

# 6. If adding new features, run E2E
make test-e2e
```

## Test Categories

### Unit Tests
- **What**: Test individual functions/components
- **Speed**: Fast (< 1s per test)
- **Dependencies**: None
- **When**: Run frequently during development
- **Command**: `make test-unit`

### Integration Tests
- **What**: Test component interactions, real APIs
- **Speed**: Moderate (5-30s per test)
- **Dependencies**: API keys (optional, tests skipped if not available)
- **When**: Before commits, in CI
- **Command**: `OPENAI_API_KEY=... make test-integration`

### E2E Tests
- **What**: Test entire system end-to-end
- **Speed**: Slow (30s+ per test)
- **Dependencies**: Built binary, API keys
- **When**: Before releases, in CI
- **Command**: `OPENAI_API_KEY=... make test-e2e`

## Current Status

### ✅ What Works

- Makefile with comprehensive targets
- Test utilities package
- Integration test framework
- E2E test framework
- CI/CD workflows
- Documentation
- Code formatting and vetting
- Coverage reports
- Cross-platform builds

### ⚠️ Known Issues

**Pre-existing Test Failures** (not introduced by this testing system):

1. `TestClaudeProviderDefaultModels` - Fails in `internal/llm/claude_provider_test.go`
2. `TestOpenAIProviderDefaultModels` - Fails in `internal/llm/openai_provider_test.go`

These tests were failing before the testing system was added. They should be fixed by updating the test expectations or the provider implementations.

**Manual Test Programs**:

The `manual_tests/` directory contains test programs with `main()` functions:
- `test_converted_plugins.go`
- `test_rpc_plugin.go`
- `test_rpc_system.go`

These files have `//go:build ignore` tags to exclude them from automated test runs.
They must be run manually using `go run`:
```bash
# Run individual test scripts
go run manual_tests/test_rpc_system.go
go run manual_tests/test_rpc_plugin.go
go run manual_tests/test_converted_plugins.go
```

## Next Steps

### Recommended Improvements

1. **Fix Pre-existing Test Failures**
   - Investigate and fix `TestClaudeProviderDefaultModels`
   - Investigate and fix `TestOpenAIProviderDefaultModels`

2. **Increase Test Coverage**
   - Add tests for HTTP handlers (`internal/*http/`)
   - Add tests for orchestration (`internal/orchestration/`)
   - Add tests for workspace (`internal/workspace/`)
   - Current coverage: ~15% (estimated)
   - Target: 60%+

3. **Manual Test Programs** ✅
   - Moved `test_*.go` to `manual_tests/` directory
   - Prevents confusion with automated tests
   - Fixed "main redeclared" error in CI

4. **Add More Integration Tests**
   - Test actual server endpoints (not just mocks)
   - Test plugin loading and execution
   - Test multi-agent workflows

5. **Performance Testing**
   - Add benchmarks with `go test -bench`
   - Test concurrent agent operations
   - Profile memory usage

6. **Test Data Management**
   - Create `testdata/` directories with fixtures
   - Add example plugin configurations
   - Add example agent configurations

## Best Practices

When contributing tests:

1. **Write Tests First** - TDD approach
2. **Use Table-Driven Tests** - For multiple scenarios
3. **Clean Up Resources** - Always use `defer` for cleanup
4. **Use Subtests** - For logical grouping
5. **Skip Appropriately** - Use `t.Skip()` for conditional tests
6. **Test Error Cases** - Not just happy path
7. **Use Helpers** - Mark helper functions with `t.Helper()`
8. **Add Context** - Use context for timeouts

## Comparison: Before vs After

### Before
- Manual testing with `go run ./cmd/server`
- No automated tests
- No CI/CD
- No test utilities
- No standardized workflow

### After
- Automated testing at 3 levels (unit, integration, E2E)
- 25+ Make commands for common tasks
- CI/CD with GitHub Actions
- Shared test utilities
- Comprehensive documentation
- Test coverage reporting
- Multi-platform builds
- Security scanning

## Resources

- [Go Testing Documentation](https://pkg.go.dev/testing)
- [Table-Driven Tests in Go](https://dave.cheney.net/2019/05/07/prefer-table-driven-tests)
- [GitHub Actions Go Guide](https://docs.github.com/en/actions/automating-builds-and-tests/building-and-testing-go)
- [TESTING.md](../../TESTING.md) - Full testing guide
- [TEST_CHEATSHEET.md](./TEST_CHEATSHEET.md) - Quick reference

## Questions?

- Check [TESTING.md](../../TESTING.md) for detailed information
- Check [TEST_CHEATSHEET.md](./TEST_CHEATSHEET.md) for quick commands
- Review existing tests in `internal/llm/*_test.go` for examples
- Open an issue on GitHub for bugs or suggestions

---

**Testing System Setup Completed**: November 2025
**Status**: ✅ Ready for use
**Test Coverage**: ~15% (needs improvement)
**Pre-existing Failures**: 2 (documented above)
