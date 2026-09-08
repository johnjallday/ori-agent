package setupjourney

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

func TestProjectReviewFailureUsesBoundedInputAndStaleReasons(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason ReasonCode
	}{
		{projectconnection.ErrInvalid, ReasonInputInvalid},
		{projectconnection.ErrChanged, ReasonReviewStale},
		{projectconnection.ErrUnavailable, ReasonOwnerUnavailable},
	} {
		if got := adapterFailure(projectConnectionFailure(tc.err), 1); got.ReasonCode != tc.reason {
			t.Fatalf("reason = %s, want %s", got.ReasonCode, tc.reason)
		}
	}
}

func TestReadSettlesOnlyUncertainProjectWithObservedCanonicalConsequence(t *testing.T) {
	ctx := context.Background()
	reads := defaultCanonicalReads()
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{Complete: true, Result: CanonicalResult{IntegrationPluginID: "neutral", IntegrationVersion: "1.0.0"}}
	service, store := serviceFixture(t, reads)
	// Only the predicate is used. A read must never call this adapter's owner.
	if err := service.SetActionAdapter(specialist.SetupStepProjectConnect, &ProjectConnectionAdapter{}); err != nil {
		t.Fatal(err)
	}
	initial, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	claim := OperationClaim{RunKind: initial.RunKind, RunID: initial.RunID, IfRevision: initial.StateRevision,
		IdempotencyKey: "interrupted-project", StepID: initial.CurrentStepID, ActionID: string(ActionCreateNewProject), InputDigest: Digest([]byte("reviewed"))}
	if _, _, _, err := store.ClaimOperation(ctx, claim); err != nil {
		t.Fatal(err)
	}
	observed := CanonicalStepRead{Complete: true, Result: CanonicalResult{HomeWorkspaceID: "home-1", ProjectWorkspaceID: "project-1"}}
	reads[specialist.SetupStepProjectConnect] = observed
	active, err := service.Read(ctx, "local", initial.RunID)
	if err != nil || !active.Busy {
		t.Fatalf("active claim settled: %+v, %v", active, err)
	}
	if _, err := store.MarkOperationReconcileRequired(ctx, claim.RunKind, claim.RunID, claim.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	reads[specialist.SetupStepProjectConnect] = CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}
	missing, err := service.Read(ctx, "local", initial.RunID)
	if err != nil || !missing.Busy || !missing.ReconciliationRequired {
		t.Fatalf("unobserved consequence settled: %+v, %v", missing, err)
	}
	reads[specialist.SetupStepProjectConnect] = observed
	settled, err := service.Read(ctx, "local", initial.RunID)
	if err != nil || settled.Busy || settled.ReconciliationRequired || settled.Receipts.ProjectWorkspaceID != "project-1" {
		t.Fatalf("observed consequence not settled: %+v, %v", settled, err)
	}
	receipt, err := store.GetOperationReceipt(ctx, claim.RunKind, claim.RunID, claim.IdempotencyKey)
	if err != nil || receipt.Status != OperationSucceeded || receipt.ResultCode != ResultAlreadyCurrent {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	again, err := service.Read(ctx, "local", initial.RunID)
	if err != nil || again.StateRevision != settled.StateRevision || again.Busy {
		t.Fatalf("repeated read: %+v, %v", again, err)
	}
}
