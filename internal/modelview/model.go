// Package modelview defines the credential-free, next-message model projection.
// Configured describes selection, not connectivity or historical reply attribution.
package modelview

// Selection contains only public configuration and a server-chosen local editor.
type Selection struct {
	Recipient    string `json:"recipient"`
	StudioID     string `json:"studio_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	Source       string `json:"source"`
	State        string `json:"state"`
	SettingsHref string `json:"settings_href,omitempty"`
}
