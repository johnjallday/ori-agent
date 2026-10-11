package sessionhttp

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/softwareassessment"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// FolderFirstTaskView reads where a workspace's first look stands, from the
// same canonical record the folder receipt is read from. ok is false when the
// workspace is not readable, is not active, has no browser route, or has no
// first task: there is nothing honest to show for it.
//
// It never starts anything. Whether a click would start the task is answered
// with the start endpoint's own rules, so Home never offers a button that the
// endpoint would refuse for a reason known in advance.
func (h *Handler) FolderFirstTaskView(ctx context.Context, workspaceID string) (personalassistant.FolderFirstTaskView, bool) {
	workspaceID = strings.TrimSpace(workspaceID)
	if h == nil || h.workspaceStore == nil || workspaceID == "" {
		return personalassistant.FolderFirstTaskView{}, false
	}
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil || ws.GetStatus() != agentworkspace.StatusActive {
		return personalassistant.FolderFirstTaskView{}, false
	}
	slug := strings.TrimSpace(ws.FolderSlug)
	task := personalassistant.FindFolderFirstTask(ws)
	if slug == "" || task == nil {
		return personalassistant.FolderFirstTaskView{}, false
	}
	state := personalassistant.FolderFirstTaskStateOf(task)
	if state == personalassistant.FolderFirstTaskNone {
		return personalassistant.FolderFirstTaskView{}, false
	}

	// Browser routes come from the folder slug, never the internal id.
	route := "/workspaces/" + url.PathEscape(slug)
	ticket := url.Values{}
	ticket.Set("ticket", task.ID)
	folder := linkedFolderName(ws)
	view := personalassistant.FolderFirstTaskView{
		State: state, TaskID: task.ID, WorkspaceID: ws.ID, WorkspaceName: ws.Name,
		WorkspaceRoute: route, TicketRoute: route + "?" + ticket.Encode(),
		FolderName: folder, Description: task.Description,
		StartedAt: task.StartedAt, Recipe: agentworkspace.FirstTaskRecipe(task),
	}
	if assignee := strings.TrimSpace(task.To); assignee != "" && !strings.EqualFold(assignee, "unassigned") {
		view.Agent = assignee
	}

	switch state {
	case personalassistant.FolderFirstTaskSeeded:
		view.StartedAt = nil
		view.Reason = h.folderFirstTaskBlockedReason(ctx, ws, view.Agent)
		if view.Reason == "" {
			view.Reason = h.firstTaskRecipeReason(task)
		}
		view.CanStart = view.Reason == ""
	case personalassistant.FolderFirstTaskWaiting:
		view.Reason = personalassistant.FolderFirstTaskReasonNeedsInput
	case personalassistant.FolderFirstTaskFinished:
		view.FinishedAt = task.CompletedAt
		view.ResultExcerpt = personalassistant.FolderFirstTaskExcerpt(task.Result)
	case personalassistant.FolderFirstTaskFailed:
		view.FinishedAt = task.CompletedAt
		view.Reason = personalassistant.FolderFirstTaskFailureReason(task)
	}
	if view.Recipe != "" {
		h.describeRecipeFirstTask(ctx, ws, task, &view)
	} else {
		view.Message = personalassistant.FolderFirstTaskMessage(view.Reason, ws.Name)
	}
	view.Detail = personalassistant.FolderFirstTaskViewDetail(view, task.Result)
	return view, true
}

