package server

import (
	"context"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/drive"
	"github.com/johnjallday/ori-agent/internal/githubhttp"
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

// isGitHubMCPEndpoint reports whether an MCP endpoint is GitHub's hosted
// server. Matched by host rather than by the registry name, because the
// exposure hook is keyed by URL and a user could add the same endpoint under
// any name -- the cap must follow the endpoint, not the label on it.
func isGitHubMCPEndpoint(endpoint string) bool {
	return strings.Contains(strings.ToLower(endpoint), "api.githubcopilot.com")
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
// are denied at both listing and execution (FR 66, 67). Either Google product
// can also be hard-disabled independently by feature flag, which denies all of
// its tools regardless of any grant or binding (FR 75). Every other server is
// unrestricted here; their tools are gated by workspace bindings elsewhere.
func (b *ServerBuilder) mcpToolExposureAllowed(serverURL, toolName string) bool {
	// GitHub's hosted MCP server exposes 44 tools, only a dozen of which
	// belong to an issue-triage workspace; the rest include push_files,
	// delete_file, and merge_pull_request. Cap it to its allowlist at both
	// listing and execution, so a denied tool is neither advertised to a
	// model nor runnable if one names it directly.
	if isGitHubMCPEndpoint(serverURL) {
		return githubhttp.IsAllowedTool(toolName)
	}
	switch connectionProductForMCPEndpoint(serverURL) {
	case connections.ProductDrive:
		if !googleProductEnabled("DRIVE") {
			return false
		}
		return drive.IsAllowedTool(toolName)
	case connections.ProductCalendar:
		// Calendar is not allowlist-capped (it supports read+write via Calendar
		// Ops), but the feature flag can still hard-disable it independently.
		return googleProductEnabled("CALENDAR")
	default:
		return true
	}
}

// googleProductEnabled reports whether a Google product (DRIVE, CALENDAR) is
// enabled. Products are ON by default; setting ORI_GOOGLE_<PRODUCT>_ENABLED to a
// falsey value (0/false/no/off) hard-disables that product independently,
// denying all of its MCP tools at listing and execution (FR 75).
func googleProductEnabled(product string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ORI_GOOGLE_" + product + "_ENABLED"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// sanitizeDriveResultText fences and bounds untrusted Google Drive result
// content — file bodies, names, metadata, comments, links — before it reaches
// the LLM (FR 71, 73). The first block of each Drive result also gets the
// untrusted-content notice. Non-Drive servers pass through unchanged.
func (b *ServerBuilder) sanitizeDriveResultText(serverURL, text string, blockIndex int) string {
	if connectionProductForMCPEndpoint(serverURL) == connections.ProductDrive {
		return drive.FenceText(text, blockIndex == 0)
	}
	return text
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
