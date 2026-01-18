# Common Errors and Fixes

## 1. Cannot find package

**Error:**
```
cannot find package "github.com/johnjallday/ori-agent/pluginapi"
```

**Fix:** Ensure `go.mod` has the correct replace directive with absolute path:
```
replace github.com/johnjallday/ori-agent => /absolute/path/to/ori-agent
```

---

## 2. Undefined pluginapi.X

**Error:**
```
undefined: pluginapi.BasePlugin
```

**Fix:** Check the import statement:
```go
import "github.com/johnjallday/ori-agent/pluginapi"
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

## Build Commands Reference

```bash
# In the plugin directory:

# Resolve dependencies
go mod tidy

# Build executable
go build -o plugin-name .

# Verify build
ls -la plugin-name
```

**Important:** Plugins are built as executables, NOT using `-buildmode=plugin`.
