package review

import (
	"context"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
)

// Runner orchestrates the review process.
type Runner struct {
	store        Store
	sessionStore session.SessionStore
	toolStore    session.ToolCallStore
	agentStore   store.Store
	config       DetectionConfig
	detector     *Detector

	mu      sync.RWMutex
	running map[string]*Run // Active runs by ID
}

// NewRunner creates a new review runner.
func NewRunner(reviewStore Store, sessionStore session.SessionStore, toolStore session.ToolCallStore, config DetectionConfig) *Runner {
	return &Runner{
		store:        reviewStore,
		sessionStore: sessionStore,
		toolStore:    toolStore,
		config:       config,
		detector:     NewDetector(config),
		running:      make(map[string]*Run),
	}
}

// SetAgentStore sets the agent store for checking per-agent review settings.
func (r *Runner) SetAgentStore(agentStore store.Store) {
	r.agentStore = agentStore
}

// StartReview begins a new review job asynchronously and returns the job ID.
func (r *Runner) StartReview(ctx context.Context, opts Options) (string, error) {
	// Create the review run record
	run, err := r.store.CreateReviewRun(ctx)
	if err != nil {
		return "", err
	}

	// Track as running
	r.mu.Lock()
	r.running[run.ID] = run
	r.mu.Unlock()

	// Start the review in a goroutine
	go r.executeReview(run.ID, opts)

	return run.ID, nil
}

// GetStatus returns the current status of a review job.
func (r *Runner) GetStatus(ctx context.Context, jobID string) (*Run, error) {
	// First check in-memory running jobs. Return a snapshot, not the live
	// pointer: executeReview keeps mutating the tracked run under r.mu, and
	// callers read the result without holding the lock.
	r.mu.RLock()
	if run, ok := r.running[jobID]; ok {
		snapshot := *run
		r.mu.RUnlock()
		return &snapshot, nil
	}
	r.mu.RUnlock()

	// Fall back to database
	return r.store.GetReviewRun(ctx, jobID)
}

// executeReview performs the actual review work.
func (r *Runner) executeReview(runID string, opts Options) {
	ctx := context.Background()

	// Get the run from our tracking map
	r.mu.RLock()
	run := r.running[runID]
	r.mu.RUnlock()

	defer func() {
		// Remove from running map when done
		r.mu.Lock()
		delete(r.running, runID)
		r.mu.Unlock()
	}()

	// Get sessions to review
	sessions, err := r.getSessionsToReview(ctx, opts)
	if err != nil {
		r.failRun(ctx, run, err)
		return
	}

	totalIssues := 0
	for _, sessItem := range sessions {
		// Check if we should skip this session (incremental review)
		if !r.shouldReviewSession(ctx, sessItem, opts) {
			continue
		}

		// Get the agent name from the session
		agentName := sessItem.AgentName
		if opts.AgentName != "" && agentName != opts.AgentName {
			continue
		}

		// Check agent-specific review settings
		sensitivity := r.getAgentSensitivity(agentName, opts.Sensitivity)
		if sensitivity == "" {
			// Review is disabled for this agent
			continue
		}

		// Review this session with agent-specific sensitivity
		issues, lastMsgID, err := r.reviewSessionWithSensitivity(ctx, sessItem.ID, agentName, sensitivity)
		if err != nil {
			// Log but continue with other sessions
			continue
		}

		// Store new issues (skip duplicates)
		for _, issue := range issues {
			existing, _ := r.store.GetIssueByHash(ctx, issue.SessionID, issue.ContentHash)
			if existing == nil {
				if err := r.store.AddIssue(ctx, &issue); err == nil {
					totalIssues++
				}
			}
		}

		// Update session review status
		if lastMsgID != "" {
			status := &SessionReviewStatus{
				SessionID:      sessItem.ID,
				LastReviewedAt: time.Now(),
				LastMessageID:  lastMsgID,
			}
			_ = r.store.UpdateSessionReviewStatus(ctx, status)
		}

		// Update run progress
		r.mu.Lock()
		run.SessionsReviewed++
		run.IssuesFound = totalIssues
		r.mu.Unlock()

		// Persist progress periodically
		_ = r.store.UpdateReviewRun(ctx, run)
	}

	// Mark as completed
	r.mu.Lock()
	run.Status = ReviewRunStatusCompleted
	run.CompletedAt = time.Now()
	run.IssuesFound = totalIssues
	r.mu.Unlock()

	_ = r.store.UpdateReviewRun(ctx, run)
}

