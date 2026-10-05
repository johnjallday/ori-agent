package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func reviewedObservation(t *testing.T, chip, candidateName string) (*folderDigestFixture, *FolderObservationService, foldercontext.Target, string, *fakeFolderCreator, *string) {
	t.Helper()
	f, service, source := observationFixture(t)
	creator := &fakeFolderCreator{}
	f.service.deps.Creator = creator
	active := new(string)
	service.ConfigureConversationReviews(func(_ context.Context, offer FolderOffer) error {
		if offer.DecidedAt == nil && *active != offer.ID {
			return ErrFolderSelection
		}
		return nil
	}, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	observation, err := service.Observe(context.Background(), source, "chip", chip)
	if err != nil {
		t.Fatal(err)
	}
	candidate := ""
	for _, project := range observation.Projects {
		if project.Name == candidateName {
			candidate = project.ID
		}
	}
	if candidate == "" {
		t.Fatal("candidate missing")
	}
	offer, err := service.Review(context.Background(), source, "conversation", observation.ID, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !offer.NeedsPick {
		t.Fatal("unlinked offer acquired authority")
	}
	service.BindSaved(source, observation.ID, "conversation")
	target := source
	target.ConversationID, target.DraftID = "conversation", ""
	*active = offer.ID // substituted canonical authorizer; host tests use real Messages.
	return f, service, target, offer.ID, creator, active
}

func TestFolderConversationReview_ReadCancelAndRetryDoNotCreateOrLearn(t *testing.T) {
	f, service, target, id, creator, _ := reviewedObservation(t, "desktop", "Desktop")
	ctx := context.Background()
	before, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.ReadReview(ctx, target, id)
	if err != nil || view.NeedsPick || view.ConversationID != target.ConversationID {
		t.Fatalf("view: %+v %v", view, err)
	}
	after, err := f.store.Read(ctx, "local")
	if err != nil || before.Version != after.Version {
		t.Fatal("read mutated the offer")
	}
	stored := after.Offer(id)
	replay, err := service.Review(ctx, target, target.ConversationID, stored.ConversationReview.ObservationID, stored.ConversationReview.CandidateID)
	if err != nil || replay.ID != id {
		t.Fatal("review retry duplicated an offer", err)
	}
	if err := service.CloseReview(ctx, target, id); err != nil {
		t.Fatal(err)
	}
	closed, err := f.store.Read(ctx, "local")
	if err != nil || closed.Offer(id).Status != FolderOfferClosed || len(closed.Decisions) != 0 || len(closed.Tombstones) != 0 || len(closed.DomainDeclines) != 0 || len(creator.requests) != 0 {
		t.Fatal("Keep chatting decided, learned, or created something")
	}
	foreign := target
	foreign.ConversationID = "another-conversation"
	if _, err := service.ReadReview(ctx, foreign, id); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Fatal("foreign review leaked")
	}
}

func TestFolderConversationReview_StaleExpiredChangedAndRestartedSourceRefuseCreator(t *testing.T) {
	for _, mode := range []string{"detached", "expired", "changed-child", "restart"} {
		t.Run(mode, func(t *testing.T) {
			f, service, target, id, creator, active := reviewedObservation(t, "documents", "Scans")
			live, err := service.ReadReview(context.Background(), target, id)
			if err != nil || !live.CreateAvailable || live.Capability != nil {
				t.Fatalf("fixture is not a live generic review: %+v %v", live, err)
			}
			switch mode {
			case "detached":
				*active = ""
			case "expired":
				f.now = f.now.Add(foldercontext.SelectionTTL + time.Second)
			case "changed-child":
				child := filepath.Join(f.home, "Documents", "Scans")
				if err := os.Rename(child, child+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(child, 0750); err != nil {
					t.Fatal(err)
				}
			case "restart":
				restarted := NewFolderObservationService(f.service)
				restarted.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
				service = restarted
			}
			if _, err := f.service.Decide(context.Background(), "local", id, FolderDecisionInput{Decision: FolderDecisionYes, Create: true, RequestID: "old-confirm", ReviewDigest: live.ReviewDigest}); err == nil || len(creator.requests) != 0 {
				t.Fatalf("%s created: %v", mode, err)
			}
			view, err := service.ReadReview(context.Background(), target, id)
			if err != nil || !view.NeedsPick {
				t.Fatalf("%s looked live: %+v %v", mode, view, err)
			}
		})
	}
}

func TestFolderConversationReview_ExplicitRepickMintsNewPendingID(t *testing.T) {
	f, service, target, oldID, creator, active := reviewedObservation(t, "desktop", "Desktop")
	ctx := context.Background()
	next, err := service.Observe(ctx, target, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	*active = "" // host retirement event on successful replacement
	reviewed, err := service.Review(ctx, target, target.ConversationID, next.ID, next.Projects[0].ID)
	if err != nil || reviewed.ID == oldID {
		t.Fatal("replacement reauthorized an old button", err)
	}
	*active = reviewed.ID
	if _, err := f.service.Decide(ctx, "local", oldID, FolderDecisionInput{Decision: FolderDecisionYes, Create: true, RequestID: "old"}); err == nil || len(creator.requests) != 0 {
		t.Fatal("old review ran")
	}
	if _, err := f.service.Decide(ctx, "local", reviewed.ID, FolderDecisionInput{Decision: FolderDecisionYes, Create: true, RequestID: "fresh", WorkspaceName: "Chosen name", ReviewDigest: reviewed.ReviewDigest}); err != nil {
		t.Fatal(err)
	}
	if len(creator.requests) != 1 || creator.requests[0].Name != "Chosen name" || creator.requests[0].FolderName != "Desktop" {
		t.Fatal("workspace rename altered the selected folder")
	}
}

func TestFolderConversationReview_BlueprintChangesRequireFreshConfirmation(t *testing.T) {
	f, service, target, id, creator, _ := reviewedObservation(t, "documents", "Thesis")
	ctx := context.Background()
	view, err := service.ReadReview(ctx, target, id)
	if err != nil || view.Blueprint != "" || view.Capability != nil {
		t.Fatalf("generic fixture: %+v %v", view, err)
	}
	f.service.deps.BlueprintAvailable = func(string) bool { return true }
	fresh, err := f.service.Decide(ctx, "local", id, FolderDecisionInput{Decision: FolderDecisionYes, Create: true, RequestID: "old-plan", ReviewDigest: view.ReviewDigest})
	if !errors.Is(err, ErrFolderPlanChanged) || len(creator.requests) != 0 || fresh.Blueprint != "writing-project" || fresh.ReviewDigest == view.ReviewDigest {
		t.Fatalf("plan change bypassed: %+v %v", fresh, err)
	}
	if _, err := f.service.Decide(ctx, "local", id, FolderDecisionInput{Decision: FolderDecisionYes, Create: true, RequestID: "new-plan", ReviewDigest: fresh.ReviewDigest}); err != nil {
		t.Fatal(err)
	}
	if len(creator.requests) != 1 || creator.requests[0].Blueprint != "writing-project" {
		t.Fatal("wrong reviewed blueprint")
	}
}

func TestFolderConversationReview_UndisclosedAndForeignCandidatesDoNotMintOffers(t *testing.T) {
	f, service, source := observationFixture(t)
	ctx := context.Background()
	observation, err := service.Observe(ctx, source, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	foreign := source
	foreign.DraftID = "another-draft"
	for _, request := range []struct {
		target    foldercontext.Target
		candidate string
	}{
		{source, "candidate-9999"}, {source, "../Thesis"}, {foreign, observation.Projects[0].ID},
	} {
		if _, err := service.Review(ctx, request.target, "conversation", observation.ID, request.candidate); err == nil {
			t.Fatal("forged candidate accepted")
		}
	}
	after, err := f.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version {
		t.Fatal("refusal mutated offers", err)
	}
}

func TestFolderConversationReview_OneCardRetainsPlanProviderAndOverlapGates(t *testing.T) {
	f, service, target, id, creator, active := reviewedObservation(t, "documents", "Album")
	ctx := context.Background()
	runner := &fakeFolderSetup{lines: oneCardLines()}
	f.service.deps.Setup = runner
	view, err := service.ReadReview(ctx, target, id)
	if err != nil || view.Plan == nil || view.Capability == nil {
		t.Fatalf("capability fixture: %+v %v", view, err)
	}
	input := FolderSetupInput{RequestID: "setup", PlanDigest: view.Plan.Digest}
	runner.setLines(append(oneCardLines(), FolderPlanLine{Kind: FolderPlanMode, Name: "Uses file-only mode"}))
	if _, err := f.service.StartSetup(ctx, "local", id, input); !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatal("changed plan ran", err)
	}
	runner.planErr = ErrFolderOutcomeUnavailable // host reports unavailable/revoked provider
	if _, err := f.service.StartSetup(ctx, "local", id, input); !errors.Is(err, ErrFolderOutcomeUnavailable) {
		t.Fatal("unavailable provider ran", err)
	}
	runner.planErr = nil
	runner.setLines(oneCardLines())
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, false })
	if _, err := f.service.StartSetup(ctx, "local", id, input); !errors.Is(err, ErrFolderScanBusy) {
		t.Fatal("overlapping operation ran", err)
	}
	service.ConfigureConversationReviews(func(_ context.Context, offer FolderOffer) error {
		if *active != offer.ID {
			return ErrFolderSelection
		}
		return nil
	}, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	*active = ""
	if _, err := f.service.StartSetup(ctx, "local", id, input); err == nil {
		t.Fatal("detached review ran")
	}
	if runner.runCount() != 0 || len(creator.requests) != 0 {
		t.Fatal("refusal crossed a consequence boundary")
	}
}
