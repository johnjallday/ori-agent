package session

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func setupTestToolCallStore(t *testing.T) (*SQLiteToolCallStore, func()) {
	t.Helper()

	ctx := context.Background()
	cfg := &database.Config{
		InMemory: true,
	}

	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	store := NewSQLiteToolCallStore(db)

	cleanup := func() {
		_ = db.Close()
	}

	return store, cleanup
}

func TestAddToolCall(t *testing.T) {
	store, cleanup := setupTestToolCallStore(t)
	defer cleanup()

	ctx := context.Background()

	// First create a session and message to satisfy foreign keys
	sqliteStore := NewSQLiteStore(store.db)
	session := &Session{
		ID:        "test-session-1",
		Title:     "Test Session",
		AgentName: "test-agent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sqliteStore.CreateSession(ctx, session); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	msg := &Message{
		ID:        "test-msg-1",
		Role:      RoleAssistant,
		Content:   "Test message",
		CreatedAt: time.Now(),
	}
	if err := sqliteStore.AddMessage(ctx, session.ID, msg); err != nil {
		t.Fatalf("failed to add message: %v", err)
	}

	// Now test adding a tool call
	tc := &ToolCall{
		ID:         "tc-1",
		MessageID:  msg.ID,
		SessionID:  session.ID,
		ToolName:   "test_tool",
		Arguments:  `{"arg1": "value1"}`,
		Result:     "success",
		DurationMs: 100,
		CreatedAt:  time.Now(),
	}

	err := store.AddToolCall(ctx, tc)
	if err != nil {
		t.Fatalf("AddToolCall failed: %v", err)
	}

	// Verify it was stored
	toolCalls, err := store.GetToolCalls(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetToolCalls failed: %v", err)
	}

	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}

	if toolCalls[0].ToolName != "test_tool" {
		t.Errorf("expected tool name 'test_tool', got %q", toolCalls[0].ToolName)
	}
	if toolCalls[0].Arguments != `{"arg1": "value1"}` {
		t.Errorf("expected arguments '{\"arg1\": \"value1\"}', got %q", toolCalls[0].Arguments)
	}
	if toolCalls[0].Result != "success" {
		t.Errorf("expected result 'success', got %q", toolCalls[0].Result)
	}
}

func TestAddToolCallWithError(t *testing.T) {
	store, cleanup := setupTestToolCallStore(t)
	defer cleanup()

	ctx := context.Background()

	// Setup session and message
	sqliteStore := NewSQLiteStore(store.db)
	session := &Session{
		ID:        "test-session-2",
		Title:     "Test Session",
		AgentName: "test-agent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sqliteStore.CreateSession(ctx, session); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	msg := &Message{
		ID:        "test-msg-2",
		Role:      RoleAssistant,
		Content:   "Test message",
		CreatedAt: time.Now(),
	}
	if err := sqliteStore.AddMessage(ctx, session.ID, msg); err != nil {
		t.Fatalf("failed to add message: %v", err)
	}

	// Add tool call with error
	tc := &ToolCall{
		MessageID:  msg.ID,
		SessionID:  session.ID,
		ToolName:   "failing_tool",
		Arguments:  `{"path": "/nonexistent"}`,
		Error:      "file not found",
		DurationMs: 50,
	}

	err := store.AddToolCall(ctx, tc)
	if err != nil {
		t.Fatalf("AddToolCall failed: %v", err)
	}

	// Verify
	toolCalls, err := store.GetToolCalls(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetToolCalls failed: %v", err)
	}

	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}

	if toolCalls[0].Error != "file not found" {
		t.Errorf("expected error 'file not found', got %q", toolCalls[0].Error)
	}
	if toolCalls[0].ID == "" {
		t.Error("expected auto-generated ID, got empty")
	}
}

