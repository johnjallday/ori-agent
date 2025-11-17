# User Testing Framework

This directory contains the **user-testing framework** for ori-agent, focused on end-to-end workflow validation, plugin testing, and real-world user scenarios.

## Overview

The user-testing system provides:
- **Automated E2E workflow tests** - Full user journeys from agent creation to plugin execution
- **Plugin integration tests** - Validate plugins work correctly with real LLM providers
- **Manual test scenarios** - Guided test cases for human verification
- **Performance benchmarks** - Track response times and resource usage
- **Test data generation** - Synthetic user queries and scenarios

## Directory Structure

```
tests/user/
├── README.md              # This file
├── workflows/             # End-to-end workflow tests
│   ├── agent_workflow_test.go
│   ├── plugin_workflow_test.go
│   └── chat_workflow_test.go
├── plugins/               # Plugin-specific tests
│   ├── plugin_integration_test.go
│   ├── plugin_compatibility_test.go
│   └── plugin_health_test.go
├── scenarios/             # Manual test scenarios
│   ├── scenario_runner.go
│   └── scenarios.json
├── helpers/               # Test utilities
│   ├── test_context.go
│   ├── assertions.go
│   └── fixtures.go
└── reports/               # Test reports (gitignored)
    └── .gitkeep
```

## Running Tests

### Quick Start

```bash
# Run all user tests (requires API key)
make test-user

# Run specific workflow tests
go test ./tests/user/workflows/... -v

# Run plugin integration tests
go test ./tests/user/plugins/... -v

# Run with detailed output
go test ./tests/user/... -v -timeout 5m

# Run specific test
go test ./tests/user/workflows -run TestCompleteAgentWorkflow -v
```

### Test Categories

**1. Workflow Tests** (Automated E2E)
```bash
go test ./tests/user/workflows/... -v
```
- Agent creation → plugin configuration → chat interaction → cleanup
- Multi-agent collaboration workflows
- Error recovery scenarios

**2. Plugin Tests** (Integration)
```bash
go test ./tests/user/plugins/... -v
```
- Plugin loading and initialization
- Tool definition validation
- Plugin execution with real LLM
- Agent-aware plugin context

**3. Manual Scenarios** (Human-verified)
```bash
# Run interactive scenario suite
go run ./tests/user/scenarios/scenario_runner.go
```
- Guided test cases with pass/fail prompts
- Real user interaction patterns
- Edge cases requiring human judgment

## Test Configuration

### Environment Variables

```bash
# Required
export OPENAI_API_KEY="sk-..."
# OR
export ANTHROPIC_API_KEY="sk-ant-..."

# Optional
export TEST_TIMEOUT="5m"           # Default: 2m
export TEST_VERBOSE="true"         # Default: false
export TEST_CLEANUP="false"        # Keep test artifacts (default: true)
export TEST_PLUGIN_DIR="./plugins" # Plugin directory
```

### Test Modes

**Short Mode** - Skip E2E tests
```bash
go test -short ./tests/user/...
```

**Coverage Mode** - Generate coverage report
```bash
go test -coverprofile=coverage.out ./tests/user/...
go tool cover -html=coverage.out
```

## Writing User Tests

### Example: Workflow Test

```go
func TestUserWorkflow_CreateAgentAndChat(t *testing.T) {
    ctx := helpers.NewTestContext(t)
    defer ctx.Cleanup()

    // Step 1: Create agent
    agent := ctx.CreateAgent("test-agent", "gpt-4o-mini")

    // Step 2: Enable plugin
    ctx.EnablePlugin(agent, "math")

    // Step 3: Send chat message
    response := ctx.SendChat(agent, "What is 5 + 3?")

    // Step 4: Verify tool was called
    ctx.AssertToolCalled(response, "math")
    ctx.AssertResponseContains(response, "8")
}
```

### Example: Plugin Test

```go
func TestPlugin_MathIntegration(t *testing.T) {
    ctx := helpers.NewTestContext(t)
    defer ctx.Cleanup()

    // Load plugin
    plugin := ctx.LoadPlugin("math")

    // Test definition
    def := plugin.Definition()
    assert.Equal(t, "math", def.Name)

    // Test execution
    result := plugin.Call(`{"operation": "add", "a": 5, "b": 3}`)
    assert.Contains(t, result, "8")
}
```

