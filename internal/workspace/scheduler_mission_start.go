package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

var ErrMissionRunInFlight = errors.New("a goal run is already in progress for this workspace")

// ErrMissionFirstRunHandled is a successful no-op for a repeated first-open.
var ErrMissionFirstRunHandled = errors.New("the first goal attempt has already been handled")

const MissionNoModelReason = "Set up a model to get suggestions"

type MissionBlockedError struct {
	Code   string
	Reason string
}

func (e *MissionBlockedError) Error() string { return e.Reason }

type MissionModelCheck func(ws *Workspace) (ok bool, reason string)

// MissionReadinessCheck evaluates canonical setup/capability/folder state.
// It runs outside store mutations: setup status may persist progress and invoke
// its completion hooks. It must neither invoke a goal model nor grant access.
type MissionReadinessCheck func(context.Context, *Workspace) *MissionBlockedError

type MissionRunStarter interface {
	StartMissionRun(ctx context.Context, workspaceID string, cycleOrdinal int, opts MissionRunOptions) (runID string, execute func() error, err error)
}

type MissionRunInFlight struct {
	RunID     string    `json:"run_id,omitempty"`
	Origin    string    `json:"origin"`
	StartedAt time.Time `json:"started_at"`
}

func (ts *TaskScheduler) SetMissionModelCheck(check MissionModelCheck) {
	if ts != nil {
		ts.missionModelCheck = check
	}
}

func (ts *TaskScheduler) SetMissionReadinessCheck(check MissionReadinessCheck) {
	if ts != nil {
		ts.missionReadinessCheck = check
	}
}

func (ts *TaskScheduler) MissionRunning(workspaceID string) (MissionRunInFlight, bool) {
	if ts == nil {
		return MissionRunInFlight{}, false
	}
	ts.missionMu.Lock()
	defer ts.missionMu.Unlock()
	if !ts.missionInFlight[workspaceID] {
		return MissionRunInFlight{}, false
	}
	running, ok := ts.missionRunning[workspaceID]
	if !ok {
		running = MissionRunInFlight{Origin: MissionOriginCadence}
	}
	return running, true
}

// acquireMissionRun is shared by automatic, manual, cadence and event starts.
// Its permit is retained through execution, including asynchronous execution.
func (ts *TaskScheduler) acquireMissionRun(workspaceID string) (func(), error) {
	if ts == nil {
		return nil, ErrMissionTriggerNotConfigured
	}
	if !ts.claimMission(workspaceID) {
		return nil, ErrMissionRunInFlight
	}
	finishAdmission, err := ts.admissionGate.Enter()
	if err != nil {
		ts.releaseMission(workspaceID)
		return nil, err
	}
	return func() {
		ts.missionMu.Lock()
		delete(ts.missionRunning, workspaceID)
		delete(ts.missionInFlight, workspaceID)
		ts.missionMu.Unlock()
		finishAdmission()
	}, nil
}

// StartMissionRun returns promptly after admission and run creation. Creating,
// opening or saving a goal does not enable cadence or imply read consent.
func (ts *TaskScheduler) StartMissionRun(workspaceID, origin string) (string, error) {
	finish, err := ts.acquireMissionRun(workspaceID)
	if err != nil {
		return "", err
	}
	ctx, cancel := ts.missionRunContext()
	runID, execute, err := ts.prepareClaimedMissionRun(ctx, workspaceID, MissionRunOptions{Origin: origin})
	if err != nil {
		cancel()
		finish()
		return "", err
	}
	ts.wg.Go(func() {
		defer finish()
		defer cancel()
		if _, err := execute(); err != nil {
			logger.Warn("goal run failed", logger.Fields{"workspace_id": workspaceID, "run_id": runID, "origin": origin, "error": err})
		}
	})
	return runID, nil
}

