package session

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

// TestIntegration_SessionLifecycle tests the full lifecycle of a session:
// create -> add messages -> rename -> tag -> delete
func TestIntegration_SessionLifecycle(t *testing.T) {
	ctx := context.Background()
	store, cleanup := createTestStore(t)
	defer cleanup()

	// 1. Create a new session
	session := &Session{
		ID:        uuid.New().String(),
		Title:     "Test Session",
		AgentName: "test-agent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := store.CreateSession(ctx, session)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// 2. Add messages
	msg1 := &Message{
		ID:        uuid.New().String(),
		Role:      RoleUser,
		Content:   "Hello, this is my first message",
		CreatedAt: time.Now(),
	}
	err = store.AddMessage(ctx, session.ID, msg1)
	if err != nil {
		t.Fatalf("Failed to add message: %v", err)
	}

	msg2 := &Message{
		ID:        uuid.New().String(),
		Role:      RoleAssistant,
		Content:   "Hello! How can I help you today?",
		CreatedAt: time.Now(),
	}
	err = store.AddMessage(ctx, session.ID, msg2)
	if err != nil {
		t.Fatalf("Failed to add assistant message: %v", err)
	}

	// Verify messages
	messages, err := store.GetMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("Failed to get messages: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}

	// 3. Rename session
	retrieved, err := store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}
	retrieved.Title = "Renamed Session"
	err = store.UpdateSession(ctx, retrieved)
	if err != nil {
		t.Fatalf("Failed to update session: %v", err)
	}

	// Verify rename
	retrieved, err = store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("Failed to get session after rename: %v", err)
	}
	if retrieved.Title != "Renamed Session" {
		t.Errorf("Expected title 'Renamed Session', got '%s'", retrieved.Title)
	}

	// 4. Add tags
	err = store.UpdateTags(ctx, session.ID, []string{"important", "test"})
	if err != nil {
		t.Fatalf("Failed to update tags: %v", err)
	}

	// Verify tags
	retrieved, err = store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("Failed to get session after tagging: %v", err)
	}
	if len(retrieved.Tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(retrieved.Tags))
	}

	// 5. Delete session
	err = store.DeleteSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	// Verify deletion
	_, err = store.GetSession(ctx, session.ID)
	if err != ErrSessionNotFound {
		t.Errorf("Expected ErrSessionNotFound, got %v", err)
	}
}

