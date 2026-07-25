package server

import (
	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/mcp"
)

// connectionGrantHealth maps a Google product grant's underlying runtime state to
// a grant health without any browser interaction (FR 85). Calendar/Drive read
// their MCP server's status from the registry; Gmail and transient states are
// left to the stored health (ok=false). Like the other connection adapters it
// reads b.mcpRegistry lazily via the builder.
type connectionGrantHealth struct{ b *ServerBuilder }

func (c connectionGrantHealth) LiveHealth(product connections.ProductKey, credentialRef string) (connections.GrantHealth, bool) {
	switch product {
	case connections.ProductCalendar, connections.ProductDrive:
		if credentialRef == "" || c.b.mcpRegistry == nil {
			return "", false
		}
		status, err := c.b.mcpRegistry.GetServerStatus(credentialRef)
		if err != nil {
			// The grant references a server that no longer exists — its credential
			// is gone, so a reconnect is required (FR 85).
			return connections.HealthReconnectRequired, true
		}
		switch status {
		case mcp.StatusRunning:
			return connections.HealthHealthy, true
		case mcp.StatusAuthRequired, mcp.StatusError, mcp.StatusStopped:
			return connections.HealthReconnectRequired, true
		default:
			// starting / restarting — transient; keep the stored health.
			return "", false
		}
	}
	return "", false
}
