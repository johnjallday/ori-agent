package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

type cancellingCommitAdapter struct {
	JourneyActionAdapter
	cancel        context.CancelFunc
	indeterminate bool
	execution     context.Context
}

func (a *cancellingCommitAdapter) Commit(ctx context.Context, scope ReadScope, action ActionID, input json.RawMessage, material ActionReviewMaterial) (CanonicalResult, error) {
	a.execution = ctx
	result, err := a.JourneyActionAdapter.Commit(ctx, scope, action, input, material)
	a.cancel()
	if a.indeterminate {
		return CanonicalResult{}, context.Canceled
	}
	return result, err
}

func TestReviewedInstallCancellationPreservesReceiptWithoutReexecution(t *testing.T) {
	for _, stage := range []string{"before_claim", "after_success", "indeterminate"} {
		t.Run(stage, func(t *testing.T) {
			entry, descriptor, report, _ := readyIntegrationFixture(t)
			manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
			adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
			readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
			for kind := range actionDefinitionsByKind {
				readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) {
					return CanonicalStepRead{}, nil
				})
			}
			readers[specialist.SetupStepIntegrationInstall] = adapter
			registry, err := NewReaderRegistry(readers)
			if err != nil {
				t.Fatal(err)
			}
			_, store := openTestStore(t)
			service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wrapped := &cancellingCommitAdapter{JourneyActionAdapter: adapter, cancel: cancel, indeterminate: stage == "indeterminate"}
			if err := service.SetActionAdapter(specialist.SetupStepIntegrationInstall, wrapped); err != nil {
				t.Fatal(err)
			}
			service = installQuestScope(t, service)
			journey, err := service.Read(t.Context(), "local", "")
			if err != nil {
				t.Fatal(err)
			}
			review, err := service.Mutate(t.Context(), "local", journey.RunID, ActionReviewInstall, ActionMutation{
				IfRevision: journey.StateRevision, IdempotencyKey: "review", Input: json.RawMessage(`{}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			request := ActionMutation{IfRevision: journey.StateRevision, IdempotencyKey: "install", ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`)}
			if stage == "before_claim" {
				cancel()
			}
			result, err := service.Mutate(ctx, "local", journey.RunID, ActionInstall, request)
			receipt, receiptErr := store.GetOperationReceipt(t.Context(), journey.RunKind, journey.RunID, request.IdempotencyKey)
			if stage == "before_claim" {
				if err == nil || manager.installCalls != 0 || !errors.Is(receiptErr, ErrNotFound) {
					t.Fatal("cancelled request acquired execution authority", err, receiptErr)
				}
				return
			}
			if wrapped.execution != ctx || ctx.Err() != context.Canceled || manager.installCalls != 1 || receiptErr != nil {
				t.Fatal("execution context or durable receipt changed", ctx.Err(), receiptErr)
			}
			if stage == "after_success" {
				if err != nil || receipt.Status != OperationSucceeded || result.Journey.Busy || !projectionHasAction(result.Journey, ActionReviewEnable) {
					t.Fatal("completed install remained busy after cancellation", err, receipt.Status)
				}
			} else if err == nil || receipt.Status != OperationReconcileRequired {
				t.Fatal("indeterminate effect was lost or claimed successful", err, receipt.Status)
			}
			// Exact explicit replay observes the result; it must not install or
			// enable again, even for an interrupted/indeterminate response.
			replayed, err := service.Mutate(t.Context(), "local", journey.RunID, ActionInstall, request)
			if err != nil || replayed.Journey.Busy || manager.installCalls != 1 || manager.installed[0].Enabled {
				t.Fatal("replay lost the saved result or repeated execution", err)
			}
		})
	}
}
