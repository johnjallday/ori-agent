package pluginapi

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
