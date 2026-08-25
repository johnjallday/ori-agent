package types

import (
	"sync"
	"time"
)

// AgentStatus represents the operational status of an agent
type AgentStatus string

const (
	AgentStatusActive   AgentStatus = "active"   // Agent is operational and ready to process requests
	AgentStatusIdle     AgentStatus = "idle"     // Agent exists but is not actively being used
	AgentStatusError    AgentStatus = "error"    // Agent encountered an error
	AgentStatusDisabled AgentStatus = "disabled" // Agent has been manually disabled
)

// AgentStage represents a progression milestone for an agent.
type AgentStage string

const (
	AgentStageSpark    AgentStage = "spark"
	AgentStageInfant   AgentStage = "infant"
	AgentStageLearner  AgentStage = "learner"
	AgentStageExpert   AgentStage = "expert"
	AgentStageSentient AgentStage = "sentient"
)

// stageSkillSlots maps an evolution stage to the number of active skill
// slots it grants (PRD FR10). Slot growth ties loadout usefulness to
// progression; an agent's stage-appropriate cap is looked up here.
var stageSkillSlots = map[AgentStage]int{
	AgentStageSpark:    2,
	AgentStageInfant:   3,
	AgentStageLearner:  4,
	AgentStageExpert:   5,
	AgentStageSentient: 6,
}

// SkillSlotsForStage returns the number of active-skill slots granted at the
// given evolution stage. An unrecognized or empty stage defaults to the
// spark-stage cap (the lowest), never an unbounded/unlimited result.
func SkillSlotsForStage(stage AgentStage) int {
	if slots, ok := stageSkillSlots[stage]; ok {
		return slots
	}
	return stageSkillSlots[AgentStageSpark]
}

// AgentPath represents the specialization path for an agent.
type AgentPath string

const (
	AgentPathCoder      AgentPath = "coder"
	AgentPathResearcher AgentPath = "researcher"
	AgentPathWriter     AgentPath = "writer"
)

// AgentStatistics tracks usage and performance metrics for an agent
type AgentStatistics struct {
	MessageCount  int64     `json:"message_count"`            // Total number of messages processed
	TokenUsage    int64     `json:"token_usage"`              // Total tokens consumed (input + output)
	TotalCost     float64   `json:"total_cost"`               // Total cost incurred in USD
	LastActive    time.Time `json:"last_active"`              // Timestamp of last activity
	CreatedAt     time.Time `json:"created_at"`               // Timestamp when agent was created
	UpdatedAt     time.Time `json:"updated_at"`               // Timestamp of last modification
	InputTokens   int64     `json:"input_tokens,omitempty"`   // Total input tokens (if tracked separately)
	OutputTokens  int64     `json:"output_tokens,omitempty"`  // Total output tokens (if tracked separately)
	AverageTokens float64   `json:"average_tokens,omitempty"` // Average tokens per message

	mu sync.RWMutex `json:"-"` // Mutex for thread-safe updates
}

// AgentRoutingProfile describes user-defined routing hints for specialist matching.
type AgentRoutingProfile struct {
	MatchPhrases    []string `json:"match_phrases,omitempty"`    // High-signal phrases that should strongly match this agent
	ExampleRequests []string `json:"example_requests,omitempty"` // Example user requests this agent should handle
	Domains         []string `json:"domains,omitempty"`          // User-defined domains or topics such as "audio" or "tax"
	ExternalSystems []string `json:"external_systems,omitempty"` // External apps or services this agent can operate on
	SideEffects     string   `json:"side_effects,omitempty"`     // none, local_app, external_account, destructive, etc.
}