func TestGetToolCallsByMessage(t *testing.T) {
	store, cleanup := setupTestToolCallStore(t)
	defer cleanup()

	ctx := context.Background()

	// Setup
	sqliteStore := NewSQLiteStore(store.db)
	session := &Session{
		ID:        "test-session-3",
		Title:     "Test Session",
		AgentName: "test-agent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sqliteStore.CreateSession(ctx, session); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	msg1 := &Message{ID: "msg-1", Role: RoleAssistant, Content: "Msg 1", CreatedAt: time.Now()}
	msg2 := &Message{ID: "msg-2", Role: RoleAssistant, Content: "Msg 2", CreatedAt: time.Now()}
	if err := sqliteStore.AddMessage(ctx, session.ID, msg1); err != nil {
		t.Fatalf("failed to add message 1: %v", err)
	}
	if err := sqliteStore.AddMessage(ctx, session.ID, msg2); err != nil {
		t.Fatalf("failed to add message 2: %v", err)
	}

	// Add tool calls for different messages
	tc1 := &ToolCall{ID: "tc-1", MessageID: msg1.ID, SessionID: session.ID, ToolName: "tool_a", CreatedAt: time.Now()}
	tc2 := &ToolCall{ID: "tc-2", MessageID: msg1.ID, SessionID: session.ID, ToolName: "tool_b", CreatedAt: time.Now()}
	tc3 := &ToolCall{ID: "tc-3", MessageID: msg2.ID, SessionID: session.ID, ToolName: "tool_c", CreatedAt: time.Now()}

	for _, tc := range []*ToolCall{tc1, tc2, tc3} {
		if err := store.AddToolCall(ctx, tc); err != nil {
			t.Fatalf("AddToolCall failed: %v", err)
		}
	}

	// Get by message 1
	toolCalls, err := store.GetToolCallsByMessage(ctx, msg1.ID)
	if err != nil {
		t.Fatalf("GetToolCallsByMessage failed: %v", err)
	}

	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls for msg1, got %d", len(toolCalls))
	}

	// Get by message 2
	toolCalls, err = store.GetToolCallsByMessage(ctx, msg2.ID)
	if err != nil {
		t.Fatalf("GetToolCallsByMessage failed: %v", err)
	}

	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call for msg2, got %d", len(toolCalls))
	}
}

func TestGetToolCallsByName(t *testing.T) {
	store, cleanup := setupTestToolCallStore(t)
	defer cleanup()

	ctx := context.Background()

	// Setup
	sqliteStore := NewSQLiteStore(store.db)
	session := &Session{
		ID:        "test-session-4",
		Title:     "Test Session",
		AgentName: "test-agent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sqliteStore.CreateSession(ctx, session); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	msg := &Message{ID: "msg-1", Role: RoleAssistant, Content: "Msg", CreatedAt: time.Now()}
	if err := sqliteStore.AddMessage(ctx, session.ID, msg); err != nil {
		t.Fatalf("failed to add message: %v", err)
	}

	// Add tool calls with different names
	toolCalls := []*ToolCall{
		{ID: "tc-1", MessageID: msg.ID, SessionID: session.ID, ToolName: "search", CreatedAt: time.Now()},
		{ID: "tc-2", MessageID: msg.ID, SessionID: session.ID, ToolName: "search", CreatedAt: time.Now()},
		{ID: "tc-3", MessageID: msg.ID, SessionID: session.ID, ToolName: "read_file", CreatedAt: time.Now()},
	}

	for _, tc := range toolCalls {
		if err := store.AddToolCall(ctx, tc); err != nil {
			t.Fatalf("AddToolCall failed: %v", err)
		}
	}

	// Get by name "search"
	results, err := store.GetToolCallsByName(ctx, session.ID, "search")
	if err != nil {
		t.Fatalf("GetToolCallsByName failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 'search' tool calls, got %d", len(results))
	}

	// Get by name "read_file"
	results, err = store.GetToolCallsByName(ctx, session.ID, "read_file")
	if err != nil {
		t.Fatalf("GetToolCallsByName failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 'read_file' tool call, got %d", len(results))
	}
}
