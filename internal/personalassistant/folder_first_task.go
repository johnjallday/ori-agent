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
	// FolderFirstTaskStartFailedKey marks, in the task's context, a spent start
	// that the executor refused. Without it a refused start would read as
	// "starting" until the grace ran out.
	FolderFirstTaskStartFailedKey = "folder_first_task_start_failed_at"

	folderFirstTaskContextTemplateID = "template_id"
	folderFirstTaskContextStarter    = "template_starter_task"

	// FolderFirstTaskExcerptMax bounds the one-line result excerpt.
	FolderFirstTaskExcerptMax = 280
)

// FolderSoftwareAssessmentTask is the first task a software project's setup
// seeds: what the ticket says before it is started. The task runs under a host
// recipe, which gives the model what a bounded inspection read and offers it no
// tools; the details describe that to the person reading the ticket.
func FolderSoftwareAssessmentTask() (description, details string) {
	return "Assess this project and suggest a first task",
		"A read-only first assessment from the project's own README, instructions and build files: what it is, how it is laid out, what it is built with, the build and test commands it declares, the rules it sets for contributors, what is missing, and one small next task. Nothing in the project is run or changed, and a command it lists is reported as declared, not as tested."
}

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
	// FolderFirstTaskReasonRecipeUnavailable means the first task runs under a
	// host recipe this server cannot run within its restriction. The task is
	// not started some other way.
	FolderFirstTaskReasonRecipeUnavailable = "assessment_unavailable"
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
	// Detail is what the receipt's task row says now. The receipt stored on an
	// offer is from the moment of setup, so the card shows this instead.
	Detail string `json:"detail,omitempty"`
	// Recipe names the host recipe the task runs under, when it has one. The
	// browser words such a task as what it is (an assessment) and never offers
	// to run it any other way.
	Recipe string `json:"recipe,omitempty"`
	// CanRetry is true for a recipe task whose last run did not finish and
	// which could be run again now. When something must be repaired first it is
	// false, and Reason, RepairRoute and RepairLabel say what and where.
	CanRetry    bool   `json:"can_retry,omitempty"`
	RepairRoute string `json:"repair_route,omitempty"`
	RepairLabel string `json:"repair_label,omitempty"`
	// NextRequest is the one next request a finished assessment suggested, as
	// plain text for the user to read, edit and submit themselves. NextRoute
	// opens the workspace's Plans page with it placed, unsubmitted, in the
	// "Start a plan" box.
	NextRequest string `json:"next_request,omitempty"`
	NextRoute   string `json:"next_route,omitempty"`
	// ReportRoute opens a finished assessment where it can be read in full:
	// the task's own page, which shows its result. A ticket's detail (the
	// TicketRoute) lists the work and its runs but not what a run wrote.
	ReportRoute string `json:"report_route,omitempty"`
	// Readiness is what a recipe task needs and can use, read now: the folder,
	// the assistant's model, and optional connections. Rows are the same shape
	// as a setup plan's prerequisites.
	Readiness []FolderPrerequisite `json:"readiness,omitempty"`
}

// Reasons specific to a first task under a host recipe. The first group is why
// it cannot start (or be tried again) until something is repaired; the second
// is how a run ended without a result.
const (
	FolderFirstTaskReasonAssistantMissing  = "assistant_unavailable"
	FolderFirstTaskReasonModelUnavailable  = "model_unavailable"
	FolderFirstTaskReasonModelUnsupported  = "model_unsupported"
	FolderFirstTaskReasonFolderUnavailable = "project_folder_unavailable"
	FolderFirstTaskReasonWorkspaceMissing  = "workspace_unavailable"

	FolderFirstTaskReasonToolRefused     = "tool_call_refused"
	FolderFirstTaskReasonContextTooSmall = "context_too_small"
	FolderFirstTaskReasonNothingToAssess = "nothing_to_assess"
	FolderFirstTaskReasonTimedOut        = "timed_out"
	FolderFirstTaskReasonProviderConfig  = "provider_configuration_required"
	FolderFirstTaskReasonProviderQuota   = "provider_quota_exhausted"
)

// Where a recipe task's reasons are repaired. Each is an existing page.
const (
	folderFirstTaskAgentsRoute   = "/agents"
	folderFirstTaskFolderRoute   = "/?panel=today&folder=show"
	folderFirstTaskProviderRoute = "/settings#api-keys"
)

