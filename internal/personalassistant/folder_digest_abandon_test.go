package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// stoppedSetupFixture runs the one-card setup until it stops, leaving an
// awaiting_outcome offer with a stopped run: the card Remove is for.
func stoppedSetupFixture(t *testing.T) *oneCardFixture {
	t.Helper()
	f := newOneCardFixture(t)
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel, RunID: "new-run"}
	}
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
		FolderSetupInput{RequestID: "start", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to stop", func() bool {
		o := f.stored(t)
		return o.Setup != nil && o.Setup.Status == FolderSetupStopped && !f.service.isRunning(f.offer.ID)
	})
	return f
}

func (f *oneCardFixture) document(t *testing.T) FolderDigestDocument {
	t.Helper()
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestFolderAbandon_AcceptsAStoppedSetupAndKeepsItsHistory(t *testing.T) {
	f := stoppedSetupFixture(t)
	ctx := context.Background()
	before := f.document(t)
	prior := f.stored(t)

	view, err := f.service.Abandon(ctx, "local", f.offer.ID, "remove-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != FolderOfferAbandoned {
		t.Fatalf("status = %s", view.Status)
	}
	got := f.stored(t)
	if got.Status != FolderOfferAbandoned || got.AbandonedAt == nil || !got.AbandonedAt.Equal(f.now) {
		t.Fatalf("stored = %+v", got)
	}
	if got.Setup == nil || got.Setup.Status != FolderSetupStopped || got.Outcome == nil || got.Decision != prior.Decision {
		t.Fatalf("the run and outcome stay for history: %+v", got)
	}
	after := f.document(t)
	if len(after.Tombstones) != len(before.Tombstones) || len(after.DomainDeclines) != len(before.DomainDeclines) ||
		len(after.Decisions) != len(before.Decisions) {
		t.Fatal("a removal writes no tombstone, domain decline or decision")
	}
}

func TestFolderAbandon_ClearsTheQueue(t *testing.T) {
	f := stoppedSetupFixture(t)
	ctx := context.Background()
	if _, err := f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
		o := d.Offer(f.offer.ID)
		o.Queue = []FolderCandidateRecord{{Key: FolderKey("/queued"), Name: "queued", Kind: FolderChoiceProject, Reason: "r"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Abandon(ctx, "local", f.offer.ID, "remove-1"); err != nil {
		t.Fatal(err)
	}
	if got := f.stored(t); len(got.Queue) != 0 {
		t.Fatalf("queue = %+v", got.Queue)
	}
	f.now = f.now.Add(FolderNextOfferDelay + 1)
	if current, err := f.service.Current(ctx, "local"); err != nil || current.Offer != nil {
		t.Fatalf("no follow-up offer is promoted: %+v, %v", current.Offer, err)
	}
}

func TestFolderAbandon_RefusesEveryOtherStatus(t *testing.T) {
	for _, status := range []FolderOfferStatus{FolderOfferPending, FolderOfferLater, FolderOfferDeclined,
		FolderOfferResolved, FolderOfferAbandoned, FolderOfferClosed} {
		t.Run(string(status), func(t *testing.T) {
			f := newOneCardFixture(t)
			ctx := context.Background()
			if _, err := f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
				d.Offer(f.offer.ID).Status = status
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.Abandon(ctx, "local", f.offer.ID, "remove-1"); !errors.Is(err, ErrFolderOfferDecided) {
				t.Fatalf("err = %v, want ErrFolderOfferDecided", err)
			}
			if f.stored(t).Status != status {
				t.Fatal("a refused removal changes nothing")
			}
		})
	}
}

func TestFolderAbandon_RefusesALiveRun(t *testing.T) {
	f := newOneCardFixture(t)
	f.setup.hold = make(chan struct{})
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to start", func() bool { return f.setup.runCount() == 1 && f.service.isRunning(f.offer.ID) })
	if _, err := f.service.Abandon(ctx, "local", f.offer.ID, "remove-1"); !errors.Is(err, ErrFolderSetupRunning) {
		t.Fatalf("err = %v, want ErrFolderSetupRunning", err)
	}
	if f.stored(t).Status == FolderOfferAbandoned {
		t.Fatal("a live run cannot be removed")
	}
	close(f.setup.hold)
	waitFor(t, "the offer to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
}

func TestFolderAbandon_AcceptsAnInterruptedRun(t *testing.T) {
	f := newOneCardFixture(t)
	f.setup.hold = make(chan struct{})
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to start", func() bool { return f.setup.runCount() == 1 })
	if stored := f.stored(t); stored.Setup == nil || stored.Setup.Status != FolderSetupRunning {
		t.Fatalf("stored run = %+v", stored.Setup)
	}
	// A restarted server has the stored "running" and no live run.
	restarted := f.newService()
	view, err := restarted.Abandon(ctx, "local", f.offer.ID, "remove-1")
	if err != nil || view.Status != FolderOfferAbandoned {
		t.Fatalf("view = %+v, err = %v", view, err)
	}
	close(f.setup.hold)
}

func TestFolderAbandon_ReplayReturnsTheEarlierResult(t *testing.T) {
	f := stoppedSetupFixture(t)
	ctx := context.Background()
	if _, err := f.service.Abandon(ctx, "local", f.offer.ID, "remove-1"); err != nil {
		t.Fatal(err)
	}
	receipts := len(f.document(t).Receipts)
	view, err := f.service.Abandon(ctx, "local", f.offer.ID, "remove-1")
	if err != nil || view.Status != FolderOfferAbandoned {
		t.Fatalf("replay = %+v, %v", view, err)
	}
	if len(f.document(t).Receipts) != receipts {
		t.Fatal("a replay records no second receipt")
	}
	// The same id for another offer, or another action, is not a replay.
	if _, err := f.service.Abandon(ctx, "local", "offer-unknown", "remove-1"); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Fatalf("unknown offer: %v", err)
	}
	if _, err := f.service.Abandon(ctx, "local", f.offer.ID, "start"); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("a receipt from another action must not replay: %v", err)
	}
}

func TestFolderAbandon_ValidatesTheRequestID(t *testing.T) {
	f := stoppedSetupFixture(t)
	if _, err := f.service.Abandon(context.Background(), "local", f.offer.ID, "  "); !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v", err)
	}
}

func TestFolderAbandon_EveryWaitingReaderSkipsTheOffer(t *testing.T) {
	f := stoppedSetupFixture(t)
	ctx := context.Background()
	if current, err := f.service.Current(ctx, "local"); err != nil || current.Offer == nil || current.Offer.ID != f.offer.ID {
		t.Fatalf("the stopped setup is pinned before removal: %+v, %v", current.Offer, err)
	}
	if _, err := f.service.Abandon(ctx, "local", f.offer.ID, "remove-1"); err != nil {
		t.Fatal(err)
	}
	doc := f.document(t)
	if doc.Awaiting() != nil || doc.Pending() != nil {
		t.Fatal("Awaiting and Pending skip an abandoned offer")
	}
	if recentProjectHomeNavigation(doc, f.now) != nil {
		t.Fatal("recent Home navigation skips an abandoned offer")
	}
	current, err := f.service.Current(ctx, "local")
	if err != nil || current.Offer != nil {
		t.Fatalf("an unnamed read shows the chooser, not a card: %+v, %v", current.Offer, err)
	}
	if waiting, err := f.service.WaitingFolderOffers(ctx, "local"); err != nil || len(waiting) != 0 {
		t.Fatalf("waiting = %+v, %v", waiting, err)
	}
	if continuations, err := f.service.PortfolioContinuations(ctx, "local", "home"); err != nil || len(continuations) != 0 {
		t.Fatalf("continuations = %+v, %v", continuations, err)
	}
	// A named read still returns it, so an open card sees its own new status.
	named, err := f.service.CurrentOffer(ctx, "local", f.offer.ID)
	if err != nil || named.Offer == nil || named.Offer.Status != FolderOfferAbandoned {
		t.Fatalf("named read = %+v, %v", named.Offer, err)
	}
}

func TestFolderAbandon_ShowingTheFolderAgainStartsAFreshOffer(t *testing.T) {
	f := stoppedSetupFixture(t)
	ctx := context.Background()
	if _, err := f.service.Abandon(ctx, "local", f.offer.ID, "remove-1"); err != nil {
		t.Fatal(err)
	}
	fresh, err := f.service.scanRoot(ctx, "local", f.folder, "")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == f.offer.ID || fresh.Status != FolderOfferPending || fresh.Declined != nil {
		t.Fatalf("fresh = %+v", fresh)
	}
	if doc := f.document(t); doc.Offer(f.offer.ID).Status != FolderOfferAbandoned {
		t.Fatal("the removed offer stays removed")
	}
}

func TestFolderDigest_AnOldDocumentWithoutTheAbandonFieldLoadsUnchanged(t *testing.T) {
	f := stoppedSetupFixture(t)
	raw, err := json.Marshal(f.document(t))
	if err != nil {
		t.Fatal(err)
	}
	var decoded FolderDigestDocument
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateFolderDigest(decoded); err != nil {
		t.Fatalf("old document must validate: %v", err)
	}
	if offer := decoded.Offer(f.offer.ID); offer == nil || offer.AbandonedAt != nil {
		t.Fatalf("offer = %+v", offer)
	}
	if _, err := f.service.Abandon(context.Background(), "local", f.offer.ID, "remove-1"); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(f.document(t))
	if err != nil {
		t.Fatal(err)
	}
	var abandoned FolderDigestDocument
	if err := json.Unmarshal(raw, &abandoned); err != nil || validateFolderDigest(abandoned) != nil {
		t.Fatalf("an abandoned document must validate: %v", err)
	}
}
