package sessionhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// writeStarterTaskTemplate writes a template with an agent roster and two
// starter tasks (the first flagged setup) into the handler's library.
func writeStarterTaskTemplate(t *testing.T, libDir, id string, withRoster bool) {
	t.Helper()
	tplDir := filepath.Join(libDir, id)
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tplDir, "{{name}}.rpp"), []byte("<REAPER_PROJECT 0.1\n  TEMPO 120 4 4\n>\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Starter Tasks Template",
		"starter_tasks":[
			{"description":"Adjust the session","details":"## Questions to ask\n- tempo?","setup":true},
			{"description":"Sketch an arrangement"}
		]`
	if withRoster {
		manifest += `,
		"agents":[{"name":"Producer","role":"orchestrator","system_prompt":"produce"}]`
	}
	manifest += "\n}"
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
}

func workspaceTasksFromStore(t *testing.T, handler *Handler, wsID string) []agentworkspace.Task {
	t.Helper()
	ws, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get: %v", err)
	}
	return ws.Tasks
}

func TestCreateWorkspaceSeedsStarterTasksServerSide(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Song Y","template_id":"starter-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if got, _ := resp["seeded_starter_tasks"].(float64); got != 2 {
		t.Fatalf("seeded_starter_tasks = %v, want 2", resp["seeded_starter_tasks"])
	}

	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	tasks := workspaceTasksFromStore(t, handler, wsID)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 seeded tasks, got %d: %+v", len(tasks), tasks)
	}

	var setupTask, plainTask *agentworkspace.Task
	for i := range tasks {
		if tasks[i].Context[taskContextTemplateSetup] == true {
			setupTask = &tasks[i]
		} else {
			plainTask = &tasks[i]
		}
	}
	if setupTask == nil || plainTask == nil {
		t.Fatalf("expected one setup + one plain task, got %+v", tasks)
	}
	if setupTask.Description != "Adjust the session" {
		t.Fatalf("setup task description = %q", setupTask.Description)
	}
	for _, task := range tasks {
		// Assigned to the roster's entry agent with system provenance at seed time.
		if task.To != "Producer" {
			t.Fatalf("task %q assigned to %q, want Producer", task.Description, task.To)
		}
		if task.AssignmentMode != agentworkspace.TaskAssignmentModeEntryAgentDefault {
			t.Fatalf("task %q assignment mode = %q", task.Description, task.AssignmentMode)
		}
		// Seeded pending, never started at creation.
		if task.Status != agentworkspace.TaskStatusPending {
			t.Fatalf("task %q status = %q, want pending", task.Description, task.Status)
		}
		if task.Context[taskContextTemplateStarterTask] != true || task.Context[taskContextTemplateID] != "starter-template" {
			t.Fatalf("task %q missing template provenance context: %+v", task.Description, task.Context)
		}
	}
	// No consumed marker at seed time: first open must find it unconsumed.
	if _, present := setupTask.Context["template_setup_autostart_consumed_at"]; present {
		t.Fatalf("setup task must not be consumed at seed time: %+v", setupTask.Context)
	}
}

func TestCreateReaperWorkspaceSeedsRealWorkWithoutSetupHelpTask(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	if err := projecttemplates.EnsureLibrary(handler.templatesRootResolver()); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	var modelStarts int
	handler.SetTemplateSetupTaskStarter(func(workspaceID, taskID string) error {
		modelStarts++
		return nil
	})
	w, resp := postCreateWorkspace(t, handler, `{"name":"Runtime Song","template_id":"reaper-song"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 without local REAPER prerequisites, got %d: %s", w.Code, w.Body.String())
	}
	if got, _ := resp["seeded_starter_tasks"].(float64); got != 2 {
		t.Fatalf("seeded_starter_tasks = %v, want only the two real work tasks", resp["seeded_starter_tasks"])
	}

	folder := resp["folder"].(map[string]any)
	workspaceID := folder["id"].(string)
	tasks := workspaceTasksFromStore(t, handler, workspaceID)
	if len(tasks) != 2 {
		t.Fatalf("seeded tasks = %+v", tasks)
	}
	wantDescriptions := []string{
		"Adjust the new REAPER session to the user's preferences",
		"Sketch the song's arrangement",
	}
	for i, task := range tasks {
		if task.Description != wantDescriptions[i] {
			t.Errorf("task %d description = %q, want %q", i, task.Description, wantDescriptions[i])
		}
		if task.Context[taskContextTemplateSetup] == true {
			t.Errorf("real starter work must not be marked as setup: %+v", task)
		}
	}

	start := postTemplateSetupStart(t, handler, workspaceID)
	if start["started"] != false || start["reason"] != "setup_wizard_owned" {
		t.Fatalf("runtime wizard must own setup without starting a task: %v", start)
	}
	handler.CompleteSetupHelpTaskOnWizardReady(workspaceID)
	for _, task := range workspaceTasksFromStore(t, handler, workspaceID) {
		if task.Status != agentworkspace.TaskStatusPending {
			t.Errorf("setup completion changed real starter work: %+v", task)
		}
	}
	if modelStarts != 0 {
		t.Fatalf("runtime setup must consume zero model starts, got %d", modelStarts)
	}
}

