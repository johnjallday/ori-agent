package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/templateonboarding"
	"github.com/johnjallday/ori-agent/internal/templateonboardinghttp"
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

func writeOnboardingTemplate(t *testing.T, libDir string) {
	t.Helper()
	tplDir := filepath.Join(libDir, "onboarding-template")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tplDir, "{{name}}.rpp"), []byte("<REAPER_PROJECT 0.1\n>\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Onboarding Template",
		"tags":["reaper"],
		"onboarding":{
			"version":"1",
			"fields":[
				{"id":"bpm","label":"BPM","type":"number","default":120},
				{"id":"song_name","label":"Song name","type":"string","required":true}
			],
			"completion":{"type":"none","instantiate_skeleton":true}
		}
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
}

func writeOnboardingActionTemplate(t *testing.T, libDir string) {
	t.Helper()
	tplDir := filepath.Join(libDir, "onboarding-action")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tplDir, "{{name}}.rpp"), []byte("<REAPER_PROJECT 0.1\n  TEMPO 120 4 4\n>\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Onboarding Action",
		"tags":["reaper"],
		"onboarding":{
			"version":"1",
			"fields":[
				{"id":"bpm","label":"BPM","type":"number","default":120,"required":true,"validation":{"min":40,"max":240}},
				{"id":"key","label":"Key","type":"enum","default":"C major","required":true,"options":["C major","A minor"]},
				{"id":"song_name","label":"Song name","type":"string","required":true}
			],
			"completion":{
				"type":"task",
				"ref":"reaper-session-setup",
				"instructions":"Create ${fields.song_name} at ${fields.bpm} BPM in ${fields.key}.",
				"skill_refs":["reaper-session-setup"],
				"inputs":{"bpm":"${fields.bpm}","key":"${fields.key}","song_name":"${fields.song_name}"},
				"instantiate_skeleton":true
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
}

func writeMalformedOnboardingTemplate(t *testing.T, libDir string) {
	t.Helper()
	tplDir := filepath.Join(libDir, "malformed-onboarding")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tplDir, "{{name}}.rpp"), []byte("<REAPER_PROJECT 0.1\n>\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Malformed Onboarding",
		"tags":["reaper"],
		"onboarding":{"version":"999","completion":{"type":"none","instantiate_skeleton":true}}
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
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
	if onboarding, present := resp["onboarding"]; present {
		t.Fatalf("unexpected onboarding response for non-onboarding template: %v", onboarding)
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

func TestCreateWorkspaceSeedsTemplateAgentRoster(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	// A metadata-only template (no skeleton files) that declares an agent roster.
	tplDir := filepath.Join(handler.templatesRootResolver(), "roster-template")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Roster Template",
		"agents":[
			{"name":"Campaign Lead","role":"orchestrator","system_prompt":"lead it"},
			{"name":"Copywriter","type":"general"},
			{"name":"Designer"}
		]
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	w, resp := postCreateWorkspace(t, handler, `{"name":"Launch","template_id":"roster-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if warnings, present := resp["agent_warnings"]; present {
		t.Fatalf("unexpected agent_warnings: %v", warnings)
	}

	for _, name := range []string{"Campaign Lead", "Copywriter", "Designer"} {
		if _, ok := handler.agentStore.GetAgent(name); !ok {
			t.Fatalf("expected agent %q seeded into the agent store", name)
		}
	}

	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatal("expected folder in response")
	}
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id in response")
	}

	sessWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	// First declared agent is the entry agent (suppresses the mandatory prompt).
	if got := currentWorkspaceEntryAgentName(sessWS); got != "Campaign Lead" {
		t.Fatalf("entry agent = %q, want Campaign Lead", got)
	}
	for _, name := range []string{"Campaign Lead", "Copywriter", "Designer"} {
		if !slices.Contains(sessWS.Agents, name) {
			t.Fatalf("workspace agents %v missing %q", sessWS.Agents, name)
		}
	}
}

func TestCreateWorkspaceWithOnboardingTemplateDefersProject(t *testing.T) {
	handler, baseDir, events, cleanup := templateTestEnv(t)
	defer cleanup()

	libDir := handler.templatesRootResolver()
	writeOnboardingTemplate(t, libDir)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Onboarding Song","template_id":"onboarding-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if warning, present := resp["project_warning"]; present {
		t.Fatalf("unexpected project_warning: %v", warning)
	}

	folder := resp["folder"].(map[string]any)
	wsID := folder["id"].(string)
	if got, present := folder["project_path"]; present && got != "" {
		t.Fatalf("project_path = %v, want empty until onboarding completion", got)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "onboarding-song", "onboarding-song")); !os.IsNotExist(err) {
		t.Fatalf("project folder should be deferred until completion (err=%v)", err)
	}

	onboarding, ok := resp["onboarding"].(map[string]any)
	if !ok {
		t.Fatalf("expected onboarding summary in response: %#v", resp["onboarding"])
	}
	if got := onboarding["status"]; got != string(templateonboarding.StatusPendingEntryAgent) {
		t.Fatalf("onboarding status = %v, want pending_entry_agent", got)
	}
	fields, ok := onboarding["fields"].([]any)
	if !ok || len(fields) != 2 {
		t.Fatalf("onboarding fields = %#v, want 2 fields", onboarding["fields"])
	}

	store := templateonboarding.NewStore(handler.workspaceStore)
	session, err := store.Load(context.Background(), wsID)
	if err != nil {
		t.Fatalf("load onboarding session: %v", err)
	}
	if session.Status != templateonboarding.StatusPendingEntryAgent {
		t.Fatalf("stored onboarding status = %q, want pending_entry_agent", session.Status)
	}
	if len(session.Spec.Fields) != 2 || session.Spec.Fields[0].ID != "bpm" {
		t.Fatalf("stored spec snapshot = %+v", session.Spec)
	}

	select {
	case event := <-events:
		t.Fatalf("project.created should not fire before onboarding completion: %+v", event)
	default:
	}
}

func TestAddingEntryAgentResumesPendingTemplateOnboarding(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeOnboardingTemplate(t, handler.templatesRootResolver())
	w, resp := postCreateWorkspace(t, handler, `{"name":"Agent Later","template_id":"onboarding-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	wsID := resp["folder"].(map[string]any)["id"].(string)
	store := templateonboarding.NewStore(handler.workspaceStore)
	session, err := store.Load(context.Background(), wsID)
	if err != nil {
		t.Fatalf("load onboarding session: %v", err)
	}
	if _, err := session.MergeValues(map[string]any{"song_name": "Late Entry"}); err != nil {
		t.Fatalf("MergeValues: %v", err)
	}
	if err := store.Save(context.Background(), session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/agents", bytes.NewBufferString(`{"agent_name":"Producer"}`))
	req.Header.Set("Content-Type", "application/json")
	addW := httptest.NewRecorder()
	handler.HandleWorkspaces(addW, req)
	if addW.Code != http.StatusCreated {
		t.Fatalf("expected add agent 201, got %d: %s", addW.Code, addW.Body.String())
	}
	var addResp map[string]any
	if err := json.Unmarshal(addW.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("decode add response: %v", err)
	}
	onboarding := addResp["onboarding"].(map[string]any)
	if got := onboarding["status"]; got != string(templateonboarding.StatusCollecting) {
		t.Fatalf("onboarding status after add = %v, want collecting", got)
	}

	reloaded, err := store.Load(context.Background(), wsID)
	if err != nil {
		t.Fatalf("reload onboarding session: %v", err)
	}
	if reloaded.Status != templateonboarding.StatusCollecting {
		t.Fatalf("stored status after add = %q, want collecting", reloaded.Status)
	}
	if reloaded.Values["song_name"] != "Late Entry" {
		t.Fatalf("stored values after add = %#v, want preserved song_name", reloaded.Values)
	}
}

func TestTemplateOnboardingCreateValuesCompleteWithStubbedTask(t *testing.T) {
	handler, baseDir, events, cleanup := templateTestEnv(t)
	defer cleanup()

	writeOnboardingActionTemplate(t, handler.templatesRootResolver())
	if err := handler.agentStore.CreateAgent("Producer", &agentstore.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	w, resp := postCreateWorkspace(t, handler, `{"name":"Studio Song","template_id":"onboarding-action","entry_agent_name":"Producer"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	folder := resp["folder"].(map[string]any)
	wsID := folder["id"].(string)
	if got, present := folder["project_path"]; present && got != "" {
		t.Fatalf("project_path = %v, want deferred until completion", got)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "studio-song", "studio-song")); !os.IsNotExist(err) {
		t.Fatalf("project folder should not exist before completion (err=%v)", err)
	}
	onboarding := resp["onboarding"].(map[string]any)
	if got := onboarding["status"]; got != string(templateonboarding.StatusCollecting) {
		t.Fatalf("onboarding status = %v, want collecting", got)
	}

	// Later edits to template.json must not alter the in-flight session's spec.
	mutatedManifest := `{"name":"Mutated","onboarding":{"version":"999","completion":{"type":"none"}}}`
	if err := os.WriteFile(filepath.Join(handler.templatesRootResolver(), "onboarding-action", "template.json"), []byte(mutatedManifest), 0o640); err != nil {
		t.Fatal(err)
	}

	store := templateonboarding.NewStore(handler.workspaceStore)
	httpHandler := templateonboardinghttp.NewHandler(store, templateonboardinghttp.EntryAgentResolverFunc(func(ctx context.Context, workspaceID string) (string, error) {
		if workspaceID != wsID {
			t.Fatalf("entry agent resolver workspaceID = %q, want %q", workspaceID, wsID)
		}
		return "Producer", nil
	}))
	runner := &stubTaskCompletionRunner{store: store, handler: handler}
	httpHandler.SetCompletionRunner(runner)
	mux := http.NewServeMux()
	httpHandler.RegisterRoutes(mux)

	patch := httptest.NewRecorder()
	mux.ServeHTTP(patch, httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/template-onboarding/values", bytes.NewBufferString(`{"values":{"bpm":132,"key":"A minor","song_name":"Solar Drift"}}`)))
	if patch.Code != http.StatusOK {
		t.Fatalf("PATCH values = %d, want 200: %s", patch.Code, patch.Body.String())
	}
	var valuesResp templateonboardinghttp.StatusResponse
	if err := json.Unmarshal(patch.Body.Bytes(), &valuesResp); err != nil {
		t.Fatalf("decode values response: %v", err)
	}
	if valuesResp.Status != templateonboarding.StatusReadyToComplete {
		t.Fatalf("status after values = %q, want ready_to_complete", valuesResp.Status)
	}
	if len(valuesResp.Fields) != 3 || valuesResp.Fields[0].ID != "bpm" {
		t.Fatalf("fields came from mutated template, got %+v", valuesResp.Fields)
	}

	complete := httptest.NewRecorder()
	mux.ServeHTTP(complete, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/template-onboarding/complete", nil))
	if complete.Code != http.StatusOK {
		t.Fatalf("complete = %d, want 200: %s", complete.Code, complete.Body.String())
	}
	var completeResp templateonboardinghttp.StatusResponse
	if err := json.Unmarshal(complete.Body.Bytes(), &completeResp); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}
	if completeResp.Status != templateonboarding.StatusSucceeded {
		t.Fatalf("complete status = %q, want succeeded", completeResp.Status)
	}
	if completeResp.ActionResult == nil || completeResp.ActionResult.RunID != "stub-run-1" || completeResp.ActionResult.ProjectPath != "studio-song" {
		t.Fatalf("action result = %+v, want stub run and project path", completeResp.ActionResult)
	}
	if !runner.called {
		t.Fatal("stub completion runner was not called")
	}
	if _, err := os.Stat(filepath.Join(baseDir, "studio-song", "studio-song", "studio-song.rpp")); err != nil {
		t.Fatalf("expected deferred project seed after completion: %v", err)
	}

	persisted, err := store.Load(context.Background(), wsID)
	if err != nil {
		t.Fatalf("load persisted onboarding session: %v", err)
	}
	if persisted.Status != templateonboarding.StatusSucceeded {
		t.Fatalf("persisted status = %q, want succeeded", persisted.Status)
	}
	if persisted.Spec.Fields[0].Label != "BPM" || persisted.Spec.Completion.Type != templateonboarding.ActionTask {
		t.Fatalf("stored spec snapshot changed unexpectedly: %+v", persisted.Spec)
	}
	if persisted.Values["song_name"] != "Solar Drift" || persisted.Values["bpm"] != float64(132) {
		t.Fatalf("persisted values = %#v", persisted.Values)
	}

	select {
	case event := <-events:
		if event.WorkspaceID != wsID || event.Data["project_path"] != "studio-song" {
			t.Fatalf("unexpected project.created payload: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("expected project.created event after completion")
	}
}

func TestMalformedOnboardingTemplateFallsBackToImmediateInstantiation(t *testing.T) {
	handler, baseDir, _, cleanup := templateTestEnv(t)
	defer cleanup()

	writeMalformedOnboardingTemplate(t, handler.templatesRootResolver())
	w, resp := postCreateWorkspace(t, handler, `{"name":"Broken Onboarding","template_id":"malformed-onboarding"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if warning, present := resp["project_warning"]; present {
		t.Fatalf("unexpected project_warning: %v", warning)
	}
	if onboarding, present := resp["onboarding"]; present {
		t.Fatalf("malformed onboarding must not create an onboarding response: %v", onboarding)
	}
	folder := resp["folder"].(map[string]any)
	wsID := folder["id"].(string)
	if got := folder["project_path"]; got != "broken-onboarding" {
		t.Fatalf("project_path = %v, want immediate instantiation", got)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "broken-onboarding", "broken-onboarding", "broken-onboarding.rpp")); err != nil {
		t.Fatalf("expected project seed despite malformed onboarding: %v", err)
	}
	store := templateonboarding.NewStore(handler.workspaceStore)
	if _, err := store.Load(context.Background(), wsID); err == nil {
		t.Fatal("malformed onboarding should not persist a session")
	}
}

type stubTaskCompletionRunner struct {
	store   *templateonboarding.Store
	handler *Handler
	called  bool
}

func (r *stubTaskCompletionRunner) Complete(ctx context.Context, session *templateonboarding.Session, entryAgentName string) (*templateonboarding.ActionResult, error) {
	if r == nil || r.store == nil || r.handler == nil {
		return nil, fmt.Errorf("stub completion runner is not configured")
	}
	if entryAgentName != "Producer" {
		return nil, fmt.Errorf("entry agent = %q, want Producer", entryAgentName)
	}
	if session.TemplateID != "onboarding-action" || session.TemplatePath == "" {
		return nil, fmt.Errorf("template metadata missing from session: id=%q path=%q", session.TemplateID, session.TemplatePath)
	}
	if session.Spec.Completion.Type != templateonboarding.ActionTask || !session.Spec.Completion.InstantiateSkeleton {
		return nil, fmt.Errorf("completion = %+v, want task with skeleton", session.Spec.Completion)
	}
	if session.Values["song_name"] != "Solar Drift" || session.Values["bpm"] != float64(132) || session.Values["key"] != "A minor" {
		return nil, fmt.Errorf("values = %#v, want patched onboarding values", session.Values)
	}

	if _, err := session.StartCompletion(); err != nil {
		return nil, err
	}
	if err := r.store.Save(ctx, session); err != nil {
		return nil, err
	}
	projectPath, err := r.handler.InstantiateProject(ctx, session.WorkspaceID, session.TemplateID, session.TemplatePath, session.ProjectName, session.Values)
	if err != nil {
		_, _ = session.MarkFailed(err.Error())
		_ = r.store.Save(ctx, session)
		return nil, err
	}
	result := &templateonboarding.ActionResult{
		Result:      "stubbed task completed",
		RunID:       "stub-run-1",
		TaskID:      "stub-task-1",
		ProjectPath: projectPath,
	}
	if _, err := session.MarkSucceeded(result); err != nil {
		return nil, err
	}
	if err := r.store.Save(ctx, session); err != nil {
		return nil, err
	}
	r.called = true
	return result, nil
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
	if onboarding, present := resp["onboarding"]; present {
		t.Fatalf("onboarding must be absent without a template, got %v", onboarding)
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
