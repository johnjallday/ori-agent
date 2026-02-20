// Package mcpregistry provides types and logic for browsing MCP server registries.
package mcpregistry

import "time"

// RegistryEntry represents a single MCP server available in a registry.
type RegistryEntry struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env,omitempty"`
	Transport   string            `json:"transport"`
	Category    string            `json:"category"`
	Maintainer  string            `json:"maintainer"`
	Tags        []string          `json:"tags,omitempty"`
	Homepage    string            `json:"homepage,omitempty"`
	License     string            `json:"license,omitempty"`
	EnvRequired map[string]string `json:"env_required,omitempty"`
	Source      string            `json:"source"` // display name of the source registry
}

// RegistrySource represents a configured registry source URL.
type RegistrySource struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	URL         string     `json:"url"`         // raw JSON URL or GitHub shorthand "user/repo"
	SourceType  string     `json:"source_type"` // "github", "url", "builtin"
	Enabled     bool       `json:"enabled"`
	IsBuiltin   bool       `json:"is_builtin"`
	LastFetched *time.Time `json:"last_fetched,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
}

// RemoteRegistry is the JSON format we expect when fetching from a remote URL.
type RemoteRegistry struct {
	Version string          `json:"version"`
	Servers []RegistryEntry `json:"servers"`
}