func TestSeedTemplateStarterTaskPreservesExplicitFileFallback(t *testing.T) {
	store := agentworkspace.NewInMemoryStore()
	handler := New(nil)
	handler.SetWorkspaceTaskStore(store)
	ws := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Fallback", Agents: []string{"Producer"}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	_, err := handler.seedTemplateStarterTasks(ws.ID, projecttemplates.Template{ID: "runtime", StarterTasks: []projecttemplates.StarterTask{{
		Description: "Adjust session", Requires: []string{"reaper_live_control"}, FileFallbackFor: []string{"reaper_live_control"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasks := saved.Tasks
	if len(tasks) != 1 || len(tasks[0].RequiredCapabilities) != 1 || len(tasks[0].FileFallbackFor) != 1 || tasks[0].FileFallbackFor[0] != "reaper_live_control" {
		t.Fatalf("seeded fallback task = %+v", tasks)
	}
}

func TestCreateWorkspaceRosterlessTemplateAutoCreatesManager(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "rosterless-template", false)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Legacy Song","template_id":"rosterless-template"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// The fallback manager exists and is the workspace entry agent.
	if _, ok := handler.agentStore.GetAgent("Legacy Song Manager"); !ok {
		t.Fatal("expected auto-created 'Legacy Song Manager' agent for roster-less template")
	}

	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	tasks := workspaceTasksFromStore(t, handler, wsID)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 seeded tasks, got %+v", tasks)
	}
	for _, task := range tasks {
		if task.To != "Legacy Song Manager" {
			t.Fatalf("task %q assigned to %q, want the fallback manager", task.Description, task.To)
		}
	}
}

func TestCreateWorkspaceTemplateAgentsOptOutSeedsUnassignedTasks(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "optout-template", true)

	w, resp := postCreateWorkspace(t, handler, `{"name":"Solo","template_id":"optout-template","create_template_agents":false}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Opt-out is respected: neither the roster agent nor a fallback manager.
	if _, ok := handler.agentStore.GetAgent("Producer"); ok {
		t.Fatal("roster agent must not be created on explicit opt-out")
	}
	if _, ok := handler.agentStore.GetAgent("Solo Manager"); ok {
		t.Fatal("fallback manager must not be created on explicit opt-out")
	}

	// Tasks still seed, unassigned, ready for the claim-on-agent-add sweep.
	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	tasks := workspaceTasksFromStore(t, handler, wsID)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 seeded tasks, got %+v", tasks)
	}
	for _, task := range tasks {
		if task.To != "" && task.To != "unassigned" {
			t.Fatalf("task %q should be unassigned on opt-out, got %q", task.Description, task.To)
		}
	}
}

func TestCreateWorkspaceWithoutTemplateSeedsNothing(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	w, resp := postCreateWorkspace(t, handler, `{"name":"Plain"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if _, present := resp["seeded_starter_tasks"]; present {
		t.Fatalf("blank workspace must not report seeded tasks: %v", resp["seeded_starter_tasks"])
	}
	// Blank (template-less) workspaces stay agent-less by design.
	if _, ok := handler.agentStore.GetAgent("Plain Manager"); ok {
		t.Fatal("blank workspace must not auto-create a manager agent")
	}

	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	if tasks := workspaceTasksFromStore(t, handler, wsID); len(tasks) != 0 {
		t.Fatalf("expected no tasks, got %+v", tasks)
	}
}

func postTemplateSetupStart(t *testing.T, handler *Handler, wsID string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/template-setup/start", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("template-setup/start = %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode setup-start response: %v", err)
	}
	return resp
}