// FolderFirstTaskRecipeMessage is the one sentence shown for why a first
// assessment cannot start, or did not finish. agent is its assistant's name.
// An unknown reason says only that it did not finish.
func FolderFirstTaskRecipeMessage(reason, workspaceName, agent string) string {
	name := strings.TrimSpace(workspaceName)
	if name == "" {
		name = "the workspace"
	}
	who := strings.TrimSpace(agent)
	if who == "" {
		who = "The assistant"
	}
	switch reason {
	case "":
		return ""
	case FolderFirstTaskReasonRecipeUnavailable:
		return "The first assessment cannot be run on this server. Nothing is sent to a model until it can."
	case FolderFirstTaskReasonUnassigned, FolderFirstTaskReasonAssistantMissing:
		return "The assessment has no assistant in " + name + " to run it. Open " + name + " to assign one."
	case FolderFirstTaskReasonNoModel:
		return who + " has no model chosen. Choose one for it, then start the assessment."
	case FolderFirstTaskReasonModelUnavailable:
		return "The model " + who + " is set to use is not set up on this computer. Set it up, or choose another for it."
	case FolderFirstTaskReasonModelUnsupported:
		return "The model " + who + " is set to use cannot be held to a read-only assessment, because Ori cannot switch its tools off. Choose another model for it."
	case FolderFirstTaskReasonFolderUnavailable:
		return "The project folder is missing, or a different folder is now in its place, so nothing was read. Put it back where it was, or show Ori the project again."
	case FolderFirstTaskReasonWorkspaceMissing:
		return name + " is not available right now."
	case FolderFirstTaskReasonLocalActivation:
		return "Open " + name + " and activate it on this computer."
	case FolderFirstTaskReasonSetupOpen:
		return "Open " + name + " to finish its setup first."
	case FolderFirstTaskReasonToolRefused:
		return "The model asked to use a tool, which a read-only assessment does not allow. Nothing was run and its answer was discarded. Try again."
	case FolderFirstTaskReasonContextTooSmall:
		return "The model " + who + " uses has too small a context window for this project. Choose a model with a larger one."
	case FolderFirstTaskReasonNothingToAssess:
		return "Ori found no README, instructions or build files it could read in this project, so there was nothing to assess."
	case FolderFirstTaskReasonTimedOut:
		return "The assessment ran out of time before the model answered. Nothing in the project was run or changed. Try again."
	case FolderFirstTaskReasonProviderConfig:
		return "The model's provider is not signed in or set up, so the assessment did not run."
	case FolderFirstTaskReasonProviderQuota:
		return "The model's provider is out of quota, so the assessment did not run."
	case FolderFirstTaskReasonNeedsInput:
		return "The assessment is paused. Open " + name + " to continue."
	case FolderFirstTaskReasonStartFailed:
		return "The assessment could not start. Try again."
	default:
		return "The assessment did not finish. Nothing in the project was run or changed. Try again."
	}
}

// FolderFirstTaskRecipeRepair says where a reason is repaired: an existing
// page and what its link is called. Both are empty when a plain retry is the
// repair, or when nothing here can repair it.
func FolderFirstTaskRecipeRepair(reason, workspaceRoute, workspaceName string) (route, label string) {
	switch reason {
	case FolderFirstTaskReasonNoModel, FolderFirstTaskReasonModelUnavailable, FolderFirstTaskReasonModelUnsupported, FolderFirstTaskReasonContextTooSmall:
		return folderFirstTaskAgentsRoute, "Choose a model"
	case FolderFirstTaskReasonFolderUnavailable:
		return folderFirstTaskFolderRoute, "Show the folder"
	case FolderFirstTaskReasonProviderConfig, FolderFirstTaskReasonProviderQuota:
		return folderFirstTaskProviderRoute, "Open AI provider settings"
	case FolderFirstTaskReasonUnassigned, FolderFirstTaskReasonAssistantMissing, FolderFirstTaskReasonLocalActivation, FolderFirstTaskReasonSetupOpen:
		if strings.HasPrefix(workspaceRoute, "/workspaces/") {
			name := strings.TrimSpace(workspaceName)
			if name == "" {
				name = "workspace"
			}
			return workspaceRoute, "Open " + name
		}
	}
	return "", ""
}