// AgentMetadata contains descriptive and organizational information about an agent.
//
// It deliberately carries no visual-identity fields. An agent's appearance lives
// in one first-class Appearance object on the agent itself, not scattered across
// generic metadata as a colour, a filename, and a nested character record that
// also owned the active mode (PRD FR-1/FR-14).
type AgentMetadata struct {
	Description       string               `json:"description,omitempty"`        // Human-readable description of the agent's purpose
	Tags              []string             `json:"tags,omitempty"`               // Organizational tags for filtering and categorization
	Favorite          bool                 `json:"favorite,omitempty"`           // Whether this agent is marked as favorite
	ReviewEnabled     *bool                `json:"review_enabled,omitempty"`     // Whether conversation review is enabled for this agent
	ReviewSensitivity string               `json:"review_sensitivity,omitempty"` // Review sensitivity level: "low", "medium", "high" (default: "medium")
	RoutingProfile    *AgentRoutingProfile `json:"routing_profile,omitempty"`    // User-defined routing hints for specialist selection
	// ExpertMode lifts stage-based active-skill slot caps (see SkillSlotsForStage)
	// for this agent when true. Nil means unset; IsExpertMode resolves the
	// default from the agent's role.
	ExpertMode *bool `json:"expert_mode,omitempty"`

	// legacyAppearance holds retired avatar/character fields found while
	// decoding an old record, for the migration to drain exactly once. It is
	// unexported so it cannot be serialized back out — see
	// agent_appearance_legacy.go.
	legacyAppearance *LegacyAppearance
}

// IsExpertMode reports whether the agent bypasses stage-based skill slot
// caps (PRD FR13). When explicitly set, that value wins. When unset,
// pre-existing/Unspecialized agents (empty or "general" role) default to
// expert mode ON — they predate the loadout-cap system; catalog-created
// agents (any other role) default OFF.
func (m *AgentMetadata) IsExpertMode(role AgentRole) bool {
	if m != nil && m.ExpertMode != nil {
		return *m.ExpertMode
	}
	return role == "" || role == RoleGeneral
}

