package personalassistant

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func entryChoiceFixture(t *testing.T) (*oneCardFixture, FolderOfferView) {
	t.Helper()
	f := newOneCardFixture(t)
	f.setup.planEntries = []string{"Song.rpp", "Alternate.rpp"}
	current, err := f.service.Current(t.Context(), "local")
	if err != nil || current.Offer == nil || current.Offer.Plan == nil {
		t.Fatal("missing entry plan", err)
	}
	return f, *current.Offer
}

func TestFolderSetupEntry_SavedChoiceSurvivesRestartAndNeedsItsOwnConfirmation(t *testing.T) {
	f, first := entryChoiceFixture(t)
	chosen, err := f.service.ChooseSetupEntry(t.Context(), "local", f.offer.ID, FolderSetupEntryInput{PlanDigest: first.Plan.Digest, EntryName: "Alternate.rpp"})
	if err != nil || chosen.Plan == nil || chosen.Plan.EntryName != "Alternate.rpp" || chosen.Plan.Digest == first.Plan.Digest || f.setup.runCount() != 0 {
		t.Fatal(chosen, err)
	}
	if stored := f.stored(t); stored.EntryName != "Alternate.rpp" || stored.Setup != nil || stored.Status != FolderOfferPending {
		t.Fatal("choice started setup", stored)
	}
	f.service = f.newService()
	f.service.deps.Setup = f.setup
	reopened, err := f.service.Current(t.Context(), "local")
	if err != nil || reopened.Offer.NeedsPick || reopened.Offer.Plan.EntryName != "Alternate.rpp" || reopened.Offer.Plan.Digest != chosen.Plan.Digest {
		t.Fatal(reopened, err)
	}
	for _, input := range []FolderSetupInput{
		{RequestID: "old-digest", PlanDigest: first.Plan.Digest, EntryName: "Alternate.rpp"},
		{RequestID: "wrong-choice", PlanDigest: chosen.Plan.Digest, EntryName: "Song.rpp"},
	} {
		if _, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, input); !errors.Is(err, ErrFolderPlanChanged) {
			t.Fatalf("unreviewed choice confirmed: %v", err)
		}
	}
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel}
	}
	view, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, FolderSetupInput{RequestID: "chosen", PlanDigest: chosen.Plan.Digest, EntryName: "Alternate.rpp"})
	if err != nil || view.Setup == nil || view.Setup.EntryName != "Alternate.rpp" {
		t.Fatal(view, err)
	}
	waitFor(t, "chosen run stopped", func() bool { return f.stored(t).Setup.Status == FolderSetupStopped && !f.service.isRunning(f.offer.ID) })
	if got := f.setup.entryNames(); len(got) != 1 || got[0] != "Alternate.rpp" {
		t.Fatal("choice not carried to run", got)
	}
	if _, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, FolderSetupInput{RequestID: "retarget", PlanDigest: chosen.Plan.Digest, EntryName: "Song.rpp"}); !errors.Is(err, ErrValidation) {
		t.Fatal("retargeted a confirmed run", err)
	}
	if _, err := f.service.ChooseSetupEntry(t.Context(), "local", f.offer.ID, FolderSetupEntryInput{PlanDigest: chosen.Plan.Digest, EntryName: "Song.rpp"}); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatal("edited an accepted plan", err)
	}
}

func TestFolderSetupEntry_OnlyACompletedRunEarnsTheFileReceipt(t *testing.T) {
	f, first := entryChoiceFixture(t)
	if _, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, FolderSetupInput{RequestID: "default-file", PlanDigest: first.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "file receipt", func() bool { return f.stored(t).Status == FolderOfferResolved && !f.service.isRunning(f.offer.ID) })
	stored := f.stored(t)
	if stored.Setup.EntryName != "Song.rpp" {
		t.Fatal("default not saved", stored.Setup)
	}
	found := 0
	for _, row := range stored.Outcome.Receipt {
		if row.Kind == FolderPlanFolder && row.Name == "Opens Song.rpp" {
			found++
		}
	}
	if found != 1 {
		t.Fatal("receipt omitted or repeated file", stored.Outcome.Receipt)
	}
	if rows := folderSetupEntryReceipt(&stored, stored.Outcome.Receipt); len(rows) != len(stored.Outcome.Receipt) {
		t.Fatal("receipt repeated on read")
	}
	stored.Setup.Status = FolderSetupStopped
	if rows := folderSetupEntryReceipt(&stored, nil); len(rows) != 0 {
		t.Fatal("a stopped choice claimed to have opened the file")
	}
}

