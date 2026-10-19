package memoryhttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func withContextFiles(t *testing.T) (*Handler, string) {
	t.Helper()
	h, fs := newTestHandler(t)
	root := t.TempDir()
	h.SetContextFiles(&workspace.MemoryContextWriter{
		Folders: fs, Workspaces: fs,
		Settings:   workspace.NewMemoryContextSettings(filepath.Join(t.TempDir(), "settings.json")),
		RootFolder: func(*workspace.Workspace) string { return root },
	})
	return h, root
}

func patchSettings(h *Handler, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/memory/settings", h.UpdateSettings)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws1/memory/settings", strings.NewReader(body)))
	return rec
}

func TestUpdateSettings_RootSwitchWritesAndRemovesTheBlock(t *testing.T) {
	h, root := withContextFiles(t)
	if rec := serve(h, http.MethodPost, "/api/workspaces/ws1/memory/entries", `{"text":"Studies use 0.6% fees.","type":"fact"}`); rec.Code != http.StatusOK {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	resp := decodeMemory(t, serve(h, http.MethodGet, "/api/workspaces/ws1/memory", ""))
	if resp.ContextFiles == nil || resp.ContextFiles.RootFiles || resp.ContextFiles.RootDir != root ||
		!strings.HasSuffix(resp.ContextFiles.ContextDir, filepath.Join(".ori", "context")) || len(resp.ContextFiles.FileNames) != 3 {
		t.Fatalf("context files status: %+v", resp.ContextFiles)
	}

	rec := patchSettings(h, `{"root_files":true}`)
	if rec.Code != http.StatusOK || !decodeMemory(t, rec).ContextFiles.RootFiles {
		t.Fatalf("switch on: %d %s", rec.Code, rec.Body.String())
	}
	for _, name := range workspace.MemoryContextFileNames {
		data, err := os.ReadFile(filepath.Join(root, name)) // #nosec G304 -- test temp folder
		if err != nil || !strings.Contains(string(data), "Studies use 0.6% fees.") || !strings.Contains(string(data), workspace.MemoryBlockStart) {
			t.Fatalf("%s after switching on: %v\n%s", name, err, data)
		}
	}

	rec = patchSettings(h, `{"root_files":false}`)
	if rec.Code != http.StatusOK || decodeMemory(t, rec).ContextFiles.RootFiles {
		t.Fatalf("switch off: %d %s", rec.Code, rec.Body.String())
	}
	for _, name := range workspace.MemoryContextFileNames {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s holding only Ori's block should be gone after switching off (err=%v)", name, err)
		}
	}
}

func TestUpdateSettings_RefusalsAndUnwired(t *testing.T) {
	h, _ := newTestHandler(t)
	if rec := patchSettings(h, `{"root_files":true}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired: %d", rec.Code)
	}
	if resp := decodeMemory(t, serve(h, http.MethodGet, "/api/workspaces/ws1/memory", "")); resp.ContextFiles != nil {
		t.Fatal("an unwired handler should not report context files")
	}
	wired, _ := withContextFiles(t)
	if rec := patchSettings(wired, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing root_files: %d", rec.Code)
	}
}
