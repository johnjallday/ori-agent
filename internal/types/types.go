package types

import (
	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// Agent types
const (
	AgentTypeToolCalling = "tool-calling" // Cost-optimized for tool calls (DEFAULT)
	AgentTypeGeneral     = "general"      // General purpose
	AgentTypeResearch    = "research"     // Complex thinking
)

// AgentTypeModels defines model restrictions by agent type
var AgentTypeModels = map[string][]string{
	AgentTypeToolCalling: {
		"gpt-5-nano",
		"gpt-4.1-nano",
		"claude-3-haiku-20240307",
	},
	AgentTypeGeneral: {
		"gpt-5-mini",
		"gpt-4.1-mini",
		"gpt-4o-mini",
		"claude-3-sonnet-20240229",
	},
	AgentTypeResearch: {
		"gpt-5",
		"gpt-4.1",
		"claude-sonnet-4-5",
		"claude-sonnet-4",
		"claude-opus-4-1",
	},
}

// GetAgentTypeForModel returns the agent type that supports the given model
func GetAgentTypeForModel(model string) string {
	for agentType, models := range AgentTypeModels {
		for _, m := range models {
			if m == model {
				return agentType
			}
		}
	}
	// Default to tool-calling if model not found
	return AgentTypeToolCalling
}

// IsModelAllowedForType checks if a model is allowed for the given agent type
func IsModelAllowedForType(model, agentType string) bool {
	models, exists := AgentTypeModels[agentType]
	if !exists {
		return false
	}
	for _, m := range models {
		if m == model {
			return true
		}
	}
	return false
}

type Settings struct {
	Model        string  `json:"model"`
	Temperature  float64 `json:"temperature"`
	APIKey       string  `json:"api_key,omitempty"`       // OpenAI API key (optional, falls back to env var)
	SystemPrompt string  `json:"system_prompt,omitempty"` // Custom system prompt for the agent
}

type LoadedPlugin struct {
	Tool       pluginapi.Tool                 `json:"-"`
	Definition openai.FunctionDefinitionParam `json:"Definition"`
	Path       string                         `json:"Path"`
	Version    string                         `json:"Version,omitempty"`
}

type Agent struct {
	Type     string                                   `json:"type"` // Agent type (tool-calling, general, research)
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
	GitHubRepo  string `json:"github_repo,omitempty"` // GitHub repository (user/repo format)
	DownloadURL string `json:"download_url,omitempty"` // Direct download URL for GitHub releases
}

type PluginRegistry struct {
	Plugins []PluginRegistryEntry `json:"plugins"`
}
