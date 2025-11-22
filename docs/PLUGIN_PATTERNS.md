# Plugin Patterns

This guide demonstrates common patterns used in Ori Agent plugins, showing how they translate across different programming languages.

## Operation Dispatch Pattern

All Ori Agent plugins use a simple, consistent pattern to route operations to their handlers. This pattern is language-agnostic and works the same way in Go, Python, JavaScript, and other languages.

### Pattern Overview

1. **Define a registry** (dict/map/object) mapping operation names to handler functions
2. **Implement Execute()** method that:
   - Looks up the operation in the registry
   - Returns an error if operation not found
   - Calls the handler and returns its result

### Go Implementation

```go
package main

import (
    "context"
    "fmt"
)

// Define the handler function signature
type OperationHandler func(ctx context.Context, t *myTool, params *MyParams) (string, error)

// Create the operation registry
var operationRegistry = map[string]OperationHandler{
    "operation1": handleOperation1,
    "operation2": handleOperation2,
    "operation3": handleOperation3,
}

// Execute method - dispatches to the appropriate handler
func (t *myTool) Execute(ctx context.Context, params *MyParams) (string, error) {
    // Look up handler in registry
    handler, ok := operationRegistry[params.Operation]
    if !ok {
        return "", fmt.Errorf("unknown operation: %s", params.Operation)
    }

    // Execute the handler
    return handler(ctx, t, params)
}

// Individual operation handlers
func handleOperation1(ctx context.Context, t *myTool, params *MyParams) (string, error) {
    // Implementation here
    return "result", nil
}

func handleOperation2(ctx context.Context, t *myTool, params *MyParams) (string, error) {
    // Implementation here
    return "result", nil
}
```

### Python Implementation (Future)

```python
class MyTool:
    def __init__(self):
        # Create the operation registry
        self.operations = {
            "operation1": self.handle_operation1,
            "operation2": self.handle_operation2,
            "operation3": self.handle_operation3,
        }

    def execute(self, params):
        """Execute method - dispatches to the appropriate handler"""
        # Look up handler in registry
        handler = self.operations.get(params.operation)
        if not handler:
            raise ValueError(f"Unknown operation: {params.operation}")

        # Execute the handler
        return handler(params)

    # Individual operation handlers
    def handle_operation1(self, params):
        # Implementation here
        return "result"

    def handle_operation2(self, params):
        # Implementation here
        return "result"
```

### JavaScript Implementation (Future)

```javascript
class MyTool {
    constructor() {
        // Create the operation registry
        this.operations = {
            "operation1": this.handleOperation1.bind(this),
            "operation2": this.handleOperation2.bind(this),
            "operation3": this.handleOperation3.bind(this),
        };
    }

    // Execute method - dispatches to the appropriate handler
    execute(params) {
        // Look up handler in registry
        const handler = this.operations[params.operation];
        if (!handler) {
            throw new Error(`Unknown operation: ${params.operation}`);
        }

        // Execute the handler
        return handler(params);
    }

    // Individual operation handlers
    handleOperation1(params) {
        // Implementation here
        return "result";
    }

    handleOperation2(params) {
        // Implementation here
        return "result";
    }
}
```

## Real-World Examples

### Math Plugin (Go)

See [`example_plugins/math/math.go`](../example_plugins/math/math.go) for a complete, working example:

```go
var operationRegistry = map[string]OperationHandler{
    "add":      handleAdd,
    "subtract": handleSubtract,
    "multiply": handleMultiply,
    "divide":   handleDivide,
}

func (m *mathTool) Execute(ctx context.Context, params *MathParams) (string, error) {
    handler, ok := operationRegistry[params.Operation]
    if !ok {
        return "", fmt.Errorf("unknown operation: %s. Valid operations: add, subtract, multiply, divide", params.Operation)
    }
    return handler(m, params)
}

func handleAdd(m *mathTool, params *MathParams) (string, error) {
    result := params.A + params.B
    return fmt.Sprintf("%g", result), nil
}
```

### REAPER Plugin (Go)

See [`plugins/ori-reaper/main.go`](../../plugins/ori-reaper/main.go) for a more complex example with many operations:

