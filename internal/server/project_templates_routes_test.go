package server

import (
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
