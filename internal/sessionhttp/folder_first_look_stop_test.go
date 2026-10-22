package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func postFolderFirstTaskStop(t *testing.T, handler *Handler, wsID string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/folder-first-task/stop", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("folder-first-task/stop = %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// A setup's start under the hire's consent is the same one start the click
// makes: the marker it stamps refuses the click afterwards, and the reverse.
func TestStartFolderFirstTaskInProcessIsTheSameOneStart(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	var started []string
	handler.SetTemplateSetupTaskStarter(func(_, taskID string) error {
		started = append(started, taskID)
		return nil
	})
	wsID := firstLookWorkspace(t, handler, `{"name":"Hired","template_id":"starter-template"}`)
	first := folderFirstTaskFor(t, handler, wsID)

	start := handler.StartFolderFirstTask(context.Background(), wsID)
	if !start.Started || start.TaskID != first.ID || start.Reason != "" {
		t.Fatalf("start = %+v", start)
	}
	if again := postFolderFirstTaskStart(t, handler, wsID); again["started"] != false || again["reason"] != "already_consumed" {
		t.Fatalf("the click after a setup's start: %v", again)
	}
	if replay := handler.StartFolderFirstTask(context.Background(), wsID); replay.Started || replay.Reason != "already_consumed" {
		t.Fatalf("a replayed start: %+v", replay)
	}
	if len(started) != 1 {
		t.Fatalf("started %d times", len(started))
	}
}

// Stop puts a running look back where Start first look starts it again: its run
// is stopped through the task handler, its marker is cleared, and nothing else
// about the workspace changes.
func TestStopFolderFirstTaskMakesTheLookStartableAgain(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	var started []string
	handler.SetTemplateSetupTaskStarter(func(_, taskID string) error {
		started = append(started, taskID)
		return nil
	})
	var stops []string
	handler.SetFolderFirstTaskStopper(func(_ context.Context, workspaceID, taskID string) error {
		stops = append(stops, taskID)
		// As the task handler does once the cancelled run has ended.
		return handler.taskMutationStore().Update(workspaceID, func(ws *agentworkspace.Workspace) error {
			for i := range ws.Tasks {
				if ws.Tasks[i].ID == taskID {
					return ws.Tasks[i].TransitionTicket(agentworkspace.TicketStateReady, agentworkspace.TicketStateChange{Actor: agentworkspace.TicketActorUser})
				}
			}
			return nil
		})
	})
	wsID := firstLookWorkspace(t, handler, `{"name":"Stopped","template_id":"starter-template"}`)
	if start := handler.StartFolderFirstTask(context.Background(), wsID); !start.Started {
		t.Fatalf("start = %+v", start)
	}
	// Nothing is running yet: a stop is refused and nothing is stopped.
	editFirstLook(t, handler, wsID, func(task *agentworkspace.Task) {
		task.Context[personalassistant.FolderFirstTaskConsumedKey] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	})
	if refused := postFolderFirstTaskStop(t, handler, wsID); refused["stopped"] != false || refused["reason"] != "not_running" || len(stops) != 0 {
		t.Fatalf("stop of a look that is not running: %v (stops %v)", refused, stops)
	}
	editFirstLook(t, handler, wsID, func(task *agentworkspace.Task) {
		if err := task.TransitionTicket(agentworkspace.TicketStateInProgress, agentworkspace.TicketStateChange{Actor: agentworkspace.TicketActorRun}); err != nil {
			t.Fatal(err)
		}
	})
	if view, _ := handler.FolderFirstTaskView(context.Background(), wsID); view.State != personalassistant.FolderFirstTaskRunning {
		t.Fatalf("running view = %+v", view)
	}

	stopped := postFolderFirstTaskStop(t, handler, wsID)
	if stopped["stopped"] != true || len(stops) != 1 {
		t.Fatalf("stop = %v (stops %v)", stopped, stops)
	}
	task := folderFirstTaskFor(t, handler, wsID)
	if _, consumed := task.Context[taskContextFolderFirstTaskConsumedAt]; consumed {
		t.Fatal("a stopped look keeps its one-start marker")
	}
	view, ok := handler.FolderFirstTaskView(context.Background(), wsID)
	if !ok || view.State != personalassistant.FolderFirstTaskSeeded || !view.CanStart || view.Agent != "Producer" {
		t.Fatalf("view after Stop = %+v ok=%t", view, ok)
	}
	// Start first look starts the same task once more.
	if again := postFolderFirstTaskStart(t, handler, wsID); again["started"] != true || again["task_id"] != task.ID {
		t.Fatalf("start after Stop: %v", again)
	}
	if len(started) != 2 || started[1] != task.ID {
		t.Fatalf("started = %v", started)
	}
}

func TestStopFolderFirstTaskWithoutAStopperStopsNothing(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	wsID := firstLookWorkspace(t, handler, `{"name":"No Stopper","template_id":"starter-template"}`)
	if resp := postFolderFirstTaskStop(t, handler, wsID); resp["stopped"] != false || resp["reason"] != "stop_unavailable" {
		t.Fatalf("stop = %v", resp)
	}
}

// A generic look that finished hands on its one suggested next task, read
// from its closing "Next:" line, and the route of its full report.
func TestFolderFirstTaskViewReadsAGenericLooksNextLine(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	for name, tc := range map[string]struct{ result, next string }{
		"closing line":        {"The folder holds three drafts.\n\nNext: Outline chapter four.", "Outline chapter four."},
		"no closing line":     {"The folder holds three drafts.", ""},
		"trailing whitespace": {"Notes.\nNext:   Merge the two outlines.   \n\n  ", "Merge the two outlines."},
		"several lines":       {"Next: an early guess.\nMore notes.\n**Next:** Tag the final mix.", "Tag the final mix."},
	} {
		wsID := firstLookWorkspace(t, handler, `{"name":"Look `+strings.ReplaceAll(name, " ", "-")+`","template_id":"starter-template"}`)
		editFirstLook(t, handler, wsID, func(task *agentworkspace.Task) {
			now := time.Now()
			task.Status, task.TicketState, task.Result, task.CompletedAt = agentworkspace.TaskStatusCompleted, agentworkspace.TicketStateReview, tc.result, &now
		})
		view, ok := handler.FolderFirstTaskView(context.Background(), wsID)
		if !ok || view.State != personalassistant.FolderFirstTaskFinished || view.NextRequest != tc.next ||
			!strings.HasSuffix(view.ReportRoute, "/task/"+view.TaskID) {
			t.Errorf("%s: view = %+v ok=%t", name, view, ok)
		}
	}
}
