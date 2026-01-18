# Common Errors and Fixes

## 1. Cannot find package

**Error:**
```
cannot find package "github.com/oriagent/ori-pluginapi"
```

**Fix:** Ensure `go.mod` has the correct require:
```
require github.com/oriagent/ori-pluginapi v1.0.1
```
Then run `go mod tidy`.

---

## 2. Undefined pluginapi.X

**Error:**
```
undefined: pluginapi.BasePlugin
```

**Fix:** Check the import statement:
```go
import "github.com/oriagent/ori-pluginapi"
```

---

## 3. JSON Unmarshal Error

**Error:**
```
invalid arguments: json: cannot unmarshal string into Go struct field
```

**Fix:** Ensure Params struct field types match JSON:
- Use `int` for integers, not `string`
- Use `bool` for booleans
- Use correct `json:"field_name"` tags

---

## 4. Does Not Embed BasePlugin

**Error:**
```
tool *MyPluginTool does not embed pluginapi.BasePlugin
```

**Fix:** Add embedded BasePlugin to your struct:
```go
type MyPluginTool struct {
    pluginapi.BasePlugin  // This line is required
}
```

---

## 5. Missing go.sum Entry

**Error:**
```
missing go.sum entry for module
```

**Fix:** Run `go mod tidy` to update go.sum.

---

## 6. Plugin Not Loading

**Symptoms:** Plugin builds but doesn't appear in Ori Agent.

**Checklist:**
1. Plugin built as executable (NOT `-buildmode=plugin`)
2. Binary has execute permissions: `chmod +x plugin-name`
3. Path is correct in registry or settings
4. Check server logs for RPC errors

---

## 7. Context Canceled

**Error:**
```
context canceled
```

**Fix:** The operation timed out. Either:
- Increase timeout in HTTP client
- Handle context cancellation gracefully
- Check for long-running operations

---

## 8. Settings Not Available

**Error:**
```
settings not available - plugin not initialized
```

**Fix:** Settings require agent context. Check:
```go
sm := t.Settings()
if sm == nil {
    // Handle gracefully - agent context not set yet
}
```

---

## 9. Empty Parameters / No Tools Showing

**Symptoms:** Plugin loads but shows empty `Parameters: {}` or no tools appear.

**Common Causes:**

1. **Invalid parameter type** - Using `int` instead of `integer`:
   ```yaml
   # WRONG - causes empty Parameters
   - name: limit
     type: int

   # CORRECT
   - name: limit
     type: integer
   ```

2. **Missing required plugin.yaml fields**:
   ```
   missing required field: license
   missing required field: repository
   missing required field: platforms
   ```

   **Fix:** Add all required fields to plugin.yaml:
   ```yaml
   license: MIT
   repository: https://github.com/user/plugin
   platforms:
     - os: darwin
       architectures: [amd64, arm64]
   ```

3. **Missing operations section** - For multi-operation plugins with enum:
   ```yaml
   # Add operations section after parameters
   operations:
     create:
       parameters:
         - name: value
           type: string
           required: true
     list:
       parameters: []
   ```

**Valid tool parameter types:** `string`, `integer`, `number`, `boolean`, `array`, `object`

**NOT valid:** `int`, `bool`, `float` (use `integer`, `boolean`, `number` instead)

---

## 10. Plugin Panic on Startup

**Error:**
```
panic: ServePlugin failed to parse config: invalid plugin config: missing required field: X
```

**Fix:** Ensure plugin.yaml has ALL required fields:
- `name`
- `version` (semver format: "1.0.0")
- `description`
- `license`
- `repository` (valid URL)
- `platforms` (at least one)
- `maintainers` (at least one with name and email)

---

## Build Commands Reference

```bash
# In the plugin directory:

# Resolve dependencies
go mod tidy

# Build executable
go build -o plugin-name .

# Verify build
ls -la plugin-name

# Test plugin starts (should show go-plugin message)
./plugin-name
```

**Important:** Plugins are built as executables, NOT using `-buildmode=plugin`.
