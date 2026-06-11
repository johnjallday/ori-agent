package sessionhttp

import (
	"bytes"
	"context"
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
func templateTestEnv(t *testing.T) (*Handler, string, <-chan agentworkspace.Event, func()) {
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
	if err := os.WriteFile(filepath.Join(libDir, "demo-template", "template.json"), []byte(`{"name":"Demo Template","tags":[" Music ","reaper","music"]}`), 0o640); err != nil {
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

	return handler, baseDir, events, cleanup
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
	handler, baseDir, events, cleanup := templateTestEnv(t)
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
	tags, ok := folder["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "music" || tags[1] != "reaper" {
		t.Fatalf("response tags = %#v, want [music reaper]", folder["tags"])
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
	diskWS, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get: %v", err)
	}
	if diskWS.ProjectPath != "song-x" {
		t.Fatalf("disk project_path = %q, want song-x", diskWS.ProjectPath)
	}
	if len(diskWS.Tags) != 2 || diskWS.Tags[0] != "music" || diskWS.Tags[1] != "reaper" {
		t.Fatalf("disk tags = %#v, want [music reaper]", diskWS.Tags)
	}
	if len(diskWS.DirectoryReferences) != 2 {
		t.Fatalf("expected project directory reference, got %#v", diskWS.DirectoryReferences)
	}
	projectRefPath := filepath.Join(baseDir, "song-x", "song-x")
	projectRef, ok := findAgentWorkspaceDirectoryReference(diskWS.DirectoryReferences, projectRefPath)
	if !ok {
		t.Fatalf("project directory reference for %q missing in %#v", projectRefPath, diskWS.DirectoryReferences)
	}
	if diskWS.SharedData[workspaceSharedDataPrimaryDirectoryIDKey] != projectRef.ID {
		t.Fatalf("primary directory = %v, want %q", diskWS.SharedData[workspaceSharedDataPrimaryDirectoryIDKey], projectRef.ID)
	}
	if diskWS.SharedData[workspaceSharedDataProjectDirectoryIDKey] != projectRef.ID {
		t.Fatalf("project directory = %v, want %q", diskWS.SharedData[workspaceSharedDataProjectDirectoryIDKey], projectRef.ID)
	}

	// Session reads hydrate project_path from workspace.json (it has no
	// SQLite column), so a bare session row must come back with the path.
	hydrated := handler.hydrateWorkspaceMetadataFromFileStore(&session.Workspace{ID: wsID})
	if hydrated == nil || hydrated.ProjectPath != "song-x" {
		t.Fatalf("hydrated project_path = %+v, want song-x", hydrated)
	}
	if len(hydrated.Tags) != 2 || hydrated.Tags[0] != "music" || hydrated.Tags[1] != "reaper" {
		t.Fatalf("hydrated tags = %#v, want [music reaper]", hydrated.Tags)
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

func TestCreateProjectForExistingWorkspace(t *testing.T) {
	handler, baseDir, events, cleanup := templateTestEnv(t)
	defer cleanup()

	w, resp := postCreateWorkspace(t, handler, `{"name":"Album"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected workspace create 201, got %d: %s", w.Code, w.Body.String())
	}
	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in response")
	}
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/project", bytes.NewBufferString(`{"template_id":"demo-template","project_name":"First Song"}`))
	req.Header.Set("Content-Type", "application/json")
	projectW := httptest.NewRecorder()
	handler.HandleWorkspaces(projectW, req)
	if projectW.Code != http.StatusCreated {
		t.Fatalf("expected project create 201, got %d: %s", projectW.Code, projectW.Body.String())
	}

	var projectResp map[string]any
	if err := json.Unmarshal(projectW.Body.Bytes(), &projectResp); err != nil {
		t.Fatalf("decode project response: %v", err)
	}
	if projectResp["project_path"] != "first-song" {
		t.Fatalf("project_path = %v, want first-song", projectResp["project_path"])
	}

	diskWS, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get: %v", err)
	}
	if diskWS.ProjectPath != "first-song" {
		t.Fatalf("disk project_path = %q, want first-song", diskWS.ProjectPath)
	}
	if len(diskWS.Tags) != 2 || diskWS.Tags[0] != "music" || diskWS.Tags[1] != "reaper" {
		t.Fatalf("disk tags = %#v, want [music reaper]", diskWS.Tags)
	}
	if len(diskWS.DirectoryReferences) != 2 {
		t.Fatalf("expected one project directory reference, got %#v", diskWS.DirectoryReferences)
	}
	projectAbs := filepath.Join(baseDir, "album", "first-song")
	projectRef, ok := findAgentWorkspaceDirectoryReference(diskWS.DirectoryReferences, projectAbs)
	if !ok {
		t.Fatalf("project directory reference for %q missing in %#v", projectAbs, diskWS.DirectoryReferences)
	}
	if diskWS.SharedData[workspaceSharedDataPrimaryDirectoryIDKey] != projectRef.ID {
		t.Fatalf("primary directory = %v, want %q", diskWS.SharedData[workspaceSharedDataPrimaryDirectoryIDKey], projectRef.ID)
	}
	if diskWS.SharedData[workspaceSharedDataProjectDirectoryIDKey] != projectRef.ID {
		t.Fatalf("project directory = %v, want %q", diskWS.SharedData[workspaceSharedDataProjectDirectoryIDKey], projectRef.ID)
	}

	sessionWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("session GetWorkspace: %v", err)
	}
	refs, err := decodeDirectoryReferences(sessionWS.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("decode directory refs: %v", err)
	}
	if _, ok := findSessionWorkspaceDirectoryReference(refs, projectAbs); !ok {
		t.Fatalf("session directory refs = %#v, want %q", refs, projectAbs)
	}
	hydrated := handler.hydrateWorkspaceMetadataFromFileStore(&session.Workspace{ID: wsID})
	if hydrated == nil || hydrated.ProjectPath != "first-song" {
		t.Fatalf("hydrated project_path = %+v, want first-song", hydrated)
	}
	if len(hydrated.Tags) != 2 || hydrated.Tags[0] != "music" || hydrated.Tags[1] != "reaper" {
		t.Fatalf("hydrated tags = %#v, want [music reaper]", hydrated.Tags)
	}

	select {
	case event := <-events:
		if event.WorkspaceID != wsID || event.Data["project_path"] != "first-song" {
			t.Fatalf("unexpected project.created payload: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("expected project.created event")
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/project", bytes.NewBufferString(`{"template_id":"demo-template","project_name":"Second Song"}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondW := httptest.NewRecorder()
	handler.HandleWorkspaces(secondW, secondReq)
	if secondW.Code != http.StatusConflict {
		t.Fatalf("expected second create 409, got %d: %s", secondW.Code, secondW.Body.String())
	}
}

func findAgentWorkspaceDirectoryReference(refs []agentworkspace.DirectoryReference, path string) (agentworkspace.DirectoryReference, bool) {
	want := cleanWorkspaceSyncPath(path)
	for _, ref := range refs {
		if cleanWorkspaceSyncPath(ref.Path) == want {
			return ref, true
		}
	}
	return agentworkspace.DirectoryReference{}, false
}

func findSessionWorkspaceDirectoryReference(refs []workspaceDirectoryReference, path string) (workspaceDirectoryReference, bool) {
	want := cleanWorkspaceSyncPath(path)
	for _, ref := range refs {
		if cleanWorkspaceSyncPath(ref.Path) == want {
			return ref, true
		}
	}
	return workspaceDirectoryReference{}, false
}

func TestCreateWorkspaceWithTemplatePathEscapeHatch(t *testing.T) {
	handler, baseDir, _, cleanup := templateTestEnv(t)
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
	handler, baseDir, _, cleanup := templateTestEnv(t)
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
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, _ := postCreateWorkspace(t, handler, `{"name":"Album","kind":"group","template_id":"demo-template"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for group+template, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspaceRejectsBothTemplateFields(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, _ := postCreateWorkspace(t, handler, `{"name":"Both","template_id":"demo-template","template_path":"/tmp/x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for both template fields, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspaceTemplateFailureIsNonFatal(t *testing.T) {
	handler, baseDir, _, cleanup := templateTestEnv(t)
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
