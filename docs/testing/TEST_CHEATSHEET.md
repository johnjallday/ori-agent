# Testing Cheat Sheet

Quick reference for common testing commands in Ori Agent.

## Quick Commands

```bash
# Run all tests
make test

# Run only fast unit tests
make test-unit

# Run integration tests (needs API key)
OPENAI_API_KEY=sk-... make test-integration

# Run E2E tests (needs build + API key)
OPENAI_API_KEY=sk-... make test-e2e

# Generate coverage report
make test-coverage

# Watch mode (auto-rerun on changes)
make test-watch

# Run all checks (fmt, vet, test)
make check
```

## Test Selection

```bash
# Run specific package
go test -v ./internal/llm/

# Run specific test
go test -v ./internal/llm/ -run TestOpenAIProvider

# Run tests matching pattern
go test -v -run "TestAgent.*" ./...

# Skip slow tests
go test -short ./...

# Run with race detector
go test -race ./...
```

## Coverage

```bash
# Basic coverage
go test -cover ./...

# Detailed coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Coverage by function
go tool cover -func=coverage.out

# Using Makefile
make test-coverage
open coverage/coverage.html
```

## Debugging Tests

```bash
# Verbose output
go test -v ./...

# Print test logs even on success
go test -v -args -test.v

# Run specific test with debugging
go test -v -run TestName ./package

# Increase timeout
go test -timeout 30s ./...
```

## Environment Setup

```bash
# Set API keys
export OPENAI_API_KEY="sk-..."
export ANTHROPIC_API_KEY="sk-ant-..."

# Custom port for E2E tests
export PORT=9000

# Check environment
echo $OPENAI_API_KEY
```

## Build & Test

```bash
# Full build
make all

# Build + unit tests
make build && make test-unit

# Build + all tests
make build && make test-all

# Clean build
make clean && make all
```

## CI/CD Simulation

```bash
# Run exactly what CI runs
make fmt
make vet
make lint
make test-all
```

## Test File Patterns

```bash
# Find all test files
find . -name "*_test.go"

# Count tests
grep -r "func Test" --include="*_test.go" | wc -l

# List test functions
grep -r "^func Test" --include="*_test.go"
```

## Common Issues

```bash
# No API key error
export OPENAI_API_KEY="your-key"

# Port in use
lsof -i :8080
kill -9 <PID>

# Module issues
go mod tidy
go mod download

# Build cache issues
go clean -cache
go clean -testcache
```

## Quick Test Templates

### Unit Test
```go
func TestMyFunction(t *testing.T) {
    result := MyFunction("input")
    if result != "expected" {
        t.Errorf("got %v, want %v", result, "expected")
    }
}
```

### Table Test
```go
func TestCases(t *testing.T) {
    tests := []struct {
        name string
        input string
        want string
    }{
        {"case1", "in1", "out1"},
        {"case2", "in2", "out2"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := MyFunc(tt.input)
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

### Integration Test
```go
func TestIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    if os.Getenv("API_KEY") == "" {
        t.Skip("API_KEY not set")
    }

    // Test code...
}
```

## Useful Aliases

Add to your `~/.bashrc` or `~/.zshrc`:

```bash
# Test aliases
alias ot='cd ~/Projects/ori/ori-agent'
alias otest='make test-unit'
alias otesta='make test-all'
alias obuild='make build'
alias orun='make run-dev'

# With API key
export OPENAI_API_KEY="sk-..."
```

## Test Workflow

Typical development workflow:

```bash
# 1. Make changes to code
vim internal/llm/provider.go

# 2. Run unit tests
make test-unit

# 3. Run specific tests
go test -v -run TestProvider ./internal/llm/

# 4. Check coverage
make test-coverage

# 5. Run all checks
make check

# 6. Build and test E2E
make build
OPENAI_API_KEY=sk-... make test-e2e

# 7. Commit
git add .
git commit -m "Add feature X"
```

## Performance Profiling

```bash
# CPU profile
go test -cpuprofile=cpu.prof -bench=.
go tool pprof cpu.prof

# Memory profile
go test -memprofile=mem.prof -bench=.
go tool pprof mem.prof

# Benchmark
go test -bench=. -benchmem ./...
```

---

For detailed information, see [TESTING.md](../../TESTING.md)
