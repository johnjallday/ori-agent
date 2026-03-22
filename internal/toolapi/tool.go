package toolapi

import "context"

// ToolDefinition describes a tool's name, description, and parameter schema.
// Parameters follows JSON Schema conventions for describing tool inputs.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// Tool is the interface that executable tools must implement.
type Tool interface {
	Definition() ToolDefinition
	Call(ctx context.Context, args string) (string, error)
}

// VersionedTool extends Tool with version information.
type VersionedTool interface {
	Tool
	Version() string
}

// AgentContext provides agent identity information to tools.
type AgentContext struct {
	Name            string
	ConfigPath      string
	SettingsPath    string
	AgentDir        string
	CurrentLocation string
}

// AgentAwareTool extends Tool to receive agent context.
type AgentAwareTool interface {
	Tool
	SetAgentContext(ctx AgentContext)
}
