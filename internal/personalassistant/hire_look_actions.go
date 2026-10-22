package personalassistant

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A hire's first look reports back under Needs you (agents-hire-with-a-brief
// FR14-FR17) with Start this and Not now, or Retry when it did not finish.
// The look is found by the hire's id among the user's own hires, never by a
// workspace the browser names, and its suggestion is read again from the
// look's run record.

// HireLookStatus is where a hire's first look stands, derived from its record
// and the workspace's run history on every read.
type HireLookStatus string

const (
	// HireLookWaiting is a hire whose look has no run yet: its workspace's
	// armed first goal run starts when the workspace is opened.
	HireLookWaiting HireLookStatus = "waiting"
	// HireLookRunning is a look whose run has not finished.
	HireLookRunning HireLookStatus = "running"
	// HireLookReported is a finished look whose report waits for the user.
	HireLookReported HireLookStatus = "reported"
	// HireLookFailed is a look that did not start, did not finish, or stopped.
	HireLookFailed HireLookStatus = "failed"
	// HireLookStarted is a report whose suggestion the user started.
	HireLookStarted HireLookStatus = "started"
	// HireLookDismissed is a report the user put aside.
	HireLookDismissed HireLookStatus = "dismissed"
)

// hireLookInterruptedAfter is how long a look with no run record and no run
// in flight is still taken to be starting. A goal run times out after 15
// minutes; one that left no record after this long stopped with the server.
const hireLookInterruptedAfter = 20 * time.Minute

// hireLookInterrupted is what the user is told about a look that stopped.
const hireLookInterrupted = "The first look stopped before it finished."

// HireTaskKey on a task names the hire whose first look it was started from.
const HireTaskKey = "hire_id"

// HireLookStatusOf is where a hire's look stands, with the reason when it
// failed and the run record when it finished. running reports whether the
// workspace has a goal run in flight; nil means it cannot be told, and the
// look is then judged by its age alone.
func HireLookStatusOf(record HireRecord, ws *workspace.Workspace, running func(workspaceID string) bool, now time.Time) (HireLookStatus, string, *workspace.MissionRunRecord) {
	switch record.State {
	case HireStateDismissed:
		return HireLookDismissed, "", HireLookRun(ws, record.RunID)
	case HireStateStarted:
		return HireLookStarted, "", HireLookRun(ws, record.RunID)
	case HireStateFailed:
		return HireLookFailed, record.Failure, nil
	}
	if strings.TrimSpace(record.RunID) == "" {
		return HireLookWaiting, "", nil
	}
	if run := HireLookRun(ws, record.RunID); run != nil {
		if run.Succeeded {
			return HireLookReported, "", run
		}
		reason := strings.TrimSpace(run.Error)
		if reason == "" {
			reason = "The first look did not finish."
		}
		return HireLookFailed, truncateRunes(reason, 400), run
	}
	if running != nil && running(record.WorkspaceID) {
		return HireLookRunning, "", nil
	}
	if now.Sub(record.UpdatedAt) < hireLookInterruptedAfter {
		return HireLookRunning, "", nil
	}
	return HireLookFailed, hireLookInterrupted, nil
}

// HireLookActions are Start this, Not now and Retry on a hire's report.
type HireLookActions struct {
	Records    HireRecordStore
	Workspaces workspace.Store
	// Start starts a Ready task through the manual-execution path, as the
	// folder report's Start this does.
	Start func(workspaceID, taskID string) error
	// Look starts one more first look on the hire's workspace (Retry).
	Look func(workspaceID string) (runID string, err error)
	// Running reports whether a workspace has a goal run in flight.
	Running func(workspaceID string) bool
	Now     func() time.Time
}

