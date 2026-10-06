package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func postFolderFirstTaskStart(t *testing.T, handler *Handler, wsID string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/folder-first-task/start", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("folder-first-task/start = %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// seedFolderFirstTask gives a workspace the starter task a shown folder gets.
func seedFolderFirstTask(t *testing.T, handler *Handler, wsID string) {
	t.Helper()
	seeded, err := handler.SeedStarterTasks(wsID, projecttemplates.Template{
		ID:           "folder-digest",
		StarterTasks: []projecttemplates.StarterTask{{Description: "Summarize the folder", Details: "Read it and say what is in it."}},
	})
	if err != nil || seeded != 1 {
		t.Fatalf("seed first task = %d, %v", seeded, err)
	}
}

func folderFirstTaskFor(t *testing.T, handler *Handler, wsID string) agentworkspace.Task {
	t.Helper()
	for _, task := range workspaceTasksFromStore(t, handler, wsID) {
		if task.Context[taskContextTemplateID] == folderFirstTaskTemplateID {
			return task
		}
	}
	t.Fatalf("workspace %s has no folder first task", wsID)
	return agentworkspace.Task{}
}

func TestFolderFirstTaskStartsOnceOnFirstOpen(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	var started []string
	handler.SetTemplateSetupTaskStarter(func(_, taskID string) error {
		started = append(started, taskID)
		return nil
	})
	_, resp := postCreateWorkspace(t, handler, `{"name":"Once","template_id":"starter-template"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)
	seedFolderFirstTask(t, handler, wsID)
	first := folderFirstTaskFor(t, handler, wsID)

	open := postFolderFirstTaskStart(t, handler, wsID)
	if open["started"] != true || open["task_id"] != first.ID {
		t.Fatalf("first open must start the folder's first task: %v", open)
	}
	if len(started) != 1 || started[0] != first.ID {
		t.Fatalf("started = %v, want only the folder task", started)
	}
	// The template's own setup task is not this trigger's to start.
	for _, id := range started {
		if id == setupTaskFor(t, handler, wsID).ID {
			t.Fatal("the folder trigger started the template's setup task")
		}
	}
	if _, ok := folderFirstTaskFor(t, handler, wsID).Context[taskContextFolderFirstTaskConsumedAt].(string); !ok {
		t.Fatal("the consumed marker was not persisted")
	}
	// A reload, or another tab, does nothing.
	for i := 0; i < 2; i++ {
		again := postFolderFirstTaskStart(t, handler, wsID)
		if again["started"] != false || again["reason"] != "already_consumed" {
			t.Fatalf("repeat open %d: %v", i, again)
		}
	}
	if len(started) != 1 {
		t.Fatalf("the starter ran %d times, want 1", len(started))
	}
}

func TestFolderFirstTaskIsNeverRetriedAfterAFailedStart(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	calls := 0
	handler.SetTemplateSetupTaskStarter(func(_, _ string) error {
		calls++
		return fmt.Errorf("no model")
	})
	_, resp := postCreateWorkspace(t, handler, `{"name":"Flaky","template_id":"starter-template"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)
	seedFolderFirstTask(t, handler, wsID)

	if first := postFolderFirstTaskStart(t, handler, wsID); first["started"] != false || first["reason"] != "start_failed" {
		t.Fatalf("first open: %v", first)
	}
	// The refusal is known at once: Home reads "could not start", not a look
	// that is still starting.
	view, ok := handler.FolderFirstTaskView(context.Background(), wsID)
	if !ok || view.State != personalassistant.FolderFirstTaskFailed ||
		view.Reason != personalassistant.FolderFirstTaskReasonStartFailed ||
		view.Message != "The first look could not start. Try again." {
		t.Fatalf("view after a refused start = %+v ok=%t", view, ok)
	}
	if second := postFolderFirstTaskStart(t, handler, wsID); second["reason"] != "already_consumed" {
		t.Fatalf("a failed start must not retry: %v", second)
	}
	if calls != 1 {
		t.Fatalf("starter calls = %d, want 1", calls)
	}
	// Still pending and startable by hand.
	if task := folderFirstTaskFor(t, handler, wsID); task.Status != agentworkspace.TaskStatusPending {
		t.Fatalf("task status = %q, want pending", task.Status)
	}
}

func TestFolderFirstTaskWaitsForAnAgentAndNeverRunsUnassigned(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "optout-template", true)
	calls := 0
	handler.SetTemplateSetupTaskStarter(func(_, _ string) error {
		calls++
		return nil
	})
	_, resp := postCreateWorkspace(t, handler, `{"name":"NoAgent","template_id":"optout-template","create_template_agents":false}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)
	seedFolderFirstTask(t, handler, wsID)

	first := postFolderFirstTaskStart(t, handler, wsID)
	if first["started"] != false || first["reason"] != "unassigned" || calls != 0 {
		t.Fatalf("an unassigned task must not run: %v calls=%d", first, calls)
	}
	if _, consumed := folderFirstTaskFor(t, handler, wsID).Context[taskContextFolderFirstTaskConsumedAt]; consumed {
		t.Fatal("waiting for an agent must not spend the one start")
	}

	// An agent joins, the claim sweep assigns the task, and the next open starts it.
	if err := handler.agentStore.CreateAgent("Helper", nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/agents", strings.NewReader(`{"agent_name":"Helper"}`))
	req.Header.Set("Content-Type", "application/json")
	add := httptest.NewRecorder()
	handler.HandleWorkspaces(add, req)
	if add.Code != http.StatusCreated {
		t.Fatalf("add agent = %d: %s", add.Code, add.Body.String())
	}
	if second := postFolderFirstTaskStart(t, handler, wsID); second["started"] != true || calls != 1 {
		t.Fatalf("after an agent joined: %v calls=%d", second, calls)
	}
}

func TestFolderFirstTaskNoTaskAndHandStartedTask(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	calls := 0
	handler.SetTemplateSetupTaskStarter(func(_, _ string) error {
		calls++
		return nil
	})
	_, resp := postCreateWorkspace(t, handler, `{"name":"Plain","template_id":"starter-template"}`)
	wsID := resp["folder"].(map[string]any)["id"].(string)
	if got := postFolderFirstTaskStart(t, handler, wsID); got["started"] != false || got["reason"] != "no_first_task" {
		t.Fatalf("a workspace with no folder task: %v", got)
	}

	seedFolderFirstTask(t, handler, wsID)
	task := folderFirstTaskFor(t, handler, wsID)
	if err := handler.taskMutationStore().Update(wsID, func(ws *agentworkspace.Workspace) error {
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == task.ID {
				ws.Tasks[i].Status = agentworkspace.TaskStatusInProgress
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := postFolderFirstTaskStart(t, handler, wsID); got["started"] != false || got["reason"] != "not_pending" || calls != 0 {
		t.Fatalf("a task the user already started: %v calls=%d", got, calls)
	}
}

// A workspace the setup journey made has a Setup Wizard. The trigger is not
// silenced by its mere presence: it holds back only while the wizard's dialog is
// about to open on this load, so it never lands an agent on top of that dialog.
func TestFolderFirstTaskHoldsBackOnlyWhileTheSetupWizardIsAboutToOpen(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		progress *agentworkspace.SetupWizardProgress
		starts   bool
	}{
		{"wizard never shown", nil, false},
		{"wizard not started", &agentworkspace.SetupWizardProgress{State: agentworkspace.SetupWizardStateNotStarted}, false},
		{"wizard in progress and already opened (the journey walked its mode step)",
			&agentworkspace.SetupWizardProgress{State: agentworkspace.SetupWizardStateInProgress, FirstOpenedAt: &now}, true},
		{"wizard in progress and dismissed",
			&agentworkspace.SetupWizardProgress{State: agentworkspace.SetupWizardStateInProgress, DismissedAt: &now}, true},
		{"wizard ready", &agentworkspace.SetupWizardProgress{State: agentworkspace.SetupWizardStateReady}, true},
		{"wizard regressed", &agentworkspace.SetupWizardProgress{State: agentworkspace.SetupWizardStateNeedsAttention}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, _, cleanup := templateTestEnv(t)
			defer cleanup()
			writeWizardStarterTaskTemplate(t, handler.templatesRootResolver(), "wizard-starter")
			var started []string
			handler.SetTemplateSetupTaskStarter(func(_, taskID string) error {
				started = append(started, taskID)
				return nil
			})
			_, resp := postCreateWorkspace(t, handler, `{"name":"Wizard WS","template_id":"wizard-starter"}`)
			wsID := resp["folder"].(map[string]any)["id"].(string)
			seedFolderFirstTask(t, handler, wsID)
			if tc.progress != nil {
				if err := handler.taskMutationStore().Update(wsID, func(ws *agentworkspace.Workspace) error {
					ws.SetSetupWizardProgress(tc.progress)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}

			got := postFolderFirstTaskStart(t, handler, wsID)
			if tc.starts {
				if got["started"] != true || len(started) != 1 {
					t.Fatalf("the task must start: %v started=%v", got, started)
				}
				return
			}
			if got["started"] != false || got["reason"] != "setup_wizard_opening" || len(started) != 0 {
				t.Fatalf("a wizard about to open must hold the task back: %v", got)
			}
			if _, consumed := folderFirstTaskFor(t, handler, wsID).Context[taskContextFolderFirstTaskConsumedAt]; consumed {
				t.Fatal("holding back must not spend the one start")
			}
		})
	}
}

// The receipt promises a start on the click only when that is true, and says
// where the look stands once it has been asked for (FR14).
func TestFolderFirstTaskReceiptPromisesOnlyWhatWillHappen(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "optout-template", true)
	handler.SetTemplateSetupTaskStarter(func(_, _ string) error { return nil })

	taskDetail := func(wsID string) string {
		rows, err := handler.FolderOfferWorkspaceReceipt(wsID, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Kind == "task" {
				return row.Detail
			}
		}
		t.Fatalf("no task row in %+v", rows)
		return ""
	}

	_, resp := postCreateWorkspace(t, handler, `{"name":"Assigned","template_id":"starter-template"}`)
	assigned := resp["folder"].(map[string]any)["id"].(string)
	seedFolderFirstTask(t, handler, assigned)
	if got := taskDetail(assigned); got != "Starts when you press Start first look" {
		t.Fatalf("an assigned task with no wizard = %q", got)
	}
	// The click was spent and the run is starting: no second start is promised.
	postFolderFirstTaskStart(t, handler, assigned)
	if got := taskDetail(assigned); got != "Running…" {
		t.Fatalf("a task just started = %q", got)
	}
	// The run finished: the row quotes the first line of what the agent said.
	editFirstLook(t, handler, assigned, func(task *agentworkspace.Task) {
		task.Status, task.TicketState = agentworkspace.TaskStatusCompleted, agentworkspace.TicketStateReview
		task.Result = "Three drafts.\n\nThe newest is chapter four."
	})
	if got := taskDetail(assigned); got != "Done · Three drafts." {
		t.Fatalf("a finished task = %q", got)
	}

	_, resp = postCreateWorkspace(t, handler, `{"name":"Nobody","template_id":"optout-template","create_template_agents":false}`)
	unassigned := resp["folder"].(map[string]any)["id"].(string)
	seedFolderFirstTask(t, handler, unassigned)
	if got := taskDetail(unassigned); got != "Ready to start" {
		t.Fatalf("an unassigned task = %q", got)
	}
}
