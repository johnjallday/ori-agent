package folderagent

import (
	"context"
	"strings"
	"testing"
)

func TestSetUp_TheViewCarriesTheRunsCleanedReceiptAndItsOwnSelection(t *testing.T) {
	h := newHarness(t, build(t, "Song.rpp"))
	h.setups.receipt = &SetupReceipt{
		Installed: []string{"Ori REAPER Plugin 0.11.0", "bad\nname"},
		Home:      &WorkspaceRef{ID: "home-1", Name: "Music Production Home", Route: "https://elsewhere.example/home"},
		Hired:     []string{"Portfolio Manager"},
		Members:   []WorkspaceRef{{ID: "m1", Name: "Draft A", Route: "/workspaces/draft-a"}, {Name: ""}},
		Listed:    6, ListedNoun: "songs", Unreached: -3,
	}
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	done, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if done.Setup == nil || done.Setup.Receipt == nil {
		t.Fatalf("no receipt on the view: %+v", done.Setup)
	}
	got := done.Setup.Receipt
	if strings.Join(got.Installed, "|") != "Ori REAPER Plugin 0.11.0" || strings.Join(got.Hired, "|") != "Portfolio Manager" {
		t.Fatalf("receipt names = %+v", got)
	}
	if got.Home == nil || got.Home.Name != "Music Production Home" || got.Home.Route != "" {
		t.Fatalf("a route off the app was kept: %+v", got.Home)
	}
	if len(got.Members) != 1 || got.Members[0].Route != "/workspaces/draft-a" || got.Listed != 6 || got.Unreached != 0 {
		t.Fatalf("receipt = %+v", got)
	}
	// The plan shown after the run is the one the run was confirmed on.
	if strings.Join(done.Plan.Selected, ",") != strings.Join(report.Plan.Selected, ",") {
		t.Fatalf("selection after the run = %v, run = %v", done.Plan.Selected, report.Plan.Selected)
	}
}

func TestView_AfterARunThePlanFollowsTheRunsSelectionNotTheRecommendation(t *testing.T) {
	h := newHarness(t, build(t, "Song.rpp"))
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if len(report.Plan.Steps) == 0 {
		t.Skip("no steps offered for this folder")
	}
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	// A run confirmed on nothing the agent recommended: the view must not
	// fall back to the recommendation.
	_, err := h.svc.deps.Store.Update(rec.WorkspaceID, func(r *Record) error {
		r.Setup = &Setup{Selected: []string{"no-such-step"}, Status: "done", StartedAt: h.svc.deps.Now()}
		r.State = StateSetUp
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	for _, step := range after.Plan.Steps {
		if step.Chosen {
			t.Fatalf("step %q shown chosen after a run that did not choose it", step.Handle)
		}
	}
}
