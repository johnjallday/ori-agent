package memoryhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newTestHandler(t *testing.T) (*Handler, *workspace.FileStore) {
	t.Helper()
	fs, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := fs.Save(&workspace.Workspace{ID: "ws1", Name: "Mem"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return NewHandler(fs, fs), fs
}

// serve dispatches a request through a mux so r.PathValue works.
func serve(h *Handler, method, target string, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/memory", h.GetMemory)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/memory/entries", h.AddEntry)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/memory/entries/{index}", h.UpdateEntry)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/memory/entries/{index}", h.DeleteEntry)

	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rdr)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeMemory(t *testing.T, rec *httptest.ResponseRecorder) memoryResponse {
	t.Helper()
	var resp memoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return resp
}

func TestGetMemory_EmptyAndLazyCreate(t *testing.T) {
	h, fs := newTestHandler(t)

	rec := serve(h, http.MethodGet, "/api/workspaces/ws1/memory", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET empty memory: status %d body %s", rec.Code, rec.Body.String())
	}
	resp := decodeMemory(t, rec)
	if len(resp.Entries) != 0 || resp.RawSize != 0 {
		t.Errorf("empty memory should have no entries and zero size, got %+v", resp)
	}
	if resp.TokenBudget != workspace.MemoryPromptTokenBudget {
		t.Errorf("token budget = %d, want %d", resp.TokenBudget, workspace.MemoryPromptTokenBudget)
	}

	// File should not exist until first write.
	folder, _ := fs.GetFolderPath("ws1")
	if _, err := os.Stat(filepath.Join(folder, workspace.MemoryFileName)); !os.IsNotExist(err) {
		t.Errorf("MEMORY.md should not exist before any write")
	}

	rec = serve(h, http.MethodPost, "/api/workspaces/ws1/memory/entries", `{"text":"staging is at stage.example.com","type":"fact"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST entry: status %d body %s", rec.Code, rec.Body.String())
	}
	resp = decodeMemory(t, rec)
	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 entry after POST, got %d", len(resp.Entries))
	}
	if resp.Entries[0].Provenance != "user" || resp.Entries[0].Type != "fact" {
		t.Errorf("user entry should be provenance=user type=fact, got %+v", resp.Entries[0])
	}
	if _, err := os.Stat(filepath.Join(folder, workspace.MemoryFileName)); err != nil {
		t.Errorf("MEMORY.md should exist after first POST: %v", err)
	}
}

func TestGetMemory_IncludesOnlyApprovedManagedLearnings(t *testing.T) {
	h, fs := newTestHandler(t)
	if err := fs.Update("ws1", func(current *workspace.Workspace) error {
		current.SetAssistantProgramState(&workspace.AssistantProgramState{
			SchemaVersion: 1,
			Key:           workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "plugin", ProgramID: "program"},
			Declaration:   &workspace.AssistantProgramDeclaration{SchemaVersion: 1, ID: "program"},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	evidence := []workspace.AssistantEvidenceReference{
		{SourceID: "a", ProjectID: "a", Summary: "Pattern in A", ObservedAt: now},
		{SourceID: "b", ProjectID: "b", Summary: "Pattern in B", ObservedAt: now},
		{SourceID: "c", ProjectID: "c", Summary: "Pattern in C", ObservedAt: now},
	}
	learningStore := workspace.NewAssistantLearningStore(fs)
	document, err := learningStore.AddCandidates("ws1", 0, []workspace.AssistantLearningCandidate{{
		Fingerprint: "pattern", Type: "preference", Text: "Keep reviews concise.", Confidence: "high", Evidence: evidence,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := learningStore.ApproveCandidate("ws1", document.Candidates[0].ID, document.Version); err != nil {
		t.Fatal(err)
	}

	rec := serve(h, http.MethodGet, "/api/workspaces/ws1/memory", "")
	resp := decodeMemory(t, rec)
	if len(resp.ManagedLearnings) != 1 || resp.ManagedLearnings[0].Text != "Keep reviews concise." {
		t.Fatalf("managed learnings = %+v", resp.ManagedLearnings)
	}
	if len(resp.Entries) != 0 {
		t.Fatalf("managed learning leaked into index-based MEMORY.md entries: %+v", resp.Entries)
	}
}

func TestAddEntry_Validation(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := serve(h, http.MethodPost, "/api/workspaces/ws1/memory/entries", `{"text":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty text should be 400, got %d", rec.Code)
	}

	rec = serve(h, http.MethodPost, "/api/workspaces/ws1/memory/entries", `{"text":"key is sk-abcdefgh12345678"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("secret-looking text should be 400, got %d", rec.Code)
	}
}

func TestUpdateAndDeleteEntry(t *testing.T) {
	h, _ := newTestHandler(t)
	serve(h, http.MethodPost, "/api/workspaces/ws1/memory/entries", `{"text":"first"}`)
	serve(h, http.MethodPost, "/api/workspaces/ws1/memory/entries", `{"text":"second"}`)

	rec := serve(h, http.MethodPut, "/api/workspaces/ws1/memory/entries/0", `{"text":"first revised","type":"decision"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: status %d body %s", rec.Code, rec.Body.String())
	}
	resp := decodeMemory(t, rec)
	if resp.Entries[0].Text != "first revised" || resp.Entries[0].Type != "decision" {
		t.Errorf("edit not applied: %+v", resp.Entries[0])
	}

	// Out-of-range index => 404.
	rec = serve(h, http.MethodPut, "/api/workspaces/ws1/memory/entries/9", `{"text":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("out-of-range edit should be 404, got %d", rec.Code)
	}
	// Non-numeric index => 400.
	rec = serve(h, http.MethodDelete, "/api/workspaces/ws1/memory/entries/abc", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric index should be 400, got %d", rec.Code)
	}

	rec = serve(h, http.MethodDelete, "/api/workspaces/ws1/memory/entries/0", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE: status %d body %s", rec.Code, rec.Body.String())
	}
	resp = decodeMemory(t, rec)
	if len(resp.Entries) != 1 || resp.Entries[0].Text != "second" {
		t.Errorf("delete removed wrong entry: %+v", resp.Entries)
	}
}

func TestGetMemory_UnknownWorkspace(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := serve(h, http.MethodGet, "/api/workspaces/nope/memory", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown workspace should be 404, got %d", rec.Code)
	}
}

func TestHandler_Unavailable(t *testing.T) {
	h := NewHandler(nil, nil) // no resolver => memory store nil
	rec := serve(h, http.MethodGet, "/api/workspaces/ws1/memory", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil store should be 503, got %d", rec.Code)
	}
}

func TestGetMemory_ExposesUnstructured(t *testing.T) {
	h, fs := newTestHandler(t)
	folder, _ := fs.GetFolderPath("ws1")
	content := "# Workspace Memory\n\nHand-written note line.\n- [fact, 2026-06-01, user] alpha\n"
	if err := os.WriteFile(filepath.Join(folder, workspace.MemoryFileName), []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := serve(h, http.MethodGet, "/api/workspaces/ws1/memory", "")
	resp := decodeMemory(t, rec)
	if len(resp.Entries) != 1 {
		t.Errorf("expected 1 structured entry, got %d", len(resp.Entries))
	}
	joined := strings.Join(resp.Unstructured, "\n")
	if !strings.Contains(joined, "Hand-written note line.") {
		t.Errorf("unstructured lines should be exposed, got %q", resp.Unstructured)
	}
}
