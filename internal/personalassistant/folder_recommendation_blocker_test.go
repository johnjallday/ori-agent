package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderRecommendationBlocker_OwnerAndStatusMatrix(t *testing.T) {
	target := foldercontext.Target{UserID: "local", WorkspaceID: "hq", AgentName: "ori", ConversationID: "albums"}
	for _, tc := range []struct {
		name, owner, kind               string
		status                          FolderOfferStatus
		invalid, arranged, blocks, link bool
	}{
		{"same conversation", "albums", "conversation", FolderOfferPending, false, false, false, true},
		{"Album-5", "album-5", "conversation", FolderOfferPending, false, false, true, true},
		{"Home", "", "home", FolderOfferPending, false, false, true, false},
		{"build", "build:workspace", "build", FolderOfferPending, false, false, true, false},
		{"abandoned confirmed build", "build:workspace", "", FolderOfferAwaitingOutcome, true, false, false, false},
		{"arranged own review", "albums", "arranged", FolderOfferPending, false, true, true, true},
		{"confirmed running chat", "album-5", "conversation", FolderOfferAwaitingOutcome, false, false, true, true},
		{"completed", "album-5", "", FolderOfferResolved, false, false, false, false},
		{"closed", "album-5", "", FolderOfferClosed, false, false, false, false},
		{"unavailable owner", "album-5", "unavailable", FolderOfferPending, true, false, true, false},
		{"unavailable confirmed owner", "album-5", "", FolderOfferAwaitingOutcome, true, false, false, false},
		{"foreign relationship", "foreign", "unavailable", FolderOfferPending, false, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &FolderObservationService{}
			guard := func(context.Context, FolderOffer) error {
				if tc.invalid {
					return errors.New("unavailable")
				}
				return nil
			}
			service.reviewGuard, service.buildGuard = guard, guard
			offer := FolderOffer{ID: "offer", Subject: FolderCandidateRecord{Name: "Album-5"}, Status: tc.status}
			if tc.owner != "" {
				owner := target
				owner.ConversationID = tc.owner
				if tc.owner == "foreign" {
					owner.AgentName = "another-assistant"
				}
				offer.ConversationReview = &FolderConversationReview{Target: owner}
			}
			if tc.arranged {
				offer.Structure = &FolderStructure{Version: FolderStructureSelectedVersion, Arrangement: &FolderStructureArrangement{RecordID: "arrangement", RecordRevision: 1}}
			}
			doc := FolderDigestDocument{Offers: []FolderOffer{offer}}
			before, _ := json.Marshal(doc)
			got := service.recommendationBlocker(t.Context(), target, doc)
			after, _ := json.Marshal(doc)
			if string(before) != string(after) {
				t.Fatal("navigation read changed the canonical offer")
			}
			if tc.kind == "" {
				if got != nil {
					t.Fatalf("unexpected blocker: %+v", got)
				}
				return
			}
			if got == nil || got.Kind != tc.kind || got.Blocks != tc.blocks || (got.ConversationID != "") != tc.link || (got.OfferID != "") != tc.link {
				t.Fatalf("blocker: %+v", got)
			}
			if tc.kind == "unavailable" && got.Subject != "Another setup" {
				t.Fatal("unverified subject leaked")
			}
		})
	}
}
