package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/go-plugin"
	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// minimalTool demonstrates the optimized plugin development experience
type minimalTool struct {
	pluginapi.BasePlugin
}

// Ensure compile-time interface conformance
var _ pluginapi.PluginTool = (*minimalTool)(nil)
var _ pluginapi.VersionedTool = (*minimalTool)(nil)
var _ pluginapi.AgentAwareTool = (*minimalTool)(nil)
var _ pluginapi.InitializationProvider = (*minimalTool)(nil)

// Definition returns the tool definition from plugin.yaml
// This is simplified - no manual parameter building!
func (t *minimalTool) Definition() pluginapi.Tool {
	tool, err := t.GetToolDefinition()
	if err != nil {
		// Fallback to basic definition if YAML parsing fails
		return pluginapi.Tool{
			Name:        "minimal-plugin",
			Description: "Minimal example plugin",
			Parameters:  map[string]interface{}{},
		}
	}
	return tool
}

// Call executes the tool with the given arguments
func (t *minimalTool) Call(ctx context.Context, args string) (string, error) {
	var params struct {
		Operation string `json:"operation"`
		Message   string `json:"message"`
		Count     int    `json:"count"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch params.Operation {
	case "echo":
		return t.handleEcho(params.Message, params.Count)
	case "status":
		return t.handleStatus()
	default:
		return "", fmt.Errorf("unknown operation: %s", params.Operation)
	}
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
	// Parse plugin config from embedded YAML
	config := pluginapi.ReadPluginConfig(configYAML)

	// Create plugin instance
	tool := &minimalTool{
		BasePlugin: pluginapi.NewBasePlugin(
			"minimal-plugin",
			config.Version,
			config.Requirements.MinOriVersion,
			"",
			"v1",
		),
	}

	// Set plugin config for YAML-based features
	tool.SetPluginConfig(&config)

	// Set metadata from config
	if metadata, err := config.ToMetadata(); err == nil {
		tool.SetMetadata(metadata)
	}

	// Serve the plugin
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pluginapi.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &pluginapi.ToolRPCPlugin{Impl: tool},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
