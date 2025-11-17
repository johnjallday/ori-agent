# Known Issues

## ✅ RESOLVED: Plugin Enablement API

**Status**: ✅ FIXED

**What was the issue:**
The "name required" error was caused by using the wrong API endpoint for enabling plugins.

**Solution:**
The correct way to enable a plugin is:
```
POST /api/plugins
Content-Type: application/x-www-form-urlencoded
Body: name={pluginName}&path={pluginPath}
```

NOT:
```
PUT /api/agents/{agentName}/plugins/{pluginName}/config
```

**Fixed in:** `tests/user/helpers/test_context.go` - EnablePlugin method now uses correct API

---

## Current Known Issues

### Server Startup Time in Tests

**Issue**: Occasional "connection reset by peer" errors during test startup

**Cause**: Server initialization can take 10-15 seconds, especially on first run

**Mitigations Applied:**
- Increased wait timeout to 15 seconds
- Added 500ms sleep after server start
- Server logs captured to `/tmp/ori-test-server-18765.log`

**If Tests Fail:**
```bash
# Check server logs
cat /tmp/ori-test-server-18765.log

# Run with verbose output
export TEST_VERBOSE=true
make test-user

# Run single test with longer timeout
go test ./tests/user/workflows -run TestCompleteAgentWorkflow -v -timeout 10m
```

**Workaround:**
Run tests with longer timeout:
```bash
go test ./tests/user/... -v -timeout 10m
```

---

## Test Environment Notes

### API Keys Required

Tests require at least one LLM provider API key:
```bash
export OPENAI_API_KEY="sk-..."
# OR
export ANTHROPIC_API_KEY="sk-ant-..."
```

### Plugin Build Requirements

Before running plugin tests, build plugins:
```bash
# Built-in plugins
./scripts/build-plugins.sh

# Shared plugins (from ../plugins)
./scripts/build-external-plugins.sh
```

### Test Port

Tests use port `18765` (not the default `8765`) to avoid conflicts with running development servers.

---

## Working Features

✅ **All Test Infrastructure**
- Interactive CLI tool
- Manual scenario runner
- Automated test suite
- Plugin enablement (fixed!)
- Tool calling verification
- Multi-agent workflows

✅ **All Testing Modes**
```bash
make test-cli       # Interactive CLI
make test-scenarios # Manual scenarios
make test-user      # Automated tests
```

---

## Tips for Reliable Testing

### 1. Ensure Clean Environment
```bash
# Kill any existing servers
lsof -ti :8765 | xargs kill
lsof -ti :18765 | xargs kill

# Clean up test artifacts
rm -rf /tmp/ori-test-*
```

### 2. Build Everything First
```bash
make build plugins
```

### 3. Run with Appropriate Timeout
```bash
# For automated tests
go test ./tests/user/... -v -timeout 10m

# For single test
go test ./tests/user/workflows -run TestCompleteAgentWorkflow -v -timeout 5m
```

### 4. Use Verbose Mode for Debugging
```bash
export TEST_VERBOSE=true
export TEST_CLEANUP=false
make test-user
```

---

## Future Improvements

### Potential Enhancements
1. **Mock LLM Provider** - Add mock provider for faster tests without API keys
2. **Test Parallelization** - Run independent tests in parallel
3. **Retry Logic** - Auto-retry on transient failures
4. **Test Data Fixtures** - Pre-generated test responses for deterministic tests

### Nice to Have
1. CI/CD integration with GitHub Actions
2. Test coverage reporting
3. Performance benchmarking
4. Load testing for multi-agent scenarios

---

## Getting Help

- **Quick Start**: See [QUICKSTART.md](QUICKSTART.md)
- **Full Docs**: See [README.md](README.md)
- **Commands**: See [TEST_CHEATSHEET.md](TEST_CHEATSHEET.md)
- **Status**: See [STATUS.md](STATUS.md)

---

**Last Updated**: 2025-01-17
**Status**: All tests working, plugin API fixed, minor timing issues may occur
