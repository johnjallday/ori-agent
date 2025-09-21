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
