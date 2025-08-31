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

// SettingsProvider is an interface for tools that manage custom settings.
// Plugins can optionally implement this interface to provide settings management.
type SettingsProvider interface {
	// GetSettings returns the current settings as a JSON string
	GetSettings() (string, error)
	// SetSettings updates settings from a JSON string
	SetSettings(settings string) error
	// GetDefaultSettings returns default settings as a JSON string
	GetDefaultSettings() (string, error)
	// IsInitialized returns true if the plugin has been properly configured
	IsInitialized() bool
}

// ConfigurableTool combines Tool with SettingsProvider for tools that need configuration.
type ConfigurableTool interface {
	Tool
	SettingsProvider
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
