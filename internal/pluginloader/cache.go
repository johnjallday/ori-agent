package pluginloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// GetPluginVersion extracts version information from a plugin tool.
// Returns empty string if the plugin doesn't implement VersionedTool.
func GetPluginVersion(tool pluginapi.Tool) string {
	if versionedTool, ok := tool.(pluginapi.VersionedTool); ok {
		return versionedTool.Version()
	}
	return ""
}

// SetAgentContext sets the agent context for plugins that support it.
func SetAgentContext(tool pluginapi.Tool, agentName, agentStorePath string) {
	if agentAwareTool, ok := tool.(pluginapi.AgentAwareTool); ok {
		// The agentStorePath is already something like "agents/default/config.json"
		// So we just need to get the agent directory from it
		agentDir := filepath.Dir(agentStorePath)
		configPath := filepath.Join(agentDir, "config.json")
		settingsPath := filepath.Join(agentDir, "agent_settings.json")


		agentAwareTool.SetAgentContext(pluginapi.AgentContext{
			Name:         agentName,
			ConfigPath:   configPath,
			SettingsPath: settingsPath,
			AgentDir:     agentDir,
		})
	}
}

// ExtractPluginSettingsSchema checks if a plugin provides default settings and creates initial settings file.
// If the plugin implements DefaultSettingsProvider, writes default settings to the agent settings file.
func ExtractPluginSettingsSchema(tool pluginapi.Tool, agentName string) error {
	// Get plugin definition
	def := tool.Definition()

	// First, check if plugin implements DefaultSettingsProvider interface
	if defaultProvider, ok := tool.(pluginapi.DefaultSettingsProvider); ok {
		// Get default settings from the interface method
		defaultSettings, err := defaultProvider.GetDefaultSettings()
		if err != nil {
			return fmt.Errorf("failed to get default settings: %w", err)
		}

		// Create agent directory if it doesn't exist
		agentDir := filepath.Join("agents", agentName)
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			return fmt.Errorf("failed to create agent directory: %w", err)
		}

		// Write the default settings to {plugin_name}_settings.json
		settingsFileName := fmt.Sprintf("%s_settings.json", def.Name)
		settingsPath := filepath.Join(agentDir, settingsFileName)

		// Only create the file if it doesn't exist (don't overwrite existing settings)
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			if err := os.WriteFile(settingsPath, []byte(defaultSettings), 0644); err != nil {
				return fmt.Errorf("failed to write plugin default settings: %w", err)
			}
		}
		return nil
	}

	// Fallback: Check if plugin supports get_settings operation (legacy method)
	if params, ok := def.Parameters["properties"].(map[string]any); ok {
		if operation, ok := params["operation"].(map[string]any); ok {
			if enum, ok := operation["enum"].([]string); ok {
				supportsGetSettings := false
				for _, op := range enum {
					if op == "get_settings" {
						supportsGetSettings = true
						break
					}
				}

				if supportsGetSettings {
					// Call get_settings to get the schema (legacy - returns field types)
					result, err := tool.Call(context.Background(), `{"operation": "get_settings"}`)
					if err != nil {
						return fmt.Errorf("failed to call get_settings: %w", err)
					}

					// Create agent directory if it doesn't exist
					agentDir := filepath.Join("agents", agentName)
					if err := os.MkdirAll(agentDir, 0755); err != nil {
						return fmt.Errorf("failed to create agent directory: %w", err)
					}

					// Write the settings schema to {plugin_name}_settings.json (legacy - field types only)
					settingsFileName := fmt.Sprintf("%s_settings.json", def.Name)
					settingsPath := filepath.Join(agentDir, settingsFileName)

					// Only create the file if it doesn't exist (don't overwrite existing settings)
					if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
						if err := os.WriteFile(settingsPath, []byte(result), 0644); err != nil {
							return fmt.Errorf("failed to write plugin settings: %w", err)
						}
						fmt.Printf("Extracted settings schema for plugin %s to %s\n", def.Name, settingsPath)
					}
				}
			}
		}
	}

	return nil
}