func TestTemplateSetupStartConsumesOnce(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)

	var started []string
	handler.SetTemplateSetupTaskStarter(func(workspaceID, taskID string) error {
		started = append(started, taskID)
		return nil
	})

	_, resp := postCreateWorkspace(t, handler, `{"name":"Once","template_id":"starter-template"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)

	first := postTemplateSetupStart(t, handler, wsID)
	if first["started"] != true || first["task_id"] == "" {
		t.Fatalf("first open should start the setup task: %v", first)
	}
	if len(started) != 1 {
		t.Fatalf("starter called %d times, want 1", len(started))
	}

	// Consumed marker persisted on the task.
	tasks := workspaceTasksFromStore(t, handler, wsID)
	var setupTask *agentworkspace.Task
	for i := range tasks {
		if tasks[i].Context[taskContextTemplateSetup] == true {
			setupTask = &tasks[i]
		}
	}
	if setupTask == nil {
		t.Fatal("setup task missing")
	}
	if _, ok := setupTask.Context[taskContextSetupConsumedAt].(string); !ok {
		t.Fatalf("consumed marker not persisted: %+v", setupTask.Context)
	}

	// Second open no-ops and never re-invokes the starter.
	second := postTemplateSetupStart(t, handler, wsID)
	if second["started"] != false || second["reason"] != "already_consumed" {
		t.Fatalf("second open should no-op: %v", second)
	}
	if len(started) != 1 {
		t.Fatalf("starter re-invoked on second open: %d calls", len(started))
	}
}

func TestTemplateSetupStartFailureKeepsMarker(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)

	calls := 0
	handler.SetTemplateSetupTaskStarter(func(workspaceID, taskID string) error {
		calls++
		return fmt.Errorf("boom")
	})

	_, resp := postCreateWorkspace(t, handler, `{"name":"Flaky","template_id":"starter-template"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)

	first := postTemplateSetupStart(t, handler, wsID)
	if first["started"] != false || first["reason"] != "start_failed" {
		t.Fatalf("expected start_failed: %v", first)
	}
	// Consumed despite the failure: no auto-retry on the next open.
	second := postTemplateSetupStart(t, handler, wsID)
	if second["started"] != false || second["reason"] != "already_consumed" {
		t.Fatalf("failed start must not retry: %v", second)
	}
	if calls != 1 {
		t.Fatalf("starter calls = %d, want 1", calls)
	}
	// The task itself is still pending and manually startable.
	for _, task := range workspaceTasksFromStore(t, handler, wsID) {
		if task.Context[taskContextTemplateSetup] == true && task.Status != agentworkspace.TaskStatusPending {
			t.Fatalf("setup task status = %q, want pending", task.Status)
		}
	}
}

