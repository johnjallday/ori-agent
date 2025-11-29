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
// Models are listed from cheapest/fastest to most expensive/capable
var TypeModels = map[string][]string{
	TypeToolCalling: {
		"gpt-3.5-turbo",           // $0.50/$1.50 per 1M tokens (cheapest)
		"gpt-4o-mini",             // $0.15/$0.60 per 1M tokens (best value)
		"claude-3-haiku-20240307", // Fast and cheap
	},
	TypeGeneral: {
		"gpt-4o-mini",                // Best value for general use
		"gpt-4o",                     // $2.50/$10 per 1M tokens
		"claude-3-5-sonnet-20241022", // Latest Sonnet
		"claude-3-sonnet-20240229",   // Previous Sonnet
	},
	TypeResearch: {
		"gpt-4o",                     // Most capable OpenAI model
		"claude-3-5-sonnet-20241022", // Latest Claude (best for complex tasks)
		"claude-sonnet-4-5",          // Future model placeholder
		"claude-opus-4-1",            // Future model placeholder
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

	// Dashboard-specific fields (optional for backward compatibility)
	Status     types.AgentStatus      `json:"status,omitempty"`     // Operational status (active, idle, error, disabled)
	Statistics *types.AgentStatistics `json:"statistics,omitempty"` // Usage and performance metrics
	Metadata   *types.AgentMetadata   `json:"metadata,omitempty"`   // Descriptive information and tags
}

// InitializeStatistics safely initializes the statistics if nil
// This method is idempotent and can be called multiple times
func (a *Agent) InitializeStatistics() {
	if a.Statistics == nil {
		a.Statistics = types.NewAgentStatistics()
	}
}

// UpdateLastActive updates the last activity timestamp for the agent
func (a *Agent) UpdateLastActive() {
	if a.Statistics != nil {
		a.Statistics.UpdateLastActive()
	}
}
