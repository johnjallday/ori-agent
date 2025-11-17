# User Testing Cheatsheet

Quick reference for testing ori-agent on your macOS system.

## Setup (One-Time)

```bash
cd ori-agent
export OPENAI_API_KEY="sk-..."
make build plugins
```

## Three Ways to Test

### 1️⃣ Interactive CLI (Easiest)
```bash
make test-cli
```
Menu-driven interface - perfect for quick tests and exploration.

### 2️⃣ Manual Scenarios (Most Comprehensive)
```bash
make test-scenarios
```
Step-by-step guided tests with pass/fail tracking.

### 3️⃣ Automated Tests (For CI/CD)
```bash
make test-user
```
Full automated test suite - runs in ~5 minutes.

---

## Common Test Commands

```bash
# Run all user tests
make test-user

# Run specific workflow test
go test ./tests/user/workflows -run TestCompleteAgentWorkflow -v

# Run all plugin tests
go test ./tests/user/plugins/... -v

# Run with verbose output
export TEST_VERBOSE=true
make test-user

# Keep test artifacts for debugging
export TEST_CLEANUP=false
make test-user
```

---

## Quick Tests (30 seconds each)

### Test 1: Health Check
```bash
make run-dev &
sleep 5
curl http://localhost:8765/health
# Expected: {"status":"ok"}
killall ori-agent
```

### Test 2: Math Plugin
```bash
make test-cli
# Select: 7 (Test specific plugin)
# Choose: math
```

### Test 3: Basic Workflow
```bash
make test-scenarios
# Select: 3 (Run single scenario)
# Choose: 1 (Basic Agent Creation)
```

---

## Manual Scenarios

| ID | Name | Time | Difficulty |
|----|------|------|------------|
| sc001 | Basic Agent Creation | 2 min | 🟢 Easy |
| sc002 | Math Plugin Integration | 3 min | 🟢 Easy |
| sc003 | Weather Plugin Test | 3 min | 🟢 Easy |
| sc004 | Multi-Turn Conversation | 5 min | 🟡 Medium |
| sc005 | Multiple Plugins on One Agent | 5 min | 🟡 Medium |
| sc006 | Music Project Manager (macOS) | 10 min | 🔴 Hard |
| sc007 | Ori-Reaper Integration (macOS) | 15 min | 🔴 Hard |
| sc008 | Plugin Health Check | 2 min | 🟢 Easy |
| sc009 | Error Recovery | 3 min | 🟡 Medium |
| sc010 | Agent Deletion | 2 min | 🟢 Easy |

---

## Troubleshooting

### Server won't start
```bash
lsof -ti :8765 | xargs kill
make run-dev
```

### Plugins not found
```bash
make plugins
ls -la plugins/*/
```

### Tests timing out
```bash
export TEST_TIMEOUT="10m"
make test-user
```

### API key issues
```bash
echo $OPENAI_API_KEY
# Should show: sk-...
```

---

## Test Reports

After running manual scenarios:

```bash
# List reports
ls -lt tests/user/reports/

# View latest report
cat tests/user/reports/*.json | jq

# Example report structure:
{
  "summary": {
    "total": 10,
    "passed": 9,
    "failed": 1
  }
}
```

---

## Environment Variables

```bash
# Required (at least one)
export OPENAI_API_KEY="sk-..."
export ANTHROPIC_API_KEY="sk-ant-..."

# Optional
export TEST_VERBOSE="true"      # Show detailed logs
export TEST_CLEANUP="false"     # Keep test data
export TEST_TIMEOUT="5m"        # Increase timeout
export PORT="9000"              # Use different port
```

---

## Writing Your Own Tests

### Automated Test Template
```go
func TestMyWorkflow(t *testing.T) {
    ctx := helpers.NewTestContext(t)
    defer ctx.Cleanup()

    agent := ctx.CreateAgent("my-agent", "gpt-4o-mini")
    ctx.EnablePlugin(agent, "math")
    resp := ctx.SendChat(agent, "What is 2+2?")
    ctx.AssertToolCalled(resp, "math")
    ctx.AssertResponseContains(resp, "4")
}
```

### Add Manual Scenario
Edit `tests/user/scenarios/scenarios.json`:
```json
{
  "id": "sc011",
  "name": "My Custom Test",
  "category": "plugin-workflow",
  "platform": "macos",
  "difficulty": "easy",
  "steps": [
    "Step 1...",
    "Step 2..."
  ]
}
```

---

## Help & Documentation

- **Quick Start**: `tests/user/QUICKSTART.md`
- **Full Docs**: `tests/user/README.md`
- **Summary**: `tests/user/IMPLEMENTATION_SUMMARY.md`
- **This File**: `tests/user/TEST_CHEATSHEET.md`

---

## Test Workflow Recommendations

### For Daily Development:
```bash
make test-cli
# Check environment, run quick tests
```

### Before Committing:
```bash
make test-user
# Run all automated tests
```

### For Plugin Development:
```bash
make test-scenarios
# Run plugin-specific scenarios
```

### For Bug Reporting:
```bash
export TEST_CLEANUP=false
make test-user
# Include test reports with bug report
```

---

**Pro Tip:** Run `make test-cli` first to verify your environment is set up correctly!