func TestTemplateSetupStartSkipsUnassignedTask(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "optout-template", true)

	calls := 0
	handler.SetTemplateSetupTaskStarter(func(workspaceID, taskID string) error {
		calls++
		return nil
	})

	_, resp := postCreateWorkspace(t, handler, `{"name":"NoAgent","template_id":"optout-template","create_template_agents":false}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)

	// Unassigned setup task: not consumable, not started, marker left off so a
	// later open (after an agent joins) still fires.
	if tasks := workspaceTasksFromStore(t, handler, wsID); len(tasks) != 2 {
		t.Fatalf("tasks before first open = %d, want 2", len(tasks))
	}
	first := postTemplateSetupStart(t, handler, wsID)
	if first["started"] != false || first["reason"] != "unassigned" {
		t.Fatalf("expected unassigned no-op: %v", first)
	}
	if tasks := workspaceTasksFromStore(t, handler, wsID); len(tasks) != 2 {
		t.Fatalf("tasks after first open = %d, want 2", len(tasks))
	}
	if calls != 0 {
		t.Fatalf("starter must not run for unassigned setup task")
	}

	// An agent joins → claim sweep assigns the task → next open starts it.
	if err := handler.agentStore.CreateAgent("Helper", nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/agents", strings.NewReader(`{"agent_name":"Helper"}`))
	req.Header.Set("Content-Type", "application/json")
	addW := httptest.NewRecorder()
	handler.HandleWorkspaces(addW, req)
	if addW.Code != http.StatusCreated {
		t.Fatalf("add agent = %d: %s", addW.Code, addW.Body.String())
	}

	// Regression guard: the add-agent portable-state sync must not clobber
	// folder-store tasks (it once did when the session row lacked task data).
	if tasks := workspaceTasksFromStore(t, handler, wsID); len(tasks) != 2 {
		t.Fatalf("tasks after add-agent = %d, want 2 (portable-state sync wiped them)", len(tasks))
	}

	second := postTemplateSetupStart(t, handler, wsID)
	if second["started"] != true {
		t.Fatalf("setup should start after agent joins: %v", second)
	}
	if calls != 1 {
		t.Fatalf("starter calls = %d, want 1", calls)
	}
}

func TestTemplateSetupStartNoSetupTask(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	_, resp := postCreateWorkspace(t, handler, `{"name":"Plain Two"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)

	result := postTemplateSetupStart(t, handler, wsID)
	if result["started"] != false || result["reason"] != "no_setup_task" {
		t.Fatalf("expected no_setup_task no-op: %v", result)
	}
}

// writeWizardStarterTaskTemplate is writeStarterTaskTemplate's wizard-enabled
// sibling: the same setup help task, plus a blueprint Setup Wizard that owns
// the setup it describes.
func writeWizardStarterTaskTemplate(t *testing.T, libDir, id string) {
	t.Helper()
	tplDir := filepath.Join(libDir, id)
	if err := os.MkdirAll(tplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"Wizard Starter Template",
		"builtin": true,
		"builtin_version": 1,
		"directory_requirements":[{"key":"inbox-root","label":"Inbox folder"}],
		"starter_tasks":[
			{"description":"Set up this workspace","details":"Explain the setup steps","setup":true},
			{"description":"Do the real work"}
		],
		"agents":[{"name":"Producer","role":"orchestrator","system_prompt":"produce"}],
		"setup_wizard":{
			"version":1,
			"title":"Set up the workspace",
			"steps":[
				{"id":"folder","kind":"directory","requirement_key":"inbox-root","required":true},
				{"id":"summary","kind":"summary","required":false}
			]
		}
	}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
}

func setupTaskFor(t *testing.T, handler *Handler, wsID string) agentworkspace.Task {
	t.Helper()
	for _, task := range workspaceTasksFromStore(t, handler, wsID) {
		if task.Context[taskContextTemplateSetup] == true {
			return task
		}
	}
	t.Fatalf("workspace %s has no setup task", wsID)
	return agentworkspace.Task{}
}

