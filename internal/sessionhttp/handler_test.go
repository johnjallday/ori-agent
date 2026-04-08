package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// createTestHandler creates a handler with an in-memory store for testing.
func createTestHandler(t *testing.T) (*Handler, func()) {
	t.Helper()

	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	store := session.NewHybridStoreWithDB(db, 50)
	handler := New(store)
	agentStorePath := filepath.Join(t.TempDir(), "agents.json")
	agentStore, err := agentstore.NewFileStore(agentStorePath, types.Settings{})
	if err != nil {
		t.Fatalf("Failed to create test agent store: %v", err)
	}
	handler.SetAgentStore(agentStore)

	return handler, func() {
		_ = store.Close()
	}
}

func containsTag(tags []string, target string) bool {
	normalizedTarget := strings.TrimSpace(strings.ToLower(target))
	for _, tag := range tags {
		if strings.TrimSpace(strings.ToLower(tag)) == normalizedTarget {
			return true
		}
	}
	return false
}

// TestHandler_CreateSession tests session creation via API.
func TestHandler_CreateSession(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	body := `{"title": "Test Session", "agent_name": "test-agent"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessions(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if !resp["success"].(bool) {
		t.Error("Expected success to be true")
	}

	sess := resp["session"].(map[string]interface{})
	if sess["title"] != "Test Session" {
		t.Errorf("Expected title 'Test Session', got '%v'", sess["title"])
	}
	if sess["id"] == nil || sess["id"] == "" {
		t.Error("Expected session ID to be set")
	}
}

// TestHandler_CreateSessionWithoutAgent tests Assistant session creation without an agent.
func TestHandler_CreateSessionWithoutAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	body := `{"title": "Assistant Session"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessions(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if !resp["success"].(bool) {
		t.Error("Expected success to be true")
	}

	sess := resp["session"].(map[string]interface{})
	if sess["title"] != "Assistant Session" {
		t.Errorf("Expected title 'Assistant Session', got %v", sess["title"])
	}
	if sess["agent_name"] != "" {
		t.Errorf("Expected empty agent_name for Assistant session, got %v", sess["agent_name"])
	}
}

func TestHandler_CreateSessionInWorkspaceUsesEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Spain Manager", &agentstore.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("failed to create workspace agent: %v", err)
	}

	now := time.Now()
	ws := &session.Workspace{
		ID:     "workspace-spain",
		Name:   "Spain",
		Agents: []string{"Spain Manager"},
		AgentInstances: []session.AgentInstance{
			{
				ID:             "spain-manager-1",
				Name:           "Spain Manager",
				InstanceNumber: 1,
				NodeID:         "spain-manager-node-1",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
		SharedData: map[string]interface{}{
			workspaceEntryAgentNameKey: "Spain Manager",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	body := `{"title":"Spain Session","folder_id":"workspace-spain"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessions(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	sess := resp["session"].(map[string]interface{})
	if got := sess["agent_name"]; got != "Spain Manager" {
		t.Fatalf("expected workspace entry agent binding, got %#v", got)
	}
}

func TestHandler_CreateSessionInWorkspaceSkipsMissingEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	now := time.Now()
	ws := &session.Workspace{
		ID:     "workspace-stale-entry",
		Name:   "Spain",
		Agents: []string{"Workspace Manager"},
		AgentInstances: []session.AgentInstance{
			{
				ID:             "workspace-manager-1",
				Name:           "Workspace Manager",
				InstanceNumber: 1,
				NodeID:         "workspace-manager-node-1",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
		SharedData: map[string]interface{}{
			workspaceEntryAgentNameKey: "Workspace Manager",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	body := `{"title":"Spain Session","folder_id":"workspace-stale-entry"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessions(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	sess := resp["session"].(map[string]interface{})
	if got := sess["agent_name"]; got != "" {
		t.Fatalf("expected missing workspace entry agent to be ignored, got %#v", got)
	}
}

func TestHandler_CreateSessionInWorkspacePreservesExplicitAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	now := time.Now()
	ws := &session.Workspace{
		ID:     "workspace-explicit",
		Name:   "Spain",
		Agents: []string{"Spain Manager"},
		AgentInstances: []session.AgentInstance{
			{
				ID:             "spain-manager-1",
				Name:           "Spain Manager",
				InstanceNumber: 1,
				NodeID:         "spain-manager-node-1",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
		SharedData: map[string]interface{}{
			workspaceEntryAgentNameKey: "Spain Manager",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	body := `{"title":"Spain Session","folder_id":"workspace-explicit","agent_name":"Writer"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessions(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	sess := resp["session"].(map[string]interface{})
	if got := sess["agent_name"]; got != "Writer" {
		t.Fatalf("expected explicit agent binding to win, got %#v", got)
	}
}

// TestHandler_GetSession tests retrieving a session by ID.
func TestHandler_GetSession(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create a session first
	createBody := `{"title": "Get Test", "agent_name": "test-agent"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessions(createW, createReq)

	var createResp map[string]interface{}
	if err := json.Unmarshal(createW.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	sess := createResp["session"].(map[string]interface{})
	sessionID := sess["id"].(string)

	// Get the session
	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	getW := httptest.NewRecorder()
	handler.HandleSessions(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", getW.Code, getW.Body.String())
	}

	var getResp map[string]interface{}
	if err := json.Unmarshal(getW.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if getResp["title"] != "Get Test" {
		t.Errorf("Expected title 'Get Test', got '%v'", getResp["title"])
	}
}

// TestHandler_GetSessionNotFound tests 404 for non-existent session.
func TestHandler_GetSessionNotFound(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/non-existent-id", nil)
	w := httptest.NewRecorder()
	handler.HandleSessions(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

// TestHandler_UpdateSession tests updating session metadata.
func TestHandler_UpdateSession(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create a session
	createBody := `{"title": "Original", "agent_name": "test-agent"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessions(createW, createReq)

	var createResp map[string]interface{}
	if err := json.Unmarshal(createW.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	sess := createResp["session"].(map[string]interface{})
	sessionID := sess["id"].(string)

	// Update the session
	updateBody := `{"title": "Updated Title"}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sessionID, bytes.NewBufferString(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	handler.HandleSessions(updateW, updateReq)

	if updateW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	// Verify update
	var updateResp map[string]interface{}
	if err := json.Unmarshal(updateW.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	updatedSess := updateResp["session"].(map[string]interface{})
	if updatedSess["title"] != "Updated Title" {
		t.Errorf("Expected title 'Updated Title', got '%v'", updatedSess["title"])
	}
}

// TestHandler_DeleteSession tests session deletion.
func TestHandler_DeleteSession(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create a session
	createBody := `{"title": "To Delete", "agent_name": "test-agent"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessions(createW, createReq)

	var createResp map[string]interface{}
	if err := json.Unmarshal(createW.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	sess := createResp["session"].(map[string]interface{})
	sessionID := sess["id"].(string)

	// Delete the session
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sessionID, nil)
	deleteW := httptest.NewRecorder()
	handler.HandleSessions(deleteW, deleteReq)

	if deleteW.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", deleteW.Code)
	}

	// Verify deletion
	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	getW := httptest.NewRecorder()
	handler.HandleSessions(getW, getReq)

	if getW.Code != http.StatusNotFound {
		t.Errorf("Expected status 404 after deletion, got %d", getW.Code)
	}
}

// TestHandler_ListSessions tests listing sessions with pagination.
func TestHandler_ListSessions(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create multiple sessions
	for i := 0; i < 5; i++ {
		body := `{"title": "Session", "agent_name": "test-agent"}`
		req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleSessions(w, req)
	}

	// List sessions
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions?limit=3", nil)
	listW := httptest.NewRecorder()
	handler.HandleSessions(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", listW.Code)
	}

	var listResp map[string]interface{}
	if err := json.Unmarshal(listW.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	sessions := listResp["sessions"].([]interface{})
	if len(sessions) != 3 {
		t.Errorf("Expected 3 sessions (limit), got %d", len(sessions))
	}

	total := int(listResp["total"].(float64))
	if total != 5 {
		t.Errorf("Expected total of 5 sessions, got %d", total)
	}
}

// TestHandler_AddAndGetMessages tests message operations.
func TestHandler_AddAndGetMessages(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create a session
	createBody := `{"title": "Chat Session", "agent_name": "test-agent"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessions(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	sess := createResp["session"].(map[string]interface{})
	sessionID := sess["id"].(string)

	// Add a message
	msgBody := `{"role": "user", "content": "Hello, world!"}`
	msgReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/messages", bytes.NewBufferString(msgBody))
	msgReq.Header.Set("Content-Type", "application/json")
	msgW := httptest.NewRecorder()
	handler.HandleSessions(msgW, msgReq)

	if msgW.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", msgW.Code, msgW.Body.String())
	}

	// Get messages
	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/messages", nil)
	getW := httptest.NewRecorder()
	handler.HandleSessions(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", getW.Code)
	}

	var msgResp map[string]interface{}
	_ = json.Unmarshal(getW.Body.Bytes(), &msgResp)

	messages := msgResp["messages"].([]interface{})
	if len(messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(messages))
	}

	msg := messages[0].(map[string]interface{})
	if msg["content"] != "Hello, world!" {
		t.Errorf("Expected content 'Hello, world!', got '%v'", msg["content"])
	}
}

// TestHandler_MultiTab_IndependentSessions tests that multiple tabs can have independent sessions.
// This simulates the multi-tab behavior where each tab creates and manages its own session.
func TestHandler_MultiTab_IndependentSessions(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Simulate Tab 1 creating a session
	tab1Body := `{"title": "Tab 1 Session", "agent_name": "agent-tab1"}`
	tab1Req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(tab1Body))
	tab1Req.Header.Set("Content-Type", "application/json")
	tab1Req.Header.Set("X-Tab-ID", "tab-1-uuid")
	tab1W := httptest.NewRecorder()
	handler.HandleSessions(tab1W, tab1Req)

	var tab1Resp map[string]interface{}
	_ = json.Unmarshal(tab1W.Body.Bytes(), &tab1Resp)
	tab1Session := tab1Resp["session"].(map[string]interface{})
	tab1SessionID := tab1Session["id"].(string)

	// Simulate Tab 2 creating a different session
	tab2Body := `{"title": "Tab 2 Session", "agent_name": "agent-tab2"}`
	tab2Req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(tab2Body))
	tab2Req.Header.Set("Content-Type", "application/json")
	tab2Req.Header.Set("X-Tab-ID", "tab-2-uuid")
	tab2W := httptest.NewRecorder()
	handler.HandleSessions(tab2W, tab2Req)

	var tab2Resp map[string]interface{}
	_ = json.Unmarshal(tab2W.Body.Bytes(), &tab2Resp)
	tab2Session := tab2Resp["session"].(map[string]interface{})
	tab2SessionID := tab2Session["id"].(string)

	// Verify sessions are different
	if tab1SessionID == tab2SessionID {
		t.Error("Expected different session IDs for different tabs")
	}

	// Tab 1 adds a message to its session
	msg1Body := `{"role": "user", "content": "Message from Tab 1"}`
	msg1Req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+tab1SessionID+"/messages", bytes.NewBufferString(msg1Body))
	msg1Req.Header.Set("Content-Type", "application/json")
	msg1W := httptest.NewRecorder()
	handler.HandleSessions(msg1W, msg1Req)

	// Tab 2 adds a message to its session
	msg2Body := `{"role": "user", "content": "Message from Tab 2"}`
	msg2Req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+tab2SessionID+"/messages", bytes.NewBufferString(msg2Body))
	msg2Req.Header.Set("Content-Type", "application/json")
	msg2W := httptest.NewRecorder()
	handler.HandleSessions(msg2W, msg2Req)

	// Verify Tab 1's messages are only in Tab 1's session
	get1Req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+tab1SessionID+"/messages", nil)
	get1W := httptest.NewRecorder()
	handler.HandleSessions(get1W, get1Req)

	var msgs1Resp map[string]interface{}
	_ = json.Unmarshal(get1W.Body.Bytes(), &msgs1Resp)
	msgs1 := msgs1Resp["messages"].([]interface{})
	if len(msgs1) != 1 {
		t.Errorf("Tab 1 should have 1 message, got %d", len(msgs1))
	}
	if msgs1[0].(map[string]interface{})["content"] != "Message from Tab 1" {
		t.Error("Tab 1's message content doesn't match")
	}

	// Verify Tab 2's messages are only in Tab 2's session
	get2Req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+tab2SessionID+"/messages", nil)
	get2W := httptest.NewRecorder()
	handler.HandleSessions(get2W, get2Req)

	var msgs2Resp map[string]interface{}
	_ = json.Unmarshal(get2W.Body.Bytes(), &msgs2Resp)
	msgs2 := msgs2Resp["messages"].([]interface{})
	if len(msgs2) != 1 {
		t.Errorf("Tab 2 should have 1 message, got %d", len(msgs2))
	}
	if msgs2[0].(map[string]interface{})["content"] != "Message from Tab 2" {
		t.Error("Tab 2's message content doesn't match")
	}
}

// TestHandler_MultiTab_SessionSwitching tests that tabs can switch between sessions.
func TestHandler_MultiTab_SessionSwitching(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create two sessions
	session1Body := `{"title": "Session A", "agent_name": "test-agent"}`
	session1Req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(session1Body))
	session1Req.Header.Set("Content-Type", "application/json")
	session1W := httptest.NewRecorder()
	handler.HandleSessions(session1W, session1Req)

	var session1Resp map[string]interface{}
	_ = json.Unmarshal(session1W.Body.Bytes(), &session1Resp)
	sessionA := session1Resp["session"].(map[string]interface{})
	sessionAID := sessionA["id"].(string)

	session2Body := `{"title": "Session B", "agent_name": "test-agent"}`
	session2Req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(session2Body))
	session2Req.Header.Set("Content-Type", "application/json")
	session2W := httptest.NewRecorder()
	handler.HandleSessions(session2W, session2Req)

	var session2Resp map[string]interface{}
	_ = json.Unmarshal(session2W.Body.Bytes(), &session2Resp)
	sessionB := session2Resp["session"].(map[string]interface{})
	sessionBID := sessionB["id"].(string)

	// Add messages to Session A
	for i := 0; i < 3; i++ {
		msgBody := `{"role": "user", "content": "Session A message"}`
		msgReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionAID+"/messages", bytes.NewBufferString(msgBody))
		msgReq.Header.Set("Content-Type", "application/json")
		msgW := httptest.NewRecorder()
		handler.HandleSessions(msgW, msgReq)
	}

	// Add messages to Session B
	for i := 0; i < 2; i++ {
		msgBody := `{"role": "user", "content": "Session B message"}`
		msgReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionBID+"/messages", bytes.NewBufferString(msgBody))
		msgReq.Header.Set("Content-Type", "application/json")
		msgW := httptest.NewRecorder()
		handler.HandleSessions(msgW, msgReq)
	}

	// Simulate tab switching: get messages from Session A
	getAReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionAID+"/messages", nil)
	getAW := httptest.NewRecorder()
	handler.HandleSessions(getAW, getAReq)

	var msgsARep map[string]interface{}
	_ = json.Unmarshal(getAW.Body.Bytes(), &msgsARep)
	msgsA := msgsARep["messages"].([]interface{})
	if len(msgsA) != 3 {
		t.Errorf("Session A should have 3 messages, got %d", len(msgsA))
	}

	// Simulate tab switching: get messages from Session B
	getBReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionBID+"/messages", nil)
	getBW := httptest.NewRecorder()
	handler.HandleSessions(getBW, getBReq)

	var msgsBRep map[string]interface{}
	_ = json.Unmarshal(getBW.Body.Bytes(), &msgsBRep)
	msgsB := msgsBRep["messages"].([]interface{})
	if len(msgsB) != 2 {
		t.Errorf("Session B should have 2 messages, got %d", len(msgsB))
	}
}

// TestHandler_MultiTab_ConcurrentUpdates tests concurrent updates from multiple tabs.
func TestHandler_MultiTab_ConcurrentUpdates(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create a shared session
	createBody := `{"title": "Shared Session", "agent_name": "test-agent"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessions(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	sess := createResp["session"].(map[string]interface{})
	sessionID := sess["id"].(string)

	// Simulate concurrent message additions from different "tabs"
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			msgBody := `{"role": "user", "content": "Concurrent message"}`
			msgReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/messages", bytes.NewBufferString(msgBody))
			msgReq.Header.Set("Content-Type", "application/json")
			msgW := httptest.NewRecorder()
			handler.HandleSessions(msgW, msgReq)
			done <- msgW.Code == http.StatusCreated
		}(i)
	}

	// Wait for all goroutines
	successCount := 0
	for i := 0; i < 10; i++ {
		if <-done {
			successCount++
		}
	}

	if successCount != 10 {
		t.Errorf("Expected all 10 concurrent messages to succeed, got %d", successCount)
	}

	// Verify all messages were saved
	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/messages", nil)
	getW := httptest.NewRecorder()
	handler.HandleSessions(getW, getReq)

	var msgsResp map[string]interface{}
	_ = json.Unmarshal(getW.Body.Bytes(), &msgsResp)
	msgs := msgsResp["messages"].([]interface{})
	if len(msgs) != 10 {
		t.Errorf("Expected 10 messages after concurrent writes, got %d", len(msgs))
	}
}

// TestHandler_Tags tests tag operations.
func TestHandler_Tags(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create a session with tags
	createBody := `{"title": "Tagged Session", "agent_name": "test-agent", "tags": ["important", "work"]}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessions(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	sess := createResp["session"].(map[string]interface{})
	sessionID := sess["id"].(string)

	// Update tags
	updateBody := `{"tags": ["urgent", "priority"]}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sessionID+"/tags", bytes.NewBufferString(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	handler.HandleSessionTags(updateW, updateReq)

	if updateW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	// Get all tags
	tagsReq := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	tagsW := httptest.NewRecorder()
	handler.HandleTags(tagsW, tagsReq)

	if tagsW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", tagsW.Code)
	}
}

// TestHandler_FilterByAgent tests filtering sessions by agent name.
func TestHandler_FilterByAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create sessions with different agents
	agents := []string{"agent-a", "agent-a", "agent-b", "agent-c"}
	for _, agent := range agents {
		body := `{"title": "Session", "agent_name": "` + agent + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleSessions(w, req)
	}

	// Filter by agent-a
	filterReq := httptest.NewRequest(http.MethodGet, "/api/sessions?agent_name=agent-a", nil)
	filterW := httptest.NewRecorder()
	handler.HandleSessions(filterW, filterReq)

	if filterW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", filterW.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(filterW.Body.Bytes(), &resp)
	sessions := resp["sessions"].([]interface{})

	if len(sessions) != 2 {
		t.Errorf("Expected 2 sessions for agent-a, got %d", len(sessions))
	}
}

// TestHandler_Search tests full-text search.
func TestHandler_Search(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create sessions with specific content
	sessions := []struct {
		title string
		msg   string
	}{
		{"Python Help", "How do I use decorators in Python?"},
		{"Go Tutorial", "Understanding goroutines and channels"},
		{"Python Basics", "What is a list comprehension?"},
	}

	for _, s := range sessions {
		// Create session
		createBody := `{"title": "` + s.title + `", "agent_name": "test-agent"}`
		createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(createBody))
		createReq.Header.Set("Content-Type", "application/json")
		createW := httptest.NewRecorder()
		handler.HandleSessions(createW, createReq)

		var resp map[string]interface{}
		_ = json.Unmarshal(createW.Body.Bytes(), &resp)
		sess := resp["session"].(map[string]interface{})
		sessionID := sess["id"].(string)

		// Add message
		msgBody := `{"role": "user", "content": "` + s.msg + `"}`
		msgReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/messages", bytes.NewBufferString(msgBody))
		msgReq.Header.Set("Content-Type", "application/json")
		msgW := httptest.NewRecorder()
		handler.HandleSessions(msgW, msgReq)
	}

	// Wait a moment for indexing
	time.Sleep(50 * time.Millisecond)

	// Search for "Python"
	searchReq := httptest.NewRequest(http.MethodGet, "/api/sessions?q=Python", nil)
	searchW := httptest.NewRecorder()
	handler.HandleSessions(searchW, searchReq)

	if searchW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", searchW.Code, searchW.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(searchW.Body.Bytes(), &resp)

	total := int(resp["total"].(float64))
	if total < 2 {
		t.Errorf("Expected at least 2 results for 'Python' search, got %d", total)
	}
}

// TestHandler_CacheStats tests cache statistics endpoint.
func TestHandler_CacheStats(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	// Create some sessions
	for i := 0; i < 5; i++ {
		body := `{"title": "Session", "agent_name": "test-agent"}`
		req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleSessions(w, req)
	}

	// Get cache stats
	statsReq := httptest.NewRequest(http.MethodGet, "/api/sessions/cache/stats", nil)
	statsW := httptest.NewRecorder()
	handler.HandleCacheStats(statsW, statsReq)

	if statsW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", statsW.Code)
	}

	var stats map[string]interface{}
	_ = json.Unmarshal(statsW.Body.Bytes(), &stats)

	size := int(stats["size"].(float64))
	if size != 5 {
		t.Errorf("Expected cache size of 5, got %d", size)
	}
}

// FolderNote Handler Tests

// createTestWorkspace creates a workspace and returns its ID.
func createTestWorkspace(t *testing.T, handler *Handler, name string) string {
	t.Helper()
	body := `{"name": "` + name + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Failed to create workspace: %d - %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	// Check for both "workspace" and "folder" keys for backward compatibility
	workspace, ok := resp["workspace"].(map[string]interface{})
	if !ok {
		workspace = resp["folder"].(map[string]interface{})
	}
	return workspace["id"].(string)
}

func TestHandler_CreateWorkspaceWithoutEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	body := `{"name":"Trip Planning"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	workspacePayload := resp["folder"].(map[string]interface{})
	workspaceID := workspacePayload["id"].(string)

	ws, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("failed to load created workspace: %v", err)
	}

	// Without an entry_agent_name, no entry agent should be set — the UI will prompt the user.
	if got := currentWorkspaceEntryAgentName(ws); got != "" {
		t.Fatalf("entry agent = %q, want empty", got)
	}
	if len(ws.AgentInstances) != 0 {
		t.Fatalf("expected no agent instances, got %d", len(ws.AgentInstances))
	}
}

func TestHandler_CreateWorkspacePersistsWorkspaceBootstrap(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	body := `{"name":"Launch Ops","workspace_bootstrap":{"goal":"Ship the launch deck","systems":"Keynote, Finder, Google Drive","capabilities":"Create slides and collect source assets","context":"Brand guide lives in /Launch/Brand"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	workspacePayload := resp["folder"].(map[string]interface{})
	workspaceID := workspacePayload["id"].(string)

	ws, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("failed to load created workspace: %v", err)
	}

	bootstrapRaw, ok := ws.SharedData["workspace_bootstrap"]
	if !ok {
		t.Fatalf("expected workspace_bootstrap in shared_data")
	}
	bootstrapMap, ok := bootstrapRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected workspace_bootstrap to be an object, got %T", bootstrapRaw)
	}
	if bootstrapMap["goal"] != "Ship the launch deck" {
		t.Fatalf("expected goal to persist, got %#v", bootstrapMap["goal"])
	}
	if bootstrapMap["systems"] != "Keynote, Finder, Google Drive" {
		t.Fatalf("expected systems to persist, got %#v", bootstrapMap["systems"])
	}
	if bootstrapMap["capabilities"] != "Create slides and collect source assets" {
		t.Fatalf("expected capabilities to persist, got %#v", bootstrapMap["capabilities"])
	}
	if bootstrapMap["context"] != "Brand guide lives in /Launch/Brand" {
		t.Fatalf("expected context to persist, got %#v", bootstrapMap["context"])
	}
	systemsList, ok := bootstrapMap["systems_list"].([]interface{})
	if !ok {
		t.Fatalf("expected systems_list to be a slice, got %T", bootstrapMap["systems_list"])
	}
	if len(systemsList) != 3 {
		t.Fatalf("expected 3 systems hints, got %d (%#v)", len(systemsList), systemsList)
	}
}

func TestHandler_CreateWorkspaceUsesExplicitEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	if err := handler.agentStore.CreateAgent("Reusable Manager", &agentstore.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("failed to create reusable agent: %v", err)
	}

	body := `{"name":"Portfolio","entry_agent_name":"Reusable Manager"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	workspacePayload := resp["folder"].(map[string]interface{})
	workspaceID := workspacePayload["id"].(string)

	ws, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("failed to load created workspace: %v", err)
	}

	if got := currentWorkspaceEntryAgentName(ws); got != "Reusable Manager" {
		t.Fatalf("entry agent = %q, want %q", got, "Reusable Manager")
	}
	if _, ok := handler.agentStore.GetAgent("Portfolio Manager"); ok {
		t.Fatal("did not expect default workspace manager to be auto-created when explicit entry agent is provided")
	}
}

func TestEnsureWorkspaceEntryAgent_CreatesWorkspaceManagerAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	agentName, created, err := handler.ensureWorkspaceEntryAgent("Spain", "")
	if err != nil {
		t.Fatalf("failed to ensure workspace entry agent: %v", err)
	}
	if !created {
		t.Fatal("expected workspace entry agent to be created")
	}
	if agentName != "Spain Manager" {
		t.Fatalf("expected default agent name %q, got %q", "Spain Manager", agentName)
	}

	ag, ok := handler.agentStore.GetAgent(agentName)
	if !ok || ag == nil {
		t.Fatalf("expected created agent %q to exist", agentName)
	}
	if ag.Type != "workspace-manager" {
		t.Fatalf("expected workspace-manager type, got %q", ag.Type)
	}
	if ag.Role != types.RoleOrchestrator {
		t.Fatalf("expected orchestrator role, got %q", ag.Role)
	}
	if ag.Metadata == nil {
		t.Fatal("expected metadata for workspace manager")
	}
	if !strings.Contains(ag.Metadata.Description, "Coordinate workspace tasks") {
		t.Fatalf("unexpected metadata description: %q", ag.Metadata.Description)
	}
	if !containsTag(ag.Metadata.Tags, "workspace-manager") || !containsTag(ag.Metadata.Tags, "orchestrator") {
		t.Fatalf("expected workspace-manager/orchestrator tags, got %#v", ag.Metadata.Tags)
	}
	if !strings.Contains(ag.Settings.SystemPrompt, "workspace manager") {
		t.Fatalf("expected workspace-manager prompt, got %q", ag.Settings.SystemPrompt)
	}
	if !strings.Contains(ag.Settings.SystemPrompt, "ask the user before adding or switching to a specialist") {
		t.Fatalf("expected specialist confirmation guidance in prompt, got %q", ag.Settings.SystemPrompt)
	}
	if !strings.Contains(ag.Settings.SystemPrompt, "do not generate an itinerary or recommendations on the first reply") {
		t.Fatalf("expected strict travel intake guidance in prompt, got %q", ag.Settings.SystemPrompt)
	}
	if !strings.Contains(ag.Settings.SystemPrompt, "default to orchestration for full travel-planning work") {
		t.Fatalf("expected specialist-first travel orchestration guidance in prompt, got %q", ag.Settings.SystemPrompt)
	}
}

// TestHandler_CreateNote tests creating a note via POST /api/notes.
func TestHandler_CreateNote(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "Test Folder")

	body := `{"workspace_id": "` + folderID + `", "name": "Test Note", "content": "Note content here"}`
	req := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleNotes(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if !resp["success"].(bool) {
		t.Error("Expected success to be true")
	}

	note := resp["note"].(map[string]interface{})
	if note["name"] != "Test Note" {
		t.Errorf("Expected name 'Test Note', got '%v'", note["name"])
	}
	if note["content"] != "Note content here" {
		t.Errorf("Expected content 'Note content here', got '%v'", note["content"])
	}
	if note["id"] == nil || note["id"] == "" {
		t.Error("Expected note ID to be set")
	}
}

// TestHandler_CreateNoteMissingFolderID tests that workspace_id is required.
func TestHandler_CreateNoteMissingFolderID(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	body := `{"name": "Test Note", "content": "Content"}`
	req := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleNotes(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// TestHandler_CreateNoteInFolder tests creating a note via POST /api/folders/{id}/notes.
func TestHandler_CreateNoteInFolder(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "Notes Folder")

	body := `{"name": "Folder Note", "content": "Content in folder"}`
	req := httptest.NewRequest(http.MethodPost, "/api/folders/"+folderID+"/notes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaceNotes(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	note := resp["note"].(map[string]interface{})
	if note["workspace_id"] != folderID {
		t.Errorf("Expected workspace_id '%s', got '%v'", folderID, note["workspace_id"])
	}
}

// TestHandler_GetNote tests retrieving a note by ID.
func TestHandler_GetNote(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "Get Note Folder")

	// Create a note
	createBody := `{"workspace_id": "` + folderID + `", "name": "Get Me", "content": "Find me"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleNotes(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	note := createResp["note"].(map[string]interface{})
	noteID := note["id"].(string)

	// Get the note
	getReq := httptest.NewRequest(http.MethodGet, "/api/notes/"+noteID, nil)
	getW := httptest.NewRecorder()
	handler.HandleNotes(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", getW.Code, getW.Body.String())
	}

	var getResp map[string]interface{}
	_ = json.Unmarshal(getW.Body.Bytes(), &getResp)
	if getResp["name"] != "Get Me" {
		t.Errorf("Expected name 'Get Me', got '%v'", getResp["name"])
	}
	if getResp["content"] != "Find me" {
		t.Errorf("Expected content 'Find me', got '%v'", getResp["content"])
	}
}

// TestHandler_GetNoteNotFound tests 404 for non-existent note.
func TestHandler_GetNoteNotFound(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/notes/non-existent-id", nil)
	w := httptest.NewRecorder()
	handler.HandleNotes(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

// TestHandler_UpdateNote tests updating note metadata.
func TestHandler_UpdateNote(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "Update Folder")

	// Create a note
	createBody := `{"workspace_id": "` + folderID + `", "name": "Original", "content": "Original content"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleNotes(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	note := createResp["note"].(map[string]interface{})
	noteID := note["id"].(string)

	// Update the note
	updateBody := `{"name": "Updated Name", "content": "Updated content"}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/notes/"+noteID, bytes.NewBufferString(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	handler.HandleNotes(updateW, updateReq)

	if updateW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	var updateResp map[string]interface{}
	_ = json.Unmarshal(updateW.Body.Bytes(), &updateResp)
	updatedNote := updateResp["note"].(map[string]interface{})
	if updatedNote["name"] != "Updated Name" {
		t.Errorf("Expected name 'Updated Name', got '%v'", updatedNote["name"])
	}
	if updatedNote["content"] != "Updated content" {
		t.Errorf("Expected content 'Updated content', got '%v'", updatedNote["content"])
	}
}

// TestHandler_UpdateNotePartial tests partial note updates.
func TestHandler_UpdateNotePartial(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "Partial Update Folder")

	// Create a note
	createBody := `{"workspace_id": "` + folderID + `", "name": "Original", "content": "Original content"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleNotes(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	note := createResp["note"].(map[string]interface{})
	noteID := note["id"].(string)

	// Update only the name
	updateBody := `{"name": "New Name Only"}`
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/notes/"+noteID, bytes.NewBufferString(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	handler.HandleNotes(updateW, updateReq)

	if updateW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", updateW.Code)
	}

	var updateResp map[string]interface{}
	_ = json.Unmarshal(updateW.Body.Bytes(), &updateResp)
	updatedNote := updateResp["note"].(map[string]interface{})
	if updatedNote["name"] != "New Name Only" {
		t.Errorf("Expected name 'New Name Only', got '%v'", updatedNote["name"])
	}
	if updatedNote["content"] != "Original content" {
		t.Errorf("Expected content to remain 'Original content', got '%v'", updatedNote["content"])
	}
}

// TestHandler_DeleteNote tests note deletion.
func TestHandler_DeleteNote(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "Delete Folder")

	// Create a note
	createBody := `{"workspace_id": "` + folderID + `", "name": "To Delete"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleNotes(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	note := createResp["note"].(map[string]interface{})
	noteID := note["id"].(string)

	// Delete the note
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/notes/"+noteID, nil)
	deleteW := httptest.NewRecorder()
	handler.HandleNotes(deleteW, deleteReq)

	if deleteW.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", deleteW.Code)
	}

	// Verify deletion
	getReq := httptest.NewRequest(http.MethodGet, "/api/notes/"+noteID, nil)
	getW := httptest.NewRecorder()
	handler.HandleNotes(getW, getReq)

	if getW.Code != http.StatusNotFound {
		t.Errorf("Expected status 404 after deletion, got %d", getW.Code)
	}
}

// TestHandler_ListNotesByFolder tests listing notes in a folder.
func TestHandler_ListNotesByFolder(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "List Folder")

	// Create multiple notes
	for i := 0; i < 3; i++ {
		body := `{"workspace_id": "` + folderID + `", "name": "Note"}`
		req := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleNotes(w, req)
	}

	// List notes
	listReq := httptest.NewRequest(http.MethodGet, "/api/folders/"+folderID+"/notes", nil)
	listW := httptest.NewRecorder()
	handler.HandleWorkspaceNotes(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", listW.Code)
	}

	var listResp map[string]interface{}
	_ = json.Unmarshal(listW.Body.Bytes(), &listResp)

	notes := listResp["notes"].([]interface{})
	if len(notes) != 3 {
		t.Errorf("Expected 3 notes, got %d", len(notes))
	}
}

// TestHandler_SearchNotes tests note search functionality.
func TestHandler_SearchNotes(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "Search Folder")

	// Create notes with specific content
	notes := []struct {
		name    string
		content string
	}{
		{"Meeting Notes", "Discussed quarterly planning"},
		{"Ideas", "New feature ideas for the product"},
		{"Project Plan", "Timeline for Q2 deliverables"},
	}

	for _, n := range notes {
		body := `{"workspace_id": "` + folderID + `", "name": "` + n.name + `", "content": "` + n.content + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleNotes(w, req)
	}

	// Wait for FTS indexing
	time.Sleep(50 * time.Millisecond)

	// Search for "feature"
	searchReq := httptest.NewRequest(http.MethodGet, "/api/notes/search?q=feature", nil)
	searchW := httptest.NewRecorder()
	handler.HandleNotes(searchW, searchReq)

	if searchW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", searchW.Code, searchW.Body.String())
	}

	var searchResp map[string]interface{}
	_ = json.Unmarshal(searchW.Body.Bytes(), &searchResp)

	results := searchResp["notes"].([]interface{})
	if len(results) != 1 {
		t.Errorf("Expected 1 result for 'feature' search, got %d", len(results))
	}

	if len(results) > 0 {
		result := results[0].(map[string]interface{})
		if result["name"] != "Ideas" {
			t.Errorf("Expected 'Ideas' note, got '%v'", result["name"])
		}
	}
}

// TestHandler_SearchNotesEmpty tests search with no query.
func TestHandler_SearchNotesEmpty(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	searchReq := httptest.NewRequest(http.MethodGet, "/api/notes/search", nil)
	searchW := httptest.NewRecorder()
	handler.HandleNotes(searchW, searchReq)

	if searchW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", searchW.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(searchW.Body.Bytes(), &resp)

	notes := resp["notes"].([]interface{})
	if len(notes) != 0 {
		t.Errorf("Expected empty results for empty query, got %d", len(notes))
	}
}

// TestHandler_CreateNoteDefaultName tests that notes get default name.
func TestHandler_CreateNoteDefaultName(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	folderID := createTestWorkspace(t, handler, "Default Name Folder")

	// Create note without name
	body := `{"workspace_id": "` + folderID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/notes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleNotes(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	note := resp["note"].(map[string]interface{})
	if note["name"] != "Untitled Note" {
		t.Errorf("Expected default name 'Untitled Note', got '%v'", note["name"])
	}
}
