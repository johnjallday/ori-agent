package assistantsetup

import (
	"context"
	"errors"
	"fmt"
)

// Safe codes for the workspace step. They are a closed set: the projection and
// the browser never carry a raw error.
const (
	codeAgentRootUnavailable   = "agent_root_unavailable"
	codeTeamPlanChanged        = "team_plan_changed"
	codeWorkspaceUnresolved    = "workspace_outcome_unresolved"
	codeAgentNotAttached       = "agent_not_attached"
	codePreparationInterrupted = "preparation_interrupted"
)

// WorkspaceObservation is what a read-only observer can prove exists for one
// workspace claim, looked up by its reserved workspace ID and this operation's
// exact markers. A same-name agent without those markers is unrelated.
type WorkspaceObservation struct {
	// WorkspacePresent: a workspace exists at the reserved ID, complete or not.
	WorkspacePresent bool
	// WorkspaceProven: it carries this operation's assistant-setup provenance
	// and the installed File Janitor capability.
	WorkspaceProven bool
	// AgentInstanceID: the reviewed role's instance attached to a proven workspace.
	AgentInstanceID string
	// ProfileCreated: a profile carries this operation's provenance ID,
	// operation ID, and reviewed config digest.
	ProfileCreated      bool
	ProfileProvenanceID string
	ProfileStoreOrigin  string
}

// Empty reports that nothing from this claim exists: the only state in which a
// refused preparation can be called Failed rather than unproven.
func (o WorkspaceObservation) Empty() bool {
	return !o.WorkspacePresent && !o.WorkspaceProven && o.AgentInstanceID == "" && !o.ProfileCreated
}

// WorkspaceObserver reads, and never writes, what a workspace claim left behind.
type WorkspaceObserver interface {
	ObserveFileJanitor(ctx context.Context, request PrepareRequest) (WorkspaceObservation, error)
}

func (s *Service) SetObserver(observer WorkspaceObserver) {
	if s != nil {
		s.observer = observer
	}
}

// workspaceUnsettled reports a claim that has not reached an outcome: accepted
// but not started, or started without a recorded result.
func workspaceUnsettled(operation *Operation) bool {
	return operation != nil && (operation.Status == OperationClaimed || operation.Status == OperationRunning)
}

// retryableWorkspaceCode names a pre-write refusal that can clear on its own,
// so the same claim may be tried again through resume.
func retryableWorkspaceCode(code string) bool {
	return code == codeAgentRootUnavailable
}

// preWriteRefusal classifies a creator error that provably happened before the
// creator wrote anything in this attempt. Adopt mode never writes, and its
// errors describe a changed target, so it is left to the unresolved path.
func preWriteRefusal(mode TargetMode, err error) (string, bool) {
	if mode != TargetCreate {
		return "", false
	}
	switch {
	case errors.Is(err, ErrAgentRootUnavailable):
		return codeAgentRootUnavailable, true
	case errors.Is(err, ErrTeamConflict):
		return codeTeamPlanChanged, true
	}
	return "", false
}

func refusalError(code string) error {
	if code == codeAgentRootUnavailable {
		return ErrAgentRootUnavailable
	}
	return ErrTeamConflict
}

// settleFailedPreparation records the truthful outcome of a failed attempt:
//
//   - a typed pre-write refusal that the observer confirms left nothing behind
//     is Failed. A retryable one keeps the run active on the same claim; a plan
//     change invalidates the run so a fresh review is possible;
//   - anything else is unproven: receipts are recorded for exactly what the
//     observer can prove exists (the run is not advanced), and the run stops
//     for review as reconcile_required.
//
// Nothing is deleted, recreated, attached by name, or retried automatically.
func (s *Service) settleFailedPreparation(ctx context.Context, run *Run, operation *Operation, request PrepareRequest, prepareErr error) (*Projection, error) {
	owner := run.OwnerUserID
	observation, observed := s.observe(ctx, request)
	if code, refused := preWriteRefusal(run.TargetMode, prepareErr); refused && observed && observation.Empty() {
		if _, err := s.store.FailWorkspace(ctx, owner, run.ID, operation.ID, code, !retryableWorkspaceCode(code)); err == nil {
			return s.currentProjection(ctx, owner, run.ID), fmt.Errorf("%w: %v", refusalError(code), prepareErr)
		}
	}
	if observed && !observation.Empty() {
		_ = s.store.RecordWorkspaceObservation(ctx, owner, run.ID, operation.ID, observation)
	}
	_, _ = s.store.MarkWorkspaceUnresolved(ctx, owner, run.ID, operation.ID, codeWorkspaceUnresolved)
	return s.currentProjection(ctx, owner, run.ID), fmt.Errorf("%w: %v", ErrReconcileRequired, prepareErr)
}

