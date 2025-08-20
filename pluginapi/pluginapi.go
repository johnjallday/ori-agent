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
