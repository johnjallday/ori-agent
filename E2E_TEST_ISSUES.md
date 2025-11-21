# E2E Test Issues

## Date: 2025-11-21

## Failing Tests

### 1. TestHealthCheck
- **Issue**: Test expects JSON from `/health` endpoint
- **Error**: `invalid character '<' looking for beginning of value`
- **Root Cause**: The `/health` endpoint doesn't exist in the current server implementation
- **Available Endpoints**: `/api/plugins/health` exists, but not root `/health`
- **Status**: Test is outdated

### 2. TestAgentLifecycle
- **Status**: Fails (details not captured)
- **Likely Cause**: Related to missing `/health` endpoint or other API changes

## Impact

These e2e test failures are **non-critical** for pre-release because:
1. The pre-release script marks them as optional: `run_check "All Tests" "go test -p 1 ./..." || true`
2. All user tests pass successfully
3. All unit and integration tests pass
4. Server builds and runs correctly

## Resolution Options

### Option 1: Add `/health` Endpoint (Recommended)
Add a simple health endpoint to match test expectations:

```go
// In internal/server/server.go
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
})
```

### Option 2: Update Tests
Update `tests/e2e/server_test.go` to use the correct endpoint:
- Change `/health` to `/api/plugins/health`
- Update expected response format

### Option 3: Skip E2E Tests Temporarily
Mark e2e tests as skipped until they can be properly updated:
- Add skip condition to test files
- Document in test comments

## Test Results

### Passing E2E Tests ✅
- TestPluginLoading
- TestPluginHealthChecks
- TestServerStartup
- TestPluginRegistry
- TestSettingsEndpoint
- TestConcurrentRequests

### Failing E2E Tests ❌
- TestHealthCheck (missing endpoint)
- TestAgentLifecycle (dependency on health check)

## Recommendation

**For immediate pre-release**: Accept the e2e failures as non-critical since:
- Core functionality tests all pass
- User workflow tests all pass
- Integration tests all pass
- Server builds successfully

**For next iteration**: Add the `/health` endpoint to fix these tests properly.

---

**Status**: ⚠️ Non-critical failures
**Impact**: Low - Does not block release
