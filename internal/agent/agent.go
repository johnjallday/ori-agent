package agent

import (
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/openai/openai-go/v3"
)

// Agent represents a configured AI agent with its settings and state
type Agent struct {
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
