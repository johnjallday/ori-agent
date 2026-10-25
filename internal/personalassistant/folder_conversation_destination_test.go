package personalassistant

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderConversationDestination_PinnedReadAndExplicitRefresh(t *testing.T) {
	f, service, source := observationFixture(t)
	setup := &fakeFolderSetup{lines: oneCardLines(), destination: &FolderSetupDestination{Status: "existing", WorkspaceID: "home-a", Name: "Music Home", Kind: "home", OwnerUserID: "local", RecordVersion: 4}}
	f.service.deps.Setup = setup
	f.service.deps.Creator = &fakeFolderCreator{}
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	markNotInbox(t, f, "Desktop")
	observed, err := service.Observe(context.Background(), source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	offer, err := service.Review(context.Background(), source, "conversation", observed.ID, observed.Projects[0].ID)
	if err != nil || offer.Destination == nil || offer.Destination.Name != "Music Home" {
		t.Fatal(offer, err)
	}
	service.BindSaved(source, observed.ID, "conversation")
	target := source
	target.ConversationID, target.DraftID = "conversation", ""
	before, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	// A new current read cannot update the canonical review's destination.
	setup.destination = &FolderSetupDestination{Status: "existing", WorkspaceID: "same-name-home-b", Name: "Music Home", Kind: "home", OwnerUserID: "local", RecordVersion: 4}
	changed, err := service.ReadReview(context.Background(), target, offer.ID)
	if err != nil || changed.DestinationStatus != "changed" || changed.Destination.WorkspaceID != "home-a" || changed.Plan != nil {
		t.Fatal(changed, err)
	}
	if release, err := f.service.acquireConversationReview(context.Background(), *before.Offer(offer.ID)); !errors.Is(err, ErrFolderPlanChanged) {
		if release != nil {
			release()
		}
		t.Fatal("changed destination could authorize old review", err)
	}
	after, err := f.store.Read(context.Background(), "local")
	if err != nil || before.Version != after.Version || after.Offer(offer.ID).ConversationReview.Destination.WorkspaceID != "home-a" {
		t.Fatal("read mutated pinned review", err)
	}
	// Only an explicit Review refreshes the disclosed destination and digest.
	refreshed, err := service.Review(context.Background(), target, "conversation", observed.ID, observed.Projects[0].ID)
	if err != nil || refreshed.ID != offer.ID || refreshed.ReviewDigest == offer.ReviewDigest || refreshed.Destination.WorkspaceID != "same-name-home-b" || refreshed.DestinationStatus != "" {
		t.Fatal("explicit review did not refresh material consent", refreshed, err)
	}
	setup.destinationErr = errors.New("private reader failure")
	unavailable, err := service.ReadReview(context.Background(), target, offer.ID)
	if err != nil || unavailable.DestinationStatus != "unavailable" || unavailable.Destination.WorkspaceID != "same-name-home-b" {
		t.Fatal("failed read adopted another destination", unavailable, err)
	}
}

func TestFolderConversationDestination_StaleLibraryConfirmationKeepsCanonicalCard(t *testing.T) {
	f, observations, source, observation := albumsObservation(t)
	setup := &fakeFolderSetup{lines: portfolioSetupLines(), destination: &FolderSetupDestination{Status: "existing", WorkspaceID: "music-home", Name: "Music Home", Kind: "home", OwnerUserID: "local", RecordVersion: 4}}
	f.service.deps.Setup = setup
	observations.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	ctx := WithFolderStructureAdjustment(t.Context(), &FolderStructureAdjustment{GroupType: FolderGroupLibrary})
	offer, err := observations.Review(ctx, source, "library", observation.ID, folderRootCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	observations.BindSaved(source, observation.ID, "library")
	target := source
	target.ConversationID, target.DraftID = "library", ""
	live, err := observations.ReadReview(t.Context(), target, offer.ID)
	if err != nil || live == nil || live.Plan == nil {
		t.Fatalf("live plan: %+v %v", live, err)
	}
	before, _ := f.store.Read(t.Context(), "local")
	setup.destination.Name = "Renamed Music Home"
	refused, err := f.service.StartSetup(t.Context(), "local", offer.ID, FolderSetupInput{RequestID: "stale-library", PlanDigest: live.Plan.Digest})
	if !errors.Is(err, ErrFolderPlanChanged) || refused.ID != offer.ID || refused.Status != FolderOfferPending || refused.DestinationStatus != "changed" || refused.Plan != nil || setup.runCount() != 0 {
		t.Fatalf("stale confirmation lost its card or ran: %+v %v", refused, err)
	}
	after, _ := f.store.Read(t.Context(), "local")
	if before.Version != after.Version || after.Offer(offer.ID).Setup != nil {
		t.Fatal("stale confirmation produced effects")
	}
}
