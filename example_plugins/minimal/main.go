package main

//go:generate ../../bin/ori-plugin-gen -yaml=plugin.yaml -output=minimal_generated.go

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// MinimalPluginTool demonstrates the optimized plugin development experience
// Most boilerplate is now auto-generated in minimal_generated.go
type MinimalPluginTool struct {
	pluginapi.BasePlugin
}

// --- Handler functions (the only code you need to write) ---
// The naming convention handle{PascalCase} is auto-wired by the generator

func handleEcho(ctx context.Context, t *MinimalPluginTool, params *Params) (string, error) {
	if params.Message == "" {
		return "", fmt.Errorf("message is required for echo operation")
	}

	// Use Settings API to check debug mode
	sm := t.Settings()
	if sm != nil {
		debugMode, _ := sm.GetBool("debug_mode")
		if debugMode {
			_ = sm.Set("last_operation", "echo")
			_ = sm.Set("last_message", params.Message)
		}
	}

	// Default count to 1 if not specified
	count := params.Count
	if count == 0 {
		count = 1
	}

	// Repeat the message
	repeated := strings.Repeat(params.Message+" ", count)
	return strings.TrimSpace(repeated), nil
}

func handleStatus(ctx context.Context, t *MinimalPluginTool, params *Params) (string, error) {
	sm := t.Settings()
	if sm == nil {
		return "Settings API not available (no agent context)", nil
	}

	// Get configuration values
	apiEndpoint, _ := sm.GetString("api_endpoint")
	timeoutSeconds, _ := sm.GetInt("timeout_seconds")
	debugMode, _ := sm.GetBool("debug_mode")
	allSettings, _ := sm.GetAll()

	status := fmt.Sprintf(`Plugin Status:
- API Endpoint: %s
- Timeout: %d seconds
- Debug Mode: %v
- Total Settings: %d

Recent Activity:`, apiEndpoint, timeoutSeconds, debugMode, len(allSettings))

	// Show last operation if available
	lastOp, _ := sm.GetString("last_operation")
	if lastOp != "" {
		lastMsg, _ := sm.GetString("last_message")
		status += fmt.Sprintf("\n- Last Operation: %s", lastOp)
		status += fmt.Sprintf("\n- Last Message: %s", lastMsg)
	}

	return status, nil
}

func main() {
	pluginapi.ServePlugin(&MinimalPluginTool{}, configYAML)
}
