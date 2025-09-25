package pluginloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"strings"
	"sync"

	"github.com/johnjallday/dolphin-agent/pluginapi"
)

// Global plugin cache to handle "plugin already loaded" errors from Go's plugin system
var (
	cache   = make(map[string]pluginapi.Tool)
	cacheMu sync.RWMutex
	
	// Track plugins by their definition name as well for cross-referencing
	nameToPathCache = make(map[string]string)
	nameToPathMu    sync.RWMutex
)

// LoadWithCache loads a plugin from the given path, using a global cache
// to handle "plugin already loaded" errors from Go's plugin system.
func LoadWithCache(path string) (pluginapi.Tool, error) {
	// Get absolute path for cache key
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path // fallback to original path
	}
	
	// Check cache first
	cacheMu.RLock()
	if tool, exists := cache[absPath]; exists {
		cacheMu.RUnlock()
		return tool, nil
	}
	cacheMu.RUnlock()
	
	// Load plugin
	p, err := plugin.Open(path)
	if err != nil {
		// If plugin is already loaded by Go runtime, try to find it in our cache
		if strings.Contains(err.Error(), "plugin already loaded") {
			// Search through all cached plugins to find one from this path
			cacheMu.RLock()
			// First try exact path match
			if tool, exists := cache[absPath]; exists {
				cacheMu.RUnlock()
				return tool, nil
			}
			
			// If not found in cache by path, search for any plugin from a similar path
			// Check if any cached plugin has the same base filename
			pathBase := filepath.Base(absPath)
			var foundTool pluginapi.Tool
			for cachedPath, tool := range cache {
				if filepath.Base(cachedPath) == pathBase {
					// Found a plugin with the same filename, reuse it
					foundTool = tool
					break
				}
			}
			cacheMu.RUnlock()
			
			if foundTool != nil {
				// Also cache it under the new path for future lookups
				cacheMu.Lock()
				cache[absPath] = foundTool
				cacheMu.Unlock()
				return foundTool, nil
			}
			
			// If still not found, the plugin might be loaded but from a different session
			return nil, errors.New("plugin file already loaded in Go runtime but not accessible. Restart the server to clear plugin state.")
		}
		return nil, err
	}
	sym, err := p.Lookup("Tool")
	if err != nil {
		return nil, err
	}
	tool, ok := sym.(pluginapi.Tool)
	if !ok {
		return nil, errors.New("invalid plugin type: symbol Tool does not implement pluginapi.Tool")
	}
	
	// Cache the loaded plugin
	cacheMu.Lock()
	cache[absPath] = tool
	cacheMu.Unlock()
	
	// Also cache the name-to-path mapping for cross-referencing
	def := tool.Definition()
	nameToPathMu.Lock()
	nameToPathCache[def.Name] = absPath
	nameToPathMu.Unlock()
	
	return tool, nil
}

// AddToCache manually adds a plugin tool to the cache for a given path.
// This is useful for pre-populating the cache with already loaded plugins.
func AddToCache(absPath string, tool pluginapi.Tool) {
	cacheMu.Lock()
	cache[absPath] = tool
	cacheMu.Unlock()
	
	// Also cache the name-to-path mapping
	def := tool.Definition()
	nameToPathMu.Lock()
	nameToPathCache[def.Name] = absPath
	nameToPathMu.Unlock()
}

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