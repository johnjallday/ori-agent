package workspace

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Asynchronous goal runs (workspace-goal-suggestions FR24-FR27).
//
// Run now, the first run after a goal is first saved, and the deferred first
// run of a workspace created with a goal all start through StartMissionRun: it
// checks that the run can start, claims the workspace's single in-flight slot
// (the same guard the cadence uses, so at most one goal run per workspace
// executes at a time), creates the run, and executes it on a scheduler
// goroutine. The caller gets the run ID at once.

// ErrMissionRunInFlight is returned when a goal run is already executing for
// the workspace. The HTTP layer surfaces it as 409.
var ErrMissionRunInFlight = errors.New("a goal run is already in progress for this workspace")

// MissionNoModelReason is what the goal card says when no model can run.
const MissionNoModelReason = "Set up a model to get suggestions"

// MissionBlockedError is a goal run that could not start for a reason the user
// can fix. Code is the API error code; Reason is shown on the goal card and is
// also stored on the goal's needs-attention state.
type MissionBlockedError struct {
	Code   string
	Reason string
}

func (e *MissionBlockedError) Error() string { return e.Reason }

// MissionModelCheck reports whether a workspace's goal runs have a model to run
// on, and why not when they do not.
type MissionModelCheck func(ws *Workspace) (ok bool, reason string)

// MissionRunStarter is a MissionTrigger that can create a run and hand back
// its execution separately. MissionBridge implements it.
type MissionRunStarter interface {
	StartMissionRun(ctx context.Context, workspaceID string, cycleOrdinal int, opts MissionRunOptions) (runID string, execute func() error, err error)
}

// MissionRunInFlight describes a goal run started through StartMissionRun
// while it executes.
type MissionRunInFlight struct {
	RunID     string    `json:"run_id,omitempty"`
	Origin    string    `json:"origin"`
	StartedAt time.Time `json:"started_at"`
}

// SetMissionModelCheck wires the model availability check StartMissionRun
// runs before a goal run. Optional.
func (ts *TaskScheduler) SetMissionModelCheck(check MissionModelCheck) {
	if ts == nil {
		return
	}
	ts.missionModelCheck = check
}

// MissionRunning reports whether a goal run is executing for the workspace —
// started by StartMissionRun, by the cadence, or by an event trigger — and
// what is known about it.
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

// StartMissionRun starts one goal run for the workspace and returns its run ID
// without waiting for it. It refuses with ErrMissionGoalRequired,
// ErrMissionBindingsUnclassified, ErrMissionRunInFlight, ErrMissionTriggerNotConfigured,
// or a *MissionBlockedError (no agent, no model, an unusable goal toolbox) —
// the last also recorded on the goal so the card can say why. It ignores
// MissionEnabled and never changes the cadence.
func (ts *TaskScheduler) StartMissionRun(workspaceID, origin string) (string, error) {
	if ts == nil || ts.missionTrigger == nil {
		return "", ErrMissionTriggerNotConfigured
	}
	ws, err := ts.workspaceStore.Get(workspaceID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(ws.Mission) == "" {
		return "", ErrMissionGoalRequired
	}
	if !MissionBindingsReady(ws) {
		return "", ErrMissionBindingsUnclassified
	}
	if blocked := ts.missionStartBlocker(ws); blocked != nil {
		_ = ts.workspaceStore.Update(workspaceID, func(w *Workspace) error {
			MarkGoalNeedsAttention(w, blocked.Reason)
			return nil
		})
		return "", blocked
	}
	if !ts.claimMission(workspaceID) {
		return "", ErrMissionRunInFlight
	}
	release := func() {
		ts.missionMu.Lock()
		delete(ts.missionRunning, workspaceID)
		ts.missionMu.Unlock()
		ts.releaseMission(workspaceID)
	}
	finishAdmission, err := ts.admissionGate.Enter()
	if err != nil {
		release()
		return "", err
	}
	_ = ts.workspaceStore.Update(workspaceID, func(w *Workspace) error {
		ClearGoalNeedsAttention(w)
		return nil
	})

	cycleOrdinal := ws.MissionExecutionCount + 1
	opts := MissionRunOptions{Origin: origin}
	ctx, cancel := ts.missionRunContext()
	finish := func() {
		cancel()
		release()
		finishAdmission()
	}

	starter, ok := ts.missionTrigger.(MissionRunStarter)
	if !ok {
		// A trigger that cannot hand back its execution runs whole in the
		// background; its run ID is known only from the run record.
		ts.markMissionRunning(workspaceID, MissionRunInFlight{Origin: origin, StartedAt: time.Now()})
		ts.wg.Go(func() {
			defer finish()
			if _, err := ts.missionTrigger.TriggerMissionRun(ctx, workspaceID, cycleOrdinal); err != nil {
				logger.Warn("goal run failed", logger.Fields{"workspace_id": workspaceID, "origin": origin, "error": err})
			}
		})
		return "", nil
	}
	runID, execute, err := starter.StartMissionRun(ctx, workspaceID, cycleOrdinal, opts)
	if err != nil {
		finish()
		return "", err
	}
	ts.markMissionRunning(workspaceID, MissionRunInFlight{RunID: runID, Origin: origin, StartedAt: time.Now()})
	ts.wg.Go(func() {
		defer finish()
		if err := execute(); err != nil {
			logger.Warn("goal run failed", logger.Fields{"workspace_id": workspaceID, "run_id": runID, "origin": origin, "error": err})
		}
	})
	return runID, nil
}

func (ts *TaskScheduler) markMissionRunning(workspaceID string, running MissionRunInFlight) {
	ts.missionMu.Lock()
	defer ts.missionMu.Unlock()
	if ts.missionRunning == nil {
		ts.missionRunning = make(map[string]MissionRunInFlight)
	}
	ts.missionRunning[workspaceID] = running
}

// missionStartBlocker returns why a goal run cannot start, or nil.
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
