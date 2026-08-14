package skills

// Skill represents a reusable prompt template with optional tool constraints.
type Skill struct {
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	Prompt             string          `json:"prompt,omitempty"`
	Source             string          `json:"source"`
	Path               string          `json:"path,omitempty"`
	AllowedTools       []string        `json:"allowed_tools,omitempty"`
	DisallowedTools    []string        `json:"disallowed_tools,omitempty"`
	RequiredMCPServers []string        `json:"required_mcp_servers,omitempty"`
	Model              string          `json:"model,omitempty"`
	Color              string          `json:"color,omitempty"`
	Enabled            bool            `json:"enabled"`
	Trusted            bool            `json:"trusted"`
	HasScripts         bool            `json:"has_scripts"`
	ValidationErrors   []string        `json:"validation_errors,omitempty"`
	OpenAIMetadata     *OpenAIMetadata `json:"openai_metadata,omitempty"`
}
