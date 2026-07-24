package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/drive"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// connectionProductForMCPEndpoint maps a Google MCP endpoint to the connection
// product its grant represents.
func connectionProductForMCPEndpoint(endpoint string) connections.ProductKey {
	switch {
	case strings.Contains(endpoint, "calendarmcp.googleapis.com"):
		return connections.ProductCalendar
	case strings.Contains(endpoint, "drivemcp.googleapis.com"):
		return connections.ProductDrive
	default:
		return ""
	}
}

// googleMCPIdentityHook verifies the ID token captured from a Google MCP
// authorization (Calendar/Drive) and, if the subject matches the active Google
// connection, attaches that product grant (FR 23, 40). It is best-effort: any
// failure — no connection, a different subject, an unverifiable token — leaves
// the grant unattached (the product still works standalone) and never blocks the
// MCP connect flow. Tokens stay in the MCP vault seam; the grant only references
// the server.
func (b *ServerBuilder) googleMCPIdentityHook(serverName, endpoint, rawIDToken, clientID string) {
	product := connectionProductForMCPEndpoint(endpoint)
	if product == "" || b.connStore == nil || strings.TrimSpace(clientID) == "" {
		return
	}

	ctx := context.Background()
	verifier, err := connections.NewGoogleVerifier(ctx, clientID)
	if err != nil {
		logger.Warn("google mcp identity: verifier init failed", logger.Fields{"server": serverName, "error": err})
		return
	}
	identity, err := verifier.VerifyNoNonce(ctx, rawIDToken)
	if err != nil {
		logger.Warn("google mcp identity: id token verification failed", logger.Fields{"server": serverName, "error": err})
		return
	}

	conn, err := b.connStore.Load()
	if err != nil || conn == nil {
		return
	}
	if err := conn.AttachMCPGrant(product, identity.Subject, serverName, nil); err != nil {
		// Subject mismatch or no active identity — per FR 23 we do not attach.
		logger.Info("google mcp grant not attached", logger.Fields{"server": serverName, "product": string(product), "reason": err.Error()})
		return
	}
	if err := b.connStore.Save(conn); err != nil {
		logger.Warn("google mcp grant: save failed", logger.Fields{"server": serverName, "error": err})
		return
	}
	logger.Info("google mcp grant attached to Google connection", logger.Fields{"server": serverName, "product": string(product)})
}

// mcpToolExposureAllowed is the server-side tool-exposure policy wired into the
// MCP registry (SetToolExposureHook). Google Drive is capped to its fail-closed
// read-only allowlist — mutations, permission tools, and any unknown/future tool
// are denied at both listing and execution (FR 66, 67). Every other server is
// unrestricted here; their tools are gated by workspace bindings elsewhere.
func (b *ServerBuilder) mcpToolExposureAllowed(serverURL, toolName string) bool {
	if connectionProductForMCPEndpoint(serverURL) == connections.ProductDrive {
		return drive.IsAllowedTool(toolName)
	}
	return true
}

// googleConnectionEmail returns the active Google connection's email (or "") so
// Google MCP authorizations can pre-select that account (FR 58).
func (b *ServerBuilder) googleConnectionEmail() string {
	if b.connStore == nil {
		return ""
	}
	conn, err := b.connStore.Load()
	if err != nil || conn == nil {
		return ""
	}
	return conn.Email
}
