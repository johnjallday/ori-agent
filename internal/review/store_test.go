package review

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func setupTestStore(t *testing.T) (*SQLiteStore, *database.DB, func()) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	store := NewSQLiteStore(db)
	return store, db, func() { db.Close() }
}

// createTestSession creates a session in the database for foreign key constraints
func createTestSession(t *testing.T, db *database.DB, sessionID, agentName string) {
	now := time.Now()
	_, err := db.Exec(`
		INSERT INTO sessions (id, title, agent_name, created_at, updated_at, message_count)
		VALUES (?, ?, ?, ?, ?, 0)
	`, sessionID, "Test Session", agentName, now, now)
	if err != nil {
		t.Fatalf("failed to create test session: %v", err)
	}
}

func TestAddAndGetIssue(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create session for foreign key
	createTestSession(t, db, "session-1", "test-agent")

	issue := &Issue{
		ID:              "test-issue-1",
		SessionID:       "session-1",
		AgentName:       "test-agent",
		Type:            IssueTypeUserRetry,
		OccurrenceCount: 3,
		FirstMessageID:  "msg-1",
		LastMessageID:   "msg-3",
		ContextSummary:  "User asked about weather",
		ContentHash:     "abc123",
		CreatedAt:       time.Now(),
	}

	// Add the issue
	err := store.AddIssue(ctx, issue)
	if err != nil {
		t.Fatalf("AddIssue failed: %v", err)
	}

	// Get it back
	issues, err := store.GetIssues(ctx, IssueQueryOptions{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("GetIssues failed: %v", err)
	}

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}

	got := issues[0]
	if got.ID != issue.ID {
		t.Errorf("ID = %q, want %q", got.ID, issue.ID)
	}
	if got.Type != IssueTypeUserRetry {
		t.Errorf("Type = %q, want %q", got.Type, IssueTypeUserRetry)
	}
	if got.OccurrenceCount != 3 {
		t.Errorf("OccurrenceCount = %d, want 3", got.OccurrenceCount)
	}
}

func TestGetIssueByHash(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create session for foreign key
	createTestSession(t, db, "session-1", "test-agent")

	issue := &Issue{
		ID:          "test-issue-1",
		SessionID:   "session-1",
		Type:        IssueTypeToolRetryLoop,
		ToolName:    "search",
		ContentHash: "unique-hash-123",
	}

	err := store.AddIssue(ctx, issue)
	if err != nil {
		t.Fatalf("AddIssue failed: %v", err)
	}

	// Find by hash
	found, err := store.GetIssueByHash(ctx, "session-1", "unique-hash-123")
	if err != nil {
		t.Fatalf("GetIssueByHash failed: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find issue, got nil")
	}
	if found.ID != issue.ID {
		t.Errorf("ID = %q, want %q", found.ID, issue.ID)
	}

	// Non-existent hash
	notFound, err := store.GetIssueByHash(ctx, "session-1", "nonexistent")
	if err != nil {
		t.Fatalf("GetIssueByHash failed: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for nonexistent hash")
	}
}

