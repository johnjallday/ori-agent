// Package review provides conversation analysis to detect problematic patterns
// like user retries, tool retry loops, and ignored errors.
package review

import (
	"time"
)

// IssueType categorizes the type of detected issue.
type IssueType string

const (
	// IssueTypeUserRetry indicates the user sent similar messages within a short time,
	// suggesting the previous response was unhelpful.
	IssueTypeUserRetry IssueType = "user_retry"

	// IssueTypeToolRetryLoop indicates the same tool was called repeatedly
	// with identical or near-identical arguments without success.
	IssueTypeToolRetryLoop IssueType = "tool_retry_loop"

	// IssueTypeIgnoredError indicates a tool error was followed by
	// the same tool call without any change in approach.
	IssueTypeIgnoredError IssueType = "ignored_error"
)

// Issue represents a detected problem in a conversation session.
type Issue struct {
	// ID is a unique identifier for the issue (UUID format).
	ID string `json:"id"`

	// SessionID is the session where this issue was detected.
	SessionID string `json:"session_id"`

	// AgentName is the agent associated with the session.
	AgentName string `json:"agent_name,omitempty"`

	// Type categorizes the issue.
	Type IssueType `json:"issue_type"`

	// ToolName is the tool involved (empty for user_retry issues).
	ToolName string `json:"tool_name,omitempty"`

	// OccurrenceCount is how many times the pattern was detected.
	OccurrenceCount int `json:"occurrence_count"`

	// FirstMessageID is the ID of the first message in the pattern.
	FirstMessageID string `json:"first_message_id"`

	// LastMessageID is the ID of the last message in the pattern.
	LastMessageID string `json:"last_message_id"`

	// ContextSummary is a brief description of what was attempted.
	ContextSummary string `json:"context_summary,omitempty"`

	// ContentHash is a hash for deduplication (arguments or user message).
	ContentHash string `json:"content_hash,omitempty"`

	// CreatedAt is when the issue was detected.
	CreatedAt time.Time `json:"created_at"`
}

// ReviewRunStatus indicates the state of a review job.
type ReviewRunStatus string

const (
	ReviewRunStatusRunning   ReviewRunStatus = "running"
	ReviewRunStatusCompleted ReviewRunStatus = "completed"
	ReviewRunStatusFailed    ReviewRunStatus = "failed"
)

// ReviewRun tracks the execution of a review job.
type ReviewRun struct {
	// ID is a unique identifier for the run (UUID format).
	ID string `json:"id"`

	// StartedAt is when the review started.
	StartedAt time.Time `json:"started_at"`

	// CompletedAt is when the review finished (zero if still running).
	CompletedAt time.Time `json:"completed_at,omitempty"`

	// SessionsReviewed is the count of sessions analyzed.
	SessionsReviewed int `json:"sessions_reviewed"`

	// IssuesFound is the count of issues detected.
	IssuesFound int `json:"issues_found"`

	// Status indicates the current state of the run.
	Status ReviewRunStatus `json:"status"`

	// ErrorMessage contains error details if the run failed.
	ErrorMessage string `json:"error_message,omitempty"`
}

// SessionReviewStatus tracks review progress for a session.
type SessionReviewStatus struct {
	// SessionID is the session being tracked.
	SessionID string `json:"session_id"`

	// LastReviewedAt is when the session was last reviewed.
	LastReviewedAt time.Time `json:"last_reviewed_at"`

	// LastMessageID is the last message included in review.
	// Used for incremental reviews to skip already-analyzed messages.
	LastMessageID string `json:"last_message_id,omitempty"`
}

// ReviewOptions configures what sessions to review.
type ReviewOptions struct {
	// AgentName filters to sessions for a specific agent.
	AgentName string `json:"agent_name,omitempty"`

	// SessionID reviews only a specific session.
	SessionID string `json:"session_id,omitempty"`

	// Since reviews sessions updated after this time.
	Since *time.Time `json:"since,omitempty"`

	// Sensitivity controls detection thresholds (low/medium/high).
	// Low = more lenient (5 retries), High = more strict (2 retries).
	Sensitivity string `json:"sensitivity,omitempty"`
}

// DetectionConfig holds thresholds for pattern detection.
type DetectionConfig struct {
	// UserRetryWindowSeconds is the time window for detecting user retries.
	// Default: 60 seconds.
	UserRetryWindowSeconds int

	// UserRetrySimilarityThreshold is the minimum similarity (0-1) to consider
	// messages as retries. Default: 0.7.
	UserRetrySimilarityThreshold float64

	// ToolRetryMinCount is the minimum number of identical tool calls
	// to flag as a retry loop. Default: 3.
	ToolRetryMinCount int
}

// DefaultDetectionConfig returns the default detection thresholds.
func DefaultDetectionConfig() DetectionConfig {
	return DetectionConfig{
		UserRetryWindowSeconds:       60,
		UserRetrySimilarityThreshold: 0.7,
		ToolRetryMinCount:            3,
	}
}

// ConfigForSensitivity returns detection config adjusted for the given sensitivity.
func ConfigForSensitivity(sensitivity string) DetectionConfig {
	config := DefaultDetectionConfig()

	switch sensitivity {
	case "low":
		config.UserRetryWindowSeconds = 30
		config.UserRetrySimilarityThreshold = 0.8
		config.ToolRetryMinCount = 5
	case "high":
		config.UserRetryWindowSeconds = 120
		config.UserRetrySimilarityThreshold = 0.5
		config.ToolRetryMinCount = 2
	default: // medium
		// Use defaults
	}

	return config
}