func (s *Service) observe(ctx context.Context, request PrepareRequest) (WorkspaceObservation, bool) {
	if s.observer == nil || request.Mode != TargetCreate {
		return WorkspaceObservation{}, false
	}
	observation, err := s.observer.ObserveFileJanitor(ctx, request)
	if err != nil {
		return WorkspaceObservation{}, false
	}
	return observation, true
}

// currentProjection re-reads the run so an error response can still carry the
// stopped state. The caller's attempt has ended, so even if recording its
// outcome failed and the claim still reads running, it is shown as stopped,
// never as Creating. It returns nil rather than a guess when anything is
// unreadable.
func (s *Service) currentProjection(ctx context.Context, owner, runID string) *Projection {
	run, err := s.store.GetRun(ctx, owner, runID)
	if err != nil {
		return nil
	}
	base, err := s.baseProjection(ctx, owner)
	if err != nil {
		return nil
	}
	projection, err := s.projectRunAfter(ctx, base, run, true)
	if err != nil {
		return nil
	}
	return projection
}

// reviewableAgain reports a run invalidated because the reviewed team plan
// changed before anything was written. It no longer blocks a fresh review.
func (s *Service) reviewableAgain(ctx context.Context, run *Run) bool {
	if run == nil || run.Status != RunInvalidated || run.LastErrorCode != codeTeamPlanChanged || run.FailedStep != StepWorkspace {
		return false
	}
	resources, err := s.store.ListResources(ctx, run.OwnerUserID, run.ID)
	return err == nil && len(resources) == 0
}

func failedWorkspaceMessage(code string) string {
	if code == codeAgentRootUnavailable {
		return "Ori could not reach your agents folder, so it stopped before creating anything. Check that your Workspace Directory is available, then try again."
	}
	return "Ori stopped before creating anything. Review the setup before trying again."
}

// applyWorkspaceStop replaces the generic "setting up" view for a workspace
// claim that is not progressing in this process.
func applyWorkspaceStop(projection *Projection, operation *Operation, live bool) {
	run := projection.Run
	if run == nil || operation == nil || run.CurrentStep != StepWorkspace {
		return
	}
	switch {
	case run.Status == RunInvalidated && run.LastErrorCode == codeTeamPlanChanged:
		projection.ViewState = "plan_changed"
		projection.StatusMessage = "The File Curator setup changed after your review, so Ori stopped before creating anything. Review the updated setup to continue."
		projection.Actions = []Action{
			{ID: "review_again", Label: "Review updated setup", Enabled: true},
			{ID: "manual", Label: "Customize manually", Enabled: true, Route: "/workspaces/new?template=file-janitor"},
		}
	case run.Status != RunActive, live && workspaceUnsettled(operation):
		return
	case operation.Status == OperationFailed:
		projection.ViewState = "needs_attention"
		projection.StatusMessage = failedWorkspaceMessage(operation.SafeErrorCode)
		projection.Actions = []Action{}
		if retryableWorkspaceCode(operation.SafeErrorCode) {
			projection.Actions = append(projection.Actions, Action{ID: "resume", Label: "Try again", Enabled: true})
		}
		projection.Actions = append(projection.Actions, Action{ID: "finish_later", Label: "Finish later", Enabled: true})
	case workspaceUnsettled(operation):
		projection.ViewState = "needs_attention"
		projection.StatusMessage = "Ori stopped before it finished preparing this setup. Continue setup checks what already exists first, so nothing is created twice."
		projection.Actions = []Action{
			{ID: "resume", Label: "Continue setup", Enabled: true},
			{ID: "finish_later", Label: "Finish later", Enabled: true},
		}
	}
}