// getSessionsToReview retrieves sessions matching the review options.
func (r *Runner) getSessionsToReview(ctx context.Context, opts Options) ([]session.SessionListItem, error) {
	// If specific session requested, just get that one
	if opts.SessionID != "" {
		sess, err := r.sessionStore.GetSession(ctx, opts.SessionID)
		if err != nil {
			return nil, err
		}
		if sess == nil {
			return []session.SessionListItem{}, nil
		}
		// Convert Session to SessionListItem
		item := session.SessionListItem{
			ID:           sess.ID,
			Title:        sess.Title,
			AgentName:    sess.AgentName,
			FolderID:     sess.FolderID,
			Tags:         sess.Tags,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt,
			UpdatedAt:    sess.UpdatedAt,
		}
		return []session.SessionListItem{item}, nil
	}

	// Build filter for agent if specified
	var filter *session.SessionFilter
	if opts.AgentName != "" {
		filter = &session.SessionFilter{
			AgentName: opts.AgentName,
		}
	}

	// Get sessions with pagination (fetch all for now)
	result, err := r.sessionStore.ListSessions(ctx, filter, &session.ListOptions{
		Limit: 1000, // Reasonable max for a single review run
	})
	if err != nil {
		return nil, err
	}

	return result.Sessions, nil
}

// shouldReviewSession determines if a session needs review (incremental).
func (r *Runner) shouldReviewSession(ctx context.Context, sess session.SessionListItem, opts Options) bool {
	// If Since is specified, check session activity
	if opts.Since != nil && sess.UpdatedAt.Before(*opts.Since) {
		return false
	}

	// Check if already reviewed up to the latest message
	status, err := r.store.GetSessionReviewStatus(ctx, sess.ID)
	if err != nil || status == nil {
		// Not reviewed before, should review
		return true
	}

	// Check if there are new messages since last review
	messages, err := r.sessionStore.GetMessages(ctx, sess.ID)
	if err != nil || len(messages) == 0 {
		return false
	}

	lastMsg := messages[len(messages)-1]
	return lastMsg.ID != status.LastMessageID
}

// failRun marks a run as failed with an error.
func (r *Runner) failRun(ctx context.Context, run *Run, err error) {
	r.mu.Lock()
	run.Status = ReviewRunStatusFailed
	run.CompletedAt = time.Now()
	run.ErrorMessage = err.Error()
	r.mu.Unlock()

	_ = r.store.UpdateReviewRun(ctx, run)
}

// getAgentSensitivity returns the sensitivity level for an agent.
// Returns "" if review is disabled for the agent.
// Falls back to defaultSensitivity if no agent-specific setting exists.
func (r *Runner) getAgentSensitivity(agentName, defaultSensitivity string) string {
	if r.agentStore == nil {
		// No agent store, use default
		if defaultSensitivity == "" {
			return "medium"
		}
		return defaultSensitivity
	}

	agent, found := r.agentStore.GetAgent(agentName)
	if !found || agent == nil {
		// Agent not found, use default
		if defaultSensitivity == "" {
			return "medium"
		}
		return defaultSensitivity
	}

	// Check agent metadata for review settings
	if agent.Metadata != nil {
		// Check if review is disabled
		if agent.Metadata.ReviewEnabled != nil && !*agent.Metadata.ReviewEnabled {
			return "" // Review disabled for this agent
		}

		// Check for agent-specific sensitivity
		if agent.Metadata.ReviewSensitivity != "" {
			return agent.Metadata.ReviewSensitivity
		}
	}

	// Fall back to default sensitivity
	if defaultSensitivity == "" {
		return "medium"
	}
	return defaultSensitivity
}

// reviewSessionWithSensitivity reviews a session using the specified sensitivity level.
func (r *Runner) reviewSessionWithSensitivity(ctx context.Context, sessionID, agentName, sensitivity string) ([]Issue, string, error) {
	var allIssues []Issue
	var lastMsgID string

	// Get messages for the session
	messages, err := r.sessionStore.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}

	if len(messages) > 0 {
		lastMsgID = messages[len(messages)-1].ID
	}

	// Create a detector with the appropriate sensitivity config
	config := ConfigForSensitivity(sensitivity)
	detector := NewDetector(config)

	// Detect user retries
	userRetryIssues := detector.DetectUserRetries(ctx, messages, agentName)
	allIssues = append(allIssues, userRetryIssues...)

	// Get tool calls for the session
	toolCalls, err := r.toolStore.GetToolCalls(ctx, sessionID)
	if err != nil {
		// Log but continue with user retry detection
		return allIssues, lastMsgID, nil
	}

	// Detect tool retry loops
	toolRetryIssues := detector.DetectToolRetryLoops(ctx, toolCalls, agentName)
	allIssues = append(allIssues, toolRetryIssues...)

	// Detect ignored errors
	ignoredErrorIssues := detector.DetectIgnoredErrors(ctx, toolCalls, agentName)
	allIssues = append(allIssues, ignoredErrorIssues...)

	return allIssues, lastMsgID, nil
}
