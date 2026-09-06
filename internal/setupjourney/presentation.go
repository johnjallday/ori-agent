package setupjourney

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/specialistevents"
)

// PresentationMutation is the strict request shared by Open and Dismiss.
type PresentationMutation struct {
	IfRevision     int64
	IdempotencyKey string
}

// Open records presentation history only. It cannot complete a step or invoke a
// canonical consequence owner.
func (s *Service) Open(ctx context.Context, userID, runID string, request PresentationMutation) (*JourneyProjection, error) {
	return s.mutatePresentation(ctx, userID, runID, request, true)
}

// Dismiss records presentation history only. Readiness remains fully derived
// from canonical owners.
func (s *Service) Dismiss(ctx context.Context, userID, runID string, request PresentationMutation) (*JourneyProjection, error) {
	return s.mutatePresentation(ctx, userID, runID, request, false)
}

func (s *Service) mutatePresentation(
	ctx context.Context,
	userID string,
	runID string,
	request PresentationMutation,
	open bool,
) (*JourneyProjection, error) {
	if request.IfRevision <= 0 || strings.TrimSpace(request.IdempotencyKey) == "" {
		return nil, failure(ReasonInputInvalid, 0)
	}
	current, declaration, _, run, err := s.authorizedCurrentRun(ctx, userID, runID)
	if err != nil {
		return nil, err
	}
	stepID := current.CurrentStepID
	if stepID == "" {
		stepID = declaration.Steps[len(declaration.Steps)-1].ID
	}
	actionID := "dismiss_journey"
	resultCode := ResultDismissed
	inputDigest := Digest([]byte("dismiss_journey:v1"))
	if open {
		actionID = "open_journey"
		resultCode = ResultOpened
		inputDigest = Digest([]byte("open_journey:v1"))
	}
	receipt, claimed, replayed, claimErr := s.store.ClaimOperation(ctx, OperationClaim{
		RunKind: run.Kind, RunID: run.ID, IfRevision: request.IfRevision,
		IdempotencyKey: request.IdempotencyKey, StepID: stepID,
		ActionID: actionID, InputDigest: inputDigest,
	})
	if claimErr != nil {
		return nil, safeStoreFailure(claimErr, current.StateRevision)
	}
	if replayed && (receipt.Status == OperationSucceeded || receipt.Status == OperationFailed) {
		if receipt.Status == OperationFailed {
			return nil, failure(receipt.ReasonCode, receipt.RunRevisionAfter)
		}
		return s.Read(ctx, userID, run.ID)
	}

	now := s.now().UTC()
	if open {
		if claimed.FirstOpenedAt == nil {
			claimed.FirstOpenedAt = &now
		}
		claimed.Dismissed = false
	} else {
		claimed.Dismissed = true
		claimed.LastDismissedAt = &now
	}
	var root *Run
	if claimed.Kind == RunKindChild {
		root, err = s.store.GetRun(ctx, claimed.RootRunID)
		if err != nil {
			_, _ = s.store.MarkOperationReconcileRequired(ctx, claimed.Kind, claimed.ID, request.IdempotencyKey)
			return nil, safeStoreFailure(err, claimed.StateRevision)
		}
	} else {
		root = claimed
	}
	candidate, reads := s.deriveCanonical(ctx, declaration, root, claimed, nil)
	completion := OperationCompletion{Status: OperationSucceeded, ResultCode: resultCode}
	_, finalized, finalizeReplayed, finalizeErr := s.store.FinalizeOperation(
		ctx, candidate, request.IdempotencyKey, completion,
	)
	if finalizeErr != nil {
		_, _ = s.store.MarkOperationReconcileRequired(ctx, claimed.Kind, claimed.ID, request.IdempotencyKey)
		return nil, safeStoreFailure(finalizeErr, claimed.StateRevision)
	}
	if finalizeReplayed {
		return s.Read(ctx, userID, run.ID)
	}
	projection := projectionFromRun(declaration, finalized, reads, nil)
	emitProjectionLifecycleTransition(current, projection)
	if open {
		switch {
		case run.FirstOpenedAt == nil:
			emitPresentationEvent(specialistevents.JourneyOpened, projection)
		case run.Dismissed:
			emitPresentationEvent(specialistevents.JourneyResumed, projection)
		}
	} else if !run.Dismissed {
		emitPresentationEvent(specialistevents.JourneyDismissed, projection)
	}
	return projection, nil
}

