package personalassistant

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

func TestFolderBuildHomeStaffingEstablishesExistingOwnerBeforeRankingAndKeepsClearAvailable(t *testing.T) {
	fx := newBuildSetupFixture(t)
	host := &folderStaffingActionHost{fakeFolderSetup: fx.setup, contextDigest: strings.Repeat("c", 64)}
	fx.folderDigestFixture.service.deps.Setup = host
	before, err := fx.store.Read(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := fx.service.BuildPlan(t.Context(), fx.draft, fx.observation.ID, fx.candidate, "My song"); err != nil {
			t.Fatal(err)
		}
	}
	after, err := fx.store.Read(t.Context(), "local")
	if err != nil || after.Version != before.Version || len(after.Offers) != len(before.Offers) {
		t.Fatal("preview minted review authority", err)
	}
	action := func(input FolderHomeStaffingInput) (FolderOfferView, error) {
		return fx.service.ReviewBuildHomeStaffing(t.Context(), fx.draft, "session-1", fx.observation.ID, fx.candidate, "My song", input)
	}
	prepared, err := action(FolderHomeStaffingInput{RequestID: "prepare", Action: "prepare_staffing"})
	if err != nil || prepared.ID == "" {
		t.Fatal("explicit preparation did not establish an owner", err)
	}
	calls := 0
	host.recommend = func(ctx context.Context, req FolderSetupRequest) (*rosterstaffing.Advice, error) {
		calls++
		stored, err := fx.folderDigestFixture.service.StoredOffer(ctx, "local", req.Offer.ID)
		if err != nil || stored.ID != prepared.ID || BuildSessionOf(stored) != "session-1" || stored.ConversationReview.Target != fx.review || stored.Setup != nil {
			t.Fatal("inference had no original canonical owner", err)
		}
		return folderHomeReviewFixture().Advice, nil
	}
	input := FolderHomeStaffingInput{RequestID: "recommend", Action: "recommend_staffing", ContextDigest: host.contextDigest}
	for range 2 {
		if _, err := action(input); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || fx.setup.runCount() != 0 {
		t.Fatal("replay inferred or started setup")
	}
	// No successful destination read/re-plan is required to drop draft advice.
	fx.folderDigestFixture.service.deps.Setup = nil
	cleared, err := action(FolderHomeStaffingInput{RequestID: "clear", Action: "clear_staffing", Revision: 1})
	if err != nil || cleared.ID != prepared.ID || cleared.HomeStaffing == nil || cleared.HomeStaffing.Revision != 2 {
		t.Fatal("unavailable provider blocked clear or lost its public revision", err)
	}
	stored, err := fx.folderDigestFixture.service.StoredOffer(t.Context(), "local", prepared.ID)
	if err != nil || stored.HomeStaffingReview.Advice != nil || stored.HomeStaffingReview.Team != nil || stored.Setup != nil || fx.setup.runCount() != 0 {
		t.Fatal("clear renewed advice or executed setup", err)
	}
}
