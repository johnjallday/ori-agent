# Agentic Pre-Release Workflow

This document describes the automated, agentic workflow for handling pre-release check failures.

## Overview

The pre-release check (`./scripts/pre-release-check.sh`) now includes intelligent, automated fixing capabilities for common failure scenarios:

- **Lint Failures** → Automated lint fixing (auto-fix + AI-powered)
- **Test Failures** → Diagnostic tool with auto-repair options

## Workflow Diagram

```
┌─────────────────────────────────┐
│  ./scripts/pre-release-check.sh │
└────────────┬────────────────────┘
             │
    ┌────────┴─────────┐
    │  Code Quality    │
    │  Checks          │
    └────────┬─────────┘
             │
     ┌───────▼────────┐
     │  Lint Check    │
     └───────┬────────┘
             │
        ┌────▼─────┐
        │  Pass?   │
        └────┬─────┘
             │
      No ────┤
             │
    ┌────────▼─────────────────────────┐
    │ Offer: Run automated lint fixer? │
    │ [y/N]                            │
    └────────┬─────────────────────────┘
             │
         y ──┤
             │
    ┌────────▼────────────────────┐
    │ ./scripts/fix-all-lint.sh   │
    │                             │
    │ Step 1: Auto-fix (simple)   │
    │ Step 2: AI-fix (complex)    │
    │ Step 3: Verify with tests   │
    └────────┬────────────────────┘
             │
    ┌────────▼────────┐
    │ Re-run lint     │
    │ check           │
    └────────┬────────┘
             │
             │
    ┌────────▼─────────┐
    │  All Tests       │
    └────────┬─────────┘
             │
        ┌────▼─────┐
        │  Pass?   │
        └────┬─────┘
             │
      No ────┤
             │
    ┌────────▼──────────────────────────┐
    │ Offer: Run test diagnostics?      │
    │ [y/N]                             │
    └────────┬──────────────────────────┘
             │
         y ──┤
             │
    ┌────────▼─────────────────────────────┐
    │ ./scripts/diagnose-test-failures.sh  │
    │                                      │
    │ Step 1: Check API configuration      │
    │ Step 2: Test connectivity           │
    │ Step 3: Run diagnostic test         │
    │ Step 4: Offer solutions             │
    │   - Update model name                │
    │   - Switch to Ollama                 │
    │   - Fix API keys                     │
    └────────┬─────────────────────────────┘
             │
    ┌────────▼────────┐
    │ Re-run tests    │
    └────────┬────────┘
             │
             ▼
        (continues...)
```

## Features

### 1. Automated Lint Fixing

**Trigger:** Lint check fails during pre-release check

**Actions:**
1. **Auto-Fix Phase**
   - Runs `golangci-lint run ./... --fix`
   - Fixes formatting, imports, simple violations

2. **AI-Fix Phase** (if issues remain)
   - Prompts user to use Claude Code
   - Analyzes remaining lint errors
   - Fixes complex issues:
     - Unused variables/imports
     - Missing error handling
     - Ineffectual assignments
     - Style violations

3. **Verification**
   - Re-runs lint check
   - Runs tests to ensure nothing broke
   - Reports success/failure

**Script:** `./scripts/fix-all-lint.sh`

**Usage:**
```bash
# Standalone
./scripts/fix-all-lint.sh

# Integrated (automatic prompt during pre-release)
./scripts/pre-release-check.sh
```

### 2. Test Failure Diagnostics

**Trigger:** Tests fail during pre-release check

**Actions:**
1. **API Configuration Check**
   - Verifies OPENAI_API_KEY set
   - Verifies ANTHROPIC_API_KEY set
   - Checks Ollama installation/status

2. **Connectivity Testing**
   - Tests OpenAI API connection
   - Validates API keys
   - Checks model availability (gpt-4o-mini, gpt-4o)

3. **Diagnostic Test**
   - Runs sample test to identify specific errors
   - Detects common issues:
     - 404: Model not found
     - 401/403: Authentication errors
     - Network issues

4. **Automated Fixes**
   - **404 Errors:**
     - Option 1: Update model from `gpt-4o-mini` to `gpt-4.1-nano`
     - Option 2: Switch to Ollama (local, free)
   - **Auth Errors:**
     - Prompts to check/update API keys
   - **Other Errors:**
     - Provides context-specific guidance

**Script:** `./scripts/diagnose-test-failures.sh`

**Usage:**
```bash
# Standalone
./scripts/diagnose-test-failures.sh

# Integrated (automatic prompt during pre-release)
./scripts/pre-release-check.sh
```

## Example Session

### Lint Failure Example

