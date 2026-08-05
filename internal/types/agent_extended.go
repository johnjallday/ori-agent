package types

import (
	"strings"
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
	Domains         []string `json:"domains,omitempty"`          // User-defined domains or topics such as "reaper" or "tax"
	ExternalSystems []string `json:"external_systems,omitempty"` // External apps or services this agent can operate on
	SideEffects     string   `json:"side_effects,omitempty"`     // none, local_app, external_account, destructive, etc.
}

// AgentDisplayMode names which visual identity an agent renders. It is stored
// explicitly rather than inferred from whichever field happens to be non-empty,
// so trying a curated character and switching back is a reversible choice
// instead of a destructive one (PRD FR-67).
type AgentDisplayMode string

const (
	// DisplayModeFallback renders the deterministic Avatar Identity signature.
	DisplayModeFallback AgentDisplayMode = "fallback"
	// DisplayModeUploaded renders the user's uploaded avatar_image.
	DisplayModeUploaded AgentDisplayMode = "uploaded"
	// DisplayModeCharacter renders the curated catalog character.
	DisplayModeCharacter AgentDisplayMode = "character"
)

// IsValidDisplayMode reports whether m is one of the three known modes. Write
// paths use this to reject an unknown mode from a direct API call rather than
// persisting a value no renderer understands (FR-64/FR-75).
func IsValidDisplayMode(m AgentDisplayMode) bool {
	switch m {
	case DisplayModeFallback, DisplayModeUploaded, DisplayModeCharacter:
		return true
	}
	return false
}

// AgentCharacterIdentity is the agent's curated visual identity and optional
// tone layer.
//
// It deliberately stores a catalog ID rather than a filename, so a reviewed
// replacement asset ships without rewriting agent records (FR-114). It carries
// no prompt text: the tone layer is derived from the reviewed catalog entry and
// gated on VoiceEnabled, so character metadata can never smuggle in arbitrary
// instructions (FR-53/FR-62/FR-75).
type AgentCharacterIdentity struct {
	// DisplayMode is the user's explicit choice of what to render. Empty means
	// the agent predates this field; ResolveDisplayMode infers the legacy
	// behaviour for those records.
	DisplayMode AgentDisplayMode `json:"display_mode,omitempty"`
	// CatalogID is the stable curated character identifier. It is retained when
	// the user switches to another mode, so switching back restores the same
	// character without re-picking it (FR-68 applied to characters).
	CatalogID string `json:"catalog_id,omitempty"`
	// CatalogVersion records which catalog entry version was chosen, so a later
	// art revision is detectable without breaking the assignment.
	CatalogVersion int `json:"catalog_version,omitempty"`
	// VoiceEnabled turns on the bounded character-tone layer. Off by default:
	// tone only applies when the user knowingly enables it (FR-60).
	VoiceEnabled bool `json:"voice_enabled,omitempty"`
}

// Clone returns a deep copy, so handlers can mutate a candidate identity
// without touching the stored one.
func (c *AgentCharacterIdentity) Clone() *AgentCharacterIdentity {
	if c == nil {
		return nil
	}
	out := *c
	return &out
}

// AgentMetadata contains descriptive and organizational information about an agent
type AgentMetadata struct {
	Description       string               `json:"description,omitempty"`        // Human-readable description of the agent's purpose
	Tags              []string             `json:"tags,omitempty"`               // Organizational tags for filtering and categorization
	AvatarColor       string               `json:"avatar_color,omitempty"`       // Color for avatar display (hex color code)
	AvatarImage       string               `json:"avatar_image,omitempty"`       // Path to custom avatar image (relative to /avatars/)
	Favorite          bool                 `json:"favorite,omitempty"`           // Whether this agent is marked as favorite
	ReviewEnabled     *bool                `json:"review_enabled,omitempty"`     // Whether conversation review is enabled for this agent
	ReviewSensitivity string               `json:"review_sensitivity,omitempty"` // Review sensitivity level: "low", "medium", "high" (default: "medium")
	RoutingProfile    *AgentRoutingProfile `json:"routing_profile,omitempty"`    // User-defined routing hints for specialist selection
	// ExpertMode lifts stage-based active-skill slot caps (see SkillSlotsForStage)
	// for this agent when true. Nil means unset; IsExpertMode resolves the
	// default from the agent's role.
	ExpertMode *bool `json:"expert_mode,omitempty"`
	// Character is the agent's curated visual identity and optional tone layer.
	// Nil on every agent that predates the character system; those keep
	// rendering through the existing uploaded/fallback priority until the user
	// picks something (FR-69).
	Character *AgentCharacterIdentity `json:"character,omitempty"`
}

// ResolveDisplayMode returns the identity mode this agent should render.
//
// An explicit stored mode always wins — that is the whole point of storing it.
// Records written before the character system have no mode, so they fall back
// to the historical rule (uploaded image if present, otherwise the generated
// identity), which is what keeps old agents looking exactly as they did
// (FR-66/FR-69).
func (m *AgentMetadata) ResolveDisplayMode() AgentDisplayMode {
	if m == nil {
		return DisplayModeFallback
	}
	if m.Character != nil && IsValidDisplayMode(m.Character.DisplayMode) {
		return m.Character.DisplayMode
	}
	if strings.TrimSpace(m.AvatarImage) != "" {
		return DisplayModeUploaded
	}
	return DisplayModeFallback
}

// CharacterCatalogID returns the selected curated character ID, or "" when the
// agent has never chosen one. The ID is returned regardless of the active
// display mode: a user who switched to their uploaded avatar has not discarded
// their character, and the Inspector still shows what it would switch back to.
func (m *AgentMetadata) CharacterCatalogID() string {
	if m == nil || m.Character == nil {
		return ""
	}
	return strings.TrimSpace(m.Character.CatalogID)
}

// IsCharacterVoiceEnabled reports whether the bounded tone layer applies. It
// requires both an active character display mode and an explicit opt-in, so
// switching away from a character silently stops its tone layer too (FR-60).
func (m *AgentMetadata) IsCharacterVoiceEnabled() bool {
	if m == nil || m.Character == nil {
		return false
	}
	return m.Character.VoiceEnabled &&
		m.ResolveDisplayMode() == DisplayModeCharacter &&
		strings.TrimSpace(m.Character.CatalogID) != ""
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
