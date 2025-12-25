package session

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func setupHybridStore(t *testing.T, cacheSize int) (HybridStore, func()) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	store := NewHybridStoreWithDB(db, cacheSize)
	return store, func() { store.Close() }
}

func TestHybridStore_CreateAndGet(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	session := &Session{
		Title:     "Test Session",
		AgentName: "assistant",
		Tags:      []string{"test"},
	}

	err := store.CreateSession(ctx, session)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	if session.ID == "" {
		t.Error("Expected session ID to be generated")
	}

	// Should be in cache
	if !store.IsSessionCached(session.ID) {
		t.Error("Expected session to be cached")
	}

	// Get should work
	got, err := store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if got.Title != "Test Session" {
		t.Errorf("Expected title 'Test Session', got %s", got.Title)
	}
}

func TestHybridStore_CacheEviction(t *testing.T) {
	store, cleanup := setupHybridStore(t, 3)
	defer cleanup()

	ctx := context.Background()

	// Create more sessions than cache can hold
	sessions := make([]*Session, 5)
	for i := range sessions {
		sessions[i] = &Session{
			Title:     "Session " + string(rune('A'+i)),
			AgentName: "assistant",
		}
		err := store.CreateSession(ctx, sessions[i])
		if err != nil {
			t.Fatalf("Failed to create session %d: %v", i, err)
		}
	}

	// First 2 sessions should have been evicted
	if store.IsSessionCached(sessions[0].ID) {
		t.Error("First session should have been evicted")
	}
	if store.IsSessionCached(sessions[1].ID) {
		t.Error("Second session should have been evicted")
	}

	// Last 3 should still be cached
	if !store.IsSessionCached(sessions[2].ID) {
		t.Error("Third session should be cached")
	}
	if !store.IsSessionCached(sessions[3].ID) {
		t.Error("Fourth session should be cached")
	}
	if !store.IsSessionCached(sessions[4].ID) {
		t.Error("Fifth session should be cached")
	}

	// Getting first session should reload it from SQLite
	got, err := store.GetSession(ctx, sessions[0].ID)
	if err != nil {
		t.Fatalf("Failed to get evicted session: %v", err)
	}
	if got.Title != "Session A" {
		t.Errorf("Expected 'Session A', got %s", got.Title)
	}

	// Now it should be cached again
	if !store.IsSessionCached(sessions[0].ID) {
		t.Error("Session should be cached after get")
	}
}

func TestHybridStore_AddMessage(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	session := &Session{
		Title:     "Message Test",
		AgentName: "assistant",
	}
	_ = store.CreateSession(ctx, session)

	// Add message
	msg := &Message{
		Role:    RoleUser,
		Content: "Hello, assistant!",
	}

	err := store.AddMessage(ctx, session.ID, msg)
	if err != nil {
		t.Fatalf("Failed to add message: %v", err)
	}

	if msg.ID == "" {
		t.Error("Expected message ID to be generated")
	}

	// Get messages
	messages, err := store.GetMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("Failed to get messages: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "Hello, assistant!" {
		t.Errorf("Expected message content to match")
	}
}

func TestHybridStore_AutoTitleGeneration(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	session := &Session{
		Title:     "New Session", // Default title
		AgentName: "assistant",
	}
	_ = store.CreateSession(ctx, session)

	// Add first message
	msg := &Message{
		Role:    RoleUser,
		Content: "What is the weather like today?",
	}
	_ = store.AddMessage(ctx, session.ID, msg)

	// Get session - title should be auto-generated
	got, _ := store.GetSession(ctx, session.ID)
	if got.Title == "New Session" {
		t.Error("Expected title to be auto-generated from first message")
	}
}

func TestHybridStore_TouchSession(t *testing.T) {
	store, cleanup := setupHybridStore(t, 3)
	defer cleanup()

	ctx := context.Background()

	// Create 3 sessions
	s1 := &Session{Title: "S1", AgentName: "a"}
	s2 := &Session{Title: "S2", AgentName: "a"}
	s3 := &Session{Title: "S3", AgentName: "a"}

	_ = store.CreateSession(ctx, s1)
	_ = store.CreateSession(ctx, s2)
	_ = store.CreateSession(ctx, s3)

	// Touch s1 to make it most recent
	err := store.TouchSession(ctx, s1.ID)
	if err != nil {
		t.Fatalf("Failed to touch session: %v", err)
	}

	// Create s4 - should evict s2 (not s1 because we touched it)
	s4 := &Session{Title: "S4", AgentName: "a"}
	_ = store.CreateSession(ctx, s4)

	if !store.IsSessionCached(s1.ID) {
		t.Error("s1 should still be cached after touch")
	}
	if store.IsSessionCached(s2.ID) {
		t.Error("s2 should have been evicted")
	}
}