func (a *HireLookActions) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// hire reads one of the user's hires and its workspace.
func (a *HireLookActions) hire(ctx context.Context, userID, hireID string) (HireRecord, *workspace.Workspace, error) {
	hireID = strings.TrimSpace(hireID)
	if a == nil || a.Records == nil || a.Workspaces == nil || hireID == "" {
		return HireRecord{}, nil, ErrFirstLookNotFound
	}
	record, err := a.Records.Get(ctx, hireID)
	if errors.Is(err, ErrHireNotFound) || (err == nil && record.UserID != userID) {
		return HireRecord{}, nil, ErrFirstLookNotFound
	}
	if err != nil {
		return HireRecord{}, nil, err
	}
	ws, err := a.Workspaces.Get(record.WorkspaceID)
	if err != nil || ws == nil {
		return HireRecord{}, nil, ErrFirstLookNotFound
	}
	return record, ws, nil
}

func hireTaskRoute(ws *workspace.Workspace, taskID string) string {
	return "/workspaces/" + url.PathEscape(ws.FolderSlug) + "/task/" + url.PathEscape(taskID)
}

// StartSuggestion is Start this: the look's suggested task — its own Backlog
// suggestion when it wrote one, else a new task — is assigned to the hired
// agent, made Ready and started at once. The click is the confirmation; the
// task runs under the workspace's autonomy. A second click finds the task
// already started and starts nothing.
func (a *HireLookActions) StartSuggestion(ctx context.Context, userID, hireID string) (FirstLookStarted, error) {
	record, ws, err := a.hire(ctx, userID, hireID)
	if err != nil {
		return FirstLookStarted{}, err
	}
	if record.State == HireStateStarted && record.TaskID != "" {
		return FirstLookStarted{TaskID: record.TaskID, WorkspaceID: ws.ID, Route: hireTaskRoute(ws, record.TaskID)}, ErrFirstLookActedOn
	}
	if record.State == HireStateDismissed {
		return FirstLookStarted{}, ErrFirstLookActedOn
	}
	status, _, run := HireLookStatusOf(record, ws, a.Running, a.now())
	if status != HireLookReported {
		return FirstLookStarted{}, ErrFirstLookNotFinished
	}
	suggestion := HireLookSuggestionOf(ws, run)
	if suggestion == nil {
		return FirstLookStarted{}, ErrFirstLookNoSuggestion
	}
	if a.Start == nil {
		return FirstLookStarted{}, ErrFolderOutcomeUnavailable
	}
	taskID := ""
	now := a.now().UTC()
	reason := "Suggested by " + record.AgentName + "'s first look"
	err = workspace.CanonicalUpdate(a.Workspaces, ws.ID, func(current *workspace.Workspace) error {
		for i := range current.Tasks {
			if started, _ := current.Tasks[i].Context[HireTaskKey].(string); started == record.ID {
				taskID = current.Tasks[i].ID
				return ErrFirstLookActedOn
			}
		}
		assignment := workspace.TaskAssignment{
			AgentName: record.AgentName, Mode: workspace.TaskAssignmentModeManual,
			AssignedBy: workspace.TaskAssignedByManual, Reason: reason,
		}
		if ticket := HireLookSuggestionTicket(current, run, suggestion.Text); ticket != nil && ticket.CanonicalState() == workspace.TicketStateBacklog {
			if err := ticket.TransitionTicket(workspace.TicketStateReady, workspace.TicketStateChange{
				Actor: workspace.TicketActorUser, Reason: "Start this on " + record.AgentName + "'s first look", Timestamp: now,
			}); err != nil {
				return err
			}
			if err := current.ApplyTaskAssignment(ticket, assignment); err != nil {
				return err
			}
			if ticket.Context == nil {
				ticket.Context = map[string]any{}
			}
			ticket.Context[HireTaskKey] = record.ID
			ticket.Context[workspace.TaskContextAutonomyGate] = workspace.TaskAutonomyGateWorkspace
			ticket.UpdatedAt = now
			taskID = ticket.ID
			return nil
		}
		task := workspace.Task{
			ID: uuid.NewString(), WorkspaceID: current.ID, Description: suggestion.Text,
			Details:    reason + ". Read and report; change nothing.",
			Priority:   workspace.TicketPriorityDefault,
			Status:     workspace.TaskStatusPending,
			SourceType: workspace.TicketSourceGoalSuggestion, SourceID: record.RunID,
			From: "Ori", CreatedAt: now,
			Context: map[string]any{
				HireTaskKey:                       record.ID,
				workspace.TaskContextAutonomyGate: workspace.TaskAutonomyGateWorkspace,
			},
		}
		if err := current.ApplyTaskAssignment(&task, assignment); err != nil {
			return err
		}
		taskID = task.ID
		return current.AddTask(task)
	})
	if err != nil && !errors.Is(err, ErrFirstLookActedOn) {
		return FirstLookStarted{}, err
	}
	started := FirstLookStarted{TaskID: taskID, WorkspaceID: ws.ID, Route: hireTaskRoute(ws, taskID)}
	if _, updateErr := a.Records.Update(ctx, record.ID, func(r *HireRecord) error {
		r.State, r.TaskID = HireStateStarted, taskID
		return nil
	}); updateErr != nil {
		return started, updateErr
	}
	if errors.Is(err, ErrFirstLookActedOn) {
		return started, err
	}
	if err := a.Start(ws.ID, taskID); err != nil {
		// The task stays Ready and assigned in its workspace, where it can be
		// started by hand; the report has done its part.
		return started, err
	}
	return started, nil
}

