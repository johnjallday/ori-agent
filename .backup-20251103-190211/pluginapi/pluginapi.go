package pluginapi

import (
	"context"
	"github.com/openai/openai-go/v2"
)

// Tool is the interface that plugins must implement to be used as tools.
type Tool interface {
	// Definition returns the function definition for OpenAI function calling.
	Definition() openai.FunctionDefinitionParam
	// Call executes the tool logic with the given arguments JSON string and returns the result JSON string.
	Call(ctx context.Context, args string) (string, error)
}

// VersionedTool extends Tool with version information.
// Plugins can optionally implement this interface to provide version info.
type VersionedTool interface {
	Tool
	// Version returns the plugin version (e.g., "1.0.0", "1.2.3-beta")
	Version() string
}

// PluginCompatibility extends Tool with detailed version compatibility information.
// Plugins should implement this interface to enable health checking and compatibility validation.
// Note: This was renamed from PluginMetadata to avoid conflict with the proto-generated struct.
type PluginCompatibility interface {
	Tool
	// Version returns the plugin version (e.g., "0.0.5", "1.2.3-beta")
	Version() string
	// MinAgentVersion returns the minimum ori-agent version required (e.g., "0.0.6")
	// Return empty string if no minimum requirement
	MinAgentVersion() string
	// MaxAgentVersion returns the maximum compatible ori-agent version (e.g., "1.0.0")
	// Return empty string if no maximum limit
	MaxAgentVersion() string
	// APIVersion returns the plugin API version (e.g., "v1", "v2")
	// This should match the agent's API version for compatibility
	APIVersion() string
}

// HealthCheckProvider allows plugins to implement custom health checks.
// Plugins can optionally implement this to validate their runtime state.
type HealthCheckProvider interface {
	// HealthCheck performs a plugin-specific health check
	// Return nil if healthy, error with description if unhealthy
	HealthCheck() error
}

// DefaultSettingsProvider allows plugins to provide default configuration values.
// This is useful for plugins that need default file paths or configuration.
type DefaultSettingsProvider interface {
	// GetDefaultSettings returns default settings as a JSON string
	GetDefaultSettings() (string, error)
}


// AgentContext provides information about the current agent to plugins.
type AgentContext struct {
	// Name is the name of the current agent (e.g., "reaper-project-manager", "default")
	Name string
	// ConfigPath is the path to the agent's main config file (agents/{name}/config.json)
	ConfigPath string
	// SettingsPath is the path to the agent's settings file (agents/{name}/agent_settings.json)
	SettingsPath string
	// AgentDir is the path to the agent's directory (agents/{name}/)
	AgentDir string
}

// AgentAwareTool extends Tool with agent context information.
// Plugins can optionally implement this interface to receive current agent info.
type AgentAwareTool interface {
	Tool
	// SetAgentContext provides the current agent information to the plugin
	SetAgentContext(ctx AgentContext)
}

// ConfigVariableType represents the type of a configuration variable.
type ConfigVariableType string

const (
	ConfigTypeString   ConfigVariableType = "string"
	ConfigTypeInt      ConfigVariableType = "int"
	ConfigTypeFloat    ConfigVariableType = "float"
	ConfigTypeBool     ConfigVariableType = "bool"
	ConfigTypeFilePath ConfigVariableType = "filepath"
	ConfigTypeDirPath  ConfigVariableType = "dirpath"
	ConfigTypePassword ConfigVariableType = "password"
	ConfigTypeURL      ConfigVariableType = "url"
	ConfigTypeEmail    ConfigVariableType = "email"
)

// ConfigVariable describes a configuration variable that the plugin requires.
type ConfigVariable struct {
	// Key is the configuration key (e.g., "api_key", "base_url", "project_path")
	Key string `json:"key"`
	// Name is the human-readable name for the variable
	Name string `json:"name"`
	// Description explains what this variable is used for
	Description string `json:"description"`
	// Type specifies the data type and input method
	Type ConfigVariableType `json:"type"`
	// Required indicates whether this variable must be provided
	Required bool `json:"required"`
	// DefaultValue provides a default value (optional)
	DefaultValue interface{} `json:"default_value,omitempty"`
	// Validation provides regex or other validation rules (optional)
	Validation string `json:"validation,omitempty"`
	// Options provides a list of valid options for enum-like variables (optional)
	Options []string `json:"options,omitempty"`
	// Placeholder text to show in input fields
	Placeholder string `json:"placeholder,omitempty"`
}

// InitializationProvider allows plugins to describe their required configuration.
// Plugins can optionally implement this interface to enable automatic initialization prompts.
type InitializationProvider interface {
	// GetRequiredConfig returns a list of configuration variables that need to be set
	GetRequiredConfig() []ConfigVariable
	// ValidateConfig checks if the provided configuration is valid
	ValidateConfig(config map[string]interface{}) error
	// InitializeWithConfig sets up the plugin with the provided configuration
	InitializeWithConfig(config map[string]interface{}) error
}

// InitializableTool combines Tool with InitializationProvider for full initialization support.
type InitializableTool interface {
	Tool
	InitializationProvider
}

// MetadataProvider allows plugins to provide detailed authorship and licensing information.
// Plugins can optionally implement this interface to provide metadata about maintainers, license, etc.
// Note: Maintainer and PluginMetadata types are generated from proto/tool.proto
type MetadataProvider interface {
	// GetMetadata returns plugin metadata (maintainers, license, repository)
	// Returns the proto-generated PluginMetadata struct
	GetMetadata() (*PluginMetadata, error)
}

// WebPageProvider allows plugins to serve custom web pages through ori-agent.
// Plugins can optionally implement this interface to provide custom UI pages.
// Example use cases: script marketplace, configuration UI, data visualization, etc.
type WebPageProvider interface {
	// ServeWebPage handles a web page request and returns HTML content
	// path: The requested path (e.g., "marketplace", "config", "stats")
	// query: URL query parameters as key-value pairs
	// Returns: HTML content, content-type (e.g., "text/html", "application/json"), error
	ServeWebPage(path string, query map[string]string) (content string, contentType string, err error)

	// GetWebPages returns a list of available web pages this plugin provides
	// Each entry should be a path like "marketplace", "settings", etc.
	GetWebPages() []string
}