func TestGetIssuesWithFilters(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create sessions for foreign keys
	createTestSession(t, db, "s1", "agent1")
	createTestSession(t, db, "s2", "agent1")
	createTestSession(t, db, "s3", "agent2")
	createTestSession(t, db, "s4", "agent2")

	// Add issues for different agents and types
	issues := []Issue{
		{ID: "1", SessionID: "s1", AgentName: "agent1", Type: IssueTypeUserRetry},
		{ID: "2", SessionID: "s2", AgentName: "agent1", Type: IssueTypeToolRetryLoop},
		{ID: "3", SessionID: "s3", AgentName: "agent2", Type: IssueTypeUserRetry},
		{ID: "4", SessionID: "s4", AgentName: "agent2", Type: IssueTypeIgnoredError},
	}

	for _, issue := range issues {
		i := issue
		if err := store.AddIssue(ctx, &i); err != nil {
			t.Fatalf("AddIssue failed: %v", err)
		}
	}

	// Filter by agent
	byAgent, err := store.GetIssues(ctx, IssueQueryOptions{AgentName: "agent1"})
	if err != nil {
		t.Fatalf("GetIssues failed: %v", err)
	}
	if len(byAgent) != 2 {
		t.Errorf("expected 2 issues for agent1, got %d", len(byAgent))
	}

	// Filter by type
	byType, err := store.GetIssues(ctx, IssueQueryOptions{IssueType: IssueTypeUserRetry})
	if err != nil {
		t.Fatalf("GetIssues failed: %v", err)
	}
	if len(byType) != 2 {
		t.Errorf("expected 2 user_retry issues, got %d", len(byType))
	}

	// Limit results
	limited, err := store.GetIssues(ctx, IssueQueryOptions{Limit: 2})
	if err != nil {
		t.Fatalf("GetIssues failed: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("expected 2 issues with limit, got %d", len(limited))
	}
}

func TestReviewRunLifecycle(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create a run
	run, err := store.CreateReviewRun(ctx)
	if err != nil {
		t.Fatalf("CreateReviewRun failed: %v", err)
	}

	if run.ID == "" {
		t.Error("expected non-empty ID")
	}
	if run.Status != ReviewRunStatusRunning {
		t.Errorf("Status = %q, want %q", run.Status, ReviewRunStatusRunning)
	}

	// Update progress
	run.SessionsReviewed = 5
	run.IssuesFound = 3
	err = store.UpdateReviewRun(ctx, run)
	if err != nil {
		t.Fatalf("UpdateReviewRun failed: %v", err)
	}

	// Get and verify
	fetched, err := store.GetReviewRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetReviewRun failed: %v", err)
	}
	if fetched.SessionsReviewed != 5 {
		t.Errorf("SessionsReviewed = %d, want 5", fetched.SessionsReviewed)
	}

	// Complete the run
	run.Status = ReviewRunStatusCompleted
	run.CompletedAt = time.Now()
	err = store.UpdateReviewRun(ctx, run)
	if err != nil {
		t.Fatalf("UpdateReviewRun failed: %v", err)
	}

	// Verify completed
	completed, err := store.GetReviewRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetReviewRun failed: %v", err)
	}
	if completed.Status != ReviewRunStatusCompleted {
		t.Errorf("Status = %q, want %q", completed.Status, ReviewRunStatusCompleted)
	}
	if completed.CompletedAt.IsZero() {
		t.Error("expected CompletedAt to be set")
	}
}

func TestGetReviewRuns(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create multiple runs
	for i := 0; i < 5; i++ {
		_, err := store.CreateReviewRun(ctx)
		if err != nil {
			t.Fatalf("CreateReviewRun failed: %v", err)
		}
		// Small delay to ensure different timestamps
		time.Sleep(time.Millisecond)
	}

	// Get with limit
	runs, err := store.GetReviewRuns(ctx, 3)
	if err != nil {
		t.Fatalf("GetReviewRuns failed: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("expected 3 runs, got %d", len(runs))
	}
}

func TestSessionReviewStatus(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create session for foreign key
	createTestSession(t, db, "session-1", "test-agent")

	// Get non-existent (no review status yet, but session exists)
	status, err := store.GetSessionReviewStatus(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetSessionReviewStatus failed: %v", err)
	}
	if status != nil {
		t.Error("expected nil for non-existent session")
	}

	// Create status
	newStatus := &SessionReviewStatus{
		SessionID:      "session-1",
		LastReviewedAt: time.Now(),
		LastMessageID:  "msg-5",
	}
	err = store.UpdateSessionReviewStatus(ctx, newStatus)
	if err != nil {
		t.Fatalf("UpdateSessionReviewStatus failed: %v", err)
	}

	// Get and verify
	fetched, err := store.GetSessionReviewStatus(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetSessionReviewStatus failed: %v", err)
	}
	if fetched == nil {
		t.Fatal("expected status, got nil")
	}
	if fetched.LastMessageID != "msg-5" {
		t.Errorf("LastMessageID = %q, want %q", fetched.LastMessageID, "msg-5")
	}

	// Update (upsert)
	newStatus.LastMessageID = "msg-10"
	err = store.UpdateSessionReviewStatus(ctx, newStatus)
	if err != nil {
		t.Fatalf("UpdateSessionReviewStatus failed: %v", err)
	}

	// Verify update
	updated, err := store.GetSessionReviewStatus(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetSessionReviewStatus failed: %v", err)
	}
	if updated.LastMessageID != "msg-10" {
		t.Errorf("LastMessageID = %q, want %q", updated.LastMessageID, "msg-10")
	}
}
