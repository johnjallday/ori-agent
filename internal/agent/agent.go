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
		"gpt-5-nano",
		"gpt-4o-mini",
		"claude-3-haiku-20240307",
	},
	TypeGeneral: {
		"gpt-5-mini",
		"gpt-4o-mini",
		"gpt-4o",
		"claude-3-5-sonnet-20241022",
		"claude-3-sonnet-20240229",
	},
	TypeResearch: {
		"gpt-5",
		"gpt-4o",
		"claude-3-5-sonnet-20241022",
		"claude-sonnet-4-5",
		"claude-opus-4-1",
	},
}

// GetTypeForModel returns the agent type that supports the given model.
// When a model appears in multiple types, priority is: tool-calling > general > research
// (returns the most cost-efficient type that supports the model)
func GetTypeForModel(model string) string {
	// Check in priority order: tool-calling (cheapest) → general → research (most capable)
	typePriority := []string{TypeToolCalling, TypeGeneral, TypeResearch}
	for _, agentType := range typePriority {
		models := TypeModels[agentType]
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
	Evolution  *types.AgentEvolution  `json:"evolution,omitempty"`  // Agent progression state
}

// InitializeStatistics safely initializes the statistics if nil
// This method is idempotent and can be called multiple times
func (a *Agent) InitializeStatistics() {
	if a.Statistics == nil {
		a.Statistics = types.NewAgentStatistics()
	}
}

// InitializeEvolution safely initializes evolution data if nil.
// This method is idempotent and can be called multiple times.
func (a *Agent) InitializeEvolution() {
	if a.Evolution == nil {
		a.Evolution = types.NewAgentEvolution()
		return
	}
	a.Evolution.EnsureDefaults()
}

// UpdateLastActive updates the last activity timestamp for the agent
func (a *Agent) UpdateLastActive() {
	if a.Statistics != nil {
		a.Statistics.UpdateLastActive()
	}
}
