package personalassistant

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// A reader that says why the destination is unavailable, by key, has that
// reason carried into the preview, the stored review and its re-read; a
// reader that only fails names nothing. The error's own text never travels.
func TestFolderConversationDestination_BlockedReaderNamesTheIntegrationByKey(t *testing.T) {
	f, service, source := observationFixture(t)
	setup := &fakeFolderSetup{destinationErr: &FolderSetupBlockedError{
		Blocker: FolderDestinationBlocker{IntegrationKey: "ori_loom", HomeProviderKey: "loom_homes"},
		Err:     errors.New("one-card folder setup is unavailable"),
	}}
	f.service.deps.Setup = setup
	f.service.deps.Creator = &fakeFolderCreator{}
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	observed, err := service.Observe(context.Background(), source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	candidate := observed.Projects[0].ID
	preview, err := service.PreviewReview(context.Background(), source, observed.ID, candidate)
	if err != nil || preview.DestinationStatus != "unavailable" || preview.Destination != nil {
		t.Fatal("blocked destination previewed as something else", preview, err)
	}
	want := FolderDestinationBlocker{IntegrationKey: "ori_loom", HomeProviderKey: "loom_homes"}
	if preview.DestinationBlocker == nil || *preview.DestinationBlocker != want {
		t.Fatal("preview lost the blocker", preview.DestinationBlocker)
	}
	offer, err := service.Review(context.Background(), source, "conversation", observed.ID, candidate)
	if err != nil || offer.DestinationStatus != "unavailable" || offer.DestinationBlocker == nil || *offer.DestinationBlocker != want {
		t.Fatal("review did not carry the blocker", offer, err)
	}
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil || doc.Offer(offer.ID).ConversationReview.DestinationBlocker == nil || *doc.Offer(offer.ID).ConversationReview.DestinationBlocker != want {
		t.Fatal("stored review lost the blocker", err)
	}
	target := source
	target.ConversationID, target.DraftID = "conversation", ""
	reread, err := service.ReadReview(context.Background(), target, offer.ID)
	if err != nil || reread.DestinationStatus != "unavailable" || reread.DestinationBlocker == nil || *reread.DestinationBlocker != want {
		t.Fatal("re-read lost the blocker", reread, err)
	}
	// Ownership/selection is checked before disclosing destination failure.
	if _, err := f.service.acquireConversationReview(context.Background(), *doc.Offer(offer.ID)); !errors.Is(err, ErrFolderSelection) {
		t.Fatal("unbound review acquired authority", err)
	}
	service.BindSaved(source, observed.ID, "conversation")
	// The stored review is not a grant: it still cannot authorize a setup.
	if _, err := f.service.acquireConversationReview(context.Background(), *doc.Offer(offer.ID)); !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatal("blocked review could authorize setup", err)
	}
	// A plain failure, or a blocked error with no keys, names nothing.
	for _, failure := range []error{errors.New("private reader failure"), &FolderSetupBlockedError{Err: errors.New("private reader failure")}} {
		setup.destinationErr = failure
		plain, err := service.PreviewReview(context.Background(), target, observed.ID, candidate)
		if err != nil || plain.DestinationStatus != "unavailable" || plain.DestinationBlocker != nil {
			t.Fatal("failure without a reason named one", plain, err)
		}
	}
}

func TestFolderConversationReview_BlockerIsBoundedOnLoad(t *testing.T) {
	review := &FolderConversationReview{
		Target:        foldercontext.Target{UserID: "local", WorkspaceID: "hq", AgentName: "ori", ConversationID: "conversation"},
		ObservationID: "observed", CandidateID: "candidate-0", CandidateIdentity: "identity",
		DestinationBlocker: &FolderDestinationBlocker{IntegrationKey: "ori_loom"},
	}
	if err := validateConversationReview(review); err != nil {
		t.Fatal("keyed blocker refused", err)
	}
	for _, bad := range []*FolderDestinationBlocker{{}, {IntegrationKey: "../escape"}, {HomeProviderKey: "a\nb"}} {
		review.DestinationBlocker = bad
		if err := validateConversationReview(review); err == nil {
			t.Fatal("malformed blocker accepted", bad)
		}
	}
}
