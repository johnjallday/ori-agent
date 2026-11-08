package personalassistant

import (
	"strings"
	"testing"
)

func TestFolderHomeStaffingContextBindsOfferAndMaterialPlanNotDraftRefresh(t *testing.T) {
	base := homeStaffingPlanFixture()
	base.Intent.HomeStaffing = nil
	req := FolderSetupRequest{UserID: "local", Offer: homeStaffingDocumentFixture().Offers[0]}
	req.Offer.Setup = nil
	original := FolderHomeStaffingContext(req, base)
	if len(original) != 64 {
		t.Fatal("known host context unavailable")
	}
	for name, change := range map[string]func(*FolderSetupRequest){
		"owner":     func(r *FolderSetupRequest) { r.UserID = "foreign" },
		"offer":     func(r *FolderSetupRequest) { r.Offer.ID = "other" },
		"root":      func(r *FolderSetupRequest) { r.Offer.RootIdentity = "changed" },
		"subject":   func(r *FolderSetupRequest) { r.Offer.SubjectIdentity = "changed" },
		"candidate": func(r *FolderSetupRequest) { r.Offer.Subject.Key = strings.Repeat("e", 64) },
		"review": func(r *FolderSetupRequest) {
			r.Offer.ConversationReview = &FolderConversationReview{ObservationID: "other"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			copy := req
			change(&copy)
			if FolderHomeStaffingContext(copy, base) == original {
				t.Fatal("material context edit was ignored")
			}
		})
	}
	changed := homeStaffingPlanFixture()
	changed.Intent.HomeStaffing = nil
	changed.Destination.DeclarationDigest = strings.Repeat("e", 64)
	if FolderHomeStaffingContext(req, changed) == original {
		t.Fatal("provider declaration was ignored")
	}
	req.Offer.HomeStaffingReview = folderHomeReviewFixture()
	base.Lines[0].State = FolderLineWorking
	base.Visual = &FolderSetupVisual{Model: &FolderSetupModel{State: "missing"}}
	if FolderHomeStaffingContext(req, base) != original {
		t.Fatal("draft/advice/progress renewed its own context")
	}
	base.Intent.HomeStaffing = req.Offer.HomeStaffingReview.Team
	if FolderHomeStaffingContext(req, base) != "" {
		t.Fatal("selected effects were mistaken for pre-selection context")
	}
	base.Intent.HomeStaffing, base.Destination = nil, nil
	if FolderHomeStaffingContext(req, base) != "" {
		t.Fatal("unknown destination gained a review context")
	}
}
