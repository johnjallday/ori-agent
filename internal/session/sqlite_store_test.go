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
	return db, func() { _ = db.Close() }
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

// FolderNote Tests

func TestSQLiteStore_CreateAndGetNote(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create a folder first
	folder := &Folder{
		ID:        "test-folder",
		Name:      "Test Folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateFolder(ctx, folder)

	note := &FolderNote{
		ID:        "test-note-1",
		FolderID:  "test-folder",
		Name:      "Test Note",
		Content:   "This is test content for the note.",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Create
	err := store.CreateNote(ctx, note)
	if err != nil {
		t.Fatalf("Failed to create note: %v", err)
	}

	// Get
	got, err := store.GetNote(ctx, "test-note-1")
	if err != nil {
		t.Fatalf("Failed to get note: %v", err)
	}

	if got.ID != note.ID {
		t.Errorf("Expected ID %s, got %s", note.ID, got.ID)
	}
	if got.FolderID != note.FolderID {
		t.Errorf("Expected FolderID %s, got %s", note.FolderID, got.FolderID)
	}
	if got.Name != note.Name {
		t.Errorf("Expected Name %s, got %s", note.Name, got.Name)
	}
	if got.Content != note.Content {
		t.Errorf("Expected Content %s, got %s", note.Content, got.Content)
	}
}

func TestSQLiteStore_NoteNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	_, err := store.GetNote(ctx, "nonexistent")
	if err != ErrNoteNotFound {
		t.Errorf("Expected ErrNoteNotFound, got %v", err)
	}
}

func TestSQLiteStore_UpdateNote(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create folder and note
	folder := &Folder{
		ID:        "update-folder",
		Name:      "Update Folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateFolder(ctx, folder)

	note := &FolderNote{
		ID:        "update-note",
		FolderID:  "update-folder",
		Name:      "Original Name",
		Content:   "Original content",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateNote(ctx, note)

	// Update
	note.Name = "Updated Name"
	note.Content = "Updated content with more details"
	note.UpdatedAt = time.Now()

	err := store.UpdateNote(ctx, note)
	if err != nil {
		t.Fatalf("Failed to update note: %v", err)
	}

	got, _ := store.GetNote(ctx, "update-note")
	if got.Name != "Updated Name" {
		t.Errorf("Expected updated name, got %s", got.Name)
	}
	if got.Content != "Updated content with more details" {
		t.Errorf("Expected updated content, got %s", got.Content)
	}
}

func TestSQLiteStore_MoveNoteBetweenFolders(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create two folders
	folder1 := &Folder{
		ID:        "folder-1",
		Name:      "Folder One",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	folder2 := &Folder{
		ID:        "folder-2",
		Name:      "Folder Two",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateFolder(ctx, folder1)
	_ = store.CreateFolder(ctx, folder2)

	// Create note in folder 1
	note := &FolderNote{
		ID:        "movable-note",
		FolderID:  "folder-1",
		Name:      "Movable Note",
		Content:   "This note will be moved",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateNote(ctx, note)

	// Verify note is in folder 1
	notes1, _ := store.ListNotesByFolder(ctx, "folder-1")
	if len(notes1) != 1 {
		t.Fatalf("Expected 1 note in folder-1, got %d", len(notes1))
	}

	// Move note to folder 2
	note.FolderID = "folder-2"
	note.UpdatedAt = time.Now()
	err := store.UpdateNote(ctx, note)
	if err != nil {
		t.Fatalf("Failed to move note: %v", err)
	}

	// Verify note moved - folder 1 should be empty
	notes1After, _ := store.ListNotesByFolder(ctx, "folder-1")
	if len(notes1After) != 0 {
		t.Errorf("Expected 0 notes in folder-1 after move, got %d", len(notes1After))
	}

	// Verify note is now in folder 2
	notes2, _ := store.ListNotesByFolder(ctx, "folder-2")
	if len(notes2) != 1 {
		t.Errorf("Expected 1 note in folder-2, got %d", len(notes2))
	}

	// Verify note data integrity after move
	got, _ := store.GetNote(ctx, "movable-note")
	if got.FolderID != "folder-2" {
		t.Errorf("Expected folder_id 'folder-2', got '%s'", got.FolderID)
	}
	if got.Name != "Movable Note" {
		t.Errorf("Expected name preserved, got '%s'", got.Name)
	}
}

func TestSQLiteStore_DeleteNote(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create folder and note
	folder := &Folder{
		ID:        "delete-note-folder",
		Name:      "Delete Folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateFolder(ctx, folder)

	note := &FolderNote{
		ID:        "delete-note",
		FolderID:  "delete-note-folder",
		Name:      "To Be Deleted",
		Content:   "This will be deleted",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateNote(ctx, note)

	err := store.DeleteNote(ctx, "delete-note")
	if err != nil {
		t.Fatalf("Failed to delete note: %v", err)
	}

	_, err = store.GetNote(ctx, "delete-note")
	if err != ErrNoteNotFound {
		t.Error("Expected note to be deleted")
	}
}

func TestSQLiteStore_ListNotesByFolder(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create two folders
	folder1 := &Folder{ID: "folder-1", Name: "Folder 1", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	folder2 := &Folder{ID: "folder-2", Name: "Folder 2", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	_ = store.CreateFolder(ctx, folder1)
	_ = store.CreateFolder(ctx, folder2)

	// Create notes in folder 1
	for i := 0; i < 3; i++ {
		note := &FolderNote{
			ID:        "note-f1-" + string(rune('a'+i)),
			FolderID:  "folder-1",
			Name:      "Note " + string(rune('A'+i)),
			Content:   "Content for note " + string(rune('A'+i)),
			CreatedAt: time.Now().Add(time.Duration(i) * time.Hour),
			UpdatedAt: time.Now().Add(time.Duration(i) * time.Hour),
		}
		_ = store.CreateNote(ctx, note)
	}

	// Create one note in folder 2
	note := &FolderNote{
		ID:        "note-f2-a",
		FolderID:  "folder-2",
		Name:      "Note in Folder 2",
		Content:   "Different folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateNote(ctx, note)

	// List notes by folder
	notes, err := store.ListNotesByFolder(ctx, "folder-1")
	if err != nil {
		t.Fatalf("Failed to list notes: %v", err)
	}

	if len(notes) != 3 {
		t.Errorf("Expected 3 notes in folder-1, got %d", len(notes))
	}

	// Notes should be ordered by updated_at DESC
	if notes[0].Name != "Note C" {
		t.Errorf("Expected most recent note first, got %s", notes[0].Name)
	}

	// Check folder 2
	notes2, _ := store.ListNotesByFolder(ctx, "folder-2")
	if len(notes2) != 1 {
		t.Errorf("Expected 1 note in folder-2, got %d", len(notes2))
	}
}

func TestSQLiteStore_SearchNotes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create folder
	folder := &Folder{
		ID:        "search-folder",
		Name:      "Search Folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateFolder(ctx, folder)

	// Create notes with different content
	notes := []FolderNote{
		{ID: "search-1", FolderID: "search-folder", Name: "Meeting Notes", Content: "Discussed project timeline and deliverables", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "search-2", FolderID: "search-folder", Name: "Ideas", Content: "New feature ideas for the app", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "search-3", FolderID: "search-folder", Name: "Project Plan", Content: "The project deadline is next month", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	for _, n := range notes {
		note := n
		_ = store.CreateNote(ctx, &note)
	}

	// Search for "project" - should match 2 notes
	results, err := store.SearchNotes(ctx, "project", 10)
	if err != nil {
		t.Fatalf("Failed to search notes: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results for 'project', got %d", len(results))
	}

	// Search for "meeting" - should match 1 note
	results, err = store.SearchNotes(ctx, "meeting", 10)
	if err != nil {
		t.Fatalf("Failed to search notes: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result for 'meeting', got %d", len(results))
	}

	if results[0].Name != "Meeting Notes" {
		t.Errorf("Expected 'Meeting Notes', got %s", results[0].Name)
	}

	// Search for "nonexistent" - should match 0 notes
	results, err = store.SearchNotes(ctx, "nonexistent", 10)
	if err != nil {
		t.Fatalf("Failed to search notes: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results for 'nonexistent', got %d", len(results))
	}
}

func TestSQLiteStore_DeleteFolderCascadesNotes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create folder with notes
	folder := &Folder{
		ID:        "cascade-folder",
		Name:      "Cascade Test",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateFolder(ctx, folder)

	note := &FolderNote{
		ID:        "cascade-note",
		FolderID:  "cascade-folder",
		Name:      "Note to Cascade",
		Content:   "This should be deleted with folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateNote(ctx, note)

	// Delete folder
	err := store.DeleteFolder(ctx, "cascade-folder")
	if err != nil {
		t.Fatalf("Failed to delete folder: %v", err)
	}

	// Note should also be deleted (foreign key cascade)
	_, err = store.GetNote(ctx, "cascade-note")
	if err != ErrNoteNotFound {
		t.Error("Expected note to be deleted with folder")
	}
}
