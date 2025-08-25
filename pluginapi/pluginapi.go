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
