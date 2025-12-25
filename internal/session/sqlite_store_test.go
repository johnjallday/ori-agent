package session

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func setupTestDB(t *testing.T) (*database.DB, func()) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	return db, func() { db.Close() }
}

func TestSQLiteStore_CreateAndGetSession(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	session := &Session{
		ID:        "test-session-1",
		Title:     "Test Session",
		AgentName: "assistant",
		Tags:      []string{"test", "demo"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Create
	err := store.CreateSession(ctx, session)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Get
	got, err := store.GetSession(ctx, "test-session-1")
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if got.ID != session.ID {
		t.Errorf("Expected ID %s, got %s", session.ID, got.ID)
	}
	if got.Title != session.Title {
		t.Errorf("Expected title %s, got %s", session.Title, got.Title)
	}
	if got.AgentName != session.AgentName {
		t.Errorf("Expected agent %s, got %s", session.AgentName, got.AgentName)
	}
	if len(got.Tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(got.Tags))
	}
}

func TestSQLiteStore_SessionNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	_, err := store.GetSession(ctx, "nonexistent")
	if err != ErrSessionNotFound {
		t.Errorf("Expected ErrSessionNotFound, got %v", err)
	}
}

func TestSQLiteStore_UpdateSession(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	session := &Session{
		ID:        "update-test",
		Title:     "Original Title",
		AgentName: "assistant",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_ = store.CreateSession(ctx, session)

	// Update
	session.Title = "Updated Title"
	session.Tags = []string{"new-tag"}
	session.UpdatedAt = time.Now()

	err := store.UpdateSession(ctx, session)
	if err != nil {
		t.Fatalf("Failed to update session: %v", err)
	}

	got, _ := store.GetSession(ctx, "update-test")
	if got.Title != "Updated Title" {
		t.Errorf("Expected updated title, got %s", got.Title)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "new-tag" {
		t.Errorf("Expected updated tags, got %v", got.Tags)
	}
}

func TestSQLiteStore_DeleteSession(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	session := &Session{
		ID:        "delete-test",
		Title:     "To Be Deleted",
		AgentName: "assistant",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_ = store.CreateSession(ctx, session)

	err := store.DeleteSession(ctx, "delete-test")
	if err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	_, err = store.GetSession(ctx, "delete-test")
	if err != ErrSessionNotFound {
		t.Error("Expected session to be deleted")
	}
}

func TestSQLiteStore_ListSessions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create multiple sessions
	for i := 0; i < 5; i++ {
		session := &Session{
			ID:        string(rune('a' + i)),
			Title:     "Session " + string(rune('A'+i)),
			AgentName: "assistant",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Hour),
			UpdatedAt: time.Now().Add(time.Duration(i) * time.Hour),
		}
		_ = store.CreateSession(ctx, session)
	}

	// List with pagination
	result, err := store.ListSessions(ctx, nil, &ListOptions{Limit: 3})
	if err != nil {
		t.Fatalf("Failed to list sessions: %v", err)
	}

	if result.Total != 5 {
		t.Errorf("Expected total 5, got %d", result.Total)
	}
	if len(result.Sessions) != 3 {
		t.Errorf("Expected 3 sessions, got %d", len(result.Sessions))
	}
	if !result.HasMore {
		t.Error("Expected HasMore to be true")
	}
}

func TestSQLiteStore_ListSessionsWithFilter(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create sessions with different agents
	s1 := &Session{ID: "s1", Title: "S1", AgentName: "agent1", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s2 := &Session{ID: "s2", Title: "S2", AgentName: "agent2", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s3 := &Session{ID: "s3", Title: "S3", AgentName: "agent1", CreatedAt: time.Now(), UpdatedAt: time.Now()}

	_ = store.CreateSession(ctx, s1)
	_ = store.CreateSession(ctx, s2)
	_ = store.CreateSession(ctx, s3)

	// Filter by agent
	result, _ := store.ListSessions(ctx, &SessionFilter{AgentName: "agent1"}, nil)

	if result.Total != 2 {
		t.Errorf("Expected 2 sessions for agent1, got %d", result.Total)
	}
}

func TestSQLiteStore_Messages(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	session := &Session{
		ID:        "msg-test",
		Title:     "Message Test",
		AgentName: "assistant",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateSession(ctx, session)

	// Add messages
	msg1 := &Message{
		ID:        "msg-1",
		SessionID: "msg-test",
		Role:      RoleUser,
		Content:   "Hello",
		CreatedAt: time.Now(),
	}
	msg2 := &Message{
		ID:        "msg-2",
		SessionID: "msg-test",
		Role:      RoleAssistant,
		Content:   "Hi there!",
		Model:     "gpt-4",
		CreatedAt: time.Now().Add(time.Second),
	}

	err := store.AddMessage(ctx, "msg-test", msg1)
	if err != nil {
		t.Fatalf("Failed to add message 1: %v", err)
	}

	err = store.AddMessage(ctx, "msg-test", msg2)
	if err != nil {
		t.Fatalf("Failed to add message 2: %v", err)
	}

	// Get messages
	messages, err := store.GetMessages(ctx, "msg-test")
	if err != nil {
		t.Fatalf("Failed to get messages: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	// Messages should be in chronological order
	if messages[0].Content != "Hello" {
		t.Errorf("Expected first message to be 'Hello', got %s", messages[0].Content)
	}
	if messages[1].Content != "Hi there!" {
		t.Errorf("Expected second message to be 'Hi there!', got %s", messages[1].Content)
	}
}

func TestSQLiteStore_Tags(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	session := &Session{
		ID:        "tag-test",
		Title:     "Tag Test",
		AgentName: "assistant",
		Tags:      []string{"Important", "Work"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateSession(ctx, session)

	// Update tags (should normalize to lowercase)
	err := store.UpdateTags(ctx, "tag-test", []string{"Personal", "Important"})
	if err != nil {
		t.Fatalf("Failed to update tags: %v", err)
	}

	got, _ := store.GetSession(ctx, "tag-test")
	if len(got.Tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(got.Tags))
	}

	// Get all tags
	allTags, err := store.GetAllTags(ctx)
	if err != nil {
		t.Fatalf("Failed to get all tags: %v", err)
	}

	if len(allTags) != 2 {
		t.Errorf("Expected 2 unique tags, got %d", len(allTags))
	}
}

func TestSQLiteStore_Folders(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create folders
	root := &Folder{
		ID:        "root-folder",
		Name:      "Root",
		Color:     "#ff0000",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	child := &Folder{
		ID:        "child-folder",
		Name:      "Child",
		ParentID:  "root-folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := store.CreateFolder(ctx, root)
	if err != nil {
		t.Fatalf("Failed to create root folder: %v", err)
	}

	err = store.CreateFolder(ctx, child)
	if err != nil {
		t.Fatalf("Failed to create child folder: %v", err)
	}

	// Get folder
	got, err := store.GetFolder(ctx, "root-folder")
	if err != nil {
		t.Fatalf("Failed to get folder: %v", err)
	}
	if got.Name != "Root" {
		t.Errorf("Expected name 'Root', got %s", got.Name)
	}

	// List folders
	folders, err := store.ListFolders(ctx)
	if err != nil {
		t.Fatalf("Failed to list folders: %v", err)
	}
	if len(folders) != 2 {
		t.Errorf("Expected 2 folders, got %d", len(folders))
	}

	// Get folder tree
	tree, err := store.GetFolderTree(ctx)
	if err != nil {
		t.Fatalf("Failed to get folder tree: %v", err)
	}
	if len(tree) != 1 { // Only root
		t.Errorf("Expected 1 root folder, got %d", len(tree))
	}
	if len(tree[0].Children) != 1 {
		t.Errorf("Expected 1 child folder, got %d", len(tree[0].Children))
	}

	// Get subfolder IDs
	subfolderIDs, err := store.GetSubfolderIDs(ctx, "root-folder")
	if err != nil {
		t.Fatalf("Failed to get subfolder IDs: %v", err)
	}
	if len(subfolderIDs) != 1 {
		t.Errorf("Expected 1 subfolder ID, got %d", len(subfolderIDs))
	}
}

func TestSQLiteStore_DeleteFolderCascade(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create folder and session in it
	folder := &Folder{
		ID:        "delete-folder",
		Name:      "To Delete",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateFolder(ctx, folder)

	session := &Session{
		ID:        "session-in-folder",
		Title:     "Session",
		AgentName: "assistant",
		FolderID:  "delete-folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateSession(ctx, session)

	// Delete folder
	err := store.DeleteFolder(ctx, "delete-folder")
	if err != nil {
		t.Fatalf("Failed to delete folder: %v", err)
	}

	// Session should still exist but have no folder
	got, err := store.GetSession(ctx, "session-in-folder")
	if err != nil {
		t.Fatalf("Session should still exist: %v", err)
	}
	if got.FolderID != "" {
		t.Errorf("Expected empty folder ID, got %s", got.FolderID)
	}
}

func TestSQLiteStore_DuplicateID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	session := &Session{
		ID:        "duplicate",
		Title:     "First",
		AgentName: "assistant",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_ = store.CreateSession(ctx, session)

	// Try to create with same ID
	session.Title = "Second"
	err := store.CreateSession(ctx, session)
	if err != ErrDuplicateID {
		t.Errorf("Expected ErrDuplicateID, got %v", err)
	}
}

func TestSQLiteStore_EmptyID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	session := &Session{
		ID:        "",
		Title:     "No ID",
		AgentName: "assistant",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := store.CreateSession(ctx, session)
	if err != ErrInvalidID {
		t.Errorf("Expected ErrInvalidID for empty ID, got %v", err)
	}
}
