package personalassistant

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderConversationDestination_PinnedReadAndExplicitRefresh(t *testing.T) {
	f, service, source := observationFixture(t)
	setup := &fakeFolderSetup{destination: &FolderSetupDestination{Status: "existing", WorkspaceID: "home-a", Name: "Music Home", Kind: "home", OwnerUserID: "local", RecordVersion: 4}}
	f.service.deps.Setup = setup
	f.service.deps.Creator = &fakeFolderCreator{}
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
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
