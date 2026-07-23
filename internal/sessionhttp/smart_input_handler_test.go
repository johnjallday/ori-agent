package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
)

func createTestSmartInputHandler(t *testing.T) (*SmartInputHandler, func(), session.HybridStore) {
	t.Helper()

	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	store := session.NewHybridStoreWithDB(db, 10)
	handler := NewSmartInputHandler(store, nil, nil)

	cleanup := func() {
		_ = store.Close()
	}

	return handler, cleanup, store
}

func TestSmartInputHandler_Classify_Heuristic(t *testing.T) {
	handler := NewSmartInputHandler(nil, nil, nil)

	body := `{"workspace_id":"ws-1","input":"todo: update docs"}`
	req := httptest.NewRequest(http.MethodPost, "/api/smart-input/classify", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleClassify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp SmartInputClassifyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Decision != SmartInputDecisionTask {
		t.Fatalf("expected task decision, got %s", resp.Decision)
	}
	if resp.Method != SmartInputMethodHeuristic {
		t.Fatalf("expected heuristic method, got %s", resp.Method)
	}
	if resp.NeedsConfirmation {
		t.Fatalf("expected no confirmation, got needs_confirmation")
	}
}

func TestSmartInputHandler_Classify_FallbackPrompt(t *testing.T) {
	handler := NewSmartInputHandler(nil, nil, nil)

	body := `{"workspace_id":"ws-1","input":"plan roadmap"}`
	req := httptest.NewRequest(http.MethodPost, "/api/smart-input/classify", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleClassify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp SmartInputClassifyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Decision != SmartInputDecisionTask {
		t.Fatalf("expected task decision, got %s", resp.Decision)
	}
	if resp.Method != SmartInputMethodFallback {
		t.Fatalf("expected fallback method, got %s", resp.Method)
	}
	if !resp.NeedsConfirmation {
		t.Fatalf("expected confirmation for low confidence")
	}
}

func TestSmartInputHandler_Classify_Backlog(t *testing.T) {
	handler := NewSmartInputHandler(nil, nil, nil)

	body := `{"workspace_id":"ws-1","input":"backlog: explore competitor pricing"}`
	req := httptest.NewRequest(http.MethodPost, "/api/smart-input/classify", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleClassify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp SmartInputClassifyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Decision != SmartInputDecisionBacklog {
		t.Fatalf("expected backlog decision, got %s", resp.Decision)
	}
	if resp.NeedsConfirmation {
		t.Fatalf("expected no confirmation for a high-confidence backlog prefix match")
	}
}

func TestSmartInputHandler_Classify_BadRequest(t *testing.T) {
	handler := NewSmartInputHandler(nil, nil, nil)

	body := `{"input":"   "}`
	req := httptest.NewRequest(http.MethodPost, "/api/smart-input/classify", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleClassify(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

func TestSmartInputHandler_OverrideLogs(t *testing.T) {
	handler, cleanup, store := createTestSmartInputHandler(t)
	defer cleanup()

	body := `{
		"workspace_id":"ws-1",
		"input":"plan roadmap",
		"predicted_decision":"task",
		"selected_decision":"chat",
		"confidence":0.55,
		"method":"heuristic"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/smart-input/override", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleOverride(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp SmartInputOverrideResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success response")
	}

	var predicted, selected, method, input, workspaceID string
	var confidence float64
	err := store.DB().QueryRow(`
		SELECT predicted_decision, selected_decision, method, input, workspace_id, confidence
		FROM smart_input_overrides
		LIMIT 1
	`).Scan(&predicted, &selected, &method, &input, &workspaceID, &confidence)
	if err != nil {
		t.Fatalf("failed to read override log: %v", err)
	}

	if predicted != "task" || selected != "chat" {
		t.Fatalf("unexpected decisions logged: %s -> %s", predicted, selected)
	}
	if method != "heuristic" {
		t.Fatalf("unexpected method logged: %s", method)
	}
	if input != "plan roadmap" || workspaceID != "ws-1" {
		t.Fatalf("unexpected log payload: %s / %s", input, workspaceID)
	}
	if confidence != 0.55 {
		t.Fatalf("unexpected confidence: %f", confidence)
	}
}

func TestSmartInputHandler_OverrideAcceptsBacklogDecision(t *testing.T) {
	handler, cleanup, _ := createTestSmartInputHandler(t)
	defer cleanup()

	body := `{
		"workspace_id":"ws-1",
		"input":"revisit the pricing page",
		"predicted_decision":"task",
		"selected_decision":"backlog",
		"confidence":0.6,
		"method":"heuristic"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/smart-input/override", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleOverride(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 for a backlog override, got %d: %s", rec.Code, rec.Body.String())
	}
}
