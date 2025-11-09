# Testing Guide for Ori Agent

This document provides comprehensive guidance on testing the Ori Agent codebase.

> **Quick Reference**: For common commands and shortcuts, see [Test Cheat Sheet](./docs/testing/TEST_CHEATSHEET.md)

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Test Types](#test-types)
- [Running Tests](#running-tests)
- [Writing Tests](#writing-tests)
- [CI/CD Integration](#cicd-integration)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

## Overview

Ori Agent uses a multi-layered testing strategy:

- **Unit Tests**: Test individual components in isolation
- **Integration Tests**: Test interactions between components (LLM providers, etc.)
- **E2E Tests**: Test the entire system end-to-end including HTTP server and plugins

### Test Coverage

```
internal/
├── llm/                    # LLM provider tests
│   ├── *_test.go          # Unit tests for each provider
│   └── integration_test.go # Integration tests with real APIs
├── health/                 # Health check tests
│   └── version_test.go
└── testutil/              # Shared test utilities
    └── testutil.go

tests/
├── integration/           # HTTP API integration tests
│   └── api_test.go
└── e2e/                   # End-to-end tests
    ├── server_test.go
    └── plugin_test.go
```

## Quick Start

### Prerequisites

```bash
# 1. Install Go 1.25+
go version

# 2. Set up API keys (for integration/E2E tests)
export OPENAI_API_KEY="your-openai-key"
export ANTHROPIC_API_KEY="your-anthropic-key"  # Optional

# 3. Install dependencies
make deps

# 4. Build the project
make build
```

### Run All Tests

```bash
# Run all tests (unit + integration)
make test

# Run only unit tests (fast, no API calls)
make test-unit

# Run integration tests (requires API keys)
make test-integration

# Run E2E tests (requires API keys, builds server)
make test-e2e

# Run everything
make test-all
```

## Test Types

### 1. Unit Tests

Unit tests verify individual functions and components in isolation.

**Location**: `internal/*/[package]_test.go`

**Characteristics**:
- Fast execution (< 1s per test)
- No external dependencies
- No API calls
- Run with `-short` flag

**Example**:
```go
// internal/llm/factory_test.go
func TestFactoryRegister(t *testing.T) {
    factory := NewFactory()
    provider := &mockProvider{}

    factory.Register("test", provider)

    retrieved, err := factory.GetProvider("test")
    if err != nil {
        t.Fatalf("Failed to get provider: %v", err)
    }

    if retrieved != provider {
        t.Error("Retrieved provider doesn't match registered")
    }
}
```

**Run**:
```bash
make test-unit
# OR
go test -v -short ./...
```

### 2. Integration Tests

Integration tests verify component interactions, including real API calls.

**Location**:
- `internal/llm/integration_test.go` - LLM provider integration
- `tests/integration/api_test.go` - HTTP API integration

**Characteristics**:
- Moderate execution time (5-30s per test)
- May require API keys
- Tests real integrations
- Skipped when API keys not available

**Example**:
```go
// internal/llm/integration_test.go
func TestProviderIntegration(t *testing.T) {
    apiKey := os.Getenv("OPENAI_API_KEY")
    if apiKey == "" {
        t.Skip("OPENAI_API_KEY not set - skipping integration test")
    }

    provider := NewOpenAIProvider(ProviderConfig{APIKey: apiKey})

    resp, err := provider.Chat(ctx, ChatRequest{
        Model: "gpt-4o-mini",
        Messages: []Message{NewUserMessage("Hello")},
    })

    if err != nil {
        t.Fatalf("Chat failed: %v", err)
    }
    // assertions...
}
```

**Run**:
```bash
OPENAI_API_KEY=your-key make test-integration
# OR
OPENAI_API_KEY=your-key go test -v -run Integration ./...
```

### 3. End-to-End (E2E) Tests

E2E tests verify the entire system including server startup, HTTP endpoints, and plugin loading.

**Location**: `tests/e2e/*.go`

**Characteristics**:
- Slow execution (30s+ per test)
- Requires built binary
- Requires API keys
- Tests full system behavior

**Example**:
```go
// tests/e2e/server_test.go
func TestServerStartup(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    cmd := startServer(t, ctx)
    defer stopServer(cmd)

    if err := waitForServer(baseURL, 10*time.Second); err != nil {
        t.Fatalf("Server failed to start: %v", err)
    }

    // Test health endpoint
    resp, err := http.Get(baseURL + "/health")
    // assertions...
}
```

**Run**:
```bash
OPENAI_API_KEY=your-key make test-e2e
# OR
make build && OPENAI_API_KEY=your-key go test -v ./tests/e2e/...
```

## Running Tests

### Using Make (Recommended)

```bash
# Run all tests
make test

# Run unit tests only
make test-unit

# Run integration tests
make test-integration

# Run E2E tests
make test-e2e

# Run with coverage report
make test-coverage

# Watch mode (requires entr: brew install entr)
make test-watch
```

### Using Go Test Directly

```bash
# All tests
go test ./...

# Verbose output
go test -v ./...

# Specific package
go test -v ./internal/llm/

# Specific test
go test -v ./internal/llm/ -run TestOpenAIProvider

# With coverage
go test -v -cover ./...

# Race detection
go test -v -race ./...

# Short mode (skip integration tests)
go test -v -short ./...
```

### Environment Variables

```bash
# API Keys (required for integration/E2E tests)
export OPENAI_API_KEY="sk-..."
export ANTHROPIC_API_KEY="sk-ant-..."

# Custom port for E2E tests (default: 18080)
export PORT="9000"
```

## Writing Tests

### Test Structure

Follow the standard Go testing patterns:

```go
package mypackage

import (
    "testing"
    "github.com/johnjallday/ori-agent/internal/testutil"
)

func TestMyFunction(t *testing.T) {
    // Arrange
    input := "test input"
    expected := "expected output"

    // Act
    result := MyFunction(input)

    // Assert
    if result != expected {
        t.Errorf("Expected %q, got %q", expected, result)
    }
}
```

### Using Test Utilities

The `internal/testutil` package provides helpful utilities:

```go
import "github.com/johnjallday/ori-agent/internal/testutil"

func TestHTTPEndpoint(t *testing.T) {
    // Create test server
    server := testutil.NewTestServer(t, handler)
    defer server.Cleanup()

    // Make requests
    rec := server.Get("/api/endpoint")

    // Assert status
    testutil.AssertStatusCode(t, http.StatusOK, rec.Code)

    // Assert JSON response
    var result map[string]interface{}
    testutil.AssertJSONResponse(t, rec, &result)
}

func TestWithTempFiles(t *testing.T) {
    // Create temp directory
    dir := testutil.CreateTempDir(t, "test-*")
    defer os.RemoveAll(dir)

    // Create temp file
    file := testutil.CreateTempFile(t, dir, "test-*.json", `{"key": "value"}`)

    // Use file in tests...
}

func TestWithAPIKey(t *testing.T) {
    // Skip if no API key
    testutil.SkipIfNoAPIKey(t)

    // Test code that requires API key...
}
```

### Table-Driven Tests

Use table-driven tests for multiple scenarios:

```go
func TestCalculator(t *testing.T) {
    tests := []struct {
        name     string
        a, b     int
        op       string
        expected int
        wantErr  bool
    }{
        {"add", 2, 3, "add", 5, false},
        {"subtract", 5, 3, "subtract", 2, false},
        {"divide by zero", 10, 0, "divide", 0, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := Calculate(tt.a, tt.b, tt.op)

            if (err != nil) != tt.wantErr {
                t.Errorf("Expected error: %v, got: %v", tt.wantErr, err)
            }

            if !tt.wantErr && result != tt.expected {
                t.Errorf("Expected %d, got %d", tt.expected, result)
            }
        })
    }
}
```

### Subtests

Use subtests for logical grouping:

```go
func TestUserManagement(t *testing.T) {
    t.Run("CreateUser", func(t *testing.T) {
        // Test user creation
    })

    t.Run("UpdateUser", func(t *testing.T) {
        // Test user update
    })

    t.Run("DeleteUser", func(t *testing.T) {
        // Test user deletion
    })
}
```

## CI/CD Integration

### GitHub Actions

Tests run automatically on:
- Push to `main` or `develop` branches
- Pull requests to `main` or `develop`

**Workflows**:
- `.github/workflows/ci.yml` - Main CI pipeline
- `.github/workflows/release.yml` - Release builds

**CI Pipeline**:
1. **Lint**: Code formatting and static analysis
2. **Unit Tests**: Fast tests on multiple OS (Linux, macOS)
3. **Integration Tests**: Tests with API keys (if configured)
4. **E2E Tests**: Full server tests
5. **Build**: Cross-platform builds
6. **Security**: Security scanning with Gosec

### Setting Up Secrets

For integration/E2E tests in CI:

1. Go to GitHub repo → Settings → Secrets
2. Add secrets:
   - `OPENAI_API_KEY`
   - `ANTHROPIC_API_KEY` (optional)

### Local CI Simulation

Run the same checks as CI locally:

```bash
# Format check
make fmt

# Lint
make lint

# Vet
make vet

# All checks
make check

# Full test suite
make test-all
```

## Best Practices

### 1. Test Naming

```go
// Good
func TestUserCreation(t *testing.T) {}
func TestInvalidEmail(t *testing.T) {}
func TestDatabaseConnection(t *testing.T) {}

// Bad
func TestStuff(t *testing.T) {}
func Test1(t *testing.T) {}
```

### 2. Use t.Helper()

Mark helper functions:

```go
func assertStatusCode(t *testing.T, expected, actual int) {
    t.Helper()  // Stack traces point to caller, not this function
    if expected != actual {
        t.Errorf("Expected %d, got %d", expected, actual)
    }
}
```

### 3. Clean Up Resources

```go
func TestWithResources(t *testing.T) {
    file, err := os.CreateTemp("", "test-*")
    if err != nil {
        t.Fatal(err)
    }
    defer os.Remove(file.Name())  // Always clean up
    defer file.Close()

    // Test code...
}
```

### 4. Use Context for Timeouts

```go
func TestWithTimeout(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    result, err := DoSomething(ctx)
    // assertions...
}
```

### 5. Skip Appropriately

```go
// Skip long-running tests in short mode
if testing.Short() {
    t.Skip("Skipping in short mode")
}

// Skip when dependencies unavailable
if os.Getenv("OPENAI_API_KEY") == "" {
    t.Skip("API key not set")
}
```

### 6. Test Error Cases

```go
func TestErrorHandling(t *testing.T) {
    _, err := FunctionThatShouldFail()
    if err == nil {
        t.Error("Expected error, got nil")
    }
}
```

## Troubleshooting

### Tests Fail with "No API Key"

**Solution**: Set environment variables:
```bash
export OPENAI_API_KEY="your-key"
export ANTHROPIC_API_KEY="your-key"
```

### E2E Tests Fail with "Server Not Ready"

**Causes**:
- Server binary not built
- Port already in use
- Missing dependencies

**Solutions**:
```bash
# Rebuild server
make build

# Check port availability
lsof -i :18080

# Check dependencies
go mod tidy
```

### Integration Tests Timeout

**Solution**: Increase timeout or use a faster model:
```go
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
```

### Race Detector Fails

**Solution**: Fix race conditions or exclude test:
```bash
# Run without race detector temporarily
go test ./...

# Fix the race condition in code
```

### Coverage Reports Missing

**Solution**:
```bash
# Generate coverage
make test-coverage

# View in browser
open coverage/coverage.html
```

## Additional Resources

- [Go Testing Package](https://pkg.go.dev/testing)
- [Table-Driven Tests](https://dave.cheney.net/2019/05/07/prefer-table-driven-tests)
- [Test Fixtures](https://dave.cheney.net/2016/05/10/test-fixtures-in-go)
- [Ori Agent API Reference](./docs/api/API_REFERENCE.md)
- [Test Cheat Sheet](./docs/testing/TEST_CHEATSHEET.md) - Quick command reference
- [Testing Setup Summary](./docs/testing/TESTING_SETUP_SUMMARY.md) - Detailed setup info

## Contributing

When adding new features:

1. Write unit tests for new functions
2. Add integration tests for external integrations
3. Update E2E tests if adding new endpoints
4. Run `make check` before committing
5. Ensure all tests pass in CI

---

**Questions?** Check the main [README.md](./README.md) or open an issue.
