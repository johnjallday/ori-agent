package types

import (
	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// Settings represents LLM configuration shared across agents
type Settings struct {
	Model        string  `json:"model"`
	Temperature  float64 `json:"temperature"`
	APIKey       string  `json:"api_key,omitempty"`       // OpenAI API key (optional, falls back to env var)
	SystemPrompt string  `json:"system_prompt,omitempty"` // Custom system prompt for the agent
}

// LoadedPlugin represents a plugin that has been loaded and is ready to use
type LoadedPlugin struct {
	Tool       pluginapi.Tool                 `json:"-"`
	Definition openai.FunctionDefinitionParam `json:"Definition"`
	Path       string                         `json:"Path"`
	Version    string                         `json:"Version,omitempty"`
}

// PluginRegistryEntry represents a plugin in the plugin registry
type PluginRegistryEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path,omitempty"`         // Local path (for local plugins)
	URL         string `json:"url,omitempty"`          // External URL (for remote plugins)
	Version     string `json:"version,omitempty"`      // Plugin version
	Checksum    string `json:"checksum,omitempty"`     // SHA256 checksum for verification
	AutoUpdate  bool   `json:"auto_update,omitempty"`  // Whether to auto-update this plugin
	GitHubRepo  string `json:"github_repo,omitempty"`  // GitHub repository (user/repo format)
	DownloadURL string `json:"download_url,omitempty"` // Direct download URL for GitHub releases
}

// PluginRegistry contains all available plugins
type PluginRegistry struct {
	Plugins []PluginRegistryEntry `json:"plugins"`
}
