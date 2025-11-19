package pluginapi

import (
	"fmt"
	"sync"
)

// BasePlugin provides default implementations for common plugin interfaces.
// Plugins can embed this struct to avoid implementing boilerplate getter methods.
//
// Example usage:
//
//	type myTool struct {
//	    pluginapi.BasePlugin
//	    // your plugin fields
//	}
//
//	func NewMyTool() *myTool {
//	    return &myTool{
//	        BasePlugin: pluginapi.NewBasePlugin("my-tool", "1.0.0", "0.0.6", "", "v1"),
//	    }
//	}
type BasePlugin struct {
	version         string
	minAgentVer     string
	maxAgentVer     string
	apiVersion      string
	metadata        *PluginMetadata
	agentContext    AgentContext
	defaultSettings string
	pluginConfig    *PluginConfig   // Stores parsed plugin.yaml config
	settingsManager SettingsManager // Lazy-initialized settings manager
	settingsMu      sync.Mutex      // Mutex for settings initialization
}

// NewBasePlugin creates a new base plugin with version and compatibility info.
//
// Parameters:
//   - name: Plugin name (e.g., "weather", "math")
//   - version: Plugin version (e.g., "1.0.0", "0.0.5")
//   - minAgentVersion: Minimum ori-agent version required (e.g., "0.0.6"), empty string for no minimum
//   - maxAgentVersion: Maximum ori-agent version supported (e.g., "1.0.0"), empty string for no maximum
//   - apiVersion: API version implemented (e.g., "v1")
func NewBasePlugin(name, version, minAgentVersion, maxAgentVersion, apiVersion string) BasePlugin {
	return BasePlugin{
		version:     version,
		minAgentVer: minAgentVersion,
		maxAgentVer: maxAgentVersion,
		apiVersion:  apiVersion,
	}
}

// Version returns the plugin version.
// Implements VersionedTool and PluginCompatibility interfaces.
func (b *BasePlugin) Version() string {
	return b.version
}

// MinAgentVersion returns the minimum compatible agent version.
// Implements PluginCompatibility interface.
func (b *BasePlugin) MinAgentVersion() string {
	return b.minAgentVer
}

// MaxAgentVersion returns the maximum compatible agent version (empty = no limit).
// Implements PluginCompatibility interface.
func (b *BasePlugin) MaxAgentVersion() string {
	return b.maxAgentVer
}

// APIVersion returns the API version this plugin implements.
// Implements PluginCompatibility interface.
func (b *BasePlugin) APIVersion() string {
	return b.apiVersion
}

// SetAgentContext stores the agent context for later use.
// Implements AgentAwareTool interface.
func (b *BasePlugin) SetAgentContext(ctx AgentContext) {
	b.agentContext = ctx
}

// GetAgentContext returns the stored agent context.
// This is a convenience method for plugins to access their context.
func (b *BasePlugin) GetAgentContext() AgentContext {
	return b.agentContext
}

// SetMetadata sets the plugin metadata.
// Call this in your plugin's constructor to enable GetMetadata().
func (b *BasePlugin) SetMetadata(metadata *PluginMetadata) {
	b.metadata = metadata
}

// GetMetadata returns the plugin metadata.
// Implements MetadataProvider interface.
// Returns nil if metadata was not set via SetMetadata().
func (b *BasePlugin) GetMetadata() (*PluginMetadata, error) {
	return b.metadata, nil
}

// SetDefaultSettings sets the default settings JSON string.
// Call this in your plugin's constructor to enable GetDefaultSettings().
func (b *BasePlugin) SetDefaultSettings(settings string) {
	b.defaultSettings = settings
}

// GetDefaultSettings returns the default settings JSON string.
// Implements DefaultSettingsProvider interface.
// Returns empty string if not set via SetDefaultSettings().
func (b *BasePlugin) GetDefaultSettings() (string, error) {
	return b.defaultSettings, nil
}

