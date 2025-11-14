package agent

import (
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/openai/openai-go/v3"
)

// Type constants define agent capability tiers
const (
	TypeToolCalling = "tool-calling" // Cost-optimized for tool calls (DEFAULT)
	TypeGeneral     = "general"      // General purpose
	TypeResearch    = "research"     // Complex thinking
)

// TypeModels defines model restrictions by agent type
var TypeModels = map[string][]string{
	TypeToolCalling: {
		"gpt-5-nano",
		"gpt-4.1-nano",
		"claude-3-haiku-20240307",
	},
	TypeGeneral: {
		"gpt-5-mini",
		"gpt-4.1-mini",
		"gpt-4o-mini",
		"claude-3-sonnet-20240229",
	},
	TypeResearch: {
		"gpt-5",
		"gpt-4.1",
		"claude-sonnet-4-5",
		"claude-sonnet-4",
		"claude-opus-4-1",
	},
}

// GetTypeForModel returns the agent type that supports the given model
func GetTypeForModel(model string) string {
	for agentType, models := range TypeModels {
		for _, m := range models {
			if m == model {
				return agentType
			}
		}
	}
	// Default to tool-calling if model not found
	return TypeToolCalling
}

// IsModelAllowedForType checks if a model is allowed for the given agent type
func IsModelAllowedForType(model, agentType string) bool {
	models, exists := TypeModels[agentType]
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

// Agent represents a configured AI agent with its settings and state
type Agent struct {
	Type         string                                   `json:"type"`         // Agent type (tool-calling, general, research)
	Role         types.AgentRole                          `json:"role"`         // Agent role for orchestration (orchestrator, researcher, analyzer, etc.)
	Capabilities []string                                 `json:"capabilities"` // Agent capabilities (web_search, code_analysis, etc.)
	Settings     types.Settings                           `json:"Settings"`
	Plugins      map[string]types.LoadedPlugin            `json:"Plugins"`
	MCPServers   []string                                 `json:"mcp_servers,omitempty"` // List of enabled MCP server names
	Messages     []openai.ChatCompletionMessageParamUnion `json:"-"`                     // in-memory only
}
