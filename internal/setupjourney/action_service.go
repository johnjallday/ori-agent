package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

const actionReviewTTL = 15 * time.Minute

// Mutate validates the generic action envelope and dispatches only to the one
// compiled adapter registered for the action's fixed step kind.
func (s *Service) Mutate(ctx context.Context, userID, runID string, actionID ActionID, request ActionMutation) (*ActionResult, error) {
	actionID, known := NormalizeActionID(string(actionID))
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.ReviewToken = strings.TrimSpace(request.ReviewToken)
	if !known || request.IfRevision <= 0 || request.IdempotencyKey == "" ||
		len(request.IdempotencyKey) > MaxIdempotencyKeyBytes || len(request.Input) > MaxActionInputBytes ||
		(len(request.Input) > 0 && !json.Valid(request.Input)) {
		return nil, failure(ReasonInputInvalid, 0)
	}
	kind, definition, ok := actionKindAndDefinition(actionID)
	if !ok {
		return nil, failure(ReasonActionUnavailable, 0)
	}
	adapter := s.actionAdapters[kind]
	if adapter == nil || definition.Effect == ActionEffectNavigation {
		return nil, failure(ReasonActionUnavailable, 0)
	}
	inputDigest, err := adapter.InputDigest(actionID, request.Input)
	if err != nil {
		return nil, failure(ReasonInputInvalid, 0)
	}

	projection, err := s.Read(ctx, userID, runID)
	if err != nil {
		return nil, err
	}
	scope, scopeErr := s.authorizedActionScope(ctx, userID, projection.RunID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	stepID := stepIDForKind(projection, kind)
	if stepID == "" {
		return nil, failure(ReasonActionUnavailable, projection.StateRevision)
	}
	if definition.Effect == ActionEffectCommit {
		if replay, replayErr := s.store.GetOperationReceipt(ctx, projection.RunKind, projection.RunID, request.IdempotencyKey); replayErr == nil {
			if replay.StepID != stepID || replay.ActionID != string(actionID) ||
				replay.InputDigest != inputDigest || replay.RunRevisionBefore != request.IfRevision {
				return nil, failure(ReasonIdempotencyConflict, projection.StateRevision)
			}
			return s.replayAction(ctx, userID, projection, scope, kind, actionID, adapter, replay)
		} else if !errors.Is(replayErr, ErrNotFound) {
			return nil, safeActionStoreFailure(replayErr, projection.StateRevision)
		}
	}
	if projection.StateRevision != request.IfRevision {
		return nil, failure(ReasonRevisionConflict, projection.StateRevision)
	}

	if definition.Effect == ActionEffectReview {
		if request.ReviewToken != "" || !projectionHasAction(projection, actionID) {
			return nil, failure(ReasonActionUnavailable, projection.StateRevision)
		}
		material, reviewErr := adapter.Review(ctx, scope, actionID, request.Input)
		if reviewErr != nil || material.InputDigest != inputDigest ||
			material.CommitAction == "" || !validateDigest(material.OwnerRevisionDigest, false) ||
			!validateDigest(material.DisclosureDigest, false) ||
			!validIntegrationProjection(material.Integration) ||
			!validProjectConnectionProjection(material.ProjectConnection) ||
			(material.Integration != nil && kind != specialist.SetupStepIntegrationInstall) ||
			(material.ProjectConnection != nil && kind != specialist.SetupStepProjectConnect) ||
			!validWorkspaceSetupProjection(material.WorkspaceSetup) ||
			(material.WorkspaceSetup != nil && kind != specialist.SetupStepWorkspaceSetup) ||
			!validStaffingProjection(material.Staffing) ||
			(material.Staffing != nil && kind != specialist.SetupStepAssistantProgramStaffing) {
			return nil, adapterFailure(reviewErr, projection.StateRevision)
		}
		commitKind, commitDefinition, exists := actionKindAndDefinition(material.CommitAction)
		if !exists || commitKind != kind || commitDefinition.Effect != ActionEffectCommit || !commitDefinition.RequiresReview {
			return nil, failure(ReasonActionUnavailable, projection.StateRevision)
		}
		receipt, _, storeErr := s.store.CreateOrGetReviewReceipt(ctx, ReviewReceiptSpec{
			RunKind: projection.RunKind, RunID: projection.RunID,
			IdempotencyKey: request.IdempotencyKey, StepID: stepID,
			ActionID: string(material.CommitAction), InputDigest: material.InputDigest,
			RunRevision:         projection.StateRevision,
			OwnerRevisionDigest: material.OwnerRevisionDigest,
			DisclosureDigest:    material.DisclosureDigest, TTL: actionReviewTTL,
		})
		if storeErr != nil {
			return nil, safeActionStoreFailure(storeErr, projection.StateRevision)
		}
		return &ActionResult{Journey: projection, Review: &ReviewProjection{
			Token: receipt.Token, CommitAction: material.CommitAction,
			ExpiresAt: receipt.ExpiresAt, Integration: cloneIntegrationProjection(material.Integration),
			ProjectConnection: cloneProjectConnectionProjection(material.ProjectConnection),
			WorkspaceSetup:    cloneWorkspaceSetupProjection(material.WorkspaceSetup),
			Staffing:          cloneStaffingProjection(material.Staffing),
		}}, nil
	}

	if !definition.RequiresReview || request.ReviewToken == "" {
		return nil, failure(ReasonReviewRequired, projection.StateRevision)
	}
	material, prepareErr := adapter.PrepareCommit(ctx, scope, actionID, request.Input)
	if prepareErr != nil || material.CommitAction != actionID || material.InputDigest != inputDigest {
		return nil, adapterFailure(prepareErr, projection.StateRevision)
	}
	review, reviewErr := s.store.GetReviewReceipt(ctx, request.ReviewToken)
	if reviewErr != nil || review.RunKind != projection.RunKind || review.RunID != projection.RunID ||
		review.StepID != stepID || review.ActionID != string(actionID) || review.InputDigest != inputDigest ||
		review.RunRevision != projection.StateRevision || review.OwnerRevisionDigest != material.OwnerRevisionDigest ||
		review.DisclosureDigest != material.DisclosureDigest || review.ConsumedAt != nil ||
		!review.ExpiresAt.After(s.now().UTC()) {
		return nil, failure(ReasonReviewStale, projection.StateRevision)
	}

	claim, claimedRun, replayed, claimErr := s.store.ClaimOperation(ctx, OperationClaim{
		RunKind: projection.RunKind, RunID: projection.RunID, IfRevision: projection.StateRevision,
		IdempotencyKey: request.IdempotencyKey, StepID: stepID, ActionID: string(actionID),
		InputDigest: inputDigest, ReviewToken: request.ReviewToken, ReviewDigest: material.DisclosureDigest,
	})
	if claimErr != nil {
		return nil, safeActionStoreFailure(claimErr, projection.StateRevision)
	}
	if replayed {
		return s.replayAction(ctx, userID, projection, scope, kind, actionID, adapter, claim)
	}

	result, commitErr := adapter.Commit(ctx, scope, actionID, request.Input, material)
	if commitErr != nil {
		_, _ = s.store.MarkOperationReconcileRequired(ctx, projection.RunKind, projection.RunID, request.IdempotencyKey)
		return nil, failure(ReasonOperationFailed, claimedRun.StateRevision)
	}
	if result.HomeWorkspaceID != "" {
		scope.HomeWorkspaceID = result.HomeWorkspaceID
	}
	if result.ProjectWorkspaceID != "" {
		scope.ProjectWorkspaceID = result.ProjectWorkspaceID
	}
	if result.SelectedModeID != "" {
		scope.SelectedModeID = result.SelectedModeID
	}
	ownerRead := s.readers.read(ctx, kind, scope)
	if !validCanonicalRead(kind, ownerRead) {
		_, _ = s.store.MarkOperationReconcileRequired(ctx, projection.RunKind, projection.RunID, request.IdempotencyKey)
		return nil, failure(ReasonOwnerUnavailable, claimedRun.StateRevision)
	}
	finalRun, finalRunErr := s.actionFinalRun(ctx, userID, projection.RunID)
	if finalRunErr != nil {
		_, _ = s.store.MarkOperationReconcileRequired(ctx, projection.RunKind, projection.RunID, request.IdempotencyKey)
		return nil, failure(ReasonOwnerUnavailable, claimedRun.StateRevision)
	}
	if !adapter.ConsequenceObserved(actionID, ownerRead) {
		_, _, _, finalizeErr := s.store.FinalizeOperation(ctx, finalRun, request.IdempotencyKey, OperationCompletion{
			Status: OperationFailed, ResultCode: ResultNotApplied, ReasonCode: ReasonOperationFailed,
		})
		if finalizeErr != nil {
			return nil, safeActionStoreFailure(finalizeErr, claimedRun.StateRevision)
		}
		return nil, failure(ReasonOperationFailed, claimedRun.StateRevision)
	}
	if normalized, _, normalizeErr := normalizeCanonicalResult(result); normalizeErr == nil {
		result = normalized
	} else {
		result = ownerRead.Result
	}
	_, _, _, finalizeErr := s.store.FinalizeOperation(ctx, finalRun, request.IdempotencyKey, OperationCompletion{
		Status: OperationSucceeded, ResultCode: ResultApplied, Result: result,
	})
	if finalizeErr != nil {
		return nil, safeActionStoreFailure(finalizeErr, claimedRun.StateRevision)
	}
	fresh, readErr := s.Read(ctx, userID, projection.RunID)
	if readErr != nil {
		return nil, readErr
	}
	return &ActionResult{Journey: fresh}, nil
}

func (s *Service) replayAction(ctx context.Context, userID string, projection *JourneyProjection, scope ReadScope, kind specialist.SetupStepKind, actionID ActionID, adapter JourneyActionAdapter, receipt *OperationReceipt) (*ActionResult, error) {
	switch receipt.Status {
	case OperationSucceeded:
		fresh, err := s.Read(ctx, userID, projection.RunID)
		if err != nil {
			return nil, err
		}
		return &ActionResult{Journey: fresh}, nil
	case OperationFailed:
		return nil, failure(receipt.ReasonCode, projection.StateRevision)
	case OperationClaimed, OperationReconcileRequired:
		ownerRead := s.readers.read(ctx, kind, scope)
		if !validCanonicalRead(kind, ownerRead) {
			if receipt.Status == OperationClaimed {
				_, _ = s.store.MarkOperationReconcileRequired(ctx, receipt.RunKind, receipt.RunID, receipt.IdempotencyKey)
			}
			return nil, failure(ReasonOwnerUnavailable, projection.StateRevision)
		}
		if !adapter.ConsequenceObserved(actionID, ownerRead) {
			if receipt.Status == OperationClaimed {
				_, _ = s.store.MarkOperationReconcileRequired(ctx, receipt.RunKind, receipt.RunID, receipt.IdempotencyKey)
			}
			return nil, failure(ReasonOperationFailed, projection.StateRevision)
		}
		finalRun, finalRunErr := s.actionFinalRun(ctx, userID, projection.RunID)
		if finalRunErr != nil {
			return nil, failure(ReasonOwnerUnavailable, projection.StateRevision)
		}
		_, _, _, finalizeErr := s.store.FinalizeOperation(ctx, finalRun, receipt.IdempotencyKey, OperationCompletion{
			Status: OperationSucceeded, ResultCode: ResultAlreadyCurrent, Result: ownerRead.Result,
		})
		if finalizeErr != nil {
			return nil, safeActionStoreFailure(finalizeErr, projection.StateRevision)
		}
		fresh, readErr := s.Read(ctx, userID, projection.RunID)
		if readErr != nil {
			return nil, readErr
		}
		return &ActionResult{Journey: fresh}, nil
	default:
		return nil, failure(ReasonOperationFailed, projection.StateRevision)
	}
}

func (s *Service) actionFinalRun(ctx context.Context, userID, runID string) (*Run, error) {
	_, declaration, err := s.currentDeclaration(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	run, storeErr := s.store.GetRun(ctx, runID)
	if storeErr != nil {
		return nil, storeErr
	}
	root := run
	if run.Kind == RunKindChild {
		root, storeErr = s.store.GetRun(ctx, run.RootRunID)
		if storeErr != nil {
			return nil, storeErr
		}
	}
	candidate, _ := s.deriveCanonical(ctx, declaration, root, run, nil)
	return candidate, nil
}

func (s *Service) authorizedActionScope(ctx context.Context, userID, runID string) (ReadScope, error) {
	userID = strings.TrimSpace(userID)
	relationship, declaration, err := s.currentDeclaration(ctx, userID)
	if err != nil {
		return ReadScope{}, err
	}
	run, storeErr := s.store.GetRun(ctx, runID)
	if storeErr != nil || run.OwnerUserID != userID || run.RelationshipID != relationship.AssistantID ||
		run.SpecialistSlug != relationship.SpecialistSlug || run.JourneyID != declaration.ID {
		return ReadScope{}, failure(ReasonRunNotFound, 0)
	}
	root := run
	if run.Kind == RunKindChild {
		root, storeErr = s.store.GetRun(ctx, run.RootRunID)
		if storeErr != nil || root.Kind != RunKindRoot || root.OwnerUserID != userID ||
			root.RelationshipID != relationship.AssistantID || root.SpecialistSlug != relationship.SpecialistSlug {
			return ReadScope{}, failure(ReasonRunNotFound, run.StateRevision)
		}
	}
	return scopeForRun(declaration, root, run), nil
}

func actionKindAndDefinition(action ActionID) (specialist.SetupStepKind, ActionDefinition, bool) {
	for kind, definitions := range actionDefinitionsByKind {
		for _, definition := range definitions {
			if definition.ID == action {
				return kind, definition, true
			}
		}
	}
	return "", ActionDefinition{}, false
}

func stepIDForKind(projection *JourneyProjection, kind specialist.SetupStepKind) string {
	for _, step := range projection.Steps {
		if step.Kind == kind {
			return step.ID
		}
	}
	return ""
}

func adapterFailure(err error, revision int64) *Failure {
	if errors.Is(err, ErrInvalid) {
		return failure(ReasonInputInvalid, revision)
	}
	if errors.Is(err, ErrConflict) {
		return failure(ReasonReviewStale, revision)
	}
	return failure(ReasonOwnerUnavailable, revision)
}

func safeActionStoreFailure(err error, revision int64) *Failure {
	switch {
	case errors.Is(err, ErrConflict):
		return failure(ReasonRevisionConflict, revision)
	case errors.Is(err, ErrIdempotencyConflict):
		return failure(ReasonIdempotencyConflict, revision)
	case errors.Is(err, ErrOperationBusy):
		return failure(ReasonOperationFailed, revision)
	case errors.Is(err, ErrInvalid):
		return failure(ReasonInputInvalid, revision)
	default:
		return failure(ReasonOperationFailed, revision)
	}
}