// CreateOrResumeChild creates only setup persistence. It does not create a
// workspace, choose a route, install anything, or staff an agent.
func (s *Service) CreateOrResumeChild(ctx context.Context, userID string, request PresentationMutation) (*JourneyProjection, error) {
	if request.IfRevision <= 0 || strings.TrimSpace(request.IdempotencyKey) == "" {
		return nil, failure(ReasonInputInvalid, 0)
	}
	current, declaration, root, _, err := s.authorizedCurrentRun(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	if current.DeclarationIncompatible || !projectionHasAction(current, ActionConnectAnotherProject) {
		return nil, failure(ReasonActionUnavailable, current.StateRevision)
	}
	receipt, claimed, replayed, claimErr := s.store.ClaimOperation(ctx, OperationClaim{
		RunKind: RunKindRoot, RunID: root.ID, IfRevision: request.IfRevision,
		IdempotencyKey: request.IdempotencyKey, StepID: declaration.Steps[len(declaration.Steps)-1].ID,
		ActionID:    string(ActionConnectAnotherProject),
		InputDigest: Digest([]byte("connect_another_project:v1")),
	})
	if claimErr != nil {
		return nil, safeStoreFailure(claimErr, current.StateRevision)
	}
	if replayed && receipt.Status == OperationSucceeded {
		if receipt.Result.ChildRunID == "" {
			return nil, failure(ReasonOperationFailed, receipt.RunRevisionAfter)
		}
		return s.Read(ctx, userID, receipt.Result.ChildRunID)
	}
	if replayed && receipt.Status == OperationFailed {
		return nil, failure(receipt.ReasonCode, receipt.RunRevisionAfter)
	}

	child, _, childErr := s.store.CreateOrGetChild(ctx, root.ID)
	if childErr != nil {
		_, _ = s.store.MarkOperationReconcileRequired(ctx, claimed.Kind, claimed.ID, request.IdempotencyKey)
		return nil, safeStoreFailure(childErr, claimed.StateRevision)
	}
	candidate, _ := s.deriveCanonical(ctx, declaration, claimed, claimed, nil)
	completion := OperationCompletion{
		Status: OperationSucceeded, ResultCode: ResultChildRunCreated,
		Result: CanonicalResult{ChildRunID: child.ID},
	}
	finalReceipt, _, _, finalizeErr := s.store.FinalizeOperation(
		ctx, candidate, request.IdempotencyKey, completion,
	)
	if finalizeErr != nil {
		_, _ = s.store.MarkOperationReconcileRequired(ctx, claimed.Kind, claimed.ID, request.IdempotencyKey)
		return nil, safeStoreFailure(finalizeErr, claimed.StateRevision)
	}
	return s.Read(ctx, userID, finalReceipt.Result.ChildRunID)
}

func (s *Service) authorizedCurrentRun(
	ctx context.Context,
	userID string,
	runID string,
) (*JourneyProjection, *specialist.SetupJourney, *Run, *Run, error) {
	projection, err := s.Read(ctx, userID, runID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	relationship, declaration, err := s.currentDeclaration(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	root, storeErr := s.store.GetRoot(
		ctx, relationship.UserID, relationship.AssistantID,
		relationship.SpecialistSlug, declaration.ID,
	)
	if storeErr != nil {
		return nil, nil, nil, nil, safeStoreFailure(storeErr, projection.StateRevision)
	}
	run := root
	if projection.RunKind == RunKindChild {
		run, storeErr = s.store.GetRun(ctx, projection.RunID)
		if storeErr != nil || run.Kind != RunKindChild || run.RootRunID != root.ID {
			return nil, nil, nil, nil, failure(ReasonRunNotFound, projection.StateRevision)
		}
	}
	return projection, declaration, root, run, nil
}

func projectionHasAction(projection *JourneyProjection, actionID ActionID) bool {
	if projection == nil {
		return false
	}
	for _, step := range projection.Steps {
		for _, action := range step.Actions {
			if action.ID == actionID {
				return true
			}
		}
	}
	return false
}
