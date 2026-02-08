package chathttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newChatHandlerForOpenAppTests(t *testing.T) *Handler {
	t.Helper()

	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	return NewHandler(st, nil)
}

func TestChatHandler_AutoRoutesOpenPromptToOpenAppCommand(t *testing.T) {
	h := newChatHandlerForOpenAppTests(t)

	original := openApplicationFn
	t.Cleanup(func() { openApplicationFn = original })

	calledWith := ""
	openApplicationFn = func(appName string) error {
		calledWith = appName
		return nil
	}

	body, _ := json.Marshal(map[string]any{
		"question": "open safari",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if calledWith != "safari" {
		t.Fatalf("expected openApplication to be called with %q, got %q", "safari", calledWith)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	responseText, _ := resp["response"].(string)
	if !strings.Contains(strings.ToLower(responseText), "opening safari") {
		t.Fatalf("unexpected response: %q", responseText)
	}
}
