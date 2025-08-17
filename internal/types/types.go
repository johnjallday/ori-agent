package types

import (
	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

type Settings struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	APIKey      string  `json:"api_key,omitempty"` // OpenAI API key (optional, falls back to env var)
}

type LoadedPlugin struct {
	Tool       pluginapi.Tool                 `json:"-"`
	Definition openai.FunctionDefinitionParam `json:"Definition"`
	Path       string                         `json:"Path"`
}

type Agent struct {
	Settings Settings                                 `json:"Settings"`
	Plugins  map[string]LoadedPlugin                  `json:"Plugins"`
	Messages []openai.ChatCompletionMessageParamUnion `json:"-"` // in-memory only
}

type PluginRegistryEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path,omitempty"`        // Local path (for local plugins)
	URL         string `json:"url,omitempty"`         // External URL (for remote plugins)
	Version     string `json:"version,omitempty"`     // Plugin version
	Checksum    string `json:"checksum,omitempty"`    // SHA256 checksum for verification
	AutoUpdate  bool   `json:"auto_update,omitempty"` // Whether to auto-update this plugin
}

type PluginRegistry struct {
	Plugins []PluginRegistryEntry `json:"plugins"`
}
