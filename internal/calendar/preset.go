package calendar

// Transport identifiers used by shipped calendar connector presets. Kept as a
// local constant so this package never imports internal/mcp; the setup HTTP
// layer maps a preset onto an mcp.ServerConfig.
const StreamableHTTPTransport = "streamable_http"

// Google Calendar's official remote MCP endpoint and the stable identifiers the
// setup flow uses to add/resolve it. The endpoint is a Developer Preview
// capability; production availability depends on the connected Google account
// being enrolled (see the docs URL in GoogleCalendarPreset).
const (
	GoogleCalendarPresetID   = "google-calendar"
	GoogleCalendarServerName = "google-calendar"
	GoogleCalendarMCPURL     = "https://calendarmcp.googleapis.com/mcp/v1"
	GoogleCalendarDocsURL    = "https://developers.google.com/workspace/calendar/api/guides/configure-mcp-server"
)

// ConnectorPreset is a shipped, credential-free remote MCP calendar connector
// descriptor the guided setup UI can offer to add with one click. It carries
// only the official endpoint/transport and human-facing prerequisites — never
// an OAuth client id or secret. The user supplies their own Web OAuth client
// credentials through the existing MCP connect flow, which stores them in the
// vault; nothing credential-bearing is ever embedded in this struct, so a
// serialized preset can never leak a secret (see preset_test.go).
type ConnectorPreset struct {
	// ID is the stable preset identifier (not a secret).
	ID string `json:"id"`
	// ServerName is the MCP server name the connector is registered under.
	ServerName string `json:"server_name"`
	// DisplayName is the human-facing connector name shown in setup.
	DisplayName string `json:"display_name"`
	// Transport is the MCP transport ("streamable_http" for remote endpoints).
	Transport string `json:"transport"`
	// URL is the connector's HTTPS MCP endpoint.
	URL string `json:"url"`
	// DeveloperPreview marks a connector whose availability depends on the
	// user's account being enrolled in a provider preview program.
	DeveloperPreview bool `json:"developer_preview"`
	// Prerequisites are the human-facing steps the user must complete in their
	// own cloud console before authentication can succeed. They are copy, not
	// configuration — Ori never performs them.
	Prerequisites []string `json:"prerequisites,omitempty"`
	// DocsURL links the provider's official setup documentation.
	DocsURL string `json:"docs_url,omitempty"`
}

// GoogleCalendarPreset returns the shipped Google Calendar (Developer Preview)
// connector preset. It fills only the official endpoint/transport and the
// prerequisite copy; it never contains OAuth client credentials.
func GoogleCalendarPreset() ConnectorPreset {
	return ConnectorPreset{
		ID:               GoogleCalendarPresetID,
		ServerName:       GoogleCalendarServerName,
		DisplayName:      "Google Calendar (Developer Preview)",
		Transport:        StreamableHTTPTransport,
		URL:              GoogleCalendarMCPURL,
		DeveloperPreview: true,
		Prerequisites: []string{
			"Enroll the Google account in the Calendar MCP Developer Preview; production access depends on that enrollment.",
			"In a Google Cloud project, enable both the Google Calendar API and the Calendar MCP API.",
			"Configure the OAuth consent screen and request the Calendar scopes for that project.",
			"Create a Web application OAuth client and add Ori's redirect URI (shown during Connect) to its authorized redirect URIs.",
			"Have that client's own client id and client secret ready — you enter them in the Connect step; Ori stores them only in your vault and never ships them.",
		},
		DocsURL: GoogleCalendarDocsURL,
	}
}

// ShippedPresets returns every shipped calendar connector preset. v1 ships only
// the Google Calendar Developer Preview connector; the slice shape lets the
// setup UI render a list without special-casing a single entry.
func ShippedPresets() []ConnectorPreset {
	return []ConnectorPreset{GoogleCalendarPreset()}
}

// FindPreset returns the shipped preset with the given id (case-insensitive on
// exact match) and whether it exists.
func FindPreset(id string) (ConnectorPreset, bool) {
	for _, p := range ShippedPresets() {
		if p.ID == id {
			return p, true
		}
	}
	return ConnectorPreset{}, false
}