// TestTemplateSetupStart_SuppressedWhenBlueprintDeclaresAWizard covers FR-67:
// a wizard-enabled blueprint owns its own setup, so the help task must not
// auto-start on top of the setup dialog.
func TestTemplateSetupStart_SuppressedWhenBlueprintDeclaresAWizard(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeWizardStarterTaskTemplate(t, handler.templatesRootResolver(), "wizard-starter")

	var started []string
	handler.SetTemplateSetupTaskStarter(func(workspaceID, taskID string) error {
		started = append(started, taskID)
		return nil
	})

	_, resp := postCreateWorkspace(t, handler, `{"name":"Wizard WS","template_id":"wizard-starter"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)

	first := postTemplateSetupStart(t, handler, wsID)
	if first["started"] != false || first["reason"] != "setup_wizard_owned" {
		t.Fatalf("a wizard-enabled blueprint must not auto-start its help task: %v", first)
	}
	if len(started) != 0 {
		t.Fatalf("no agent execution may be triggered, got %d", len(started))
	}

	// The task is still there, still pending, and still unconsumed: it remains
	// available as optional help, and suppressing it is not the same as
	// spending it.
	task := setupTaskFor(t, handler, wsID)
	if task.Status != agentworkspace.TaskStatusPending {
		t.Fatalf("setup task status = %q, want pending", task.Status)
	}
	if _, consumed := task.Context[taskContextSetupConsumedAt]; consumed {
		t.Fatalf("suppression must not consume the auto-start marker: %+v", task.Context)
	}

	// Repeat opens keep reporting the same thing, without side effects.
	if second := postTemplateSetupStart(t, handler, wsID); second["reason"] != "setup_wizard_owned" {
		t.Fatalf("second open: %v", second)
	}
	if len(started) != 0 {
		t.Fatalf("no agent execution may be triggered, got %d", len(started))
	}
}

// TestTemplateSetupStart_LegacyTemplatesKeepAutoStarting covers FR-68: a
// blueprint without a wizard behaves exactly as it did before this feature.
func TestTemplateSetupStart_LegacyTemplatesKeepAutoStarting(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "legacy-starter", true)

	var started []string
	handler.SetTemplateSetupTaskStarter(func(workspaceID, taskID string) error {
		started = append(started, taskID)
		return nil
	})

	_, resp := postCreateWorkspace(t, handler, `{"name":"Legacy WS","template_id":"legacy-starter"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)

	if first := postTemplateSetupStart(t, handler, wsID); first["started"] != true {
		t.Fatalf("a template with no wizard must keep its auto-start: %v", first)
	}
	if len(started) != 1 {
		t.Fatalf("starter called %d times, want 1", len(started))
	}
}

func TestRuntimeMigrationSupersedesOnlyUntouchedLegacyReaperSetupTask(t *testing.T) {
	store := agentworkspace.NewInMemoryStore()
	handler := New(nil)
	handler.SetWorkspaceTaskStore(store)
	ws := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Legacy Reaper"})
	ws.SetTemplateProvenance(&agentworkspace.TemplateProvenance{TemplateID: "reaper-song", Builtin: true, Version: 8})
	baseContext := func() map[string]any {
		return map[string]any{
			taskContextTemplateID:          "reaper-song",
			taskContextTemplateStarterTask: true,
			taskContextTemplateSetup:       true,
		}
	}
	ws.Tasks = []agentworkspace.Task{
		{ID: "untouched", Description: legacyReaperSetupTaskDescription, Details: legacyReaperSetupTaskDetails, Status: agentworkspace.TaskStatusPending, Context: baseContext()},
		{ID: "edited", Description: legacyReaperSetupTaskDescription, Details: legacyReaperSetupTaskDetails + " User note.", Status: agentworkspace.TaskStatusPending, Context: baseContext()},
		{ID: "historical", Description: legacyReaperSetupTaskDescription, Details: legacyReaperSetupTaskDetails, Status: agentworkspace.TaskStatusFailed, Context: baseContext(), Error: "kept"},
		{ID: "real-work", Description: "Adjust the session", Status: agentworkspace.TaskStatusPending, Context: map[string]any{taskContextTemplateID: "reaper-song", taskContextTemplateStarterTask: true}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	handler.SupersedeLegacyReaperSetupHelpTask(ws.ID)
	saved, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	var firstCompletedAt *time.Time
	for _, task := range saved.Tasks {
		switch task.ID {
		case "untouched":
			if task.Status != agentworkspace.TaskStatusCompleted || task.CompletedAt == nil || task.Result != legacyReaperSetupSupersededNote {
				t.Fatalf("untouched setup task was not superseded: %+v", task)
			}
			firstCompletedAt = task.CompletedAt
		case "edited":
			if task.Status != agentworkspace.TaskStatusPending || !strings.HasSuffix(task.Details, "User note.") {
				t.Fatalf("edited setup task changed: %+v", task)
			}
		case "historical":
			if task.Status != agentworkspace.TaskStatusFailed || task.Error != "kept" {
				t.Fatalf("historical setup task changed: %+v", task)
			}
		case "real-work":
			if task.Status != agentworkspace.TaskStatusPending {
				t.Fatalf("real starter work changed: %+v", task)
			}
		}
	}

	handler.SupersedeLegacyReaperSetupHelpTask(ws.ID)
	again, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range again.Tasks {
		if task.ID == "untouched" && (task.CompletedAt == nil || !task.CompletedAt.Equal(*firstCompletedAt)) {
			t.Fatalf("replayed supersede rewrote completion: %+v", task)
		}
	}
}

// TestSetupWizardCompletesTheHelpTask covers FR-70 to FR-73: when setup first
// becomes ready the help task is completed by the server, with a note, without
// a model call, once.
func TestSetupWizardCompletesTheHelpTask(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeWizardStarterTaskTemplate(t, handler.templatesRootResolver(), "wizard-starter")

	var started []string
	handler.SetTemplateSetupTaskStarter(func(workspaceID, taskID string) error {
		started = append(started, taskID)
		return nil
	})

	_, resp := postCreateWorkspace(t, handler, `{"name":"Wizard WS","template_id":"wizard-starter"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)

	handler.CompleteSetupHelpTaskOnWizardReady(wsID)

	task := setupTaskFor(t, handler, wsID)
	if task.Status != agentworkspace.TaskStatusCompleted {
		t.Fatalf("setup task status = %q, want completed", task.Status)
	}
	if task.CompletedAt == nil {
		t.Fatal("completion time not recorded")
	}
	if !strings.Contains(task.Result, "Setup Wizard") {
		t.Fatalf("completion must be attributed to the wizard, got %q", task.Result)
	}
	if len(started) != 0 {
		t.Fatalf("completion must not run an agent, got %d executions", len(started))
	}
	marker, ok := task.Context[taskContextSetupWizardCompletedAt].(string)
	if !ok || marker == "" {
		t.Fatalf("completion marker not recorded: %+v", task.Context)
	}

	// Replay (a retried hook, a second tab, a repair-then-ready cycle) changes
	// nothing: no duplicate completion, no rewritten history.
	handler.CompleteSetupHelpTaskOnWizardReady(wsID)
	again := setupTaskFor(t, handler, wsID)
	if again.Context[taskContextSetupWizardCompletedAt] != marker {
		t.Fatalf("a replayed completion rewrote the marker: %v -> %v", marker, again.Context[taskContextSetupWizardCompletedAt])
	}
	if !again.CompletedAt.Equal(*task.CompletedAt) {
		t.Fatalf("a replayed completion rewrote the completion time: %v -> %v", task.CompletedAt, again.CompletedAt)
	}

	// The blueprint's other starter task is untouched: setup being ready says
	// nothing about the actual work.
	var others int
	for _, other := range workspaceTasksFromStore(t, handler, wsID) {
		if other.Context[taskContextTemplateSetup] != true && other.Status == agentworkspace.TaskStatusPending {
			others++
		}
	}
	if others != 1 {
		t.Fatalf("expected the non-setup starter task to stay pending, got %d pending", others)
	}
}

// TestSetupWizardCompletion_LeavesOtherWorkAlone covers FR-74 and the
// don't-rewrite-someone-else's-work rule.
func TestSetupWizardCompletion_LeavesOtherWorkAlone(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	// A workspace whose blueprint seeds no setup task at all: readiness is
	// simply not represented by a task, and that is not an error.
	_, resp := postCreateWorkspace(t, handler, `{"name":"No Task WS","template_id":"demo-template"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)
	handler.CompleteSetupHelpTaskOnWizardReady(wsID)
	for _, task := range workspaceTasksFromStore(t, handler, wsID) {
		if task.Status == agentworkspace.TaskStatusCompleted {
			t.Fatalf("nothing should have been completed: %+v", task)
		}
	}

	// An unknown workspace is a no-op rather than a panic or an error path.
	handler.CompleteSetupHelpTaskOnWizardReady("does-not-exist")
}
