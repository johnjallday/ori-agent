package session

import (
	"context"
	"encoding/json"
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

func TestSQLiteStore_Workspaces(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create workspaces
	root := &Workspace{
		ID:    "root-workspace",
		Name:  "Root",
		Color: "#ff0000",
		SharedData: map[string]interface{}{
			"kanban_board": KanbanBoardConfig{
				Version: 1,
				Columns: []KanbanBoardColumn{
					{ID: "todo", Name: "Todo", Order: 1},
					{ID: "doing", Name: "Doing", Order: 2},
					{ID: "done", Name: "Done", Order: 3},
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	child := &Workspace{
		ID:        "child-workspace",
		Name:      "Child",
		ParentID:  "root-workspace",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := store.CreateWorkspace(ctx, root)
	if err != nil {
		t.Fatalf("Failed to create root workspace: %v", err)
	}

	err = store.CreateWorkspace(ctx, child)
	if err != nil {
		t.Fatalf("Failed to create child workspace: %v", err)
	}

	// Get workspace
	got, err := store.GetWorkspace(ctx, "root-workspace")
	if err != nil {
		t.Fatalf("Failed to get workspace: %v", err)
	}
	if got.Name != "Root" {
		t.Errorf("Expected name 'Root', got %s", got.Name)
	}
	board, ok := GetWorkspaceKanbanBoardConfig(got)
	if !ok {
		t.Fatalf("Expected kanban board config to persist")
	}
	if len(board.Columns) != 3 {
		t.Fatalf("Expected 3 columns, got %d", len(board.Columns))
	}
	if board.Columns[0].ID != "todo" {
		t.Fatalf("Expected first column id 'todo', got %s", board.Columns[0].ID)
	}

	// List workspaces
	workspaces, err := store.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("Failed to list workspaces: %v", err)
	}
	if len(workspaces) != 2 {
		t.Errorf("Expected 2 workspaces, got %d", len(workspaces))
	}

	// Get workspace tree
	tree, err := store.GetWorkspaceTree(ctx)
	if err != nil {
		t.Fatalf("Failed to get workspace tree: %v", err)
	}
	if len(tree) != 1 { // Only root
		t.Errorf("Expected 1 root workspace, got %d", len(tree))
	}
	if len(tree[0].Children) != 1 {
		t.Errorf("Expected 1 child workspace, got %d", len(tree[0].Children))
	}

	// Get subworkspace IDs
	subworkspaceIDs, err := store.GetSubworkspaceIDs(ctx, "root-workspace")
	if err != nil {
		t.Fatalf("Failed to get subworkspace IDs: %v", err)
	}
	if len(subworkspaceIDs) != 1 {
		t.Errorf("Expected 1 subworkspace ID, got %d", len(subworkspaceIDs))
	}
}

func TestSQLiteStore_WorkspaceImportMetadataPersistence(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()
	now := time.Now().UTC().Round(time.Second)

	initialRefsJSON, err := json.Marshal([]map[string]interface{}{
		{
			"id":           "dir-ref-1",
			"workspace_id": "import-workspace",
			"name":         "repo",
			"path":         "/tmp/repo",
			"x":            400,
			"y":            300,
			"created_at":   now.Format(time.RFC3339),
			"updated_at":   now.Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal initial directory references: %v", err)
	}

	workspace := &Workspace{
		ID:   "import-workspace",
		Name: "Imported Workspace",
		SharedData: map[string]interface{}{
			"folder_import": map[string]interface{}{
				"enabled":     true,
				"path":        "/tmp/repo",
				"path_hash":   "abc123:repo",
				"entry_point": "dashboard_button",
				"imported_at": now.Format(time.RFC3339),
			},
		},
		DirectoryReferencesJSON: initialRefsJSON,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("failed to create imported workspace: %v", err)
	}

	got, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("failed to fetch imported workspace: %v", err)
	}

	importMetaRaw, ok := got.SharedData["folder_import"]
	if !ok {
		t.Fatalf("expected folder_import metadata in shared_data")
	}

	importMeta, ok := importMetaRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected folder_import metadata to be a map, got %T", importMetaRaw)
	}
	if importMeta["path"] != "/tmp/repo" {
		t.Fatalf("expected folder_import.path to persist, got %#v", importMeta["path"])
	}
	if importMeta["entry_point"] != "dashboard_button" {
		t.Fatalf("expected folder_import.entry_point to persist, got %#v", importMeta["entry_point"])
	}

	var refs []map[string]interface{}
	if err := json.Unmarshal(got.DirectoryReferencesJSON, &refs); err != nil {
		t.Fatalf("failed to decode persisted directory references: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 persisted directory reference, got %d", len(refs))
	}
	if refs[0]["path"] != "/tmp/repo" {
		t.Fatalf("expected persisted directory reference path '/tmp/repo', got %#v", refs[0]["path"])
	}

	updatedRefsJSON, err := json.Marshal([]map[string]interface{}{
		{
			"id":           "dir-ref-1",
			"workspace_id": "import-workspace",
			"name":         "repo",
			"path":         "/tmp/repo",
		},
		{
			"id":           "dir-ref-2",
			"workspace_id": "import-workspace",
			"name":         "docs",
			"path":         "/tmp/docs",
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal updated directory references: %v", err)
	}

	got.SharedData["folder_import"] = map[string]interface{}{
		"enabled":         true,
		"path":            "/tmp/repo",
		"path_hash":       "abc123:repo",
		"entry_point":     "create_modal",
		"allow_duplicate": true,
		"imported_at":     now.Format(time.RFC3339),
	}
	got.DirectoryReferencesJSON = updatedRefsJSON
	got.UpdatedAt = time.Now().UTC().Round(time.Second)

	if err := store.UpdateWorkspace(ctx, got); err != nil {
		t.Fatalf("failed to update imported workspace metadata: %v", err)
	}

	updated, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated imported workspace: %v", err)
	}

	updatedMetaRaw, ok := updated.SharedData["folder_import"]
	if !ok {
		t.Fatalf("expected folder_import metadata after update")
	}
	updatedMeta, ok := updatedMetaRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected updated folder_import metadata to be a map, got %T", updatedMetaRaw)
	}

	if updatedMeta["entry_point"] != "create_modal" {
		t.Fatalf("expected updated entry_point to be create_modal, got %#v", updatedMeta["entry_point"])
	}
	if allow, ok := updatedMeta["allow_duplicate"].(bool); !ok || !allow {
		t.Fatalf("expected allow_duplicate=true, got %#v", updatedMeta["allow_duplicate"])
	}

	var updatedRefs []map[string]interface{}
	if err := json.Unmarshal(updated.DirectoryReferencesJSON, &updatedRefs); err != nil {
		t.Fatalf("failed to decode updated directory references: %v", err)
	}
	if len(updatedRefs) != 2 {
		t.Fatalf("expected 2 updated directory references, got %d", len(updatedRefs))
	}
}

func TestSQLiteStore_DeleteWorkspaceCascade(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create workspace and session in it
	workspace := &Workspace{
		ID:        "delete-workspace",
		Name:      "To Delete",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateWorkspace(ctx, workspace)

	session := &Session{
		ID:        "session-in-workspace",
		Title:     "Session",
		AgentName: "assistant",
		FolderID:  "delete-workspace",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateSession(ctx, session)

	// Delete workspace
	err := store.DeleteWorkspace(ctx, "delete-workspace")
	if err != nil {
		t.Fatalf("Failed to delete workspace: %v", err)
	}

	// Session should still exist but have no workspace
	got, err := store.GetSession(ctx, "session-in-workspace")
	if err != nil {
		t.Fatalf("Session should still exist: %v", err)
	}
	if got.FolderID != "" {
		t.Errorf("Expected empty workspace ID, got %s", got.FolderID)
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
	folder := &Workspace{
		ID:        "test-folder",
		Name:      "Test Folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateWorkspace(ctx, folder)

	note := &WorkspaceNote{
		ID:          "test-note-1",
		WorkspaceID: "test-folder",
		Name:        "Test Note",
		Content:     "This is test content for the note.",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
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
	if got.WorkspaceID != note.WorkspaceID {
		t.Errorf("Expected FolderID %s, got %s", note.WorkspaceID, got.WorkspaceID)
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
	folder := &Workspace{
		ID:        "update-folder",
		Name:      "Update Folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateWorkspace(ctx, folder)

	note := &WorkspaceNote{
		ID:          "update-note",
		WorkspaceID: "update-folder",
		Name:        "Original Name",
		Content:     "Original content",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
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
	folder1 := &Workspace{
		ID:        "folder-1",
		Name:      "Folder One",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	folder2 := &Workspace{
		ID:        "folder-2",
		Name:      "Folder Two",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateWorkspace(ctx, folder1)
	_ = store.CreateWorkspace(ctx, folder2)

	// Create note in folder 1
	note := &WorkspaceNote{
		ID:          "movable-note",
		WorkspaceID: "folder-1",
		Name:        "Movable Note",
		Content:     "This note will be moved",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	_ = store.CreateNote(ctx, note)

	// Verify note is in folder 1
	notes1, _ := store.ListNotesByWorkspace(ctx, "folder-1")
	if len(notes1) != 1 {
		t.Fatalf("Expected 1 note in folder-1, got %d", len(notes1))
	}

	// Move note to folder 2
	note.WorkspaceID = "folder-2"
	note.UpdatedAt = time.Now()
	err := store.UpdateNote(ctx, note)
	if err != nil {
		t.Fatalf("Failed to move note: %v", err)
	}

	// Verify note moved - folder 1 should be empty
	notes1After, _ := store.ListNotesByWorkspace(ctx, "folder-1")
	if len(notes1After) != 0 {
		t.Errorf("Expected 0 notes in folder-1 after move, got %d", len(notes1After))
	}

	// Verify note is now in folder 2
	notes2, _ := store.ListNotesByWorkspace(ctx, "folder-2")
	if len(notes2) != 1 {
		t.Errorf("Expected 1 note in folder-2, got %d", len(notes2))
	}

	// Verify note data integrity after move
	got, _ := store.GetNote(ctx, "movable-note")
	if got.WorkspaceID != "folder-2" {
		t.Errorf("Expected workspace_id 'folder-2', got '%s'", got.WorkspaceID)
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
	folder := &Workspace{
		ID:        "delete-note-folder",
		Name:      "Delete Folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateWorkspace(ctx, folder)

	note := &WorkspaceNote{
		ID:          "delete-note",
		WorkspaceID: "delete-note-folder",
		Name:        "To Be Deleted",
		Content:     "This will be deleted",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
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

func TestSQLiteStore_ListNotesByWorkspace(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewSQLiteStore(db)
	ctx := context.Background()

	// Create two folders
	folder1 := &Workspace{ID: "folder-1", Name: "Folder 1", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	folder2 := &Workspace{ID: "folder-2", Name: "Folder 2", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	_ = store.CreateWorkspace(ctx, folder1)
	_ = store.CreateWorkspace(ctx, folder2)

	// Create notes in folder 1
	for i := 0; i < 3; i++ {
		note := &WorkspaceNote{
			ID:          "note-f1-" + string(rune('a'+i)),
			WorkspaceID: "folder-1",
			Name:        "Note " + string(rune('A'+i)),
			Content:     "Content for note " + string(rune('A'+i)),
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Hour),
			UpdatedAt:   time.Now().Add(time.Duration(i) * time.Hour),
		}
		_ = store.CreateNote(ctx, note)
	}

	// Create one note in folder 2
	note := &WorkspaceNote{
		ID:          "note-f2-a",
		WorkspaceID: "folder-2",
		Name:        "Note in Folder 2",
		Content:     "Different folder",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	_ = store.CreateNote(ctx, note)

	// List notes by folder
	notes, err := store.ListNotesByWorkspace(ctx, "folder-1")
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
	notes2, _ := store.ListNotesByWorkspace(ctx, "folder-2")
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
	folder := &Workspace{
		ID:        "search-folder",
		Name:      "Search Folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateWorkspace(ctx, folder)

	// Create notes with different content
	notes := []WorkspaceNote{
		{ID: "search-1", WorkspaceID: "search-folder", Name: "Meeting Notes", Content: "Discussed project timeline and deliverables", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "search-2", WorkspaceID: "search-folder", Name: "Ideas", Content: "New feature ideas for the app", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "search-3", WorkspaceID: "search-folder", Name: "Project Plan", Content: "The project deadline is next month", CreatedAt: time.Now(), UpdatedAt: time.Now()},
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
	folder := &Workspace{
		ID:        "cascade-folder",
		Name:      "Cascade Test",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.CreateWorkspace(ctx, folder)

	note := &WorkspaceNote{
		ID:          "cascade-note",
		WorkspaceID: "cascade-folder",
		Name:        "Note to Cascade",
		Content:     "This should be deleted with folder",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	_ = store.CreateNote(ctx, note)

	// Delete workspace
	err := store.DeleteWorkspace(ctx, "cascade-folder")
	if err != nil {
		t.Fatalf("Failed to delete workspace: %v", err)
	}

	// Note should also be deleted (foreign key cascade)
	_, err = store.GetNote(ctx, "cascade-note")
	if err != ErrNoteNotFound {
		t.Error("Expected note to be deleted with folder")
	}
}
