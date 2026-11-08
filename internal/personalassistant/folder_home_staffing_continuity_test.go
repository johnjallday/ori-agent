package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFolderBuildPreviewCarriesOriginalHomeReviewWithoutRenewalOrPublicProof(t *testing.T) {
	fx := newBuildSetupFixture(t)
	ctx := t.Context()
	view, err := fx.service.ReviewBuild(ctx, fx.draft, "session-1", fx.observation.ID, fx.candidate, "My song")
	if err != nil {
		t.Fatal(err)
	}
	original := folderHomeReviewFixture()
	before, err := fx.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error {
		for i := range doc.Offers {
			if doc.Offers[i].ID == view.ID {
				doc.Offers[i].HomeStaffingReview = original.Clone()
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fx.service.BuildPlan(ctx, fx.draft, fx.observation.ID, fx.candidate, "My song")
	if err != nil {
		t.Fatal(err)
	}
	review := fx.setup.planned[len(fx.setup.planned)-1].Offer.HomeStaffingReview
	if fx.setup.planned[len(fx.setup.planned)-1].Offer.ID != view.ID || !reflect.DeepEqual(review, original) {
		t.Fatal("preview lost or renewed the original choices/advice")
	}
	data, err := json.Marshal(plan)
	if err != nil || strings.Contains(string(data), "source_witness") || strings.Contains(string(data), "original-source") || strings.Contains(string(data), "home_staffing_review") {
		t.Fatal("preview leaked private authority", err)
	}
	after, err := fx.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version {
		t.Fatal("preview wrote the review", err)
	}
	if len(fx.setup.ran) != 0 {
		t.Fatal("preview executed setup")
	}
}

type homeReviewChangingPlanner struct {
	*fakeFolderSetup
	during func()
}

func (p *homeReviewChangingPlanner) Plan(ctx context.Context, req FolderSetupRequest) (FolderSetupPlan, error) {
	plan, err := p.fakeFolderSetup.Plan(ctx, req)
	if change := p.during; change != nil {
		p.during = nil
		change()
	}
	return plan, err
}

func TestFolderStartSetupRefusesHomeChoiceChangeDuringUnlockedPlanning(t *testing.T) {
	fx := newBuildSetupFixture(t)
	ctx := t.Context()
	view, err := fx.service.ReviewBuild(ctx, fx.draft, "session-1", fx.observation.ID, fx.candidate, "My song")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fx.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error {
		for i := range doc.Offers {
			if doc.Offers[i].ID == view.ID {
				doc.Offers[i].HomeStaffingReview = folderHomeReviewFixture()
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fx.folderDigestFixture.service.deps.Setup = &homeReviewChangingPlanner{fakeFolderSetup: fx.setup, during: func() {
		_, err := fx.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error {
			for i := range doc.Offers {
				if doc.Offers[i].ID == view.ID {
					r := doc.Offers[i].HomeStaffingReview
					r.Revision++
					r.Team = nil // An explicit clear, not a renewed choice.
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}}
	_, err = fx.folderDigestFixture.service.StartSetup(ctx, "local", view.ID, FolderSetupInput{RequestID: "setup-after-review", PlanDigest: view.Plan.Digest})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("changed team was accepted: %v", err)
	}
	stored, err := fx.folderDigestFixture.service.StoredOffer(ctx, "local", view.ID)
	if err != nil || stored.Setup != nil || stored.HomeStaffingReview.Team != nil || stored.HomeStaffingReview.Revision != 2 || fx.setup.runCount() != 0 {
		t.Fatal("stale plan executed or overwrote the explicit clear", err)
	}
}
