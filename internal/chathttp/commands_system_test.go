package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newCommandHandlerForSystemTests(t *testing.T) *CommandHandler {
	t.Helper()

	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	return NewCommandHandler(st)
}

func decodeSystemCommandResponse(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	response, _ := body["response"].(string)
	return response
}

func TestHandleOpenApp_RequiresName(t *testing.T) {
	ch := newCommandHandlerForSystemTests(t)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleOpenApp(rr, req, "")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "Usage: `/openapp <application-name>`") {
		t.Fatalf("expected usage guidance, got: %q", response)
	}
}

func TestHandleOpenApp_Success(t *testing.T) {
	ch := newCommandHandlerForSystemTests(t)

	original := openApplicationFn
	t.Cleanup(func() { openApplicationFn = original })

	calledWith := ""
	openApplicationFn = func(appName string) error {
		calledWith = appName
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleOpenApp(rr, req, "Obsidian")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if calledWith != "Obsidian" {
		t.Fatalf("expected app name Obsidian, got %q", calledWith)
	}

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "Opening Obsidian now") {
		t.Fatalf("expected success response, got: %q", response)
	}
}

func TestHandleOpenApp_Failure(t *testing.T) {
	ch := newCommandHandlerForSystemTests(t)

	original := openApplicationFn
	t.Cleanup(func() { openApplicationFn = original })

	openApplicationFn = func(appName string) error {
		return fmt.Errorf("boom")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleOpenApp(rr, req, "Obsidian")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "Failed to open application") {
		t.Fatalf("expected failure response, got: %q", response)
	}
}

func TestHandleHelp_RemovesSwitchGuidance(t *testing.T) {
	ch := newCommandHandlerForSystemTests(t)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleHelp(rr, req)

	response := decodeSystemCommandResponse(t, rr)
	if strings.Contains(response, "/switch <agent-name>") {
		t.Fatalf("did not expect /switch guidance, got %q", response)
	}
	if !strings.Contains(response, "Assistant sessions run without a global current agent") {
		t.Fatalf("expected Assistant-first guidance, got %q", response)
	}
}
