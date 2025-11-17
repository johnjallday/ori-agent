# User Testing Quick Start Guide

Get started with testing ori-agent in under 5 minutes!

## Prerequisites

```bash
# 1. Set your API key
export OPENAI_API_KEY="sk-..."
# OR
export ANTHROPIC_API_KEY="sk-ant-..."

# 2. Build the project
cd ori-agent
make build plugins
```

## Option 1: Interactive CLI (Recommended for Beginners)

The easiest way to test - a guided, menu-driven interface:

```bash
# Launch the interactive testing CLI
make test-cli

# Or build and run manually:
go build -o bin/ori-test-cli ./cmd/test-cli
./bin/ori-test-cli
```

**What you can do:**
- ✅ Check your environment (API keys, builds, etc.)
- ✅ Build server and plugins
- ✅ Start/stop server
- ✅ Run quick tests
- ✅ Test specific plugins
- ✅ Interactive chat testing
- ✅ View logs and cleanup

**Perfect for:** First-time users, quick sanity checks, manual testing

---

## Option 2: Manual Scenario Runner

Guided step-by-step test scenarios with pass/fail tracking:

```bash
# Run the scenario runner
make test-scenarios

# Or run manually:
cd tests/user/scenarios
go run scenario_runner.go
```

**What you get:**
- 📋 10+ pre-defined test scenarios
- 📊 Pass/fail tracking
- 📝 Exportable test reports
- 🎯 Platform-specific tests (macOS, Linux, etc.)
- 🔍 Difficulty levels (easy, medium, hard)

**Perfect for:** Comprehensive testing, plugin validation, bug reporting

---

## Option 3: Automated Tests (For CI/CD)

Run the full automated test suite:

```bash
# Run all user tests
make test-user

# Or run specific test packages
go test ./tests/user/workflows/... -v
go test ./tests/user/plugins/... -v
```

**Test categories:**
- 🔄 **Workflows** - End-to-end user journeys
- 🔌 **Plugins** - Plugin integration tests
- ⚡ **Quick Tests** - Fast smoke tests

**Perfect for:** Automated testing, regression detection, CI/CD pipelines

---

## Quick Test Examples

### Test 1: Verify Everything Works (30 seconds)

```bash
# 1. Start server
make run-dev

# 2. In another terminal, run quick test
curl http://localhost:8765/health

# Expected: {"status":"ok"}
```

### Test 2: Plugin Test (2 minutes)

```bash
# 1. Use test CLI
make test-cli

# 2. Select: "7. Test specific plugin"
# 3. Choose "math"
# 4. Observe results
```

### Test 3: Full Workflow (5 minutes)

```bash
# 1. Run scenario runner
make test-scenarios

# 2. Select: "3. Run single scenario"
# 3. Choose: "Math Plugin Integration"
# 4. Follow steps
# 5. Report pass/fail
```

---

## Test Reports

After running manual scenarios, reports are saved to `tests/user/reports/`:

```bash
# List reports
ls -lt tests/user/reports/

# View report
cat tests/user/reports/scenario-2025-01-15-14-30.json | jq

# Example report structure:
{
  "run_date": "2025-01-15T14:30:00Z",
  "platform": "darwin",
  "results": [
    {
      "scenario_id": "sc001",
      "name": "Basic Agent Creation",
      "passed": true,
      "duration": "1m30s"
    }
  ],
  "summary": {
    "total": 10,
    "passed": 9,
    "failed": 1
  }
}
```

---

## Common Issues

### Issue: "Server binary not found"
**Solution:**
```bash
make build
```

### Issue: "Plugins not found"
**Solution:**
```bash
make plugins
# OR
./scripts/build-plugins.sh
```

### Issue: "No API key set"
**Solution:**
```bash
export OPENAI_API_KEY="your-key-here"
# OR
export ANTHROPIC_API_KEY="your-key-here"
```

### Issue: "Port 8765 already in use"
**Solution:**
```bash
# Kill existing server
lsof -ti :8765 | xargs kill

# Or use different port
PORT=9000 make run-dev
```

---

## What to Test First

### For New Users:
1. ✅ Basic agent creation (sc001)
2. ✅ Math plugin integration (sc002)
3. ✅ Multi-turn conversation (sc004)

### For Plugin Developers:
1. ✅ Plugin loading performance
2. ✅ Multiple plugins on one agent (sc005)
3. ✅ Agent-aware plugin context
4. ✅ Plugin health checks (sc008)

### For macOS Users:
1. ✅ Music project manager (sc006)
2. ✅ Ori-Reaper integration (sc007)
3. ✅ macOS-specific plugins

---

## Next Steps

After running initial tests:

1. **Read the full guide:** [tests/user/README.md](README.md)
2. **Add custom scenarios:** Edit `scenarios/scenarios.json`
3. **Write automated tests:** See example in `workflows/agent_workflow_test.go`
4. **Integrate with CI:** Add to GitHub Actions workflow

---

## Getting Help

- **Documentation:** See [tests/user/README.md](README.md)
- **Examples:** Check `workflows/` and `plugins/` directories
- **Issues:** File bugs with test reports attached
- **Questions:** Include relevant test scenario IDs

---

## Quick Command Reference

```bash
# Build everything
make build plugins

# Interactive testing CLI
make test-cli

# Manual scenario runner
make test-scenarios

# Automated tests
make test-user

# Run specific workflow tests
go test ./tests/user/workflows/... -v -run TestCompleteAgentWorkflow

# Run plugin tests
go test ./tests/user/plugins/... -v

# Clean up test artifacts
export TEST_CLEANUP=false  # Keep artifacts for debugging
make test-user
```

Happy Testing! 🚀