```go
var operationRegistry = map[string]OperationHandler{
    "list":                   handleList,
    "run":                    handleRun,
    "add":                    handleAdd,
    "delete":                 handleDelete,
    "list_available_scripts": handleListAvailableScripts,
    "download_script":        handleDownloadScript,
    "register_script":        handleRegisterScript,
    "register_all_scripts":   handleRegisterAllScripts,
    "clean_scripts":          handleCleanScripts,
    "get_context":            handleGetContext,
    "get_web_remote_port":    handleGetWebRemotePort,
    "get_tracks":             handleGetTracks,
}

func (t *ori_reaperTool) Execute(ctx context.Context, params *OriReaperParams) (string, error) {
    handler, ok := operationRegistry[params.Operation]
    if !ok {
        validOps := t.getValidOperations()
        return "", fmt.Errorf("unknown operation: %s. Valid operations: %v", params.Operation, validOps)
    }
    return handler(ctx, t, params)
}
```

## Why This Pattern?

### ✅ Advantages

1. **Simple**: Only 5-6 lines of boilerplate code
2. **Explicit**: All operations visible in one place
3. **Cross-language**: Same concept in every language
4. **Maintainable**: Easy to add/remove operations
5. **Testable**: Can test handlers independently
6. **No magic**: No reflection, no code generation, no hidden behavior

### 📚 Consistency

Using the same pattern across all plugins means:
- New developers can quickly understand any plugin
- Documentation is straightforward
- Debugging is easier (same structure everywhere)
- Multi-language support is simpler

### 🔍 Discoverability

The operation registry serves as documentation:
```go
var operationRegistry = map[string]OperationHandler{
    "operation1": handleOperation1,  // ← All operations listed here
    "operation2": handleOperation2,
    "operation3": handleOperation3,
}
```

Anyone reading the code can immediately see all available operations.

## Alternative Patterns (Not Recommended)

### Switch Statement

```go
func (t *myTool) Execute(ctx context.Context, params *MyParams) (string, error) {
    switch params.Operation {
    case "operation1":
        return t.handleOperation1(ctx, params)
    case "operation2":
        return t.handleOperation2(ctx, params)
    default:
        return "", fmt.Errorf("unknown operation: %s", params.Operation)
    }
}
```

**Why avoid**: Becomes unwieldy with many operations, harder to test, less flexible.

### Reflection-Based Auto-Discovery

```go
// Automatically find all methods starting with "Op"
func (t *myTool) Execute(ctx context.Context, params *MyParams) (string, error) {
    methodName := "Op" + strings.Title(params.Operation)
    method := reflect.ValueOf(t).MethodByName(methodName)
    // ... call via reflection
}
```

**Why avoid**:
- Hard to understand (magic behavior)
- Slower (reflection overhead)
- Breaks cross-language consistency
- Python/JavaScript developers won't understand it

## Best Practices

### 1. Keep Handlers Simple

Each handler should do one thing:
```go
func handleAdd(m *mathTool, params *MathParams) (string, error) {
    result := params.A + params.B
    return fmt.Sprintf("%g", result), nil
}
```

### 2. Use Descriptive Names

```go
// ✅ Good
"list_scripts": handleListScripts,
"run_script":   handleRunScript,

// ❌ Avoid
"ls":  handleList,
"run": handleRun,
```

### 3. Return Structured Results When Appropriate

For complex data, use structured results:
```go
func handleList(t *myTool, params *MyParams) (string, error) {
    items := getItems()
    return pluginapi.NewTableResult(items), nil  // Returns formatted table
}
```

### 4. Validate Early

```go
func handleDivide(m *mathTool, params *MathParams) (string, error) {
    if params.B == 0 {
        return "", errors.New("division by zero")  // Fail early
    }
    result := params.A / params.B
    return fmt.Sprintf("%g", result), nil
}
```

### 5. Keep Registry at Package Level

```go
// ✅ Good - registry at package level
var operationRegistry = map[string]OperationHandler{
    "op1": handleOp1,
}

// ❌ Avoid - registry inside Execute
func (t *myTool) Execute(ctx context.Context, params *MyParams) (string, error) {
    registry := map[string]OperationHandler{  // Created on every call
        "op1": handleOp1,
    }
    // ...
}
```

## Summary

The operation dispatch pattern is:
- **5 lines** of boilerplate
- **Same in every language**
- **Easy to understand**
- **Simple to maintain**

When adding Python/JavaScript plugin support, this same pattern will work identically, making it easy for developers familiar with any language to create plugins.