// TestIntegration_WorkspaceOrganization tests workspace creation, nesting, and session organization
func TestIntegration_WorkspaceOrganization(t *testing.T) {
	ctx := context.Background()
	store, cleanup := createTestStore(t)
	defer cleanup()

	// 1. Create root workspace
	rootWorkspace := &Workspace{
		ID:        uuid.New().String(),
		Name:      "Projects",
		Color:     "#FF5733",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err := store.CreateWorkspace(ctx, rootWorkspace)
	if err != nil {
		t.Fatalf("Failed to create root workspace: %v", err)
	}

	// 2. Create nested workspace
	childWorkspace := &Workspace{
		ID:        uuid.New().String(),
		Name:      "Work Projects",
		ParentID:  rootWorkspace.ID,
		Color:     "#33FF57",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err = store.CreateWorkspace(ctx, childWorkspace)
	if err != nil {
		t.Fatalf("Failed to create child workspace: %v", err)
	}

	// 3. Create session in child workspace
	session := &Session{
		ID:        uuid.New().String(),
		Title:     "Project Discussion",
		AgentName: "assistant",
		FolderID:  childWorkspace.ID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err = store.CreateSession(ctx, session)
	if err != nil {
		t.Fatalf("Failed to create session in workspace: %v", err)
	}

	// 4. Verify workspaces exist
	workspaces, err := store.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("Failed to list workspaces: %v", err)
	}
	if len(workspaces) != 2 {
		t.Errorf("Expected 2 workspaces, got %d", len(workspaces))
	}

	// 5. Filter sessions by workspace
	filter := &SessionFilter{FolderID: &childWorkspace.ID}
	result, err := store.ListSessions(ctx, filter, nil)
	if err != nil {
		t.Fatalf("Failed to list sessions by workspace: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Expected 1 session in workspace, got %d", result.Total)
	}

	// 6. Delete workspace
	err = store.DeleteWorkspace(ctx, childWorkspace.ID)
	if err != nil {
		t.Fatalf("Failed to delete workspace: %v", err)
	}

	// Verify workspace was deleted
	_, err = store.GetWorkspace(ctx, childWorkspace.ID)
	if err != ErrWorkspaceNotFound {
		t.Errorf("Expected workspace to be deleted, got error: %v", err)
	}

	// Session should still exist (moved to root or orphaned)
	retrieved, err := store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("Session should still exist after workspace deletion: %v", err)
	}
	_ = retrieved // Session exists, workspace handling is implementation-specific
}

// TestIntegration_SearchAndFilter tests search and filter combinations
func TestIntegration_SearchAndFilter(t *testing.T) {
	ctx := context.Background()
	store, cleanup := createTestStore(t)
	defer cleanup()

	// Create test sessions
	sessions := []struct {
		title   string
		agent   string
		tags    []string
		message string
	}{
		{"Python Tutorial", "code-assistant", []string{"programming", "python"}, "How do I write a for loop in Python?"},
		{"Go Best Practices", "code-assistant", []string{"programming", "golang"}, "What are Go best practices for error handling?"},
		{"Recipe Ideas", "general-assistant", []string{"cooking"}, "Give me some dinner recipes"},
		{"Travel Planning", "general-assistant", []string{"travel"}, "Plan a trip to Japan"},
	}

	for _, s := range sessions {
		session := &Session{
			ID:        uuid.New().String(),
			Title:     s.title,
			AgentName: s.agent,
			Tags:      s.tags,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Add message
		msg := &Message{
			ID:        uuid.New().String(),
			Role:      RoleUser,
			Content:   s.message,
			CreatedAt: time.Now(),
		}
		if err := store.AddMessage(ctx, session.ID, msg); err != nil {
			t.Fatalf("Failed to add message: %v", err)
		}
	}

	// Test 1: Filter by agent
	filter := &SessionFilter{AgentName: "code-assistant"}
	result, err := store.ListSessions(ctx, filter, nil)
	if err != nil {
		t.Fatalf("Failed to filter by agent: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("Expected 2 sessions for code-assistant, got %d", result.Total)
	}

	// Test 2: Filter by tags
	filter = &SessionFilter{Tags: []string{"programming"}}
	result, err = store.ListSessions(ctx, filter, nil)
	if err != nil {
		t.Fatalf("Failed to filter by tags: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("Expected 2 sessions with 'programming' tag, got %d", result.Total)
	}

	// Test 3: Full-text search
	searchResults, total, err := store.Search(ctx, "Python", nil, nil)
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}
	if total < 1 {
		t.Errorf("Expected at least 1 result for 'Python' search, got %d", total)
	}
	_ = searchResults // Use the variable

	// Test 4: Combined filter + search
	filter = &SessionFilter{AgentName: "code-assistant"}
	_, total, err = store.Search(ctx, "error", filter, nil)
	if err != nil {
		t.Fatalf("Failed to search with filter: %v", err)
	}
	if total != 1 {
		t.Errorf("Expected 1 result for 'error' in code-assistant sessions, got %d", total)
	}
}

// TestIntegration_LRUEvictionAndRestore tests cache eviction and SQLite restore cycle
func TestIntegration_LRUEvictionAndRestore(t *testing.T) {
	ctx := context.Background()

	// Create store with small cache (3 sessions max)
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	store := NewHybridStoreWithDB(db, 3)
	defer func() { _ = store.Close() }()

	// Create 5 sessions (more than cache size)
	var sessionIDs []string
	for i := 0; i < 5; i++ {
		session := &Session{
			ID:        uuid.New().String(),
			Title:     "Session " + string(rune('A'+i)),
			AgentName: "test-agent",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("Failed to create session %d: %v", i, err)
		}
		sessionIDs = append(sessionIDs, session.ID)

		// Add a message to each
		msg := &Message{
			ID:        uuid.New().String(),
			Role:      RoleUser,
			Content:   "Message for session " + string(rune('A'+i)),
			CreatedAt: time.Now(),
		}
		if err := store.AddMessage(ctx, session.ID, msg); err != nil {
			t.Fatalf("Failed to add message: %v", err)
		}
	}

	// Flush to ensure all are persisted
	if err := store.FlushToStorage(ctx); err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Check that some sessions were evicted from cache
	stats := store.GetCacheStats()
	if stats.Size > 3 {
		t.Errorf("Expected cache size <= 3, got %d", stats.Size)
	}

	// Access the first session (should load from SQLite)
	session, err := store.GetSession(ctx, sessionIDs[0])
	if err != nil {
		t.Fatalf("Failed to get evicted session: %v", err)
	}
	if session.Title != "Session A" {
		t.Errorf("Expected 'Session A', got '%s'", session.Title)
	}

	// Verify messages were preserved
	messages, err := store.GetMessages(ctx, sessionIDs[0])
	if err != nil {
		t.Fatalf("Failed to get messages for restored session: %v", err)
	}
	if len(messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(messages))
	}
}

// TestIntegration_CleanupInactiveSessions tests the cleanup of inactive sessions
func TestIntegration_CleanupInactiveSessions(t *testing.T) {
	ctx := context.Background()
	store, cleanup := createTestStore(t)
	defer cleanup()

	hs, ok := store.(*hybridStore)
	if !ok {
		t.Fatal("Expected hybridStore type")
	}

	// Create an old session
	oldSession := &Session{
		Title:     "Old Session",
		AgentName: "test-agent",
		UpdatedAt: time.Now().AddDate(0, 0, -60), // 60 days ago
	}
	if err := store.CreateSession(ctx, oldSession); err != nil {
		t.Fatalf("Failed to create old session: %v", err)
	}

	// Update the timestamp directly in DB to simulate old session
	_, _ = hs.db.ExecContext(ctx, "UPDATE sessions SET updated_at = ? WHERE id = ?",
		time.Now().AddDate(0, 0, -60), oldSession.ID)

	// Create a recent session
	newSession := &Session{
		Title:     "New Session",
		AgentName: "test-agent",
	}
	if err := store.CreateSession(ctx, newSession); err != nil {
		t.Fatalf("Failed to create new session: %v", err)
	}

	// Run cleanup (this queries the database)
	deleted, err := store.Cleanup(ctx, 30)
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("Expected 1 deleted session, got %d", deleted)
	}

	// Verify old session is gone
	_, err = store.GetSession(ctx, oldSession.ID)
	if err != ErrSessionNotFound {
		t.Errorf("Expected ErrSessionNotFound for deleted session, got %v", err)
	}

	// Verify new session still exists
	_, err = store.GetSession(ctx, newSession.ID)
	if err != nil {
		t.Errorf("New session should still exist: %v", err)
	}
}

// TestIntegration_StorageStats tests storage statistics
func TestIntegration_StorageStats(t *testing.T) {
	ctx := context.Background()
	store, cleanup := createTestStore(t)
	defer cleanup()

	// Create sessions with messages
	for i := 0; i < 3; i++ {
		session := &Session{
			ID:        uuid.New().String(),
			Title:     "Test Session",
			AgentName: "test-agent",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Add messages
		for j := 0; j < 5; j++ {
			msg := &Message{
				ID:        uuid.New().String(),
				Role:      RoleUser,
				Content:   "Test message",
				CreatedAt: time.Now(),
			}
			if err := store.AddMessage(ctx, session.ID, msg); err != nil {
				t.Fatalf("Failed to add message: %v", err)
			}
		}
	}

	// Flush to ensure accurate stats
	if err := store.FlushToStorage(ctx); err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Get storage stats
	stats, err := store.GetStorageStats(ctx)
	if err != nil {
		t.Fatalf("Failed to get storage stats: %v", err)
	}

	if stats.TotalSessions != 3 {
		t.Errorf("Expected 3 sessions, got %d", stats.TotalSessions)
	}
	if stats.TotalMessages != 15 {
		t.Errorf("Expected 15 messages, got %d", stats.TotalMessages)
	}
	if stats.DatabaseSizeBytes <= 0 {
		t.Errorf("Expected positive database size, got %d", stats.DatabaseSizeBytes)
	}
}

// createTestStore creates a test hybrid store with in-memory database
func createTestStore(t *testing.T) (HybridStore, func()) {
	t.Helper()

	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	store := NewHybridStoreWithDB(db, 50)
	return store, func() {
		_ = store.Close()
	}
}
