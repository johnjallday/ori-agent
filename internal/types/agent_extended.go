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

// AgentMetadata contains descriptive and organizational information about an agent
type AgentMetadata struct {
	Description string   `json:"description,omitempty"`  // Human-readable description of the agent's purpose
	Tags        []string `json:"tags,omitempty"`         // Organizational tags for filtering and categorization
	AvatarColor string   `json:"avatar_color,omitempty"` // Color for avatar display (hex color code)
	Favorite    bool     `json:"favorite,omitempty"`     // Whether this agent is marked as favorite
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
	ActivityEventPluginEnabled  ActivityEventType = "plugin_enabled"  // Plugin was enabled for the agent
	ActivityEventPluginDisabled ActivityEventType = "plugin_disabled" // Plugin was disabled for the agent
	ActivityEventStatusChanged  ActivityEventType = "status_changed"  // Agent status changed
)

// ActivityLog represents a single activity log entry for an agent
type ActivityLog struct {
	ID        string                 `json:"id"`                // Unique identifier for the log entry
	AgentName string                 `json:"agent_name"`        // Name of the agent this activity relates to
	EventType ActivityEventType      `json:"event_type"`        // Type of activity event
	Timestamp time.Time              `json:"timestamp"`         // When the activity occurred
	Details   map[string]interface{} `json:"details,omitempty"` // Additional event-specific details (JSON)
	User      string                 `json:"user,omitempty"`    // User who triggered the activity (if applicable)
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
