package sessionhttp

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
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
	folder, _ := linkedFolderName(ws)
	view := personalassistant.FolderFirstTaskView{
		State: state, TaskID: task.ID, WorkspaceID: ws.ID, WorkspaceName: ws.Name,
		WorkspaceRoute: route, TicketRoute: route + "?" + ticket.Encode(),
		FolderName: folder, Description: task.Description,
		StartedAt: task.StartedAt,
	}
	if assignee := strings.TrimSpace(task.To); assignee != "" && !strings.EqualFold(assignee, "unassigned") {
		view.Agent = assignee
	}

	switch state {
	case personalassistant.FolderFirstTaskSeeded:
		view.StartedAt = nil
		view.Reason = h.folderFirstTaskBlockedReason(ctx, ws, view.Agent)
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
	view.Message = personalassistant.FolderFirstTaskMessage(view.Reason, ws.Name)
	view.Detail = personalassistant.FolderFirstTaskRowDetail(state, view.CanStart, task.Result)
	return view, true
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
// never its path. primary is true when the folder is the workspace's primary
// project directory, false when it is linked through the project entry the
// setup journey connected.
func linkedFolderName(ws *agentworkspace.Workspace) (name string, primary bool) {
	if ws == nil {
		return "", false
	}
	if id, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); strings.TrimSpace(id) != "" {
		if ref, err := ws.GetDirectoryReference(id); err == nil && ref != nil {
			return strings.TrimSpace(ref.Name), true
		}
		return "", true
	}
	locator, err := agentworkspace.GetProjectEntryLocator(ws.SharedData)
	if err != nil || locator == nil || locator.Kind != agentworkspace.ProjectEntryDirectoryReference {
		return "", false
	}
	if ref, err := ws.GetDirectoryReference(locator.DirectoryReferenceID); err == nil && ref != nil {
		return strings.TrimSpace(ref.Name), false
	}
	return "", false
}