// Dismiss is Not now: the report leaves Needs you and Today, its suggestion
// goes into the workspace's dismissed suggestions so later goal runs do not
// suggest it again, and the run stays readable in the workspace's goal history.
func (a *HireLookActions) Dismiss(ctx context.Context, userID, hireID string) error {
	record, ws, err := a.hire(ctx, userID, hireID)
	if err != nil {
		return err
	}
	switch record.State {
	case HireStateDismissed:
		return nil
	case HireStateStarted:
		return ErrFirstLookActedOn
	}
	_, _, run := HireLookStatusOf(record, ws, a.Running, a.now())
	if suggestion := HireLookSuggestionOf(ws, run); suggestion != nil {
		now := a.now()
		if err := workspace.CanonicalUpdate(a.Workspaces, ws.ID, func(current *workspace.Workspace) error {
			current.RememberDismissedSuggestion(suggestion.Text, "Not now on "+record.AgentName+"'s first look", record.RunID, now)
			return nil
		}); err != nil {
			return err
		}
	}
	_, err = a.Records.Update(ctx, record.ID, func(r *HireRecord) error {
		r.State = HireStateDismissed
		if r.FinishedAt.IsZero() {
			r.FinishedAt = a.now().UTC()
		}
		return nil
	})
	return err
}

// ErrHireLookNotFailed refuses Retry on a look that did not fail.
var ErrHireLookNotFailed = errors.New("personal assistant: only a first look that did not finish can be retried")

// Retry starts one more first look for a hire whose look did not start, did
// not finish, or stopped. It returns the hire as it now stands.
func (a *HireLookActions) Retry(ctx context.Context, userID, hireID string) (HireRecord, error) {
	record, ws, err := a.hire(ctx, userID, hireID)
	if err != nil {
		return HireRecord{}, err
	}
	if status, _, _ := HireLookStatusOf(record, ws, a.Running, a.now()); status != HireLookFailed {
		return record, ErrHireLookNotFailed
	}
	if a.Look == nil {
		return record, ErrFolderOutcomeUnavailable
	}
	runID, startErr := a.Look(ws.ID)
	return a.Records.Update(ctx, record.ID, func(r *HireRecord) error {
		if startErr != nil {
			r.State, r.Failure, r.FinishedAt = HireStateFailed, truncateRunes(strings.Join(strings.Fields(startErr.Error()), " "), 400), a.now().UTC()
			return nil
		}
		r.State, r.RunID, r.Failure, r.FinishedAt = HireStateLooking, runID, "", time.Time{}
		return nil
	})
}
