package personalassistant

import (
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A folder the user shows the assistant gets one read-only first task when its
// workspace is set up: the assistant's "first look". This file is the one place
// that says which task that is and where it stands, so the mission, the Home
// receipt, Today, and the workspace page all read the same answer.
const (
	// FolderFirstTaskTemplateID is the template id the seeded task's context
	// carries. Never change it: tasks on disk are found by it.
	FolderFirstTaskTemplateID = "folder-digest"
	// FolderFirstTaskConsumedKey marks, in the task's context, that the one
	// click-to-start was spent. A start that then failed keeps the marker and
	// leaves the task pending, to be run again by hand.
	FolderFirstTaskConsumedKey = "folder_first_task_autostart_consumed_at"

	folderFirstTaskContextTemplateID = "template_id"
	folderFirstTaskContextStarter    = "template_starter_task"

	// FolderFirstTaskExcerptMax bounds the one-line result excerpt.
	FolderFirstTaskExcerptMax = 280
)

// FolderFirstTaskState is where a folder's first look stands.
type FolderFirstTaskState string

const (
	// FolderFirstTaskNone means the workspace has no first task.
	FolderFirstTaskNone FolderFirstTaskState = "none"
	// FolderFirstTaskSeeded means the task is waiting for its one start.
	FolderFirstTaskSeeded FolderFirstTaskState = "seeded"
	// FolderFirstTaskRunning means a run is in flight.
	FolderFirstTaskRunning FolderFirstTaskState = "running"
	// FolderFirstTaskWaiting means a run paused to ask the user something. It
	// continues from the workspace, where the question is shown.
	FolderFirstTaskWaiting FolderFirstTaskState = "waiting"
	// FolderFirstTaskFinished means a run finished and left a result.
	FolderFirstTaskFinished FolderFirstTaskState = "finished"
	// FolderFirstTaskFailed means the last attempt did not produce a result:
	// it failed, timed out, or never started.
	FolderFirstTaskFailed FolderFirstTaskState = "failed"
)

// Reasons a first look cannot start now, or did not finish. They are codes the
// browser may branch on; FolderFirstTaskMessage is the sentence the user reads.
const (
	FolderFirstTaskReasonSetupOpen       = "setup_wizard_opening"
	FolderFirstTaskReasonUnassigned      = "unassigned"
	FolderFirstTaskReasonNoModel         = "no_model"
	FolderFirstTaskReasonLocalActivation = "local_activation_required"
	FolderFirstTaskReasonNeedsInput      = "needs_input"
	FolderFirstTaskReasonStartFailed     = "start_failed"
	FolderFirstTaskReasonRunFailed       = "run_failed"
)

// FolderFirstTaskView is a folder's first look as the browser sees it: a state,
// what the user can do about it, and routes built from the workspace slug. It
// never carries a filesystem path.
type FolderFirstTaskView struct {
	State FolderFirstTaskState `json:"state"`
	// OfferID names the folder offer whose setup seeded this look, when the
	// view was read through the folder digest.
	OfferID        string     `json:"offer_id,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	WorkspaceID    string     `json:"workspace_id,omitempty"`
	WorkspaceName  string     `json:"workspace_name,omitempty"`
	WorkspaceRoute string     `json:"workspace_route,omitempty"`
	FolderName     string     `json:"folder_name,omitempty"`
	Description    string     `json:"description,omitempty"`
	Agent          string     `json:"agent,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	// ResultExcerpt is the result on one line, at most FolderFirstTaskExcerptMax
	// runes. TicketRoute opens the full report.
	ResultExcerpt string `json:"result_excerpt,omitempty"`
	TicketRoute   string `json:"ticket_route,omitempty"`
	// CanStart is true when the seeded task would start on a click right now.
	CanStart bool `json:"can_start"`
	// Reason and Message say why not, or what went wrong. Message is one plain
	// sentence; both are empty when there is nothing to explain.
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// IsFolderFirstTask reports whether a task is the first look a shown folder's
// workspace was seeded with.
func IsFolderFirstTask(task *workspace.Task) bool {
	return task != nil && task.Context[folderFirstTaskContextTemplateID] == FolderFirstTaskTemplateID &&
		task.Context[folderFirstTaskContextStarter] == true
}

// FindFolderFirstTask returns the workspace's first look, or nil when it has
// none. The pointer is into ws.Tasks.
func FindFolderFirstTask(ws *workspace.Workspace) *workspace.Task {
	if ws == nil {
		return nil
	}
	for i := range ws.Tasks {
		if IsFolderFirstTask(&ws.Tasks[i]) {
			return &ws.Tasks[i]
		}
	}
	return nil
}

// FolderFirstTaskFinishedWithResult reports whether the first look has run and
// left something to read. This is what the mission pays for.
func FolderFirstTaskFinishedWithResult(task *workspace.Task) bool {
	return FolderFirstTaskStateOf(task) == FolderFirstTaskFinished
}

// FolderFirstTaskStateOf reads a first look's state from the task record.
//
// A successful run leaves the ticket in Review (or Done once accepted) with a
// result. A failed or timed-out run leaves it In Progress with an error, which
// is "failed" here, not "running"; so does a run that paused to ask the user
// something, which is "waiting". A task still waiting whose one start was
// already spent is a start that failed. A cancelled task reads as none: the
// user put it away.
func FolderFirstTaskStateOf(task *workspace.Task) FolderFirstTaskState {
	if !IsFolderFirstTask(task) {
		return FolderFirstTaskNone
	}
	switch task.CanonicalState() {
	case workspace.TicketStateReview, workspace.TicketStateDone:
		if strings.TrimSpace(task.Result) == "" {
			return FolderFirstTaskFailed
		}
		return FolderFirstTaskFinished
	case workspace.TicketStateInProgress:
		if task.Status == workspace.TaskStatusWaitingForChoice {
			return FolderFirstTaskWaiting
		}
		if task.NeedsAttention() {
			return FolderFirstTaskFailed
		}
		return FolderFirstTaskRunning
	case workspace.TicketStateCancelled:
		return FolderFirstTaskNone
	default:
		if _, consumed := task.Context[FolderFirstTaskConsumedKey]; consumed {
			return FolderFirstTaskFailed
		}
		return FolderFirstTaskSeeded
	}
}

// FolderFirstTaskFailureReason says which kind of failure a failed first look
// is: a run that did not finish, or a start that never happened.
func FolderFirstTaskFailureReason(task *workspace.Task) string {
	if task != nil && task.CanonicalState() == workspace.TicketStateReady {
		return FolderFirstTaskReasonStartFailed
	}
	return FolderFirstTaskReasonRunFailed
}

// FolderFirstTaskExcerpt puts a result on one line and trims it for a card. A
// trimmed excerpt ends in an ellipsis and still fits the limit.
func FolderFirstTaskExcerpt(result string) string {
	line := []rune(strings.Join(strings.Fields(result), " "))
	if len(line) <= FolderFirstTaskExcerptMax {
		return string(line)
	}
	return strings.TrimRight(string(line[:FolderFirstTaskExcerptMax-1]), " ") + "…"
}

// FolderFirstTaskMessage is the one sentence shown for a reason. An unknown
// reason says nothing rather than guessing.
func FolderFirstTaskMessage(reason, workspaceName string) string {
	name := strings.TrimSpace(workspaceName)
	if name == "" {
		name = "the workspace"
	}
	switch reason {
	case FolderFirstTaskReasonSetupOpen:
		return "Open " + name + " to finish its setup first."
	case FolderFirstTaskReasonUnassigned:
		return "The first task has no agent yet. Open " + name + " to assign one."
	case FolderFirstTaskReasonNoModel:
		return "Add a model in Settings to run the first look."
	case FolderFirstTaskReasonLocalActivation:
		return "Open " + name + " and activate it on this computer."
	case FolderFirstTaskReasonNeedsInput:
		return "The first look is waiting for your answer. Open " + name + " to continue."
	case FolderFirstTaskReasonStartFailed:
		return "The first look could not start. Try again."
	case FolderFirstTaskReasonRunFailed:
		return "The first look did not finish. Try again."
	default:
		return ""
	}
}
