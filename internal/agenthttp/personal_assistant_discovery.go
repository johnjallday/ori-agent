package agenthttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
)

// AssistantDiscoveryReader exposes only metadata reads. It cannot install,
// dispatch MCP, execute a command, or perform an unreviewed external lookup.
// Accepted turn/relationship checks belong to the dedicated tool broker.
type AssistantDiscoveryReader interface {
	Installed(context.Context, string) assistantdiscovery.Result
	MCPCatalog(context.Context, string) assistantdiscovery.Result
}
