// Package drive holds the shipped Google Drive (Developer Preview) remote-MCP
// connector preset and the fail-closed tool allowlist that caps V1 to read-only
// access. The Drive grant's identity binding is handled by the shared Google-MCP
// path in internal/mcp + internal/connections; this package owns the Drive
// specifics: the preset descriptor and the tool allowlist enforcement.
package drive

import "github.com/johnjallday/ori-agent/internal/calendar"

// Google Drive's official remote MCP endpoint and the stable identifiers the
// setup flow uses. Availability depends on the connected account being enrolled
// in the Developer Preview (see the docs URL in GoogleDrivePreset).
const (
	GooglePresetID   = "google-drive"
	GoogleServerName = "google-drive"
	GoogleMCPURL     = "https://drivemcp.googleapis.com/mcp/v1"
	GoogleDocsURL    = "https://developers.google.com/workspace/drive/api/guides/configure-mcp-server"
)

// GoogleDrivePreset returns the shipped Google Drive (Developer Preview) remote
// connector preset. It is credential-free — the user supplies their own Web
// OAuth client through the existing MCP connect flow, stored in the vault; the
// preset never carries a token or secret (same contract as the Calendar preset).
func GoogleDrivePreset() calendar.ConnectorPreset {
	return calendar.ConnectorPreset{
		ID:               GooglePresetID,
		ServerName:       GoogleServerName,
		DisplayName:      "Google Drive (Developer Preview)",
		Transport:        calendar.StreamableHTTPTransport,
		URL:              GoogleMCPURL,
		DeveloperPreview: true,
		Prerequisites: []string{
			"Enroll the Google account in the Drive MCP Developer Preview; production access depends on that enrollment.",
			"In a Google Cloud project, enable both the Google Drive API and the Drive MCP API.",
			"Configure the OAuth consent screen and request the Drive scopes (drive.readonly and drive.file) for that project.",
			"Create a Web application OAuth client and add Ori's redirect URI (shown during Connect) to its authorized redirect URIs.",
			"Have that client's own client id and client secret ready — you enter them in the Connect step; Ori stores them only in your vault and never ships them.",
		},
		DocsURL: GoogleDocsURL,
	}
}

// ReadOnlyToolAllowlist is the EXACT, fail-closed set of Google Drive MCP tools
// Ori exposes in V1 (FR 66). Every other tool — the mutations create_file and
// copy_file, the permission read get_file_permissions, and any unknown or
// future tool — is denied (FR 67).
var ReadOnlyToolAllowlist = []string{
	"search_files",
	"list_recent_files",
	"get_file_metadata",
	"read_file_content",
	"download_file_content",
}

// IsAllowedTool reports whether a Drive tool name is on the read-only allowlist.
// It is the single source of truth for the fail-closed cap: an unknown tool
// returns false (FR 67).
func IsAllowedTool(tool string) bool {
	for _, t := range ReadOnlyToolAllowlist {
		if t == tool {
			return true
		}
	}
	return false
}

// FilterTools returns only the discovered tool names that are on the read-only
// allowlist, dropping mutations, permission tools, and any unknown/future tool
// even when the MCP server advertises them (FR 67). Order is preserved.
func FilterTools(discovered []string) []string {
	out := make([]string, 0, len(discovered))
	for _, t := range discovered {
		if IsAllowedTool(t) {
			out = append(out, t)
		}
	}
	return out
}
