package agent

import (
	"strings"

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
		"gpt-5-codex-mini",
		"gpt-5.1-codex-mini",
		"codex-mini-latest",
		"claude-3-5-sonnet-20241022",
		"claude-3-sonnet-20240229",
	},
	TypeResearch: {
		"gpt-5",
		"gpt-4o",
		"gpt-5-codex",
		"gpt-5.1-codex",
		"gpt-5.2-codex",
		"gpt-5.3-codex",
		"gpt-5.1-codex-max",
		"claude-3-5-sonnet-20241022",
		"claude-sonnet-4-5",
		"claude-opus-4-1",
	},
}

// GetTypeForModel returns the agent type that supports the given model.
// When a model appears in multiple types, priority is: tool-calling > general > research
// (returns the most cost-efficient type that supports the model)
func GetTypeForModel(model string) string {
	if inferredType, ok := inferCodexModelType(model); ok {
		return inferredType
	}

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
	if allowed, handled := codexModelAllowedForType(model, agentType); handled {
		return allowed
	}

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

func codexModelAllowedForType(model, agentType string) (allowed bool, handled bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" || !strings.Contains(normalized, "codex") {
		return false, false
	}

	switch {
	case strings.Contains(normalized, "nano"):
		return agentType == TypeToolCalling, true
	case strings.Contains(normalized, "mini"):
		return agentType == TypeToolCalling || agentType == TypeGeneral, true
	default:
		return agentType == TypeResearch, true
	}
}

func inferCodexModelType(model string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" || !strings.Contains(normalized, "codex") {
		return "", false
	}

	if strings.Contains(normalized, "nano") {
		return TypeToolCalling, true
	}
	if strings.Contains(normalized, "mini") {
		return TypeGeneral, true
	}
	return TypeResearch, true
}

// Agent represents a configured AI agent with its settings and state
type Agent struct {
	Type         string                                   `json:"type"`         // Agent type (tool-calling, general, research)
	Role         types.AgentRole                          `json:"role"`         // Agent role for orchestration (orchestrator, researcher, analyzer, etc.)
	Capabilities []string                                 `json:"capabilities"` // Agent capabilities (web_search, code_analysis, etc.)
	Settings     types.Settings                           `json:"Settings"`
	Messages     []openai.ChatCompletionMessageParamUnion `json:"-"` // in-memory only

	// Dashboard-specific fields (optional for backward compatibility)
	Status     types.AgentStatus      `json:"status,omitempty"`     // Operational status (active, idle, error, disabled)
	Statistics *types.AgentStatistics `json:"statistics,omitempty"` // Usage and performance metrics
	Metadata   *types.AgentMetadata   `json:"metadata,omitempty"`   // Descriptive information and tags
	Evolution  *types.AgentEvolution  `json:"evolution,omitempty"`  // Agent progression state

	// Appearance is the agent's visual configuration: one active source
	// (generated, character, or uploaded) plus the retained state of the
	// inactive ones.
	//
	// It is first-class rather than a few fields inside Metadata because it is a
	// concept the user edits directly, with its own validation rules, its own
	// mutation endpoints, and its own migration. Burying it in generic metadata
	// is what previously let "avatar" and "character" drift into two unrelated
	// features (PRD FR-1).
	//
	// Nil only on a record that has not been normalized yet; EnsureAppearance
	// and the store's load path both guarantee a non-nil value (FR-4).
	Appearance *types.AgentAppearance `json:"appearance,omitempty"`

	// DefaultToolbox is the agent's explicit skill selection for DIRECT,
	// non-workspace chat (PRD FR-24). It is skill-only and cannot reference a
	// workspace binding, credential, scope, or agent instance (FR-25) — see
	// types.AgentDefaultToolbox, which has no field able to hold one.
	//
	// It is deliberately separate from every workspace Toolbox this agent is
	// used with: editing a workspace Toolbox must not touch it, and editing it
	// must not touch any workspace assignment (FR-26, FR-27). Nil on an agent
	// that predates the field; migration fills it from the agent's globally
	// enabled skills so direct-chat behavior is unchanged (FR-28).
	DefaultToolbox *types.AgentDefaultToolbox `json:"default_toolbox,omitempty"`
}

// InitializeDefaultToolbox safely initializes the Default Toolbox if nil.
// Idempotent, like InitializeStatistics/InitializeEvolution.
func (a *Agent) InitializeDefaultToolbox() {
	if a.DefaultToolbox == nil {
		a.DefaultToolbox = types.NewAgentDefaultToolbox()
	}
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