func TestHybridStore_FlushToStorage(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	session := &Session{
		Title:     "Flush Test",
		AgentName: "assistant",
	}
	_ = store.CreateSession(ctx, session)

	// Modify title in cache
	got, _ := store.GetSession(ctx, session.ID)
	got.Title = "Modified Title"
	_ = store.UpdateSession(ctx, got)

	// Flush to storage
	err := store.FlushToStorage(ctx)
	if err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Verify it was persisted (would need to check database directly)
	// For now, just verify no error
}

func TestHybridStore_CacheStats(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	// Create some sessions
	for i := 0; i < 5; i++ {
		_ = store.CreateSession(ctx, &Session{Title: "S", AgentName: "a"})
	}

	stats := store.GetCacheStats()

	if stats.Size != 5 {
		t.Errorf("Expected size 5, got %d", stats.Size)
	}
	if stats.MaxSize != 10 {
		t.Errorf("Expected max size 10, got %d", stats.MaxSize)
	}
}

func TestHybridStore_Cleanup(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	// Create old session
	old := &Session{
		Title:     "Old Session",
		AgentName: "assistant",
		UpdatedAt: time.Now().AddDate(0, 0, -60), // 60 days ago
	}
	_ = store.CreateSession(ctx, old)
	// Manually update to old date
	_, _ = store.(*hybridStore).db.ExecContext(ctx, "UPDATE sessions SET updated_at = ? WHERE id = ?",
		time.Now().AddDate(0, 0, -60), old.ID)

	// Create new session
	new := &Session{
		Title:     "New Session",
		AgentName: "assistant",
	}
	_ = store.CreateSession(ctx, new)

	// Cleanup sessions older than 30 days
	deleted, err := store.Cleanup(ctx, 30)
	if err != nil {
		t.Fatalf("Failed to cleanup: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Expected 1 deleted, got %d", deleted)
	}

	// Old session should be gone
	_, err = store.GetSession(ctx, old.ID)
	if err != ErrSessionNotFound {
		t.Error("Expected old session to be deleted")
	}

	// New session should still exist
	_, err = store.GetSession(ctx, new.ID)
	if err != nil {
		t.Error("New session should still exist")
	}
}

func TestHybridStore_ListSessions(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	// Create sessions
	for i := 0; i < 10; i++ {
		_ = store.CreateSession(ctx, &Session{
			Title:     "Session " + string(rune('A'+i)),
			AgentName: "assistant",
		})
	}

	// List should return from SQLite
	result, err := store.ListSessions(ctx, nil, &ListOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Failed to list: %v", err)
	}

	if result.Total != 10 {
		t.Errorf("Expected total 10, got %d", result.Total)
	}
	if len(result.Sessions) != 5 {
		t.Errorf("Expected 5 sessions, got %d", len(result.Sessions))
	}
	if !result.HasMore {
		t.Error("Expected HasMore to be true")
	}
}

func TestHybridStore_Folders(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	folder := &Folder{
		Name:  "Work",
		Color: "#0000ff",
	}

	err := store.CreateFolder(ctx, folder)
	if err != nil {
		t.Fatalf("Failed to create folder: %v", err)
	}

	if folder.ID == "" {
		t.Error("Expected folder ID to be generated")
	}

	// Get folder
	got, err := store.GetFolder(ctx, folder.ID)
	if err != nil {
		t.Fatalf("Failed to get folder: %v", err)
	}

	if got.Name != "Work" {
		t.Errorf("Expected name 'Work', got %s", got.Name)
	}

	// List folders
	folders, err := store.ListFolders(ctx)
	if err != nil {
		t.Fatalf("Failed to list folders: %v", err)
	}

	if len(folders) != 1 {
		t.Errorf("Expected 1 folder, got %d", len(folders))
	}
}

func TestHybridStore_Tags(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()

	session := &Session{
		Title:     "Tagged Session",
		AgentName: "assistant",
		Tags:      []string{"important", "work"},
	}
	_ = store.CreateSession(ctx, session)

	// Update tags
	err := store.UpdateTags(ctx, session.ID, []string{"personal"})
	if err != nil {
		t.Fatalf("Failed to update tags: %v", err)
	}

	// Get all tags
	tags, err := store.GetAllTags(ctx)
	if err != nil {
		t.Fatalf("Failed to get tags: %v", err)
	}

	if len(tags) != 1 {
		t.Errorf("Expected 1 tag, got %d", len(tags))
	}
	if tags[0].Name != "personal" {
		t.Errorf("Expected 'personal', got %s", tags[0].Name)
	}
}