## Manual Test Scenarios

The scenario runner provides guided manual tests:

```bash
go run ./tests/user/scenarios/scenario_runner.go

# Example output:
# ╔════════════════════════════════════════╗
# ║   Ori Agent - User Test Scenarios     ║
# ╚════════════════════════════════════════╝
#
# Scenario 1: Create Music Production Agent
# ──────────────────────────────────────────
# 1. Open http://localhost:8765
# 2. Click "Create Agent"
# 3. Name: "Music Producer"
# 4. Enable: ori-reaper plugin
# 5. Chat: "Create a new project called Test Song"
#
# Did the agent create the project? (y/n):
```

### Scenario Format

Scenarios are defined in `scenarios/scenarios.json`:

```json
{
  "scenarios": [
    {
      "id": "sc001",
      "name": "Create Music Production Agent",
      "category": "plugin-workflow",
      "platform": "macos",
      "steps": [
        "Open http://localhost:8765",
        "Click 'Create Agent'",
        "Name: 'Music Producer'",
        "Enable: ori-reaper plugin",
        "Chat: 'Create a new project called Test Song'"
      ],
      "verification": "Did the agent create the project?",
      "expected": "New Reaper project created with name 'Test Song'"
    }
  ]
}
```

## Test Reports

After running tests, reports are generated in `tests/user/reports/`:

```
reports/
├── workflow-2025-01-15-14-30.json
├── plugin-2025-01-15-14-35.json
└── scenarios-2025-01-15-14-40.json
```

View reports:
```bash
# List recent reports
ls -lt tests/user/reports/

# View specific report
cat tests/user/reports/workflow-2025-01-15-14-30.json | jq
```

## Continuous Integration

Add to GitHub Actions workflow:

```yaml
- name: Run User Tests
  run: make test-user
  env:
    OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
```

## Best Practices

1. **Isolation** - Each test should be independent and clean up after itself
2. **Realistic** - Use actual LLM providers, not mocks (for integration tests)
3. **Fast** - Keep workflows under 30s when possible
4. **Observable** - Log key actions for debugging
5. **Deterministic** - Avoid flaky tests with proper waits/retries

## Troubleshooting

### Tests Timing Out
```bash
# Increase timeout
go test ./tests/user/... -timeout 10m
```

### Plugin Not Found
```bash
# Build plugins first
make plugins

# Check plugin path
ls -la plugins/math/math
```

### API Rate Limits
```bash
# Use cheaper model for tests
export TEST_MODEL="gpt-4o-mini"

# Add delays between tests
export TEST_DELAY="2s"
```

## Contributing

When adding new user tests:
1. Choose appropriate category (workflow, plugin, scenario)
2. Use test helpers in `helpers/` for common operations
3. Clean up test artifacts in defer statements
4. Add documentation for complex scenarios
5. Consider both success and failure cases

## Shared Plugins Testing

This framework supports testing plugins from both:
- **Built-in plugins**: `plugins/` (math, weather, result-handler)
- **Shared plugins**: `../plugins/` (ori-music-project-manager, ori-reaper, ori-mac-os-tools, etc.)

See [SHARED_PLUGINS_TESTING.md](SHARED_PLUGINS_TESTING.md) for complete guide on testing shared plugins.

**Quick commands:**
```bash
# Build shared plugins
./scripts/build-external-plugins.sh

# Test shared plugins
go test ./tests/user/plugins -run TestSharedPluginsAvailable -v

# Run shared plugin scenarios
make test-scenarios
# Select category: shared-plugin
```

## Related Documentation

- [QUICKSTART.md](QUICKSTART.md) - Get started in 5 minutes
- [TEST_CHEATSHEET.md](TEST_CHEATSHEET.md) - Quick command reference
- [SHARED_PLUGINS_TESTING.md](SHARED_PLUGINS_TESTING.md) - Testing ../plugins directory
- [IMPLEMENTATION_SUMMARY.md](IMPLEMENTATION_SUMMARY.md) - What was built
- [TESTING.md](../../TESTING.md) - Complete testing guide
- [PROJECT_GUIDELINES.md](../../PROJECT_GUIDELINES.md) - Architecture overview
- [Plugin Development](../../CLAUDE.md#plugin-development) - Plugin creation guide
