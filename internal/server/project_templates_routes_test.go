package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func TestHandleProjectTemplates(t *testing.T) {
	libDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(libDir, "alpha"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "alpha", "template.json"), []byte(`{"name":"Alpha","description":"first","tags":[" Music ","music","Client"]}`), 0o640); err != nil {
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
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		} `json:"templates"`
		TemplatesRoot string `json:"templates_root"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Templates) != 1 || resp.Templates[0].ID != "alpha" || resp.Templates[0].Name != "Alpha" {
		t.Fatalf("unexpected templates: %+v", resp.Templates)
	}
	if len(resp.Templates[0].Tags) != 2 || resp.Templates[0].Tags[0] != "music" || resp.Templates[0].Tags[1] != "client" {
		t.Fatalf("unexpected template tags: %#v", resp.Templates[0].Tags)
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

func TestHandleProjectTemplateUpdateRuntimeRequirementsRoundTrip(t *testing.T) {
	previousAdapters := append([]string(nil), projecttemplates.ValidRuntimeRequirementAdapters...)
	projecttemplates.ValidRuntimeRequirementAdapters = []string{"test_runtime"}
	t.Cleanup(func() { projecttemplates.ValidRuntimeRequirementAdapters = previousAdapters })
	libDir := t.TempDir()
	templateDir := filepath.Join(libDir, "runtime-demo")
	if err := os.MkdirAll(templateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(templateDir, projecttemplates.ManifestFileName)
	if err := os.WriteFile(manifestPath, []byte(`{"name":"Runtime Demo","agents":[{"name":"Lead"}],"custom_key":"kept"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	s := newTemplateRoutesServer(t, libDir)

	callUpdate := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/project-templates/runtime-demo", bytes.NewBufferString(body))
		req.SetPathValue("templateID", "runtime-demo")
		s.handleProjectTemplateUpdate(w, req)
		return w
	}

	valid := `{
		"name":"Runtime Demo",
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[
				{"id":"limited","label":"Limited","description":"Use files."},
				{"id":"assisted","label":"Assisted","description":"Use live control.","requires":["runtime"]}
			],
			"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"test_runtime"}]
		}
	}`
	w := callUpdate(valid)
	if w.Code != http.StatusOK {
		t.Fatalf("valid update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Template struct {
			RuntimeRequirements *projecttemplates.RuntimeRequirementsContract `json:"runtime_requirements"`
		} `json:"template"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Template.RuntimeRequirements == nil || len(response.Template.RuntimeRequirements.OperatingModes) != 2 || response.Template.RuntimeRequirements.Requirements[0].Adapter != "test_runtime" {
		t.Fatalf("update response lost public runtime metadata: %s", w.Body.String())
	}

	// The list API returns the same normalized contract.
	list := httptest.NewRecorder()
	s.handleProjectTemplates(list, httptest.NewRequest(http.MethodGet, "/api/project-templates", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"runtime_requirements"`) || !strings.Contains(list.Body.String(), `"operating_modes"`) {
		t.Fatalf("list response lost runtime metadata: %d %s", list.Code, list.Body.String())
	}

	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	invalid := `{
		"name":"Must not persist",
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[{"id":"limited","label":"Limited","description":"Use files."}],
			"requirements":[],
			"command":"run"
		}
	}`
	w = callUpdate(invalid)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid runtime requirements") || !strings.Contains(w.Body.String(), "unknown field") {
		t.Fatalf("invalid update: expected actionable 400, got %d: %s", w.Code, w.Body.String())
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("invalid HTTP update partially saved template.json:\nbefore %s\nafter %s", before, after)
	}

	w = callUpdate(`{"name":"Runtime Demo","runtime_requirements":null}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"runtime_requirements"`) {
		t.Fatalf("explicit null did not clear runtime contract: %d %s", w.Code, w.Body.String())
	}
}

func TestHandleProjectTemplateUpdateProjectEntryTriState(t *testing.T) {
	libDir := t.TempDir()
	templateDir := filepath.Join(libDir, "song")
	if err := os.MkdirAll(templateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "{{name}}.rpp"), []byte("project"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "template.json"), []byte(`{"name":"Song","custom_key":"kept"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	s := newTemplateRoutesServer(t, libDir)

	callUpdate := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/project-templates/song", bytes.NewBufferString(body))
		req.SetPathValue("templateID", "song")
		s.handleProjectTemplateUpdate(w, req)
		return w
	}

	w := callUpdate(`{"name":"Song","project_entry":{"relative_path":"{{name}}.rpp","open_after_create_default":false}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("set project entry: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Template struct {
			ProjectEntry *struct {
				RelativePath           string `json:"relative_path"`
				OpenAfterCreateDefault bool   `json:"open_after_create_default"`
			} `json:"project_entry"`
		} `json:"template"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Template.ProjectEntry == nil || response.Template.ProjectEntry.RelativePath != "{{name}}.rpp" || response.Template.ProjectEntry.OpenAfterCreateDefault {
		t.Fatalf("unexpected project entry response: %+v", response.Template.ProjectEntry)
	}

	// Omitting the field preserves it for older clients.
	w = callUpdate(`{"name":"Song","description":"preserved"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"project_entry"`) {
		t.Fatalf("omitted project entry was not preserved: %d %s", w.Code, w.Body.String())
	}

	// Explicit null clears it.
	w = callUpdate(`{"name":"Song","project_entry":null}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"project_entry"`) {
		t.Fatalf("project entry was not cleared: %d %s", w.Code, w.Body.String())
	}

	for _, body := range []string{
		`{"name":"Song","project_entry":{"relative_path":"../escape.rpp","open_after_create_default":true}}`,
		`{"name":"Song","project_entry":{"relative_path":"missing.rpp","open_after_create_default":true}}`,
		`{"name":"Song","project_entry":"{{name}}.rpp"}`,
	} {
		w = callUpdate(body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid project entry: expected 400, got %d: %s", w.Code, w.Body.String())
		}
	}

	data, err := os.ReadFile(filepath.Join(templateDir, "template.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"custom_key": "kept"`) {
		t.Fatalf("unknown manifest field was lost: %s", data)
	}
}
