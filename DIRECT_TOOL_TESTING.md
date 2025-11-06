# Direct Tool Launch Feature - Testing Guide

## Overview
The direct tool launch feature allows users to execute tools directly without LLM decision-making, providing faster execution, no API costs, and deterministic results.

## Feature Summary
- **Command syntax**: `/tool <tool_name> <json_args>`
- **Bypasses LLM**: No API calls to OpenAI/Claude/Ollama
- **Fast execution**: Direct tool invocation
- **Cost-free**: No LLM API costs
- **Deterministic**: Exact tool selection

## Files Modified/Created

### New Files
1. **internal/chathttp/direct_tool_executor.go**
   - Core logic for parsing and executing direct tool commands
   - Functions: `parseDirectToolCommand()`, `executeDirectTool()`, `getAvailableToolNames()`, `formatDirectToolResponse()`

2. **internal/chathttp/direct_tool_executor_test.go**
   - Unit tests for parsing, execution, and formatting
   - Mock tools for testing

3. **DIRECT_TOOL_TESTING.md** (this file)
   - Testing documentation

### Modified Files
1. **internal/chathttp/handlers.go** (line ~700)
   - Added detection and routing for `/tool` commands
   - Integrated direct tool execution into ChatHandler

2. **internal/chathttp/commands.go** (line ~346)
   - Updated `/help` command documentation
   - Added direct tool execution section

## Manual Testing Instructions

### Prerequisites
1. Build the server: `./scripts/build.sh` or `go build -o bin/ori-agent ./cmd/server`
2. Ensure you have at least one plugin loaded (e.g., math plugin)
3. Start the server: `./bin/ori-agent` or `go run ./cmd/server`

### Test Cases

#### 1. Basic Math Operations
```bash
# Test addition
/tool math {"operation": "add", "a": 5, "b": 3}
Expected: "8"

# Test multiplication
/tool math {"operation": "multiply", "a": 7, "b": 6}
Expected: "42"

# Test subtraction
/tool math {"operation": "subtract", "a": 10, "b": 4}
Expected: "6"

# Test division
/tool math {"operation": "divide", "a": 20, "b": 5}
Expected: "4"
```

#### 2. Error Handling
```bash
# Tool not found
/tool nonexistent {"param": "value"}
Expected: Error message with available tools list

# Invalid JSON
/tool math {invalid json}
Expected: Parsing error with format help

# Missing tool name
/tool {"operation": "add"}
Expected: Error about missing tool name

# Missing arguments
/tool math
Expected: Error about missing JSON arguments
```

#### 3. Help Command
```bash
# View help with new /tool command
/help
Expected: Help message including /tool command documentation
```

#### 4. Tool Listing
```bash
# List available tools
/tools
Expected: List of available tools with their parameters
```

#### 5. Verify Response Format
Check that direct tool responses include:
- `response`: The tool result
- `direct_tool_call`: true
- `tool_name`: Name of the executed tool
- `execution_time_ms`: Execution duration
- `success`: Boolean indicating success/failure

#### 6. Test with Other Plugins
If you have other plugins loaded (weather, custom plugins, etc.):
```bash
# Example with weather plugin (if available)
/tool weather {"city": "San Francisco"}

# Example with custom plugin
/tool your_plugin_name {"param1": "value1", "param2": "value2"}
```

### Expected Behavior

#### Success Case
- Tool executes immediately (no LLM roundtrip)
- Result returned in < 100ms (typically < 50ms for simple operations)
- Response includes execution metadata
- Conversation history updated with command and result

#### Error Cases
- **Tool not found**: Helpful error with list of available tools
- **Invalid JSON**: Clear error message with format example
- **Tool execution error**: Error message with details, but no server crash
- **Timeout**: Graceful handling with timeout message

### Performance Comparison

#### Traditional Chat (with LLM):
1. User sends: "Add 5 and 3"
2. LLM API call (~1-3 seconds)
3. LLM decides to use math tool
4. Tool execution (~5ms)
5. LLM API call for response (~1-3 seconds)
6. **Total: 2-6 seconds**

#### Direct Tool Call:
1. User sends: `/tool math {"operation": "add", "a": 5, "b": 3}`
2. Tool execution (~5ms)
3. **Total: < 100ms**

### Automated Testing

Run unit tests:
```bash
# Test parsing
go test ./internal/chathttp/... -v -run TestParseDirectToolCommand

# Test execution
go test ./internal/chathttp/... -v -run TestExecuteDirectTool

# Test formatting
go test ./internal/chathttp/... -v -run TestFormatDirectToolResponse

# Run all chathttp tests
go test ./internal/chathttp/... -v
```

### Integration Testing

1. **Start server in one terminal:**
   ```bash
   go run ./cmd/server
   ```

2. **Test via web UI:**
   - Open http://localhost:8080
   - Type `/tool math {"operation": "add", "a": 5, "b": 3}` in chat
   - Verify instant response

3. **Test via API:**
   ```bash
   curl -X POST http://localhost:8080/api/chat \
     -H "Content-Type: application/json" \
     -d '{"question": "/tool math {\"operation\": \"add\", \"a\": 5, \"b\": 3}"}'
   ```

### Troubleshooting

#### Issue: Command not recognized
- **Cause**: Might be using wrong syntax
- **Solution**: Ensure format is exactly `/tool <name> <json>`

#### Issue: Tool not found
- **Cause**: Plugin not loaded or wrong tool name
- **Solution**: Run `/tools` to see available tools

#### Issue: Invalid JSON error
- **Cause**: JSON syntax error in arguments
- **Solution**: Use online JSON validator or check quotes

#### Issue: Execution timeout
- **Cause**: Tool taking too long (>30 seconds)
- **Solution**: Check tool implementation, reduce timeout if needed

## Benefits Summary

1. **Speed**: 50-100x faster than LLM-based tool calling
2. **Cost**: $0 per call (vs. $0.001-0.01 for LLM calls)
3. **Determinism**: Always calls exact tool specified
4. **Debugging**: Easy to test plugins directly
5. **Offline**: Works even if LLM API is down

## Future Enhancements

Potential improvements:
1. **Tab completion**: Autocomplete tool names in UI
2. **Parameter hints**: Show expected parameters for each tool
3. **History**: Track frequently used direct tool calls
4. **Aliases**: Create shortcuts like `/math add 5 3`
5. **Batch calls**: Execute multiple tools in sequence
6. **JSON schema validation**: Validate args against tool schema

## Feedback

If you encounter issues or have suggestions:
1. Check server logs for detailed error messages
2. Verify plugin is properly loaded with `/tools`
3. Test with simple math operations first
4. Report issues with full command and error message
