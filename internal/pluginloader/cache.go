package pluginloader

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/logger"
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

// mapConfigTypeToFrontendType converts ConfigVariableType to frontend-compatible type strings
func mapConfigTypeToFrontendType(configType pluginapi.ConfigVariableType) string {
	switch configType {
	case pluginapi.ConfigTypeString:
		return "string"
	case pluginapi.ConfigTypeInt:
		return "int"
	case pluginapi.ConfigTypeFloat:
		return "float"
	case pluginapi.ConfigTypeBool:
		return "bool"
	case pluginapi.ConfigTypeFilePath:
		return "filepath"
	case pluginapi.ConfigTypeDirPath:
		return "filepath" // Frontend uses "filepath" for both files and directories
	case pluginapi.ConfigTypePassword:
		return "password"
	case pluginapi.ConfigTypeURL:
		return "string" // Frontend treats URLs as strings
	case pluginapi.ConfigTypeEmail:
		return "string" // Frontend treats emails as strings
	default:
		return "string" // Default to string for unknown types
	}
}

// SetAgentContext sets the agent context for plugins that support it.
func SetAgentContext(tool pluginapi.Tool, agentName, agentStorePath, currentLocation string) {
	if agentAwareTool, ok := tool.(pluginapi.AgentAwareTool); ok {
		// The agentStorePath is already something like "agents/default/config.json"
		// So we just need to get the agent directory from it
		agentDir := filepath.Dir(agentStorePath)
		configPath := filepath.Join(agentDir, "config.json")
		settingsPath := filepath.Join(agentDir, "agent_settings.json")

		agentAwareTool.SetAgentContext(pluginapi.AgentContext{
			Name:            agentName,
			ConfigPath:      configPath,
			SettingsPath:    settingsPath,
			AgentDir:        agentDir,
			CurrentLocation: currentLocation,
		})
	}
}

// ExtractPluginSettingsSchema checks if a plugin provides default settings and creates initial settings file.
// Priority order:
// 1. InitializationProvider (modern, declarative)
// 2. DefaultSettingsProvider (simple defaults)
func ExtractPluginSettingsSchema(tool pluginapi.Tool, agentName string) error {
	// Get plugin definition
	def := tool.Definition()

	// First priority: Check if plugin implements InitializationProvider (modern approach)
	if initProvider, ok := tool.(pluginapi.InitializationProvider); ok {
		// Get required config variables from the plugin
		configVars := initProvider.GetRequiredConfig()

		// Convert ConfigVariables to field types map for frontend compatibility
		fieldTypes := make(map[string]string)
		defaultValues := make(map[string]interface{})

		for _, cv := range configVars {
			// Map ConfigVariableType to frontend-compatible field type string
			fieldTypes[cv.Key] = mapConfigTypeToFrontendType(cv.Type)

			// Include default value if provided
			if cv.DefaultValue != nil {
				defaultValues[cv.Key] = cv.DefaultValue
			}
		}

		// Create agent directory if it doesn't exist
		agentDir := filepath.Join("agents", agentName)
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			return fmt.Errorf("failed to create agent directory: %w", err)
		}

		// Write the settings schema to {plugin_name}_settings.json
		settingsFileName := fmt.Sprintf("%s_settings.json", def.Name)
		settingsPath := filepath.Join(agentDir, settingsFileName)

		// Only create the file if it doesn't exist (don't overwrite existing settings)
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			// If we have default values, use them; otherwise just use field types
			var settingsData []byte
			var err error

			if len(defaultValues) > 0 {
				settingsData, err = json.MarshalIndent(defaultValues, "", "  ")
			} else {
				settingsData, err = json.MarshalIndent(fieldTypes, "", "  ")
			}

			if err != nil {
				return fmt.Errorf("failed to marshal settings schema: %w", err)
			}

			if err := os.WriteFile(settingsPath, settingsData, 0644); err != nil {
				return fmt.Errorf("failed to write plugin settings: %w", err)
			}
			logger.Verbosef("✅ Extracted settings schema for plugin %s using InitializationProvider", def.Name)
		}
		return nil
	}

	// Second priority: Check if plugin implements DefaultSettingsProvider interface
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

	// No configuration methods available
	return nil
}