```bash
$ ./scripts/pre-release-check.sh

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Running: Lint Check
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
internal/server.go:45:2: ineffectual assignment to err
internal/plugin.go:123:5: unused variable 'result'
❌ Lint Check: FAILED

💡 Tip: Automated lint fixing is available

Run automated lint fixer? [y/N]: y

╔════════════════════════════════════════════╗
║     Automated Lint Fixing Workflow        ║
╚════════════════════════════════════════════╝

Step 1: Running golangci-lint auto-fix
✓ Auto-fix complete

Step 2: Checking for remaining issues
Some issues require AI assistance:
  internal/server.go:45:2 - ineffectual assignment...

Step 3: AI-Powered Fix
Use Claude Code to fix remaining issues? [y/N]: y

(Claude analyzes and fixes issues...)

✅ All lint issues resolved!
✅ Tests passed!

Re-running lint check after fixes...
✅ Lint Check (after fixes): PASSED
```

### Test Failure Example

```bash
$ ./scripts/pre-release-check.sh

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Running: All Tests
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FAIL: TestMathPluginIntegration
POST "https://api.openai.com/v1/chat/completions": 404 Not Found
❌ All Tests: FAILED

💡 Tip: Test diagnostic tool is available

Run test diagnostics and auto-fix? [y/N]: y

╔════════════════════════════════════════════╗
║     Test Failure Diagnostic Tool          ║
╚════════════════════════════════════════════╝

Step 1: Checking API Configuration
✓ OPENAI_API_KEY is set
⚠️  ANTHROPIC_API_KEY is not set
⚠️  Ollama is not installed

Step 2: Testing API Connectivity
Testing OpenAI API...
✓ OpenAI API is accessible
⚠️  gpt-4o-mini not found, but gpt-4o is available

Step 3: Running Quick Test
❌ Test failed
Error: 404 Not Found

Step 4: Recommended Solutions
Issue: 404 Not Found (Model doesn't exist)

Possible fixes:
  1. Update test model to 'gpt-4o'
  2. Use Ollama instead (local, free)
  3. Exit

Would you like to: [1] Update to gpt-4o, [2] Use Ollama, [3] Exit: 1

Updating tests to use gpt-4o...
✓ Updated model references to gpt-4o

Re-running tests...
✅ Tests passed with gpt-4o!
```

## Manual Usage

Both scripts can be run independently outside the pre-release workflow:

### Fix Lint Issues Only
```bash
./scripts/fix-all-lint.sh
```

### Diagnose Test Failures Only
```bash
./scripts/diagnose-test-failures.sh
```

### Full Pre-Release with Agentic Fixes
```bash
./scripts/pre-release-check.sh
# Follow prompts when checks fail
```

## Configuration

### Environment Variables

The diagnostic tool respects these environment variables:

```bash
# LLM Provider Configuration
export OPENAI_API_KEY="sk-..."          # OpenAI API key
export ANTHROPIC_API_KEY="sk-ant-..."   # Anthropic API key
export USE_OLLAMA=true                   # Use Ollama for tests
export OLLAMA_MODEL=granite4             # Ollama model name
```

### Script Permissions

Ensure scripts are executable:

```bash
chmod +x ./scripts/fix-all-lint.sh
chmod +x ./scripts/diagnose-test-failures.sh
chmod +x ./scripts/pre-release-check.sh
```

## Troubleshooting

### "fix-all-lint.sh not found"

The script should be in `./scripts/`. If missing:

```bash
# Verify location
ls -la ./scripts/fix-all-lint.sh

# If in different location, update path in pre-release-check.sh
```

### "Claude Code CLI not found"

Install Claude Code CLI:
```bash
# Visit: https://claude.ai/code
```

### Tests Still Failing After Diagnostics

1. **Check API Key Validity**
   ```bash
   # OpenAI
   curl -H "Authorization: Bearer $OPENAI_API_KEY" \
     https://api.openai.com/v1/models
   ```

2. **Use Ollama Instead**
   ```bash
   # Install Ollama
   # Visit: https://ollama.com

   # Run tests with Ollama
   USE_OLLAMA=true go test ./...
   ```

3. **Check Plugin Builds**
   ```bash
   make plugins
   ```

## Benefits

✅ **Reduced Manual Work** - Automated fixing reduces manual debugging time

✅ **Intelligent Diagnostics** - Identifies root causes, not just symptoms

✅ **Multi-Tier Fixing** - Auto-fix → AI-fix → Manual (escalation path)

✅ **Safe** - Always verifies fixes with tests before proceeding

✅ **Interactive** - User maintains control with prompts at key decision points

✅ **Educational** - Shows what was fixed and why

## Future Enhancements

Potential future additions:

- **Build Failure Diagnostics** - Diagnose and fix build errors
- **Security Scan Auto-Fix** - Automated vulnerability patching
- **Dependency Update Automation** - Smart dependency updates
- **Performance Test Baseline** - Auto-adjust performance baselines
- **Coverage Report Analysis** - Identify and add missing test coverage

## Related Documentation

- [Pre-Release Check Script](../scripts/pre-release-check.sh)
- [Fix All Lint Script](../scripts/fix-all-lint.sh)
- [Diagnose Test Failures Script](../scripts/diagnose-test-failures.sh)
- [Testing Guide](TESTING.md)
- [Git Workflow](../GIT_WORKFLOW.md)