// describeRecipeFirstTask finishes the view of a first task that runs under a
// host recipe. Such a task is an assessment: it asks the user nothing, it can
// be tried again only when what stopped it has been repaired, and every reason
// is read now rather than remembered, so the view follows a repair made on
// another page.
func (h *Handler) describeRecipeFirstTask(ctx context.Context, ws *agentworkspace.Workspace, task *agentworkspace.Task, view *personalassistant.FolderFirstTaskView) {
	// The code the recipe's own run recorded when it last ended without a report.
	ended, _ := task.Context[agentworkspace.TaskContextFirstTaskFailure].(string)
	ended = strings.TrimSpace(ended)
	if view.State == personalassistant.FolderFirstTaskFailed {
		// What stands in the way of another try, if anything, comes first; else
		// the reason is how the last run ended.
		blocked := h.folderFirstTaskBlockedReason(ctx, ws, view.Agent)
		if blocked == "" {
			blocked = h.firstTaskRecipeReason(task)
		}
		switch {
		case blocked != "":
			view.Reason = blocked
		case ended != "":
			view.Reason = ended
		}
		view.CanRetry = blocked == ""
	}
	view.Message = personalassistant.FolderFirstTaskRecipeMessage(view.Reason, ws.Name, view.Agent)
	// The sentence recorded when a run ended says more than its code does. It is
	// shown only beside the code the recipe's own run recorded with it: that
	// pairing is what says the text is one of the recipe's plain sentences and
	// not an error some other path wrote onto the task.
	if view.State == personalassistant.FolderFirstTaskFailed && view.CanRetry && strings.TrimSpace(task.Error) != "" &&
		ended != "" &&
		view.Reason != personalassistant.FolderFirstTaskReasonToolRefused && view.Reason != personalassistant.FolderFirstTaskReasonTimedOut {
		view.Message = strings.TrimSpace(task.Error)
	}
	view.RepairRoute, view.RepairLabel = personalassistant.FolderFirstTaskRecipeRepair(view.Reason, view.WorkspaceRoute, ws.Name)
	if view.State == personalassistant.FolderFirstTaskFinished {
		view.ResultExcerpt = personalassistant.FolderFirstTaskRecipeExcerpt(task.Result)
		view.ReportRoute = view.WorkspaceRoute + "/task/" + url.PathEscape(task.ID)
		if next := softwareassessment.NextTask(task.Result); next != "" {
			// The workspace's Plans page is where a request is drafted into a plan,
			// and where nothing is created until the user presses Create plan.
			view.NextRequest = next
			view.NextRoute = view.WorkspaceRoute + "/plans?next=assessment"
		}
	}
}

// folderFirstTaskBlockedReason answers, in the start endpoint's own order, why a
// seeded first task would not start on a click: the workspace is not activated
// on this computer, its setup dialog is still to open, or the task has no agent.
// Empty means a click would start it.
func (h *Handler) folderFirstTaskBlockedReason(ctx context.Context, ws *agentworkspace.Workspace, agent string) string {
	if store := h.taskMutationStore(); store != nil {
		if err := agentworkspace.RequireWorkspaceExecution(ctx, store, ws.ID, true); errors.Is(err, agentworkspace.ErrWorkspaceExecutionInactive) {
			return personalassistant.FolderFirstTaskReasonLocalActivation
		}
	}
	if h.setupWizardOpening(ws.ID) {
		return personalassistant.FolderFirstTaskReasonSetupOpen
	}
	if agent == "" {
		return personalassistant.FolderFirstTaskReasonUnassigned
	}
	return ""
}

// linkedFolderName is the base name of the folder a workspace was set up for,
// never its path. It reads the primary project directory, else the project
// entry the setup journey connected.
func linkedFolderName(ws *agentworkspace.Workspace) string {
	if ws == nil {
		return ""
	}
	if id, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); strings.TrimSpace(id) != "" {
		if ref, err := ws.GetDirectoryReference(id); err == nil && ref != nil {
			return strings.TrimSpace(ref.Name)
		}
		return ""
	}
	locator, err := agentworkspace.GetProjectEntryLocator(ws.SharedData)
	if err != nil || locator == nil || locator.Kind != agentworkspace.ProjectEntryDirectoryReference {
		return ""
	}
	if ref, err := ws.GetDirectoryReference(locator.DirectoryReferenceID); err == nil && ref != nil {
		return strings.TrimSpace(ref.Name)
	}
	return ""
}
