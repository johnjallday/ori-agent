package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
)

func TestHandleProjectTemplates(t *testing.T) {
	libDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(libDir, "alpha"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "alpha", "template.json"), []byte(`{"name":"Alpha","description":"first"}`), 0o640); err != nil {
		t.Fatal(err)
	}

	configMgr := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := configMgr.Load(); err != nil {
		t.Fatal(err)
	}
	if err := configMgr.SetTemplatesRoot(libDir); err != nil {
		t.Fatal(err)
	}

	s := &Server{}
	s.Core = NewCoreSystemFacade(nil, nil, configMgr, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/project-templates", nil)
	w := httptest.NewRecorder()
	s.handleProjectTemplates(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Templates []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"templates"`
		TemplatesRoot string `json:"templates_root"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Templates) != 1 || resp.Templates[0].ID != "alpha" || resp.Templates[0].Name != "Alpha" {
		t.Fatalf("unexpected templates: %+v", resp.Templates)
	}
	if resp.TemplatesRoot != libDir {
		t.Fatalf("templates_root = %q, want %q", resp.TemplatesRoot, libDir)
	}

	// Method guard.
	w = httptest.NewRecorder()
	s.handleProjectTemplates(w, httptest.NewRequest(http.MethodPost, "/api/project-templates", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", w.Code)
	}
}

func newTemplateRoutesServer(t *testing.T, libDir string) *Server {
	t.Helper()
	configMgr := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := configMgr.Load(); err != nil {
		t.Fatal(err)
	}
	if err := configMgr.SetTemplatesRoot(libDir); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	s.Core = NewCoreSystemFacade(nil, nil, configMgr, nil, nil)
	return s
}

func TestProjectTemplateManagementRoutes(t *testing.T) {
	libDir := t.TempDir()
	s := newTemplateRoutesServer(t, libDir)

	// Import an arbitrary folder.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "seed.txt"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	body := `{"path":"` + src + `","name":"Imported Pack"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/project-templates/import", bytes.NewBufferString(body))
	s.handleProjectTemplateImport(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("import: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(libDir, "imported-pack", "seed.txt")); err != nil {
		t.Fatalf("imported file missing: %v", err)
	}

	// Re-import conflicts.
	w = httptest.NewRecorder()
	s.handleProjectTemplateImport(w, httptest.NewRequest(http.MethodPost, "/api/project-templates/import", bytes.NewBufferString(body)))
	if w.Code != http.StatusConflict {
		t.Fatalf("re-import: expected 409, got %d", w.Code)
	}

	// Update metadata via the wildcard route.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/project-templates/imported-pack", bytes.NewBufferString(`{"name":"Renamed","description":"d"}`))
	req.SetPathValue("templateID", "imported-pack")
	s.handleProjectTemplateUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updateResp struct {
		Template struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"template"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &updateResp); err != nil {
		t.Fatal(err)
	}
	if updateResp.Template.Name != "Renamed" || updateResp.Template.Description != "d" {
		t.Fatalf("unexpected update response: %+v", updateResp)
	}

	// Unknown template → 404.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/project-templates/nope", bytes.NewBufferString(`{"name":"x"}`))
	req.SetPathValue("templateID", "nope")
	s.handleProjectTemplateUpdate(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("update missing: expected 404, got %d", w.Code)
	}

	// Delete removes the folder (trash or permanent depending on platform).
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/project-templates/imported-pack", nil)
	req.SetPathValue("templateID", "imported-pack")
	s.handleProjectTemplateDelete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(libDir, "imported-pack")); !os.IsNotExist(err) {
		t.Fatalf("template still present after delete (err=%v)", err)
	}
}
