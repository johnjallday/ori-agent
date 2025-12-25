package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
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

	return handler, func() {
		store.Close()
	}
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

// TestHandler_CreateSessionMissingAgent tests that agent_name is required.
func TestHandler_CreateSessionMissingAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	body := `{"title": "Test Session"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
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
	json.Unmarshal(createW.Body.Bytes(), &createResp)
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
	json.Unmarshal(getW.Body.Bytes(), &getResp)
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
	json.Unmarshal(createW.Body.Bytes(), &createResp)
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
	json.Unmarshal(updateW.Body.Bytes(), &updateResp)
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
	json.Unmarshal(createW.Body.Bytes(), &createResp)
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
	json.Unmarshal(listW.Body.Bytes(), &listResp)

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
	json.Unmarshal(createW.Body.Bytes(), &createResp)
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
	json.Unmarshal(getW.Body.Bytes(), &msgResp)

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
	json.Unmarshal(tab1W.Body.Bytes(), &tab1Resp)
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
	json.Unmarshal(tab2W.Body.Bytes(), &tab2Resp)
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
	json.Unmarshal(get1W.Body.Bytes(), &msgs1Resp)
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
	json.Unmarshal(get2W.Body.Bytes(), &msgs2Resp)
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
	json.Unmarshal(session1W.Body.Bytes(), &session1Resp)
	sessionA := session1Resp["session"].(map[string]interface{})
	sessionAID := sessionA["id"].(string)

	session2Body := `{"title": "Session B", "agent_name": "test-agent"}`
	session2Req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(session2Body))
	session2Req.Header.Set("Content-Type", "application/json")
	session2W := httptest.NewRecorder()
	handler.HandleSessions(session2W, session2Req)

	var session2Resp map[string]interface{}
	json.Unmarshal(session2W.Body.Bytes(), &session2Resp)
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
	json.Unmarshal(getAW.Body.Bytes(), &msgsARep)
	msgsA := msgsARep["messages"].([]interface{})
	if len(msgsA) != 3 {
		t.Errorf("Session A should have 3 messages, got %d", len(msgsA))
	}

	// Simulate tab switching: get messages from Session B
	getBReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionBID+"/messages", nil)
	getBW := httptest.NewRecorder()
	handler.HandleSessions(getBW, getBReq)

	var msgsBRep map[string]interface{}
	json.Unmarshal(getBW.Body.Bytes(), &msgsBRep)
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
	json.Unmarshal(createW.Body.Bytes(), &createResp)
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
	json.Unmarshal(getW.Body.Bytes(), &msgsResp)
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
	json.Unmarshal(createW.Body.Bytes(), &createResp)
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
	json.Unmarshal(filterW.Body.Bytes(), &resp)
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
		json.Unmarshal(createW.Body.Bytes(), &resp)
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
	json.Unmarshal(searchW.Body.Bytes(), &resp)

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
	json.Unmarshal(statsW.Body.Bytes(), &stats)

	size := int(stats["size"].(float64))
	if size != 5 {
		t.Errorf("Expected cache size of 5, got %d", size)
	}
}
