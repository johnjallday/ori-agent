package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
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
	// Mirror production task wiring: task mutations go through the SyncStore
	// (SQLite primary via the session adapter + disk write-through), so the
	// session row's TasksJSON stays consistent with workspace.json and the
	// portable-state sync cannot clobber folder-seeded tasks.
	handler.SetWorkspaceTaskStore(agentworkspace.NewSyncStore(session.NewWorkspaceStoreAdapter(handler.store), fileStore))

	libDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(libDir, "demo-template"), 0o750); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "demo-template", "{{name}}.rpp"), []byte("<REAPER_PROJECT 0.1\n>\n"), 0o640); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "demo-template", "template.json"), []byte(`{"name":"Demo Template","tags":[" Music ","reaper","music"],"project_entry":{"relative_path":"{{name}}.rpp","open_after_create_default":true}}`), 0o640); err != nil {
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
	if diskWS.SharedData[projecttemplates.ProjectEntryPathKey] != "song-x.rpp" {
		t.Fatalf("project entry = %v, want song-x.rpp", diskWS.SharedData[projecttemplates.ProjectEntryPathKey])
	}

	// A task mutation reads through the SQLite-primary SyncStore, whose table
	// does not carry project_path. Its write-through save must merge the
	// disk-canonical value instead of erasing it immediately after creation.
	taskWorkspace, err := handler.workspaceTaskStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceTaskStore.Get: %v", err)
	}
	taskWorkspace.Tasks = append(taskWorkspace.Tasks, agentworkspace.Task{
		ID:     "post-create-task-update",
		Status: agentworkspace.TaskStatusCompleted,
	})
	if err := handler.workspaceTaskStore.Save(taskWorkspace); err != nil {
		t.Fatalf("workspaceTaskStore.Save: %v", err)
	}
	diskWS, err = handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get after task save: %v", err)
	}
	if diskWS.ProjectPath != "song-x" {
		t.Fatalf("disk project_path after task save = %q, want song-x", diskWS.ProjectPath)
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
	if hydrated.SharedData[projecttemplates.ProjectEntryPathKey] != "song-x.rpp" {
		t.Fatalf("hydrated project entry = %v, want song-x.rpp", hydrated.SharedData[projecttemplates.ProjectEntryPathKey])
	}

	select {
	case event := <-events:
		if event.WorkspaceID != wsID || event.Data["project_path"] != "song-x" || event.Data[projecttemplates.ProjectEntryPathKey] != "song-x.rpp" {
			t.Fatalf("unexpected project.created payload: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project.created event not published")
	}
}

// TestCreateWorkspaceIgnoresLegacyOnboardingBlock pins the post-intake
// behavior: a template that still carries an intake-era `onboarding` block
// instantiates its skeleton immediately (nothing defers), and the create
// response carries no onboarding payload.
func TestCreateWorkspaceIgnoresLegacyOnboardingBlock(t *testing.T) {
	handler, baseDir, _, cleanup := templateTestEnv(t)
	defer cleanup()

	tplDir := filepath.Join(handler.templatesRootResolver(), "legacy-onboarding")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tplDir, "{{name}}.rpp"), []byte("<REAPER_PROJECT 0.1\n  TEMPO 120 4 4\n>\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Legacy Onboarding",
		"onboarding":{"version":"1","fields":[{"id":"bpm","label":"BPM","type":"number"}],"completion":{"type":"none","instantiate_skeleton":true}}
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	w, resp := postCreateWorkspace(t, handler, `{"name":"Legacy Flow","template_id":"legacy-onboarding"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if warning, present := resp["project_warning"]; present {
		t.Fatalf("unexpected project_warning: %v", warning)
	}
	if _, present := resp["onboarding"]; present {
		t.Fatal("legacy onboarding block must not produce an onboarding response")
	}
	// The skeleton instantiates immediately — nothing defers on intake.
	if _, err := os.Stat(filepath.Join(baseDir, "legacy-flow", "legacy-flow", "legacy-flow.rpp")); err != nil {
		t.Fatalf("skeleton should instantiate immediately: %v", err)
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
	instanceNames := make([]string, len(sessWS.AgentInstances))
	for i, inst := range sessWS.AgentInstances {
		instanceNames[i] = inst.Name
	}
	for _, name := range []string{"Campaign Lead", "Copywriter", "Designer"} {
		if !slices.Contains(instanceNames, name) {
			t.Fatalf("workspace agent instances %v missing %q", instanceNames, name)
		}
	}
}

func TestCreateWorkspaceTemplateAgentPlanEndpoint(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	handler.SetSystemModelReader(fakeSystemModelReader{provider: "codex", model: "gpt-5.3-codex"})

	tplDir := filepath.Join(handler.templatesRootResolver(), "roster-template")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Roster Template",
		"agents":[
			{"name":"Campaign Lead","role":"orchestrator","system_prompt":"lead it"},
			{"name":"Copywriter","type":"general"}
		]
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/template-agent-plan", bytes.NewBufferString(`{"template_id":"roster-template"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var plan templateAgentPlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if !plan.HasAgents || plan.EntryAgentName != "Campaign Lead" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Agents) != 2 || plan.Agents[0].Action != "create" || plan.Agents[0].Model != "gpt-5.3-codex" {
		t.Fatalf("unexpected planned agents: %+v", plan.Agents)
	}
	if plan.Agents[0].SystemPrompt != "lead it" {
		t.Fatalf("expected system prompt in plan, got %q", plan.Agents[0].SystemPrompt)
	}
}

func TestCreateWorkspaceAppliesTemplateAgentOverrides(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	var appliedTools []string
	handler.SetAgentToolApplier(func(_ string, agentName string, tools projecttemplates.ToolDefaults) ([]string, []string) {
		if len(tools.Skills) > 0 {
			appliedTools = append(appliedTools, agentName+":"+strings.Join(tools.Skills, ","))
		}
		return tools.Skills, nil
	})

	tplDir := filepath.Join(handler.templatesRootResolver(), "roster-template")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Roster Template",
		"agents":[
			{"name":"Campaign Lead","role":"orchestrator","model":"gpt-5-mini","system_prompt":"lead it","tools":{"skills":["planning"]}},
			{"name":"Copywriter","type":"general"}
		]
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	body := `{
		"name":"Launch",
		"template_id":"roster-template",
		"template_agent_overrides":[
			{"index":0,"name":"Launch Lead","model":"gpt-5.7","provider":"codex","system_prompt":"custom launch prompt"}
		]
	}`
	w, resp := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := handler.agentStore.GetAgent("Campaign Lead"); ok {
		t.Fatal("original agent name should not be created after override")
	}
	created, ok := handler.agentStore.GetAgent("Launch Lead")
	if !ok {
		t.Fatal("expected overridden agent name to be created")
	}
	if created.Settings.Model != "gpt-5.7" || created.Settings.Provider != "codex" || created.Settings.SystemPrompt != "custom launch prompt" {
		t.Fatalf("overridden settings not applied: %+v", created.Settings)
	}
	if len(appliedTools) != 1 || appliedTools[0] != "Launch Lead:planning" {
		t.Fatalf("expected original tools bound to renamed agent, got %v", appliedTools)
	}

	folder := resp["folder"].(map[string]any)
	wsID := folder["id"].(string)
	sessWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got := currentWorkspaceEntryAgentName(sessWS); got != "Launch Lead" {
		t.Fatalf("entry agent = %q, want Launch Lead", got)
	}
}

func TestCreateWorkspaceRejectsDuplicateTemplateAgentOverrides(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	tplDir := filepath.Join(handler.templatesRootResolver(), "roster-template")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Roster Template",
		"agents":[
			{"name":"Campaign Lead","role":"orchestrator"},
			{"name":"Copywriter","type":"general"}
		]
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	body := `{
		"name":"Launch",
		"template_id":"roster-template",
		"template_agent_overrides":[
			{"index":0,"name":"Copywriter"}
		]
	}`
	w, _ := postCreateWorkspace(t, handler, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspaceCanSkipTemplateAgentRoster(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	tplDir := filepath.Join(handler.templatesRootResolver(), "roster-template")
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Roster Template",
		"agents":[
			{"name":"Campaign Lead","role":"orchestrator"},
			{"name":"Copywriter","type":"general"}
		]
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}

	w, resp := postCreateWorkspace(t, handler, `{"name":"Launch","template_id":"roster-template","create_template_agents":false}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := handler.agentStore.GetAgent("Campaign Lead"); ok {
		t.Fatal("entry agent should not be created when create_template_agents=false")
	}
	folder := resp["folder"].(map[string]any)
	wsID := folder["id"].(string)
	sessWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if len(sessWS.AgentInstances) != 0 || currentWorkspaceEntryAgentName(sessWS) != "" {
		t.Fatalf("expected agentless workspace, got instances=%v entry=%q", sessWS.AgentInstances, currentWorkspaceEntryAgentName(sessWS))
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
	if warning, present := projectResp["project_warning"]; present {
		t.Fatalf("unexpected project_warning: %v", warning)
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
	if diskWS.SharedData[projecttemplates.ProjectEntryPathKey] != "first-song.rpp" {
		t.Fatalf("project entry = %v, want first-song.rpp", diskWS.SharedData[projecttemplates.ProjectEntryPathKey])
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
	if hydrated.SharedData[projecttemplates.ProjectEntryPathKey] != "first-song.rpp" {
		t.Fatalf("hydrated project entry = %v, want first-song.rpp", hydrated.SharedData[projecttemplates.ProjectEntryPathKey])
	}

	select {
	case event := <-events:
		if event.WorkspaceID != wsID || event.Data["project_path"] != "first-song" || event.Data[projecttemplates.ProjectEntryPathKey] != "first-song.rpp" {
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