// SetPluginConfig sets the parsed plugin.yaml configuration.
// Call this in your plugin's constructor to enable GetConfigFromYAML().
func (b *BasePlugin) SetPluginConfig(config *PluginConfig) {
	b.pluginConfig = config
}

// GetConfigFromYAML returns config variables defined in plugin.yaml.
// Returns empty slice if no config section exists in plugin.yaml.
// Template variables ({{USER_HOME}}, {{OS}}, {{ARCH}}) are automatically expanded.
// Platform-specific defaults are applied based on runtime.GOOS.
//
// This method is useful for implementing hybrid config systems where:
// 1. Simple, static config is defined in plugin.yaml
// 2. Complex, dynamic logic is added in GetRequiredConfig()
//
// Example usage in a plugin:
//
//	func (t *myTool) GetRequiredConfig() []pluginapi.ConfigVariable {
//	    // Start with YAML config
//	    vars := t.BasePlugin.GetConfigFromYAML()
//
//	    // Add dynamic logic
//	    if needsExtraConfig() {
//	        vars = append(vars, pluginapi.ConfigVariable{...})
//	    }
//
//	    return vars
//	}
func (b *BasePlugin) GetConfigFromYAML() []ConfigVariable {
	if b.pluginConfig == nil {
		return []ConfigVariable{}
	}
	return b.pluginConfig.ToConfigVariables()
}

// Settings returns the settings manager for this plugin.
// The settings manager is lazily initialized when first accessed.
// This method is thread-safe and can be called multiple times.
//
// Returns nil if the agent context has not been set yet.
// Call this method only after SetAgentContext has been called.
//
// Example usage:
//
//	func (t *myTool) Call(ctx context.Context, args string) (string, error) {
//	    settings := t.Settings()
//	    if settings == nil {
//	        return "", fmt.Errorf("plugin not initialized with agent context")
//	    }
//
//	    apiKey, err := settings.GetString("api_key")
//	    if err != nil {
//	        return "", err
//	    }
//	    // Use apiKey...
//	}
func (b *BasePlugin) Settings() SettingsManager {
	b.settingsMu.Lock()
	defer b.settingsMu.Unlock()

	// Return existing settings manager if already initialized
	if b.settingsManager != nil {
		return b.settingsManager
	}

	// Cannot initialize without agent context
	if b.agentContext.AgentDir == "" {
		return nil
	}

	// Extract plugin name from metadata or use a default
	pluginName := "unknown"
	if b.metadata != nil && b.metadata.Name != "" {
		pluginName = b.metadata.Name
	}

	// Lazy initialize the settings manager
	sm, err := NewSettingsManager(b.agentContext.AgentDir, pluginName)
	if err != nil {
		// Log error but return nil - caller should handle this
		// TODO: Consider adding logging here
		return nil
	}

	b.settingsManager = sm
	return b.settingsManager
}

// GetToolDefinition returns the tool definition from plugin.yaml if available.
// This method allows plugins to define their tool interface in YAML instead of code.
// Returns an error if no tool definition is found in the plugin config.
//
// Example usage in a plugin:
//
//	func (t *myTool) Definition() pluginapi.Tool {
//	    // Try to get definition from YAML
//	    tool, err := t.BasePlugin.GetToolDefinition()
//	    if err == nil {
//	        return tool
//	    }
//
//	    // Fallback to code-defined definition
//	    return pluginapi.NewTool("my-tool", "Does something", map[string]interface{}{...})
//	}
func (b *BasePlugin) GetToolDefinition() (Tool, error) {
	if b.pluginConfig == nil {
		return Tool{}, fmt.Errorf("plugin config not set")
	}

	if b.pluginConfig.Tool == nil {
		return Tool{}, fmt.Errorf("no tool definition in plugin.yaml")
	}

	// Convert YAML tool definition to pluginapi.Tool
	return b.pluginConfig.Tool.ToToolDefinition()
}
