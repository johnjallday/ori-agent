package personalassistant

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFolderReviewPresentation_UsesFactsNotNamesOrCounts(t *testing.T) {
	for _, tc := range []struct {
		name                string
		offer               FolderOffer
		view                FolderOfferView
		kind, effect, state string
	}{
		{"blank", FolderOffer{}, FolderOfferView{CreateAvailable: true}, "workspace", "workspace", "proposed"},
		{"blueprint provenance unknown", FolderOffer{}, FolderOfferView{CreateAvailable: true, BlueprintLabel: "Music Home"}, "workspace", "workspace", "proposed"},
		{"unavailable", FolderOffer{}, FolderOfferView{}, "unknown", "unknown", "unavailable"},
		{"portfolio without typed intent", FolderOffer{Portfolio: &FolderPortfolioEvidence{Projects: 5}}, FolderOfferView{CreateAvailable: true, Plan: &FolderSetupPlan{}}, "unknown", "unknown", "proposed"},
		{"new home library", FolderOffer{Portfolio: &FolderPortfolioEvidence{Projects: 5}}, FolderOfferView{Plan: &FolderSetupPlan{Intent: FolderSetupIntent{Portfolio: true, CreatesHome: true}}}, "group", "home_library", "proposed"},
		{"existing library", FolderOffer{Portfolio: &FolderPortfolioEvidence{Projects: 5, ExistingHome: true}}, FolderOfferView{Plan: &FolderSetupPlan{Intent: FolderSetupIntent{Portfolio: true}}}, "group", "library", "proposed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.offer.Subject.Name = "District of five supporting workspaces"
			got := folderReviewPresentation(tc.offer, tc.view)
			if got.Version != 1 || got.Kind != tc.kind || got.Effect != tc.effect || got.State != tc.state || got.DestinationState != "unknown" {
				t.Fatalf("invented effects/placement: %+v", got)
			}
			data, _ := json.Marshal(got)
			for _, denied := range []string{"projects", "count", "workspace_id", "provider", "plan_digest", "District of"} {
				if strings.Contains(string(data), denied) {
					t.Fatalf("private or speculative display: %s", data)
				}
			}
		})
	}
}

func TestFolderReviewPresentation_VerifiedBoundedDestinationOnly(t *testing.T) {
	dest := &FolderSetupDestination{Status: "existing", WorkspaceID: "secret-id", OwnerUserID: "secret-owner", Name: strings.Repeat("界", 130), Kind: "music-home"}
	offer := FolderOffer{ConversationReview: &FolderConversationReview{Destination: dest}}
	got := folderReviewPresentation(offer, FolderOfferView{CreateAvailable: true})
	if got.DestinationState != "existing" || len([]rune(got.DestinationName)) > 96 || !strings.HasSuffix(got.DestinationName, "…") {
		t.Fatal(got)
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "music-home") {
		t.Fatal(string(data))
	}
	dest.Name = "/private/source"
	if got := folderReviewPresentation(offer, FolderOfferView{CreateAvailable: true}); got.DestinationState != "unknown" {
		t.Fatal(got)
	}
	offer.ConversationReview.DestinationUnavailable = true
	if got := folderReviewPresentation(offer, FolderOfferView{CreateAvailable: true}); got.State != "unavailable" || got.DestinationState != "unknown" {
		t.Fatal(got)
	}
}
