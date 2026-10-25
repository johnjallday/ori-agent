package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestFolderHireCandidate_ExactPendingOwnerAndReadOnlyExceptions(t *testing.T) {
	for _, mode := range []string{"pending beside stopped", "stopped only", "identical names", "foreign", "missing", "own conversation", "same project Home", "other Home"} {
		t.Run(mode, func(t *testing.T) {
			fx := newBuildSetupFixture(t)
			target := fx.draft
			target.DraftID, target.ConversationID = "", "attached"
			observation := observeSelected(t, fx.folderDigestFixture, fx.service, target, fx.song)
			held := fx.service.selections[observation.ID]
			offer, err := fx.service.prepareReviewOffer(t.Context(), held, target, fx.candidate)
			if err != nil {
				t.Fatal(err)
			}
			offer.ID, offer.Subject.Name = "album-5", "Album-5"
			offer.ConversationReview.Target.ConversationID = "owner-5"
			fx.service.reviewGuard = func(context.Context, FolderOffer) error {
				if mode == "missing" {
					return errors.New("deleted conversation")
				}
				return nil
			}
			switch mode {
			case "identical names":
				offer.Subject.Name = held.observation.Folder
			case "foreign":
				offer.ConversationReview.Target.UserID = "someone-else"
			case "own conversation":
				offer.ConversationReview.Target = target
			case "same project Home", "other Home":
				offer.ConversationReview = nil
				if mode == "other Home" {
					offer.Subject.Key = FolderKey("other-project")
				}
			}
			stopped := offer
			stopped.ID, stopped.Subject.Name, stopped.Status = "album-4", "Album-4", FolderOfferAwaitingOutcome
			if _, err := fx.store.Mutate(t.Context(), target.UserID, func(doc *FolderDigestDocument) error {
				doc.Offers = []FolderOffer{stopped}
				if mode != "stopped only" {
					doc.Offers = append(doc.Offers, offer)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, _ := fx.store.Read(t.Context(), target.UserID)
			encoded, _ := json.Marshal(before)
			ids := fx.ids
			got := fx.service.HireCandidate(t.Context(), target, observation.ID)
			allowed := mode == "stopped only" || mode == "own conversation" || mode == "same project Home"
			if allowed {
				if got.CandidateID != fx.candidate || got.Blocker != nil || got.Unavailable != "" {
					t.Fatalf("allowed hire: %+v", got)
				}
			} else {
				if got.CandidateID != "" || got.Unavailable != FolderStructureReviewPending || got.Blocker == nil {
					t.Fatalf("blocked hire: %+v", got)
				}
				b := got.Blocker
				if mode == "foreign" || mode == "missing" {
					if b.Kind != "unavailable" || b.Subject != "Another setup" || b.ConversationID != "" || b.OfferID != "" {
						t.Fatalf("unverified owner leaked: %+v", b)
					}
				} else if b.Subject != offer.Subject.Name || (mode != "other Home" && (b.OfferID != offer.ID || b.ConversationID != "owner-5")) {
					t.Fatalf("wrong pending owner: %+v", b)
				}
			}
			after, _ := fx.store.Read(t.Context(), target.UserID)
			encodedAfter, _ := json.Marshal(after)
			if string(encoded) != string(encodedAfter) || fx.ids != ids || fx.setup.runCount() != 0 || fx.service.selections[observation.ID].observation.ScannedAt != held.observation.ScannedAt {
				t.Fatal("hire read changed stored state, allocated, ran setup or extended the selection")
			}
		})
	}
}

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
		{"missing build", "build:workspace", "unavailable", FolderOfferPending, true, false, true, false},
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
			if got == nil || got.Kind != tc.kind || got.Blocks != tc.blocks || (got.ConversationID != "") != tc.link || (got.OfferID != "") != (tc.link || tc.kind == "home") {
				t.Fatalf("blocker: %+v", got)
			}
			if tc.kind == "unavailable" && got.Subject != "Another setup" {
				t.Fatal("unverified subject leaked")
			}
		})
	}
}
