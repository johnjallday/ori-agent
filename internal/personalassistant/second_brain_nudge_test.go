package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// A line made into a follow-up some other way than the panel's triage (by hand,
// by an older acceptance) reads as accepted, in any state, and is no longer
// suggested. Nothing about it is written to the record.
func TestSecondBrain_LookMarksLinesThatAreAlreadyFollowUps(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	look, err := h.service.Look(ctx, "local")
	if err != nil || look.Result == nil || len(look.Result.Items) < 3 {
		t.Fatalf("look = %+v, %v", look, err)
	}
	for _, item := range look.Result.Items {
		if item.Accepted {
			t.Fatalf("before any follow-up, %q reads as accepted", item.Text)
		}
	}
	var suggested secondbrain.LookItem
	for _, item := range look.Result.Items {
		if item.Suggested {
			suggested = item
			break
		}
	}
	if suggested.ID == "" {
		t.Fatal("fixture: no suggested item")
	}
	other := look.Result.Items[len(look.Result.Items)-1]
	for _, item := range []secondbrain.LookItem{suggested, other} {
		made, err := h.followUps.Capture(ctx, followup.CaptureInput{
			UserID: "local", WorkspaceID: "hq-local", Category: followup.CategoryIOwe, Direction: followup.DirectionOutbound,
			Title: item.Text, Source: followup.SourceRef{Type: secondbrain.FollowUpSourceType, ID: item.ID},
			Provenance: followup.ProvenanceExplicit, Confidence: followup.ConfidenceHigh,
		})
		if err != nil {
			t.Fatal(err)
		}
		if item.ID == other.ID {
			if _, err := h.followUps.Complete(ctx, "local", made.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	look, err = h.service.Look(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	marked := map[string]secondbrain.LookItem{}
	for _, item := range look.Result.Items {
		if item.Accepted {
			marked[item.ID] = item
		}
	}
	if len(marked) != 2 {
		t.Fatalf("accepted items = %d, want 2 (an open and a completed follow-up)", len(marked))
	}
	if marked[suggested.ID].Suggested {
		t.Fatal("an accepted line is still suggested")
	}

	doc, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range doc.Connected().LastLook.Items {
		if item.Accepted {
			t.Fatalf("the record stored the accepted mark on %q", item.Text)
		}
		if item.ID == suggested.ID && !item.Suggested {
			t.Fatal("the record lost the stored suggestion")
		}
	}
}

type failingFollowUps struct{}

func (failingFollowUps) Capture(context.Context, followup.CaptureInput) (*followup.FollowUp, error) {
	return nil, errors.New("follow-ups are unavailable")
}

func (failingFollowUps) FindBySource(context.Context, string, string, string) (*followup.FollowUp, error) {
	return nil, errors.New("follow-ups are unavailable")
}

func TestSecondBrain_AcceptedMarksLeaveTheLookAloneWhenFollowUpsCannotBeRead(t *testing.T) {
	finished := secondBrainNow
	item := secondbrain.LookItem{ID: secondbrain.ItemID("s", "Plans.md", "Pay rent"), Text: "Pay rent", Note: "Plans.md", Line: 1, Suggested: true}
	view := SecondBrainLookView{State: secondbrain.LookFinished, LookID: "look-1", Result: &secondbrain.Look{
		ID: "look-1", State: secondbrain.LookFinished, StartedAt: secondBrainNow, FinishedAt: &finished, Items: []secondbrain.LookItem{item},
	}}
	service := &SecondBrainService{deps: SecondBrainDeps{FollowUps: failingFollowUps{}}}
	got := service.withAccepted(context.Background(), "local", view)
	if got.Result.Items[0].Accepted || !got.Result.Items[0].Suggested {
		t.Fatalf("item = %+v, want it unmarked and still suggested", got.Result.Items[0])
	}
	none := &SecondBrainService{}
	if got := none.withAccepted(context.Background(), "local", view); got.Result.Items[0].Accepted {
		t.Fatal("no follow-up service marked an item")
	}
}

func TestSecondBrain_NudgeAfterAWeekAndNotNowHoldsUntilTheNextLook(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)

	if _, ok := h.service.Nudge(ctx, "local"); ok {
		t.Fatal("a fresh look nudges")
	}
	*h.clock = h.clock.Add(8 * 24 * time.Hour)
	report, ok := h.service.Nudge(ctx, "local")
	if !ok || !report.Stale || report.Days != 8 {
		t.Fatalf("after eight days = %+v, %v", report, ok)
	}

	if _, err := h.service.DismissNudge(ctx, "local", "nudge-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.DismissNudge(ctx, "local", "nudge-1"); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, ok := h.service.Nudge(ctx, "local"); ok {
		t.Fatal("Not now did not put the nudge away")
	}
	if _, err := h.service.DismissNudge(ctx, "local", "connect-1"); !errors.Is(err, secondbrain.ErrRequestReused) {
		t.Fatalf("a connect's request id: %v", err)
	}
	if _, err := h.service.DismissNudge(ctx, "local", "has space"); !errors.Is(err, ErrValidation) {
		t.Fatalf("a bad request id: %v", err)
	}

	*h.clock = h.clock.Add(time.Hour)
	if _, err := h.service.LookAgain(ctx, "local", "look-again-1"); err != nil {
		t.Fatal(err)
	}
	h.service.Wait()
	*h.clock = h.clock.Add(8 * 24 * time.Hour)
	if report, ok := h.service.Nudge(ctx, "local"); !ok || !report.Stale {
		t.Fatalf("a week after the next look = %+v, %v", report, ok)
	}
}

func TestSecondBrain_NoNudgeWithNothingConnected(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	if _, ok := h.service.Nudge(ctx, "local"); ok {
		t.Fatal("nothing connected nudges")
	}
	if _, err := h.service.DismissNudge(ctx, "local", "nudge-1"); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("dismiss with nothing connected: %v", err)
	}
}