func TestFolderSetupEntry_RejectsUnlistedNamesWithoutStartingAnything(t *testing.T) {
	f, first := entryChoiceFixture(t)
	before := f.stored(t)
	for _, name := range []string{"", "Other.rpp", "../Song.rpp", "dir/Song.rpp"} {
		if _, err := f.service.ChooseSetupEntry(t.Context(), "local", f.offer.ID, FolderSetupEntryInput{PlanDigest: first.Plan.Digest, EntryName: name}); !errors.Is(err, ErrValidation) {
			t.Fatalf("choice %q: %v", name, err)
		}
		if name != "" {
			if _, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, FolderSetupInput{RequestID: "bad", PlanDigest: first.Plan.Digest, EntryName: name}); !errors.Is(err, ErrValidation) {
				t.Fatalf("setup %q: %v", name, err)
			}
		}
	}
	if after := f.stored(t); !reflect.DeepEqual(before, after) || f.setup.runCount() != 0 {
		t.Fatal("refusal changed the review")
	}
	legacy := newOneCardFixture(t)
	if _, err := legacy.service.StartSetup(t.Context(), "local", legacy.offer.ID, FolderSetupInput{RequestID: "unoffered", PlanDigest: legacy.offer.Plan.Digest, EntryName: "Song.rpp"}); !errors.Is(err, ErrValidation) {
		t.Fatal("accepted a name not offered by the plan", err)
	}
}

type changingEntryPlan struct {
	*fakeFolderSetup
	once   sync.Once
	change func()
}

func (f *changingEntryPlan) Plan(ctx context.Context, req FolderSetupRequest) (FolderSetupPlan, error) {
	plan, err := f.fakeFolderSetup.Plan(ctx, req)
	f.once.Do(f.change)
	return plan, err
}

