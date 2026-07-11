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
