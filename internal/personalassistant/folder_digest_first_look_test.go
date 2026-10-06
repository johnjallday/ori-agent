package personalassistant

import (
	"context"
	"testing"
	"time"
)

// firstLookHost is the host's read of each workspace's first look, as a test
// sets it.
type firstLookHost struct {
	views map[string]FolderFirstTaskView
	asked []string
}

func (h *firstLookHost) read(_ context.Context, workspaceID string) (FolderFirstTaskView, bool) {
	h.asked = append(h.asked, workspaceID)
	view, ok := h.views[workspaceID]
	return view, ok
}

// setUpThesis shows the Documents folder and sets its Thesis project up, the
// way the card's confirmed plan does. The offer resolves in the same request.
func setUpThesis(t *testing.T, f *folderDigestFixture) FolderOfferView {
	t.Helper()
	ctx := context.Background()
	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	decided, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{
		Decision: FolderDecisionYes, Choice: FolderChoiceProject, Create: true, RequestID: "create-" + offer.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decided.Status != FolderOfferResolved || decided.Outcome == nil || decided.Outcome.WorkspaceID != "ws-thesis" {
		t.Fatalf("setup did not resolve to the Thesis workspace: %+v", decided)
	}
	return decided
}

func newFirstLookFixture(t *testing.T) (*folderDigestFixture, *firstLookHost) {
	t.Helper()
	f := newFolderDigestFixture(t)
	host := &firstLookHost{views: map[string]FolderFirstTaskView{}}
	f.service.deps.Creator = &fakeFolderCreator{}
	f.service.deps.FirstTaskView = host.read
	return f, host
}

// A resolved project offer carries its folder's first look, read live from the
// host, on every view of it: the response to Set up, the plain read, and the
// read by offer id (FR17).
func TestFolderDigest_ResolvedProjectOfferCarriesItsFirstLook(t *testing.T) {
	f, host := newFirstLookFixture(t)
	ctx := context.Background()
	host.views["ws-thesis"] = FolderFirstTaskView{
		State: FolderFirstTaskSeeded, CanStart: true, TaskID: "task-1", WorkspaceID: "ws-thesis",
		WorkspaceName: "Thesis", WorkspaceRoute: "/workspaces/thesis",
	}

	decided := setUpThesis(t, f)
	if decided.FirstTask == nil || decided.FirstTask.State != FolderFirstTaskSeeded || !decided.FirstTask.CanStart ||
		decided.FirstTask.OfferID != decided.ID || decided.FirstTask.TaskID != "task-1" {
		t.Fatalf("the Set up response did not carry the first look: %+v", decided.FirstTask)
	}

	// The look moves on; the stored offer is untouched and the next read says so.
	started := f.now.Add(time.Minute)
	host.views["ws-thesis"] = FolderFirstTaskView{
		State: FolderFirstTaskRunning, StartedAt: &started, WorkspaceID: "ws-thesis", WorkspaceName: "Thesis",
	}
	named, err := f.service.CurrentOffer(ctx, "local", decided.ID)
	if err != nil {
		t.Fatal(err)
	}
	if named.Offer == nil || named.Offer.FirstTask == nil || named.Offer.FirstTask.State != FolderFirstTaskRunning {
		t.Fatalf("the read by offer id = %+v", named.Offer)
	}
	// A read for one named offer is about that offer only.
	if named.FirstLook != nil {
		t.Fatalf("a named read carried the digest-wide first look: %+v", named.FirstLook)
	}
	stored, err := f.service.StoredOffer(ctx, "local", decided.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range stored.Outcome.Receipt {
		if row.Kind == "task" && row.Detail == "Running…" {
			t.Fatalf("the live state was written onto the stored receipt: %+v", row)
		}
	}
}

// Offers that are not a finished project setup never ask the host anything.
func TestFolderDigest_OnlyResolvedProjectOffersHaveAFirstLook(t *testing.T) {
	f, host := newFirstLookFixture(t)
	ctx := context.Background()

	pending, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if pending.FirstTask != nil {
		t.Fatalf("a pending offer carried a first look: %+v", pending.FirstTask)
	}
	later, err := f.service.Decide(ctx, "local", pending.ID, FolderDecisionInput{Decision: FolderDecisionLater, RequestID: "later-1"})
	if err != nil {
		t.Fatal(err)
	}
	if later.FirstTask != nil {
		t.Fatalf("an offer set aside carried a first look: %+v", later.FirstTask)
	}

	// A tidy sets up File Janitor, not a folder workspace with a first look.
	f.now = f.now.Add(time.Minute)
	tidy, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	done, err := f.service.Decide(ctx, "local", tidy.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceTidy, RequestID: "tidy-1"})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != FolderOfferResolved || done.FirstTask != nil {
		t.Fatalf("a tidy carried a first look: %+v", done)
	}
	if len(host.asked) != 0 {
		t.Fatalf("the host was asked about %v", host.asked)
	}

	if _, ok, err := f.service.LatestFirstLook(ctx, "local"); err != nil || ok {
		t.Fatalf("a digest with no folder workspace reported a first look: ok=%t err=%v", ok, err)
	}
	view, err := f.service.Current(ctx, "local")
	if err != nil || view.FirstLook != nil {
		t.Fatalf("first_look = %+v err=%v", view.FirstLook, err)
	}
}

// With a host that cannot read first looks, the digest is exactly what it was.
func TestFolderDigest_NoFirstLookReadWired(t *testing.T) {
	f := newFolderDigestFixture(t)
	f.service.deps.Creator = &fakeFolderCreator{}
	ctx := context.Background()
	decided := setUpThesis(t, f)
	if decided.FirstTask != nil {
		t.Fatalf("first_task without a host read: %+v", decided.FirstTask)
	}
	view, err := f.service.Current(ctx, "local")
	if err != nil || view.FirstLook != nil || view.Offer != nil {
		t.Fatalf("current = %+v err=%v", view, err)
	}
	if _, ok, err := f.service.LatestFirstLook(ctx, "local"); err != nil || ok {
		t.Fatalf("LatestFirstLook ok=%t err=%v", ok, err)
	}
}

// The plain read names the latest look for the mission card and, when nothing
// else is waiting, brings the folder's receipt back while the look still needs
// the user, so a reload keeps "Start first look" and "Running…" (FR13, FR17).
func TestFolderDigest_CurrentBringsBackAReceiptWhoseFirstLookIsOpen(t *testing.T) {
	for _, state := range []FolderFirstTaskState{
		FolderFirstTaskSeeded, FolderFirstTaskRunning, FolderFirstTaskWaiting, FolderFirstTaskFailed, FolderFirstTaskFinished,
	} {
		t.Run(string(state), func(t *testing.T) {
			f, host := newFirstLookFixture(t)
			ctx := context.Background()
			decided := setUpThesis(t, f)
			// Twenty minutes on: the folder's next queued candidate is not due
			// yet, so nothing else is waiting.
			f.now = f.now.Add(20 * time.Minute)
			look := FolderFirstTaskView{State: state, WorkspaceID: "ws-thesis", WorkspaceName: "Thesis"}
			if state == FolderFirstTaskFinished {
				finished := f.now.Add(-5 * time.Minute)
				look.FinishedAt, look.ResultExcerpt = &finished, "Three drafts."
			}
			host.views["ws-thesis"] = look

			view, err := f.service.Current(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			if view.FirstLook == nil || view.FirstLook.State != state || view.FirstLook.OfferID != decided.ID {
				t.Fatalf("first_look = %+v", view.FirstLook)
			}
			if view.Offer == nil || view.Offer.ID != decided.ID || view.Offer.Status != FolderOfferResolved ||
				view.Offer.FirstTask == nil || view.Offer.FirstTask.State != state {
				t.Fatalf("offer = %+v", view.Offer)
			}
		})
	}

	// A workspace with no look left to show brings nothing back.
	f, host := newFirstLookFixture(t)
	setUpThesis(t, f)
	f.now = f.now.Add(20 * time.Minute)
	delete(host.views, "ws-thesis")
	view, err := f.service.Current(context.Background(), "local")
	if err != nil || view.Offer != nil || view.FirstLook != nil {
		t.Fatalf("no look: offer=%+v first_look=%+v err=%v", view.Offer, view.FirstLook, err)
	}
}

func TestFolderDigest_AFirstLookNeverDisplacesAQuestionOrAnOldSetup(t *testing.T) {
	f, host := newFirstLookFixture(t)
	ctx := context.Background()
	decided := setUpThesis(t, f)
	host.views["ws-thesis"] = FolderFirstTaskView{State: FolderFirstTaskSeeded, CanStart: true, WorkspaceID: "ws-thesis"}

	// A new question is waiting: it is the offer shown. The look is still named.
	f.now = f.now.Add(time.Minute)
	pending, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.service.Current(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if view.Offer == nil || view.Offer.ID != pending.ID {
		t.Fatalf("a first look displaced the pending question: %+v", view.Offer)
	}
	if view.FirstLook == nil || view.FirstLook.OfferID != decided.ID {
		t.Fatalf("first_look = %+v", view.FirstLook)
	}
	if _, err := f.service.Decide(ctx, "local", pending.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "no-1"}); err != nil {
		t.Fatal(err)
	}

	// A setup from weeks ago does not come back just because its look never ran.
	if !firstLookNeedsHome(FolderOffer{ResolvedAt: &f.now}, FolderFirstTaskView{State: FolderFirstTaskSeeded}, f.now) {
		t.Fatal("a fresh setup with a seeded look must come back")
	}
	old := f.now.Add(-8 * 24 * time.Hour)
	if firstLookNeedsHome(FolderOffer{ResolvedAt: &old}, FolderFirstTaskView{State: FolderFirstTaskSeeded}, f.now) {
		t.Fatal("a setup older than a week came back")
	}
	future := f.now.Add(time.Hour)
	if firstLookNeedsHome(FolderOffer{ResolvedAt: &future}, FolderFirstTaskView{State: FolderFirstTaskSeeded}, f.now) {
		t.Fatal("a setup dated in the future came back")
	}
	if firstLookNeedsHome(FolderOffer{}, FolderFirstTaskView{State: FolderFirstTaskSeeded}, f.now) {
		t.Fatal("an offer with no resolve time came back")
	}
	if firstLookNeedsHome(FolderOffer{ResolvedAt: &f.now}, FolderFirstTaskView{State: FolderFirstTaskNone}, f.now) {
		t.Fatal("a workspace with no look came back")
	}
	if firstLookNeedsHome(FolderOffer{ResolvedAt: &f.now}, FolderFirstTaskView{State: FolderFirstTaskFinished}, f.now) {
		t.Fatal("a finished look with no finish time came back")
	}
	// A result is news for an hour; after that it lives in Today's Done.
	setUp := f.now.Add(-3 * time.Hour)
	recent, stale := f.now.Add(-5*time.Minute), f.now.Add(-2*time.Hour)
	if !firstLookNeedsHome(FolderOffer{ResolvedAt: &setUp}, FolderFirstTaskView{State: FolderFirstTaskFinished, FinishedAt: &recent}, f.now) {
		t.Fatal("a look that just finished did not keep its receipt")
	}
	if firstLookNeedsHome(FolderOffer{ResolvedAt: &setUp}, FolderFirstTaskView{State: FolderFirstTaskFinished, FinishedAt: &stale}, f.now) {
		t.Fatal("a look that finished hours ago brought its receipt back")
	}
}

// Once the next question about the same folder is due, it takes the card: the
// receipt steps aside, and the mission card still knows the look.
func TestFolderDigest_ADueQuestionTakesTheCardFromAnOpenFirstLook(t *testing.T) {
	f, host := newFirstLookFixture(t)
	ctx := context.Background()
	decided := setUpThesis(t, f)
	host.views["ws-thesis"] = FolderFirstTaskView{State: FolderFirstTaskSeeded, CanStart: true, WorkspaceID: "ws-thesis"}

	f.now = f.now.Add(FolderNextOfferDelay + time.Minute)
	view, err := f.service.Current(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if view.Offer == nil || view.Offer.Status != FolderOfferPending || view.Offer.ID == decided.ID {
		t.Fatalf("the due candidate was not promoted: %+v", view.Offer)
	}
	if view.FirstLook == nil || view.FirstLook.OfferID != decided.ID || !view.FirstLook.CanStart {
		t.Fatalf("first_look = %+v", view.FirstLook)
	}
}

// The latest look is the newest folder workspace that still has one, and the
// read is bounded because it is polled.
func TestFolderDigest_LatestFirstLookTriesTheNewestWorkspacesFirst(t *testing.T) {
	f, host := newFirstLookFixture(t)
	ctx := context.Background()
	base := f.now
	resolvedAt := func(minutes int) *time.Time {
		at := base.Add(time.Duration(minutes) * time.Minute)
		return &at
	}
	// Five resolved project offers, written oldest first as the sidecar holds
	// them, plus a tidy that must never be read.
	names := []string{"a", "b", "c", "d", "e"}
	key := "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
		for i, name := range names {
			d.Offers = append(d.Offers, FolderOffer{
				ID: "offer-" + name, Status: FolderOfferResolved, FolderKey: key, FolderName: name,
				Verdict: "project", Subject: FolderCandidateRecord{Key: key, Name: name, Kind: FolderChoiceProject},
				Choice: FolderChoiceProject, CreatedAt: base, ResolvedAt: resolvedAt(i),
				Outcome: &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "ws-" + name},
			})
		}
		d.Offers = append(d.Offers, FolderOffer{
			ID: "offer-tidy", Status: FolderOfferResolved, FolderKey: key, FolderName: "Downloads",
			Verdict: "dump", Subject: FolderCandidateRecord{Key: key, Name: "Downloads", Kind: FolderChoiceTidy},
			Choice: FolderChoiceTidy, CreatedAt: base, ResolvedAt: resolvedAt(30),
			Outcome: &FolderOutcome{Kind: FolderChoiceTidy, WorkspaceID: "ws-janitor"},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.now = base.Add(time.Hour)

	// The newest (e) has nothing to show, so the next newest (d) is the look.
	host.views["ws-d"] = FolderFirstTaskView{State: FolderFirstTaskRunning, WorkspaceID: "ws-d"}
	host.views["ws-a"] = FolderFirstTaskView{State: FolderFirstTaskSeeded, WorkspaceID: "ws-a"}
	look, ok, err := f.service.LatestFirstLook(ctx, "local")
	if err != nil || !ok || look.WorkspaceID != "ws-d" || look.OfferID != "offer-d" {
		t.Fatalf("look = %+v ok=%t err=%v", look, ok, err)
	}
	if len(host.asked) != 2 || host.asked[0] != "ws-e" || host.asked[1] != "ws-d" {
		t.Fatalf("read %v, want the newest first and no further than the first hit", host.asked)
	}

	// Nothing among the newest three: the read stops there rather than walking
	// the whole history, so the oldest workspace's look is not found.
	delete(host.views, "ws-d")
	host.asked = nil
	if look, ok, err := f.service.LatestFirstLook(ctx, "local"); err != nil || ok {
		t.Fatalf("read past the bound: %+v ok=%t err=%v", look, ok, err)
	}
	if len(host.asked) != folderFirstLookCandidates {
		t.Fatalf("read %d workspaces (%v), want %d", len(host.asked), host.asked, folderFirstLookCandidates)
	}
	for _, id := range host.asked {
		if id == "ws-janitor" {
			t.Fatal("a tidy's workspace was read for a first look")
		}
	}
}

func TestFolderDigest_LatestFirstLookNeedsAStore(t *testing.T) {
	var none *FolderDigestService
	if _, ok, err := none.LatestFirstLook(context.Background(), "local"); ok || err == nil {
		t.Fatalf("a nil service: ok=%t err=%v", ok, err)
	}
}