// AgentEvolution tracks progression state for an agent.
type AgentEvolution struct {
	Level         int        `json:"level"`
	Experience    int64      `json:"experience"`
	Stage         AgentStage `json:"stage"`
	Path          AgentPath  `json:"path,omitempty"`
	ParentID      string     `json:"parent_id,omitempty"`
	FeedCount     int64      `json:"feed_count,omitempty"`
	LastEvolvedAt time.Time  `json:"last_evolved_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// DashboardStats contains aggregate statistics across all agents
type DashboardStats struct {
	TotalAgents             int     `json:"total_agents"`
	ActiveAgents            int     `json:"active_agents"`
	IdleAgents              int     `json:"idle_agents"`
	DisabledAgents          int     `json:"disabled_agents"`
	ErrorAgents             int     `json:"error_agents"`
	TotalMessages           int64   `json:"total_messages"`
	TotalTokens             int64   `json:"total_tokens"`
	TotalCost               float64 `json:"total_cost"`
	MostActiveAgent         string  `json:"most_active_agent,omitempty"`
	MostCostlyAgent         string  `json:"most_costly_agent,omitempty"`
	NewestAgent             string  `json:"newest_agent,omitempty"`
	AverageMessagesPerAgent float64 `json:"average_messages_per_agent"`
	AverageCostPerAgent     float64 `json:"average_cost_per_agent"`
}

// NewAgentStatistics creates a new AgentStatistics instance with current timestamp
func NewAgentStatistics() *AgentStatistics {
	now := time.Now()
	return &AgentStatistics{
		MessageCount: 0,
		TokenUsage:   0,
		TotalCost:    0.0,
		LastActive:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// NewAgentEvolution creates a new AgentEvolution with safe defaults.
func NewAgentEvolution() *AgentEvolution {
	now := time.Now()
	return &AgentEvolution{
		Level:      0,
		Experience: 0,
		Stage:      AgentStageSpark,
		UpdatedAt:  now,
	}
}

// EnsureDefaults fills missing values for backward-compatible migrations.
func (e *AgentEvolution) EnsureDefaults() {
	if e == nil {
		return
	}
	if e.Level < 0 {
		e.Level = 0
	}
	if e.Experience < 0 {
		e.Experience = 0
	}
	if e.FeedCount < 0 {
		e.FeedCount = 0
	}
	if e.Stage == "" {
		e.Stage = AgentStageSpark
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
}

// RecordMessage records a message interaction with token count and cost
// This method is thread-safe and can be called concurrently
func (s *AgentStatistics) RecordMessage(tokenCount int, cost float64) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.MessageCount++
	s.TokenUsage += int64(tokenCount)
	s.TotalCost += cost
	s.LastActive = time.Now()
	s.UpdatedAt = time.Now()

	// Update average tokens per message
	if s.MessageCount > 0 {
		s.AverageTokens = float64(s.TokenUsage) / float64(s.MessageCount)
	}
}

// RecordTokens records token usage with separate input and output counts
// This method is thread-safe and can be called concurrently
func (s *AgentStatistics) RecordTokens(inputTokens, outputTokens int, cost float64) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.MessageCount++
	s.InputTokens += int64(inputTokens)
	s.OutputTokens += int64(outputTokens)
	s.TokenUsage += int64(inputTokens + outputTokens)
	s.TotalCost += cost
	s.LastActive = time.Now()
	s.UpdatedAt = time.Now()

	// Update average tokens per message
	if s.MessageCount > 0 {
		s.AverageTokens = float64(s.TokenUsage) / float64(s.MessageCount)
	}
}

// UpdateLastActive updates the last activity timestamp
// This method is thread-safe and can be called concurrently
func (s *AgentStatistics) UpdateLastActive() {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.LastActive = time.Now()
}

// GetSafeStats returns a copy of the statistics in a thread-safe manner
func (s *AgentStatistics) GetSafeStats() AgentStatistics {
	if s == nil {
		return AgentStatistics{}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy without the mutex
	return AgentStatistics{
		MessageCount:  s.MessageCount,
		TokenUsage:    s.TokenUsage,
		TotalCost:     s.TotalCost,
		LastActive:    s.LastActive,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
		InputTokens:   s.InputTokens,
		OutputTokens:  s.OutputTokens,
		AverageTokens: s.AverageTokens,
	}
}

// ActivityEventType represents the type of activity being logged
type ActivityEventType string

const (
	ActivityEventCreated        ActivityEventType = "created"         // Agent was created
	ActivityEventUpdated        ActivityEventType = "updated"         // Agent configuration was updated
	ActivityEventDeleted        ActivityEventType = "deleted"         // Agent was deleted
	ActivityEventMessageSent    ActivityEventType = "message_sent"    // Chat message was sent to the agent
	ActivityEventStatusChanged  ActivityEventType = "status_changed"  // Agent status changed
	ActivityEventEvolutionFeed  ActivityEventType = "evolution_feed"  // Feed action granted evolution XP
	ActivityEventEvolutionStage ActivityEventType = "evolution_stage" // Evolution stage changed
	ActivityEventEvolutionPath  ActivityEventType = "evolution_path"  // Evolution path selected/changed
	ActivityEventEvolutionTask  ActivityEventType = "evolution_task"  // Completed task run granted evolution XP
)

// ActivityLog represents a single activity log entry for an agent
type ActivityLog struct {
	ID        string            `json:"id"`                // Unique identifier for the log entry
	AgentName string            `json:"agent_name"`        // Name of the agent this activity relates to
	EventType ActivityEventType `json:"event_type"`        // Type of activity event
	Timestamp time.Time         `json:"timestamp"`         // When the activity occurred
	Details   map[string]any    `json:"details,omitempty"` // Additional event-specific details (JSON)
	User      string            `json:"user,omitempty"`    // User who triggered the activity (if applicable)
}

// ActivityLogEntry is a formatted activity log entry for UI rendering
type ActivityLogEntry struct {
	ID          string    `json:"id"`
	AgentName   string    `json:"agent_name"`
	EventType   string    `json:"event_type"`
	EventTitle  string    `json:"event_title"` // Human-readable event title
	Description string    `json:"description"` // Human-readable description
	Timestamp   time.Time `json:"timestamp"`
	User        string    `json:"user,omitempty"`
	Icon        string    `json:"icon,omitempty"`  // Icon/emoji for the event type
	Color       string    `json:"color,omitempty"` // Color for UI display
}