// FolderFirstTaskRecipeBlocks reports a reason that must be repaired before a
// recipe task can be started or tried again. Any other reason is how one run
// ended; trying again is then allowed.
func FolderFirstTaskRecipeBlocks(reason string) bool {
	switch reason {
	case FolderFirstTaskReasonRecipeUnavailable, FolderFirstTaskReasonUnassigned, FolderFirstTaskReasonAssistantMissing,
		FolderFirstTaskReasonNoModel, FolderFirstTaskReasonModelUnavailable, FolderFirstTaskReasonModelUnsupported,
		FolderFirstTaskReasonFolderUnavailable, FolderFirstTaskReasonWorkspaceMissing,
		FolderFirstTaskReasonLocalActivation, FolderFirstTaskReasonSetupOpen:
		return true
	}
	return false
}

// FolderFirstTaskViewDetail is the receipt row for a first task as it stands.
// A task under a host recipe that cannot start yet says what it is waiting
// for: "Ready to start" there would promise a start the click cannot deliver.
func FolderFirstTaskViewDetail(view FolderFirstTaskView, result string) string {
	if view.Recipe == "" {
		return FolderFirstTaskRowDetail(view.State, view.CanStart, result)
	}
	switch view.State {
	case FolderFirstTaskSeeded:
		if view.CanStart {
			return "Starts when you press Start assessment"
		}
		return folderFirstTaskWaitsFor(view.Reason)
	case FolderFirstTaskFailed:
		if FolderFirstTaskRecipeBlocks(view.Reason) {
			waits := folderFirstTaskWaitsFor(view.Reason)
			return "Did not finish · " + strings.ToLower(waits[:1]) + waits[1:]
		}
		return "Did not finish"
	case FolderFirstTaskWaiting:
		return "Paused"
	case FolderFirstTaskFinished:
		return FolderFirstTaskRowDetail(view.State, view.CanStart, folderFirstTaskLead(result))
	}
	return FolderFirstTaskRowDetail(view.State, view.CanStart, result)
}

// FolderFirstTaskRecipeExcerpt is the one-line excerpt of a recipe task's
// report. Such a report opens with the recipe's own first heading, which says
// nothing about the project, so the excerpt is its first paragraph instead.
func FolderFirstTaskRecipeExcerpt(result string) string {
	return FolderFirstTaskExcerpt(folderFirstTaskLead(result))
}

// folderFirstTaskLead is the first paragraph of a report that is not a
// Markdown heading, on one line. A report with no such paragraph is returned
// as it is.
func folderFirstTaskLead(result string) string {
	var lead []string
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			if len(lead) > 0 {
				break
			}
			continue
		}
		lead = append(lead, line)
	}
	if len(lead) == 0 {
		return result
	}
	return strings.Join(lead, " ")
}

