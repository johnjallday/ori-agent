package agenthttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// editDefinitionOnDisk changes an agent's system prompt the way a text editor
// would, behind the store's back.
func editDefinitionOnDisk(t *testing.T, dir, name, prompt string) {
	t.Helper()
	path := filepath.Join(dir, "agents", name, "agent_settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read definition: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode definition: %v", err)
	}
	doc["Settings"].(map[string]any)["system_prompt"] = prompt
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("encode definition: %v", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatalf("write definition: %v", err)
	}
}

func TestPatchAfterAnEditOnDiskAnswersConflictAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(dir, "agents.json"), types.Settings{Model: "gpt-4o-mini", Temperature: 1.0})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent("Scout", &store.CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	editDefinitionOnDisk(t, dir, "Scout", "edited in a text editor")

	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Scout", strings.NewReader(`{"description":"a different field"}`))
	rr := httptest.NewRecorder()
	New(st).ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != agentChangedOnDiskCode {
		t.Errorf("code = %q, want %q", body.Code, agentChangedOnDiskCode)
	}
	if body.Message != "This agent was changed on disk. Ori reloaded it. Review it and try again." {
		t.Errorf("message = %q", body.Message)
	}

	got, _ := st.GetAgent("Scout")
	if got.Settings.SystemPrompt != "edited in a text editor" {
		t.Errorf("the store did not reload the on-disk edit: prompt %q", got.Settings.SystemPrompt)
	}
	if got.Metadata != nil && got.Metadata.Description == "a different field" {
		t.Error("the refused change was kept in memory")
	}

	// Retrying against the reloaded agent succeeds and keeps the edit.
	rr = httptest.NewRecorder()
	New(st).ServeHTTP(rr, httptest.NewRequest(http.MethodPatch, "/api/agents/Scout", strings.NewReader(`{"description":"a different field"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("retry: expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	got, _ = st.GetAgent("Scout")
	if got.Settings.SystemPrompt != "edited in a text editor" || got.Metadata.Description != "a different field" {
		t.Errorf("after retry: prompt %q description %q", got.Settings.SystemPrompt, got.Metadata.Description)
	}
}

func TestRenameAfterAnEditOnDiskDoesNotMoveTheStaleCopy(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(dir, "agents.json"), types.Settings{Model: "gpt-4o-mini", Temperature: 1.0})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent("Scout", &store.CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	editDefinitionOnDisk(t, dir, "Scout", "edited in a text editor")

	rr := httptest.NewRecorder()
	New(st).ServeHTTP(rr, httptest.NewRequest(http.MethodPatch, "/api/agents/Scout", strings.NewReader(`{"name":"Ranger"}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := st.GetAgent("Ranger"); ok {
		t.Error("a stale copy was renamed")
	}
	got, ok := st.GetAgent("Scout")
	if !ok || got.Settings.SystemPrompt != "edited in a text editor" {
		t.Errorf("the edited agent was not kept under its name: %+v", got)
	}
}

func TestRenameMovesTheAgentFolderWithItsSidecars(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(dir, "agents.json"), types.Settings{Model: "gpt-4o-mini", Temperature: 1.0})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent("Scout", &store.CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	sidecar := filepath.Join(dir, "agents", "Scout", "mcp_servers.json")
	if err := os.WriteFile(sidecar, []byte(`{"enabled_servers":["git"]}`), 0o600); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	rr := httptest.NewRecorder()
	New(st).ServeHTTP(rr, httptest.NewRequest(http.MethodPatch, "/api/agents/Scout", strings.NewReader(`{"name":"Ranger"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "Ranger", "mcp_servers.json")); err != nil {
		t.Errorf("the sidecar did not move with the agent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "Scout")); !os.IsNotExist(err) {
		t.Errorf("the old folder is still there (err=%v)", err)
	}
}

func TestWriteAgentStoreErrorKeepsOtherFailuresInternal(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteAgentStoreError(rr, "Failed to update agent", errors.New("disk full"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Failed to update agent") {
		t.Errorf("body = %s", rr.Body.String())
	}
}
