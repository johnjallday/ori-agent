package sessionhttp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// firstLookWorkspace creates a workspace from a starter template and seeds the
// folder's first look on it, returning the workspace id.
func firstLookWorkspace(t *testing.T, handler *Handler, body string) string {
	t.Helper()
	_, resp := postCreateWorkspace(t, handler, body)
	wsID := resp["folder"].(map[string]any)["id"].(string)
	seedFolderFirstTask(t, handler, wsID)
	return wsID
}

// editFirstLook changes the seeded first look in place, as a run would.
func editFirstLook(t *testing.T, handler *Handler, wsID string, change func(*agentworkspace.Task)) {
	t.Helper()
	if err := handler.taskMutationStore().Update(wsID, func(ws *agentworkspace.Workspace) error {
		task := personalassistant.FindFolderFirstTask(ws)
		if task == nil {
			t.Fatal("workspace has no first look to edit")
		}
		change(task)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFolderFirstTaskView_FollowsTheTaskThroughItsLife(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	wsID := firstLookWorkspace(t, handler, `{"name":"Thesis Draft","template_id":"starter-template"}`)
	task := folderFirstTaskFor(t, handler, wsID)
	ctx := context.Background()

	// Seeded: a click would start it, and nothing is promised beyond that.
	view, ok := handler.FolderFirstTaskView(ctx, wsID)
	if !ok || view.State != personalassistant.FolderFirstTaskSeeded || !view.CanStart || view.Reason != "" || view.Message != "" {
		t.Fatalf("seeded view = %+v ok=%t", view, ok)
	}
	if view.TaskID != task.ID || view.WorkspaceID != wsID || view.WorkspaceName != "Thesis Draft" || view.Description != task.Description {
		t.Fatalf("seeded view identity = %+v", view)
	}
	if view.Agent == "" || view.StartedAt != nil || view.FinishedAt != nil || view.ResultExcerpt != "" {
		t.Fatalf("seeded view carries run data: %+v", view)
	}
	if view.Detail != "Starts when you press Start first look" {
		t.Fatalf("seeded row detail = %q", view.Detail)
	}
	// Browser routes are built from the folder slug, never the internal id.
	if view.WorkspaceRoute != "/workspaces/thesis-draft" || view.TicketRoute != "/workspaces/thesis-draft?ticket="+task.ID {
		t.Fatalf("routes = %q %q", view.WorkspaceRoute, view.TicketRoute)
	}
	if strings.Contains(view.WorkspaceRoute, wsID) {
		t.Fatalf("the workspace route leaks the internal id: %q", view.WorkspaceRoute)
	}

	started := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	editFirstLook(t, handler, wsID, func(task *agentworkspace.Task) {
		task.Status, task.TicketState, task.StartedAt = agentworkspace.TaskStatusInProgress, agentworkspace.TicketStateInProgress, &started
	})
	view, ok = handler.FolderFirstTaskView(ctx, wsID)
	if !ok || view.State != personalassistant.FolderFirstTaskRunning || view.CanStart || view.Message != "" {
		t.Fatalf("running view = %+v ok=%t", view, ok)
	}
	if view.StartedAt == nil || !view.StartedAt.Equal(started) {
		t.Fatalf("running view started at %v, want %v", view.StartedAt, started)
	}

	finished := started.Add(40 * time.Second)
	editFirstLook(t, handler, wsID, func(task *agentworkspace.Task) {
		task.Status, task.TicketState = agentworkspace.TaskStatusCompleted, agentworkspace.TicketStateReview
		task.CompletedAt = &finished
		task.Result = "Three drafts.\n\n" + strings.Repeat("The newest is chapter four. ", 30)
	})
	view, ok = handler.FolderFirstTaskView(ctx, wsID)
	if !ok || view.State != personalassistant.FolderFirstTaskFinished || view.CanStart {
		t.Fatalf("finished view = %+v ok=%t", view, ok)
	}
	if view.FinishedAt == nil || !view.FinishedAt.Equal(finished) {
		t.Fatalf("finished at %v, want %v", view.FinishedAt, finished)
	}
	if !strings.HasPrefix(view.ResultExcerpt, "Three drafts. The newest is chapter four.") ||
		strings.Contains(view.ResultExcerpt, "\n") || len([]rune(view.ResultExcerpt)) > personalassistant.FolderFirstTaskExcerptMax {
		t.Fatalf("excerpt = %q (%d runes)", view.ResultExcerpt, len([]rune(view.ResultExcerpt)))
	}
}

func TestFolderFirstTaskView_SaysWhyAStartOrARunDidNotHappen(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "optout-template", true)
	writeWizardStarterTaskTemplate(t, handler.templatesRootResolver(), "wizard-starter")
	ctx := context.Background()

	// No agent on the task: the start endpoint would answer "unassigned".
	unassigned := firstLookWorkspace(t, handler, `{"name":"Nobody","template_id":"optout-template","create_template_agents":false}`)
	view, ok := handler.FolderFirstTaskView(ctx, unassigned)
	if !ok || view.State != personalassistant.FolderFirstTaskSeeded || view.CanStart ||
		view.Reason != personalassistant.FolderFirstTaskReasonUnassigned ||
		view.Message != "The first task has no agent yet. Open Nobody to assign one." || view.Agent != "" {
		t.Fatalf("unassigned view = %+v ok=%t", view, ok)
	}

	// A setup dialog about to open: the same hold-back the endpoint applies.
	wizard := firstLookWorkspace(t, handler, `{"name":"Wizard WS","template_id":"wizard-starter"}`)
	view, ok = handler.FolderFirstTaskView(ctx, wizard)
	if !ok || view.CanStart || view.Reason != personalassistant.FolderFirstTaskReasonSetupOpen ||
		view.Message != "Open Wizard WS to finish its setup first." {
		t.Fatalf("wizard view = %+v ok=%t", view, ok)
	}

	// The view agrees with the endpoint: what it calls startable starts, and
	// what it holds back is refused for the reason it gave.
	handler.SetTemplateSetupTaskStarter(func(_, _ string) error { return nil })
	if got := postFolderFirstTaskStart(t, handler, unassigned); got["started"] != false || got["reason"] != view0Reason(t, handler, unassigned) {
		t.Fatalf("endpoint and view disagree on the unassigned task: %v", got)
	}
	if got := postFolderFirstTaskStart(t, handler, wizard); got["started"] != false || got["reason"] != view0Reason(t, handler, wizard) {
		t.Fatalf("endpoint and view disagree on the wizard workspace: %v", got)
	}

	// The click was just spent: the run is handed to the executor and the task
	// turns In Progress a moment later. Until then it reads as running, never
	// as a failure.
	stalled := firstLookWorkspace(t, handler, `{"name":"Stalled","template_id":"starter-template"}`)
	editFirstLook(t, handler, stalled, func(task *agentworkspace.Task) {
		task.Context[taskContextFolderFirstTaskConsumedAt] = time.Now().UTC().Format(time.RFC3339)
	})
	view, ok = handler.FolderFirstTaskView(ctx, stalled)
	if !ok || view.State != personalassistant.FolderFirstTaskRunning || view.CanStart || view.Message != "" || view.Detail != "Running…" {
		t.Fatalf("starting view = %+v ok=%t", view, ok)
	}
	// Minutes later the run never began: a start that failed.
	editFirstLook(t, handler, stalled, func(task *agentworkspace.Task) {
		task.Context[taskContextFolderFirstTaskConsumedAt] = time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	})
	view, ok = handler.FolderFirstTaskView(ctx, stalled)
	if !ok || view.State != personalassistant.FolderFirstTaskFailed || view.CanStart ||
		view.Reason != personalassistant.FolderFirstTaskReasonStartFailed ||
		view.Message != "The first look could not start. Try again." || view.Detail != "Did not finish" {
		t.Fatalf("stalled view = %+v ok=%t", view, ok)
	}

	// A run that paused to ask the user something: answered in the workspace.
	paused := firstLookWorkspace(t, handler, `{"name":"Paused","template_id":"starter-template"}`)
	editFirstLook(t, handler, paused, func(task *agentworkspace.Task) {
		task.Status, task.TicketState = agentworkspace.TaskStatusWaitingForChoice, agentworkspace.TicketStateInProgress
	})
	view, ok = handler.FolderFirstTaskView(ctx, paused)
	if !ok || view.State != personalassistant.FolderFirstTaskWaiting || view.CanStart ||
		view.Reason != personalassistant.FolderFirstTaskReasonNeedsInput ||
		view.Message != "The first look is waiting for your answer. Open Paused to continue." {
		t.Fatalf("paused view = %+v ok=%t", view, ok)
	}

	// A run that failed leaves the ticket in progress with an error.
	failed := firstLookWorkspace(t, handler, `{"name":"Broken","template_id":"starter-template"}`)
	editFirstLook(t, handler, failed, func(task *agentworkspace.Task) {
		task.Status, task.TicketState, task.Error = agentworkspace.TaskStatusFailed, agentworkspace.TicketStateInProgress, "provider error"
	})
	view, ok = handler.FolderFirstTaskView(ctx, failed)
	if !ok || view.State != personalassistant.FolderFirstTaskFailed ||
		view.Reason != personalassistant.FolderFirstTaskReasonRunFailed ||
		view.Message != "The first look did not finish. Try again." {
		t.Fatalf("failed view = %+v ok=%t", view, ok)
	}
	// The run's own error text is never shown on Home.
	if strings.Contains(view.Message, "provider error") || strings.Contains(view.ResultExcerpt, "provider error") {
		t.Fatalf("the view leaked the run's error: %+v", view)
	}
}

// view0Reason reads the reason the view gives for a workspace.
func view0Reason(t *testing.T, handler *Handler, wsID string) string {
	t.Helper()
	view, ok := handler.FolderFirstTaskView(context.Background(), wsID)
	if !ok {
		t.Fatalf("no view for %s", wsID)
	}
	return view.Reason
}

func TestFolderFirstTaskView_NothingToShow(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	ctx := context.Background()

	for _, id := range []string{"", "   ", "missing-workspace"} {
		if view, ok := handler.FolderFirstTaskView(ctx, id); ok {
			t.Fatalf("workspace %q produced a view: %+v", id, view)
		}
	}
	var nilHandler *Handler
	if _, ok := nilHandler.FolderFirstTaskView(ctx, "anything"); ok {
		t.Fatal("a nil handler produced a view")
	}

	// A workspace with no first look.
	_, resp := postCreateWorkspace(t, handler, `{"name":"Plain","template_id":"starter-template"}`)
	plain := resp["folder"].(map[string]any)["id"].(string)
	if view, ok := handler.FolderFirstTaskView(ctx, plain); ok {
		t.Fatalf("a workspace without a first look produced a view: %+v", view)
	}

	// A first look the user cancelled is put away, not offered again.
	cancelled := firstLookWorkspace(t, handler, `{"name":"Cancelled","template_id":"starter-template"}`)
	editFirstLook(t, handler, cancelled, func(task *agentworkspace.Task) {
		task.Status, task.TicketState = agentworkspace.TaskStatusCancelled, agentworkspace.TicketStateCancelled
	})
	if view, ok := handler.FolderFirstTaskView(ctx, cancelled); ok {
		t.Fatalf("a cancelled first look produced a view: %+v", view)
	}

	// A trashed workspace is not somewhere to send the user.
	trashed := firstLookWorkspace(t, handler, `{"name":"Trashed","template_id":"starter-template"}`)
	if view, ok := handler.FolderFirstTaskView(ctx, trashed); !ok || view.State != personalassistant.FolderFirstTaskSeeded {
		t.Fatalf("before the trash: %+v ok=%t", view, ok)
	}
	record, err := handler.workspaceStore.Get(trashed)
	if err != nil {
		t.Fatal(err)
	}
	record.Status = agentworkspace.StatusTrashed
	if err := handler.workspaceStore.Save(record); err != nil {
		t.Fatal(err)
	}
	if view, ok := handler.FolderFirstTaskView(ctx, trashed); ok {
		t.Fatalf("a trashed workspace produced a view: %+v", view)
	}
}
