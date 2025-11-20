package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// minimalTool demonstrates the optimized plugin development experience
type minimalTool struct {
	pluginapi.BasePlugin
}

// OperationParams holds all possible parameters for operations
type OperationParams struct {
	Operation string `json:"operation"`
	Message   string `json:"message"`
	Count     int    `json:"count"`
}

// OperationHandler is a function that handles a specific operation
type OperationHandler func(t *minimalTool, params *OperationParams) (string, error)

// operationRegistry maps operation names to their handler functions
var operationRegistry = map[string]OperationHandler{
	"echo":   handleEcho,
	"status": handleStatus,
}

// Ensure compile-time interface conformance
var _ pluginapi.PluginTool = (*minimalTool)(nil)
var _ pluginapi.VersionedTool = (*minimalTool)(nil)
var _ pluginapi.AgentAwareTool = (*minimalTool)(nil)
var _ pluginapi.InitializationProvider = (*minimalTool)(nil)

// Note: Definition() is inherited from BasePlugin, which automatically reads from plugin.yaml

// Call executes the tool with the given arguments
func (t *minimalTool) Call(ctx context.Context, args string) (string, error) {
	var params OperationParams
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Look up handler in registry
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s. Valid operations: echo, status", params.Operation)
	}

	// Execute the handler
	return handler(t, &params)
}

// Operation handlers

func handleEcho(t *minimalTool, params *OperationParams) (string, error) {
	return t.handleEcho(params.Message, params.Count)
}

func handleStatus(t *minimalTool, params *OperationParams) (string, error) {
	return t.handleStatus()
}

// handleEcho demonstrates using the Settings API
func (t *minimalTool) handleEcho(message string, count int) (string, error) {
	if message == "" {
		return "", fmt.Errorf("message is required for echo operation")
	}

	// Use Settings API to check debug mode
	sm := t.Settings()
	if sm != nil {
		debugMode, _ := sm.GetBool("debug_mode")
		if debugMode {
			// Store some debug information
			sm.Set("last_operation", "echo")
			sm.Set("last_message", message)
		}
	}

	// Default count to 1 if not specified
	if count == 0 {
		count = 1
	}

	// Repeat the message
	repeated := strings.Repeat(message+" ", count)
	return strings.TrimSpace(repeated), nil
}

// handleStatus demonstrates reading from Settings API
func (t *minimalTool) handleStatus() (string, error) {
	sm := t.Settings()
	if sm == nil {
		return "Settings API not available (no agent context)", nil
	}

	// Get configuration values
	apiEndpoint, _ := sm.GetString("api_endpoint")
	timeoutSeconds, _ := sm.GetInt("timeout_seconds")
	debugMode, _ := sm.GetBool("debug_mode")

	// Get all settings
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

// GetRequiredConfig returns configuration requirements from plugin.yaml
func (t *minimalTool) GetRequiredConfig() []pluginapi.ConfigVariable {
	return t.GetConfigFromYAML()
}

// ValidateConfig validates the provided configuration
func (t *minimalTool) ValidateConfig(config map[string]interface{}) error {
	// api_endpoint is required
	endpoint, ok := config["api_endpoint"].(string)
	if !ok || endpoint == "" {
		return fmt.Errorf("api_endpoint is required")
	}

	// Validate timeout if provided
	if timeout, ok := config["timeout_seconds"]; ok {
		var timeoutInt int
		switch v := timeout.(type) {
		case float64:
			timeoutInt = int(v)
		case int:
			timeoutInt = v
		default:
			return fmt.Errorf("timeout_seconds must be a number")
		}

		if timeoutInt < 1 || timeoutInt > 300 {
			return fmt.Errorf("timeout_seconds must be between 1 and 300")
		}
	}

	return nil
}

// InitializeWithConfig stores the configuration using Settings API
func (t *minimalTool) InitializeWithConfig(config map[string]interface{}) error {
	sm := t.Settings()
	if sm == nil {
		return fmt.Errorf("settings manager not available")
	}

	// Store all configuration values
	for key, value := range config {
		if err := sm.Set(key, value); err != nil {
			return fmt.Errorf("failed to store config %s: %w", key, err)
		}
	}

	return nil
}

func main() {
	pluginapi.ServePlugin(&minimalTool{}, configYAML)
}