// folderFirstTaskWaitsFor is the short row text for what a recipe task that
// cannot start is waiting on.
func folderFirstTaskWaitsFor(reason string) string {
	switch reason {
	case FolderFirstTaskReasonNoModel:
		return "Needs a model before it can start"
	case FolderFirstTaskReasonModelUnavailable:
		return "Needs its model set up before it can start"
	case FolderFirstTaskReasonModelUnsupported:
		return "Needs a different model before it can start"
	case FolderFirstTaskReasonUnassigned, FolderFirstTaskReasonAssistantMissing:
		return "Needs an assistant before it can start"
	case FolderFirstTaskReasonFolderUnavailable:
		return "Needs the project folder back before it can start"
	case FolderFirstTaskReasonLocalActivation:
		return "Needs this workspace activated on this computer first"
	case FolderFirstTaskReasonSetupOpen:
		return "Waits for the workspace's own setup to finish"
	case FolderFirstTaskReasonRecipeUnavailable:
		return "Cannot start here yet"
	default:
		return "Not ready to start yet"
	}
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

// folderFirstTaskStartGrace is how long a spent start may sit on a task that
// has not begun before it reads as a start that failed. A start is handed to
// the executor and returns at once; the task turns In Progress a moment later.
const folderFirstTaskStartGrace = 30 * time.Second

// FolderFirstTaskStateOf reads a first look's state from the task record, as of
// now.
func FolderFirstTaskStateOf(task *workspace.Task) FolderFirstTaskState {
	return FolderFirstTaskStateAt(task, time.Now())
}

// FolderFirstTaskStateAt reads a first look's state from the task record.
//
// A successful run leaves the ticket in Review (or Done once accepted) with a
// result. A failed or timed-out run leaves it In Progress with an error, which
// is "failed" here, not "running"; so does a run that paused to ask the user
// something, which is "waiting". A task still waiting whose one start was
// spent is starting for a moment and, past that, a start that failed. A
// cancelled task reads as none: the user put it away.
func FolderFirstTaskStateAt(task *workspace.Task, now time.Time) FolderFirstTaskState {
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
		// A run under a host recipe that the server lost (it was restarted while
		// the model was answering) is put back to pending at start-up while its
		// ticket still says In Progress. Nothing is running it, so it did not
		// finish; saying "running" would leave it stuck with nothing to press.
		if task.Status == workspace.TaskStatusPending && workspace.FirstTaskRecipe(task) != "" {
			return FolderFirstTaskFailed
		}
		return FolderFirstTaskRunning
	case workspace.TicketStateCancelled:
		return FolderFirstTaskNone
	default:
		spent, consumed := task.Context[FolderFirstTaskConsumedKey]
		if !consumed {
			return FolderFirstTaskSeeded
		}
		if _, refused := task.Context[FolderFirstTaskStartFailedKey]; refused {
			return FolderFirstTaskFailed
		}
		// The marker is the time the start was spent. One that cannot be read
		// is treated as old.
		stamp, _ := spent.(string)
		if at, err := time.Parse(time.RFC3339, stamp); err == nil && !at.After(now) && now.Sub(at) < folderFirstTaskStartGrace {
			return FolderFirstTaskRunning
		}
		return FolderFirstTaskFailed
	}
}

// FolderFirstTaskRowDetail is what the receipt's task row says about a first
// look (FR14). result is the look's result, for a finished one.
func FolderFirstTaskRowDetail(state FolderFirstTaskState, canStart bool, result string) string {
	switch state {
	case FolderFirstTaskSeeded:
		if canStart {
			return "Starts when you press Start first look"
		}
		return "Ready to start"
	case FolderFirstTaskRunning:
		return "Running…"
	case FolderFirstTaskWaiting:
		return "Waiting for your answer"
	case FolderFirstTaskFinished:
		if line := folderFirstTaskFirstLine(result); line != "" {
			return "Done · " + line
		}
		return "Done"
	case FolderFirstTaskFailed:
		return "Did not finish"
	default:
		return ""
	}
}

// folderFirstTaskRowLineMax bounds the result line shown on the receipt row.
const folderFirstTaskRowLineMax = 120

// folderFirstTaskFirstLine is the first line of a result that says something,
// without Markdown heading or list marks, trimmed for a row.
func folderFirstTaskFirstLine(result string) string {
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#*-> "))
		if line == "" {
			continue
		}
		runes := []rune(strings.Join(strings.Fields(line), " "))
		if len(runes) <= folderFirstTaskRowLineMax {
			return string(runes)
		}
		return strings.TrimRight(string(runes[:folderFirstTaskRowLineMax-1]), " ") + "…"
	}
	return ""
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

// FolderFirstTaskMessageFor is FolderFirstTaskMessage for a look whose agent
// is known: the sentence names the agent doing the look rather than "the
// first look" alone.
func FolderFirstTaskMessageFor(reason, workspaceName, agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return FolderFirstTaskMessage(reason, workspaceName)
	}
	name := strings.TrimSpace(workspaceName)
	if name == "" {
		name = "the workspace"
	}
	switch reason {
	case FolderFirstTaskReasonNeedsInput:
		return agent + " is waiting for your answer. Open " + name + " to continue."
	case FolderFirstTaskReasonStartFailed:
		return agent + "'s first look could not start. Try again."
	case FolderFirstTaskReasonRunFailed:
		return agent + "'s first look did not finish. Try again."
	default:
		return FolderFirstTaskMessage(reason, workspaceName)
	}
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
	case FolderFirstTaskReasonRecipeUnavailable:
		return "The first assessment cannot run here yet. It stays ready, and nothing is sent to a model until it can."
	default:
		return ""
	}
}