// TriggerMissionRunOpts keeps the synchronous event/manual API while sharing
// the same admission and single-flight protection as HTTP and cadence. The
// ordinal supplied by an event snapshot is deliberately recomputed under claim.
func (ts *TaskScheduler) TriggerMissionRunOpts(ctx context.Context, workspaceID string, _ int, opts MissionRunOptions) (string, error) {
	finish, err := ts.acquireMissionRun(workspaceID)
	if err != nil {
		return "", err
	}
	defer finish()
	_, execute, err := ts.prepareClaimedMissionRun(ctx, workspaceID, opts)
	if err != nil {
		return "", err
	}
	return execute()
}

// missionWorkspace uses canonical metadata whenever the composed store exposes
// it. An unreadable folder is not a workspace with no setup requirements.
func (ts *TaskScheduler) missionWorkspace(id string) (*Workspace, error) {
	if reader, ok := ts.workspaceStore.(interface {
		GetFolderWorkspace(string) (*Workspace, error)
	}); ok {
		return reader.GetFolderWorkspace(id)
	}
	return ts.workspaceStore.Get(id)
}

// prepareClaimedMissionRun requires the caller's single-flight/reset permit.
// Claiming is durable BEFORE readiness or bridge creation. A crash at any point
// leaves explicit recovery, not a fresh automatic intent. Nothing here reads
// project contents; consent precedes even setup evaluation.
func (ts *TaskScheduler) prepareClaimedMissionRun(ctx context.Context, id string, opts MissionRunOptions) (string, func() (string, error), error) {
	origin := opts.Origin
	if origin == "" {
		origin = MissionOriginCadence
		if opts.Event != nil {
			origin = MissionOriginEvent
		}
	}
	first := origin == MissionOriginFirstGoal
	handled := false
	err := ts.workspaceStore.Update(id, func(ws *Workspace) error {
		if first && (!ws.GoalFirstRunPending || ws.GoalFirstRunState != "" || goalFirstRunAccepted(ws)) {
			handled = true
			ws.GoalFirstRunPending = false
			return nil
		}
		if strings.TrimSpace(ws.Mission) == "" {
			if ws.GoalFirstRunPending {
				cancelDeferredGoalRun(ws)
			}
			return nil
		}
		ws.GoalFirstRunPending = false
		if !goalFirstRunAccepted(ws) {
			ws.GoalFirstRunState = GoalFirstRunStarting
		}
		return nil
	})
	if err != nil {
		return "", nil, err // never invoke the bridge after a failed claim write
	}
	if handled {
		return "", nil, ErrMissionFirstRunHandled
	}
	fail := func(cause error, state string) (string, func() (string, error), error) {
		persistErr := ts.workspaceStore.Update(id, func(ws *Workspace) error {
			if strings.TrimSpace(ws.Mission) == "" {
				cancelDeferredGoalRun(ws)
				return nil
			}
			if !goalFirstRunAccepted(ws) {
				ws.GoalFirstRunState = state
			}
			MarkGoalNeedsAttention(ws, cause.Error())
			return nil
		})
		if persistErr != nil {
			return "", nil, fmt.Errorf("record goal admission: %w (start refused: %v)", persistErr, cause)
		}
		return "", nil, cause
	}
	ws, err := ts.missionWorkspace(id)
	if err != nil || ws == nil {
		return fail(&MissionBlockedError{Code: "goal_setup_unavailable", Reason: "Workspace setup could not be read. Repair workspace storage, then choose Run now."}, GoalFirstRunBlocked)
	}
	if strings.TrimSpace(ws.Mission) == "" {
		return "", nil, ErrMissionGoalRequired
	}
	if first && GoalRequiresReadConsent(ws) {
		return fail(&MissionBlockedError{Code: "goal_read_consent_required", Reason: GoalReadConsentReason}, GoalFirstRunBlocked)
	}
	if err := RequireWorkspaceExecution(ctx, ts.workspaceStore, id, origin != MissionOriginManual); err != nil {
		return fail(&MissionBlockedError{Code: "goal_local_admission_required", Reason: "Activate this workspace on this installation before running its goal."}, GoalFirstRunBlocked)
	}
	if ts.missionTrigger == nil {
		return fail(ErrMissionTriggerNotConfigured, GoalFirstRunBlocked)
	}
	if !MissionBindingsReady(ws) {
		return fail(ErrMissionBindingsUnclassified, GoalFirstRunBlocked)
	}
	if ts.missionReadinessCheck != nil {
		if blocked := ts.missionReadinessCheck(ctx, ws); blocked != nil {
			return fail(blocked, GoalFirstRunBlocked)
		}
	} else if p := ws.GetTemplateProvenance(); p != nil && (p.SetupWizard != nil || p.RuntimeRequirements != nil || len(p.CapabilityRequirements)+len(p.DirectoryRequirements)+len(p.IntakeRequirements) > 0) {
		return fail(&MissionBlockedError{Code: "goal_setup_unavailable", Reason: "Required workspace setup cannot be checked. Restore setup support, then choose Run now."}, GoalFirstRunBlocked)
	}
	if blocked := ts.missionStartBlocker(ws); blocked != nil {
		return fail(blocked, GoalFirstRunBlocked)
	}
	// Recheck the saved goal before bridge preparation, after potentially slow
	// readiness checks. Clearing during admission cancels deferred intent.
	current, err := ts.workspaceStore.Get(id)
	if err != nil {
		return fail(err, GoalFirstRunFailed)
	}
	if strings.TrimSpace(current.Mission) == "" {
		return "", nil, ErrMissionGoalRequired
	}
	ordinal := current.MissionExecutionCount + 1
	var runID string
	var execute func() (string, error)
	if starter, ok := ts.missionTrigger.(MissionRunStarter); ok {
		var run func() error
		runID, run, err = starter.StartMissionRun(ctx, id, ordinal, opts)
		if err != nil {
			return fail(err, GoalFirstRunFailed)
		}
		execute = func() (string, error) { return runID, run() }
	} else {
		// Legacy triggers cannot separate creation from execution. Persist
		// acceptance conservatively before dispatching them, never after.
		execute = func() (string, error) {
			if trigger, ok := ts.missionTrigger.(MissionOptionsTrigger); ok {
				return trigger.TriggerMissionRunOpts(ctx, id, ordinal, opts)
			}
			return ts.missionTrigger.TriggerMissionRun(ctx, id, ordinal)
		}
	}
	err = ts.workspaceStore.Update(id, func(ws *Workspace) error {
		if strings.TrimSpace(ws.Mission) == "" {
			return ErrMissionGoalRequired
		}
		ws.GoalFirstRunPending = false
		ws.GoalFirstRunState = GoalFirstRunAccepted
		ClearGoalNeedsAttention(ws)
		return nil
	})
	if err != nil {
		return "", nil, err // created-but-unexecuted is ambiguous, never replayed
	}
	ts.markMissionRunning(id, MissionRunInFlight{RunID: runID, Origin: origin, StartedAt: time.Now()})
	return runID, execute, nil
}

func (ts *TaskScheduler) markMissionRunning(workspaceID string, running MissionRunInFlight) {
	ts.missionMu.Lock()
	defer ts.missionMu.Unlock()
	if ts.missionRunning == nil {
		ts.missionRunning = make(map[string]MissionRunInFlight)
	}
	ts.missionRunning[workspaceID] = running
}

func (ts *TaskScheduler) missionStartBlocker(ws *Workspace) *MissionBlockedError {
	if strings.TrimSpace(ws.EntryAgentName()) == "" {
		return &MissionBlockedError{Code: "goal_no_agent", Reason: "Add an agent to this workspace so it can suggest work"}
	}
	if ts.missionModelCheck != nil {
		if ok, reason := ts.missionModelCheck(ws); !ok {
			if strings.TrimSpace(reason) == "" {
				reason = MissionNoModelReason
			}
			return &MissionBlockedError{Code: "goal_no_model", Reason: reason}
		}
	}
	if preflight := ts.preflightGoalToolbox(ws); !preflight.OK {
		return &MissionBlockedError{Code: "goal_toolbox_unavailable", Reason: preflight.Reason}
	}
	return nil
}