func TestFolderSetupEntry_AConcurrentChoiceIsNotOverwrittenBySetup(t *testing.T) {
	f, first := entryChoiceFixture(t)
	f.service.deps.Setup = &changingEntryPlan{fakeFolderSetup: f.setup, change: func() {
		if _, err := f.store.Mutate(t.Context(), "local", func(doc *FolderDigestDocument) error { doc.Offer(f.offer.ID).EntryName = "Alternate.rpp"; return nil }); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, FolderSetupInput{RequestID: "old-click", PlanDigest: first.Plan.Digest, EntryName: "Song.rpp"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale click overwrote choice", err)
	}
	if stored := f.stored(t); stored.EntryName != "Alternate.rpp" || stored.Setup != nil || f.setup.runCount() != 0 {
		t.Fatal("concurrent choice lost", stored)
	}
}

func TestFolderSetupEntry_AdjustAndReviewAgainKeepOnlyAStillListedChoice(t *testing.T) {
	f, observations, target, id, _, active := reviewedObservation(t, "documents", "Album")
	setup := &fakeFolderSetup{lines: oneCardLines(), planEntries: []string{"Song.rpp", "Alternate.rpp"}}
	f.service.deps.Setup = setup
	view, err := observations.ReadReview(t.Context(), target, id)
	if err != nil || view.Plan == nil {
		t.Fatal("missing plan", err)
	}
	chosen, err := f.service.ChooseSetupEntry(t.Context(), "local", id, FolderSetupEntryInput{PlanDigest: view.Plan.Digest, EntryName: "Alternate.rpp"})
	if err != nil || chosen.Plan == nil || chosen.Plan.EntryName != "Alternate.rpp" {
		t.Fatal(chosen, err)
	}
	stored, _ := f.service.StoredOffer(t.Context(), "local", id)
	review := stored.ConversationReview
	refresh := func(ctx context.Context) FolderOfferView {
		t.Helper()
		result, err := observations.Review(ctx, target, target.ConversationID, review.ObservationID, review.CandidateID)
		if err != nil || result.ID != id || result.Plan == nil {
			t.Fatal("review refresh", result, err)
		}
		return result
	}
	adjusted := refresh(WithFolderReviewWorkspaceName(t.Context(), "My album"))
	if adjusted.Plan.EntryName != "Alternate.rpp" {
		t.Fatal("Adjust lost choice", adjusted.Plan)
	}
	setup.planEntries = []string{"New.rpp", "Song.rpp", "Alternate.rpp"}
	kept := refresh(t.Context())
	if kept.Plan.EntryName != "Alternate.rpp" || len(kept.Plan.EntryCandidates) != 3 {
		t.Fatal("Review again lost listed choice", kept.Plan)
	}
	setup.planEntries = []string{"New.rpp", "Song.rpp"}
	changed := refresh(t.Context())
	if changed.Plan.EntryName != "New.rpp" || changed.Plan.Digest == kept.Plan.Digest {
		t.Fatal("removed choice retained its confirmation", changed.Plan)
	}
	*active = "another-review"
	if _, err := f.service.ChooseSetupEntry(t.Context(), "local", id, FolderSetupEntryInput{PlanDigest: changed.Plan.Digest, EntryName: "Song.rpp"}); err == nil {
		t.Fatal("lost canonical ownership still allowed a choice")
	}
	if setup.runCount() != 0 {
		t.Fatal("choice or refresh started setup")
	}
}

func TestFolderSetupEntry_BuildPlansKeepTheChoiceWithLiveAndRestoredHolds(t *testing.T) {
	f := newBuildSetupFixture(t)
	f.setup.planEntries = []string{"Song.rpp", "Alternate.rpp"}
	plan, err := f.service.BuildPlan(t.Context(), f.draft, f.observation.ID, f.candidate, "Kept name")
	if err != nil || plan == nil || plan.EntryName != "Song.rpp" {
		t.Fatal(plan, err)
	}
	chosen, err := f.service.ChooseBuildSetupEntry(t.Context(), f.draft, "session-1", f.observation.ID, f.candidate, "Kept name", FolderSetupEntryInput{PlanDigest: plan.Digest, EntryName: "Alternate.rpp"})
	if err != nil {
		t.Fatal(err)
	}
	for _, restored := range []bool{false, true} {
		if restored {
			f.now = f.now.Add(31 * time.Minute)
		}
		before, _ := f.store.Read(t.Context(), "local")
		plan, err := f.service.BuildPlan(t.Context(), f.draft, f.observation.ID, f.candidate, "Kept name")
		if err != nil || plan == nil || plan.EntryName != "Alternate.rpp" || plan.Digest != chosen.Plan.Digest {
			t.Fatal("choice lost", restored, plan, err)
		}
		after, _ := f.store.Read(t.Context(), "local")
		if !reflect.DeepEqual(before, after) {
			t.Fatal("plan read mutated the review")
		}
	}
	if f.setup.runCount() != 0 {
		t.Fatal("choosing started setup")
	}
}

func TestFolderSetupEntry_AFileChoiceNeverImpliesTheHomeManagersOpen(t *testing.T) {
	f, _ := handoffCardFixture(t)
	f.setup.planEntries = []string{"Song.rpp", "Alternate.rpp"}
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopHandoffReady, HomeID: "home-1"}
	}
	current, err := f.service.Current(t.Context(), "local")
	if err != nil || current.Offer.Plan == nil {
		t.Fatal(err)
	}
	digest := current.Offer.Plan.Digest
	if _, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, FolderSetupInput{RequestID: "home", PlanDigest: digest, EntryName: "Song.rpp"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Home ready", func() bool {
		return f.stored(t).Setup.StopReason == FolderStopHandoffReady && !f.service.isRunning(f.offer.ID)
	})
	before := f.stored(t)
	if _, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, FolderSetupInput{RequestID: "repeat-home", PlanDigest: digest, EntryName: "Song.rpp"}); err != nil {
		t.Fatal(err)
	}
	if after := f.stored(t); !reflect.DeepEqual(before, after) || after.Setup.Opening || f.setup.runCount() != 1 {
		t.Fatal("repeated file-bearing Set up opened the song")
	}
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel}
	}
	if _, err := f.service.StartSetup(t.Context(), "local", f.offer.ID, FolderSetupInput{RequestID: "open", PlanDigest: digest, EntryName: "Song.rpp", Open: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "explicit Open", func() bool { return f.setup.runCount() == 2 && !f.service.isRunning(f.offer.ID) })
	if !f.stored(t).Setup.Opening {
		t.Fatal("explicit Open was lost")
	}
}
