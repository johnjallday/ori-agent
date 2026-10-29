package personalassistant

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func homePlacementWitness(offer FolderOffer) FolderSetupHomeOperation {
	return FolderSetupHomeOperation{Version: 1, OfferID: offer.ID, RequestID: offer.RequestID, PlanDigest: offer.Setup.PlanDigest,
		GroupTemplateID: "plugin-home:music-project-management:music-producer-assistant", Revision: strings.Repeat("a", 64),
		Name: "Music Production Home", ReviewToken: "review-token", IdempotencyKey: "original-home-operation"}
}

func TestHomePlacement_WitnessIsBoundedImmutableAndSurvivesContinuation(t *testing.T) {
	f, _ := handoffCardFixture(t)
	f.setup.hold = make(chan struct{})
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopHandoffReady, HomeID: "home-1"}
	}
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "accept", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return f.setup.runCount() == 1 })
	witness := homePlacementWitness(f.stored(t))
	if err := f.service.RecordSetupHomeOperation(ctx, "local", witness); err != nil {
		t.Fatal(err)
	}
	if err := f.service.RecordSetupHomeOperation(ctx, "local", witness); err != nil {
		t.Fatal("exact replay", err)
	}
	for _, mutate := range []func(*FolderSetupHomeOperation){
		func(w *FolderSetupHomeOperation) { w.Version = 2 },
		func(w *FolderSetupHomeOperation) { w.OfferID = "foreign" },
		func(w *FolderSetupHomeOperation) { w.PlanDigest = strings.Repeat("b", 64) },
		func(w *FolderSetupHomeOperation) { w.RequestID = "different-accept" },
		func(w *FolderSetupHomeOperation) { w.IdempotencyKey = "replacement" },
		func(w *FolderSetupHomeOperation) { w.ReviewToken = strings.Repeat("x", 201) },
	} {
		bad := witness
		mutate(&bad)
		if err := f.service.RecordSetupHomeOperation(ctx, "local", bad); err == nil {
			t.Fatal("accepted changed witness", bad)
		}
	}
	close(f.setup.hold)
	waitFor(t, "Home-ready", func() bool { return !f.service.isRunning(f.offer.ID) })
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "continue", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "continued", func() bool { return f.setup.runCount() == 2 && !f.service.isRunning(f.offer.ID) })
	stored := f.stored(t)
	if stored.Setup.HomeOperation == nil || *stored.Setup.HomeOperation != witness {
		t.Fatal("continuation lost witness")
	}
	encoded, err := json.Marshal(stored.Setup)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FolderSetupRun
	if err := json.Unmarshal(encoded, &decoded); err != nil || validateFolderSetupRun(decoded) != nil || !reflect.DeepEqual(*stored.Setup, decoded) {
		t.Fatal("witness round trip", err)
	}
	decoded.HomeOperation.Version = 2
	if validateFolderSetupRun(decoded) == nil {
		t.Fatal("future proof version accepted")
	}
	decoded.HomeOperation = nil
	if err := validateFolderSetupRun(decoded); err != nil {
		t.Fatal("old record must remain readable", err)
	}
}

type homePlacementObserver struct {
	*fakeFolderSetup
	result FolderHomeResult
}

func (o homePlacementObserver) ObserveSetupHome(context.Context, FolderSetupRequest) FolderHomeResult {
	return o.result
}

func TestHomePlacement_PhaseProjectionDoesNotExecuteAndLeaksNoLookup(t *testing.T) {
	f, _ := handoffCardFixture(t)
	for _, tc := range []struct {
		status, reason      string
		offerStatus         FolderOfferStatus
		homeID, proof, want string
		created             bool
	}{
		{FolderSetupStopped, FolderStopHandoffReady, FolderOfferAwaitingOutcome, "home-1", "verified", "ready", true},
		{FolderSetupDone, "", FolderOfferResolved, "home-1", "verified", "ready", true},
		{FolderSetupStopped, FolderStopHandoffReady, FolderOfferAwaitingOutcome, "home-1", "verified", "ready", false},
		{FolderSetupStopped, FolderStopFailed, FolderOfferAwaitingOutcome, "home-1", "verified", "partial", true},
		{FolderSetupStopped, FolderStopInterrupted, FolderOfferAwaitingOutcome, "", "verified", "partial", true},
		{FolderSetupStopped, FolderStopHandoffReady, FolderOfferAwaitingOutcome, "home-1", "unproven", "unproven", false},
		{FolderSetupStopped, FolderStopHandoffReady, FolderOfferAwaitingOutcome, "foreign", "unavailable", "unavailable", false},
	} {
		t.Run(tc.want+"-"+tc.reason+"-"+tc.homeID, func(t *testing.T) {
			f.service.deps.Setup = homePlacementObserver{f.setup, FolderHomeResult{State: tc.proof, HomeID: "home-1", Created: tc.created}}
			offer := FolderOffer{ID: "offer", RequestID: "accept", Decision: FolderDecisionYes, Status: tc.offerStatus,
				Setup: &FolderSetupRun{PlanDigest: strings.Repeat("d", 64), Status: tc.status, StopReason: tc.reason, HomeID: tc.homeID, Intent: FolderSetupIntent{Portfolio: true}}}
			var view FolderOfferView
			f.service.attachHomeResult(t.Context(), "local", offer, &view)
			got := view.HomeResult
			if got == nil || got.State != tc.want || got.LayoutSubjectNew != (tc.want == "ready" && tc.created) || got.OfferID != offer.ID || got.PlanDigest != offer.Setup.PlanDigest || got.RequestID != offer.RequestID {
				t.Fatalf("projection = %+v", got)
			}
			data, _ := json.Marshal(view)
			if strings.Contains(string(data), "review_token") || strings.Contains(string(data), "idempotency_key") || f.setup.runCount() != 0 {
				t.Fatal("observation executed or leaked authority")
			}
		})
	}
}
