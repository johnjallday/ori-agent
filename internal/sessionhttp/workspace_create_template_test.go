package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// templateTestEnv wires a handler with a folder store, a templates library
// holding one template, and an event bus subscription for project.created.
func templateTestEnv(t *testing.T) (*Handler, string, string, <-chan agentworkspace.Event, func()) {
	t.Helper()
	handler, cleanup := createTestHandler(t)

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		cleanup()
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	libDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(libDir, "demo-template"), 0o750); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "demo-template", "{{name}}.rpp"), []byte("<REAPER_PROJECT 0.1\n>\n"), 0o640); err != nil {
		cleanup()
		t.Fatal(err)
	}
	handler.SetTemplatesRootResolver(func() string { return libDir })

	bus := agentworkspace.NewEventBus(10, 10)
	events := make(chan agentworkspace.Event, 10)
	bus.SubscribeToEventType(agentworkspace.EventProjectCreated, func(event agentworkspace.Event) {
		events <- event
	})
	handler.SetEventBus(bus)

	return handler, baseDir, libDir, events, cleanup
}

func postCreateWorkspace(t *testing.T, handler *Handler, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response (%d): %v: %s", w.Code, err, w.Body.String())
	}
	return w, resp
}

func TestCreateWorkspaceWithTemplate(t *testing.T) {
	handler, baseDir, _, events, cleanup := templateTestEnv(t)
	defer cleanup()

	w, resp := postCreateWorkspace(t, handler, `{"name":"Song X","template_id":"demo-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if warning, present := resp["project_warning"]; present {
		t.Fatalf("unexpected project_warning: %v", warning)
	}

	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in response")
	}
	if got := folder["project_path"]; got != "song-x" {
		t.Fatalf("project_path = %v, want song-x", got)
	}

	// Project name defaulted to the workspace name; filename token substituted;
	// project folder sits beside files/ and notes/, not inside files/.
	seed := filepath.Join(baseDir, "song-x", "song-x", "song-x.rpp")
	if _, err := os.Stat(seed); err != nil {
		t.Fatalf("expected instantiated seed at %s: %v", seed, err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "song-x", "files", "song-x")); !os.IsNotExist(err) {
		t.Fatalf("project must not land inside files/ (err=%v)", err)
	}

	// workspace.json carries the project path for portability.
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id in response")
	}
	info, err := handler.workspaceStore.GetProjectPathInfo(wsID)
	if err != nil || info == nil {
		t.Fatalf("GetProjectPathInfo: info=%v err=%v", info, err)
	}
	if !info.Resolved || info.RelativePath != "song-x" {
		t.Fatalf("project path not resolved via folder store: %+v", info)
	}

	// Session reads hydrate project_path from workspace.json (it has no
	// SQLite column), so a bare session row must come back with the path.
	hydrated := handler.hydrateWorkspaceMetadataFromFileStore(&session.Workspace{ID: wsID})
	if hydrated == nil || hydrated.ProjectPath != "song-x" {
		t.Fatalf("hydrated project_path = %+v, want song-x", hydrated)
	}

	select {
	case event := <-events:
		if event.WorkspaceID != wsID || event.Data["project_path"] != "song-x" {
			t.Fatalf("unexpected project.created payload: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project.created event not published")
	}
}

func TestCreateWorkspaceWithTemplatePathEscapeHatch(t *testing.T) {
	handler, baseDir, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	adHoc := t.TempDir()
	if err := os.WriteFile(filepath.Join(adHoc, "notes-{{date}}.md"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	w, resp := postCreateWorkspace(t, handler, `{"name":"Ad Hoc","template_path":"`+adHoc+`","project_name":"Side Quest"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if warning, present := resp["project_warning"]; present {
		t.Fatalf("unexpected project_warning: %v", warning)
	}
	date := time.Now().Format("2006-01-02")
	if _, err := os.Stat(filepath.Join(baseDir, "ad-hoc", "side-quest", "notes-"+date+".md")); err != nil {
		t.Fatalf("expected escape-hatch project file: %v", err)
	}
}

func TestCreateWorkspaceWithoutTemplateUnchanged(t *testing.T) {
	handler, baseDir, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, resp := postCreateWorkspace(t, handler, `{"name":"Plain"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if _, present := resp["project_warning"]; present {
		t.Fatal("project_warning must be absent without a template")
	}
	folder := resp["folder"].(map[string]any)
	if got, present := folder["project_path"]; present && got != "" {
		t.Fatalf("unexpected project_path: %v", got)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "plain", "files")); err != nil {
		t.Fatalf("standard scaffolding missing: %v", err)
	}
}

func TestCreateGroupWorkspaceRejectsTemplate(t *testing.T) {
	handler, _, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, _ := postCreateWorkspace(t, handler, `{"name":"Album","kind":"group","template_id":"demo-template"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for group+template, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspaceRejectsBothTemplateFields(t *testing.T) {
	handler, _, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, _ := postCreateWorkspace(t, handler, `{"name":"Both","template_id":"demo-template","template_path":"/tmp/x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for both template fields, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspaceTemplateFailureIsNonFatal(t *testing.T) {
	handler, baseDir, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, resp := postCreateWorkspace(t, handler, `{"name":"Broken","template_id":"no-such-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 despite template failure, got %d: %s", w.Code, w.Body.String())
	}
	warning, _ := resp["project_warning"].(string)
	if warning == "" {
		t.Fatal("expected project_warning describing the failure")
	}

	// Workspace is fully usable: scaffolding exists, no project folder, no
	// dangling project_path (PRD success metric 4).
	if _, err := os.Stat(filepath.Join(baseDir, "broken", "files")); err != nil {
		t.Fatalf("workspace scaffolding missing after failed template: %v", err)
	}
	folder := resp["folder"].(map[string]any)
	if got, present := folder["project_path"]; present && got != "" {
		t.Fatalf("dangling project_path after failure: %v", got)
	}
}
