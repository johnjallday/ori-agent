package personalassistant

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// buildSetupFixture is a held REAPER song pick under a build's draft target,
// with Home's setup host faked and the build's guard and lease recording what
// they were asked.
type buildSetupFixture struct {
	*folderDigestFixture
	service     *FolderObservationService
	setup       *fakeFolderSetup
	draft       foldercontext.Target
	review      foldercontext.Target
	song        string
	observation *foldercontext.Observation
	candidate   string
	mu          sync.Mutex
	guarded     int
	leased      int
	guardErr    error
	settled     chan FolderOffer
}

func newBuildSetupFixture(t *testing.T) *buildSetupFixture {
	t.Helper()
	f, service, target := observationFixture(t)
	draft := target
	draft.DraftID = BuildReviewPrefix + "session-1"
	fx := &buildSetupFixture{folderDigestFixture: f, service: service, draft: draft, review: BuildReviewTarget(draft, "session-1"),
		settled: make(chan FolderOffer, 4)}
	fx.setup = &fakeFolderSetup{
		lines:       []FolderPlanLine{{Kind: FolderPlanWorkspace, Name: "Creates a song workspace named Summer Demos"}},
		destination: &FolderSetupDestination{Status: "standalone"},
		finish: func() FolderSetupUpdate {
			return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopInstallFailed, RunID: "new-run"}
		},
	}
	f.service.deps.Setup = fx.setup
	// A conversation's guard and lease never vouch for a build's review.
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error {
		t.Error("the conversation guard was asked about a build review")
		return errors.New("not a conversation")
	}, func(foldercontext.Target) (func(), bool) {
		t.Error("the conversation lease was asked about a build review")
		return func() {}, false
	})
	service.ConfigureBuildReviews(func(_ context.Context, offer FolderOffer) error {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		if offer.ConversationReview == nil || offer.ConversationReview.Target != fx.review {
			t.Errorf("the build guard was asked about %+v", offer.ConversationReview)
		}
		fx.guarded++
		return fx.guardErr
	}, func(target foldercontext.Target) (func(), bool) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		fx.leased++
		return func() {}, target == fx.review
	})
	f.service.AddSetupObserver(func(_ context.Context, _ string, offer FolderOffer) { fx.settled <- offer })
	fx.song = filepath.Join(f.home, "Music", "Ori Music Demo")
	writeFolderFiles(t, fx.song, "Ori Music Demo.rpp", "Media/take.wav")
	fx.observation = observeSelected(t, f, service, draft, fx.song)
	recognition, err := service.Recognize(context.Background(), draft, fx.observation.ID)
	if err != nil || !recognition.Recognized() {
		t.Fatalf("recognition %+v %v", recognition, err)
	}
	fx.candidate = recognition.CandidateID
	return fx
}

func (fx *buildSetupFixture) waitSettled(t *testing.T) FolderOffer {
	t.Helper()
	select {
	case offer := <-fx.settled:
		return offer
	case <-time.After(5 * time.Second):
		t.Fatal("the run never settled")
		return FolderOffer{}
	}
}

// The build's held pick reaches Home's setup journey without a path ever
// leaving the server: the canonical offer is the build's own review, the pick
// moves to it, and the run reads the folder through the build's guard.
func TestFolderBuildSetup_TheHeldPickRunsHomesJourneyThroughTheBuildsReview(t *testing.T) {
	fx := newBuildSetupFixture(t)
	ctx := context.Background()
	fx.setup.hire = &FolderHire{Mode: FolderHireCreate, AgentName: "Song Assistant", RoleLabel: "Song"}
	fx.setup.briefDefault = "Keep the session tidy."

	plan, err := fx.service.BuildPlan(ctx, fx.draft, fx.observation.ID, fx.candidate, "Summer Demos")
	if err != nil || !plan.Ready {
		t.Fatalf("plan %+v %v", plan, err)
	}
	// The build shows the same hire and brief as Home's card.
	if plan.Hire == nil || plan.Hire.AgentName != "Song Assistant" || plan.BriefDefault != "Keep the session tidy." {
		t.Fatalf("plan hire %+v brief %q", plan.Hire, plan.BriefDefault)
	}
	view, err := fx.service.ReviewBuild(ctx, fx.draft, "session-1", fx.observation.ID, fx.candidate, "Summer Demos")
	if err != nil || view.ID == "" {
		t.Fatalf("review %+v %v", view, err)
	}
	stored, err := fx.service.digest.StoredOffer(ctx, "local", view.ID)
	if err != nil || stored.ConversationReview == nil || stored.ConversationReview.Target != fx.review ||
		stored.ConversationReview.WorkspaceName != "Summer Demos" || stored.Status != FolderOfferPending || BuildSessionOf(stored) != "session-1" {
		t.Fatalf("stored offer %+v %v", stored.ConversationReview, err)
	}
	// The pick moved from the build's draft to its review, exactly once.
	if _, err := fx.service.ResolveRoot(ctx, fx.draft, fx.observation.ID); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("the draft still holds the pick: %v", err)
	}
	if root, err := fx.service.ResolveRoot(ctx, fx.review, fx.observation.ID); err != nil || root != fx.song {
		t.Fatalf("the review holds the pick: %q %v", root, err)
	}
	// The plan read the build shows is the plan Set it up confirms.
	again, err := fx.service.BuildPlan(ctx, fx.review, fx.observation.ID, fx.candidate, "Summer Demos")
	if err != nil || again.Digest != plan.Digest {
		t.Fatalf("plan after the move %+v %v (was %s)", again, err, plan.Digest)
	}

	if _, err := fx.service.StartBuildSetup(ctx, "local", view.ID, plan.Digest, "build-setup-1", "", " Mix the  chorus first. "); err != nil {
		t.Fatal(err)
	}
	settled := fx.waitSettled(t)
	if settled.ID != view.ID || settled.Setup == nil || settled.Setup.Status != FolderSetupStopped || settled.Setup.StopReason != FolderStopInstallFailed {
		t.Fatalf("observer heard %+v", settled.Setup)
	}
	ran := fx.setup.ran
	if len(ran) != 1 || ran[0].Path != fx.song || ran[0].WorkspaceName != "Summer Demos" || ran[0].Offer.ID != view.ID ||
		ran[0].Brief != "Mix the chorus first." {
		t.Fatalf("the run got %+v", ran)
	}
	fx.mu.Lock()
	guarded, leased := fx.guarded, fx.leased
	fx.mu.Unlock()
	if guarded == 0 || leased == 0 {
		t.Fatalf("the build's guard (%d) and lease (%d) vouched for the run", guarded, leased)
	}
	if read, err := fx.service.ReadBuildSetup(ctx, fx.review, view.ID); err != nil || read.Setup == nil || read.Setup.StopReason != FolderStopInstallFailed {
		t.Fatalf("read the run %+v %v", read, err)
	}
	if _, err := fx.service.ReadBuildSetup(ctx, fx.draft, view.ID); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Fatalf("only the build's review reads it: %v", err)
	}

	// A Retry, after the pick is attached again, continues the same offer's run
	// and keeps the name it was planned with.
	fresh := observeSelected(t, fx.folderDigestFixture, fx.service, fx.draft, fx.song)
	retried, err := fx.service.ReviewBuild(ctx, fx.draft, "session-1", fresh.ID, fx.candidate, "Another Name")
	if err != nil || retried.ID != view.ID {
		t.Fatalf("retry review %+v %v", retried, err)
	}
	rebound, _ := fx.service.digest.StoredOffer(ctx, "local", view.ID)
	if rebound.ConversationReview.ObservationID != fresh.ID || rebound.ConversationReview.WorkspaceName != "Summer Demos" {
		t.Fatalf("rebound review %+v", rebound.ConversationReview)
	}
	if _, err := fx.service.StartBuildSetup(ctx, "local", view.ID, plan.Digest, "build-setup-2", "", ""); err != nil {
		t.Fatal(err)
	}
	fx.waitSettled(t)
	if len(fx.setup.ran) != 2 || fx.setup.ran[1].WorkspaceName != "Summer Demos" {
		t.Fatalf("the retry ran %+v", fx.setup.ran)
	}
}

// Build reviews never supply an "Open existing conversation" handoff. A
// pending build remains protected by Review's single-pending-offer check.
func TestFolderBuildSetup_PendingBuildIsNotAConversationHandoff(t *testing.T) {
	fx := newBuildSetupFixture(t)
	ctx := context.Background()
	offer, err := fx.service.ReviewBuild(ctx, fx.draft, "session-1", fx.observation.ID, fx.candidate, "Summer Demos")
	if err != nil {
		t.Fatal(err)
	}
	chat := fx.draft
	chat.DraftID = "chat-draft"
	before, err := fx.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere, err := fx.service.PendingElsewhere(ctx, chat); err != nil || elsewhere != nil {
		t.Fatalf("build exposed as a conversation: %+v %v", elsewhere, err)
	}
	observation, err := fx.service.Observe(ctx, chat, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.Review(ctx, chat, "chat", observation.ID, observation.Projects[0].ID); !errors.Is(err, ErrFolderReviewPending) {
		t.Fatalf("unconfirmed build was not protected: %v", err)
	}
	after, err := fx.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version || after.Pending() == nil || after.Pending().ID != offer.ID {
		t.Fatalf("build offer changed: %+v %v", after.Pending(), err)
	}
}

// An attached song with a plan is the review the composer opens as soon as the
// folder is attached; a folder the user said no to and a project no reviewed
// tool owns are not.
func TestFolderHireCandidate_ARecognizedProjectWithAPlanIsReviewedOnAttach(t *testing.T) {
	fx := newBuildSetupFixture(t)
	ctx := context.Background()
	if got := fx.service.HireCandidate(ctx, fx.draft, fx.observation.ID); got != (FolderHireCandidate{CandidateID: fx.candidate}) || fx.candidate == "" {
		t.Fatalf("song: %+v, want %q", got, fx.candidate)
	}
	// Reading created nothing: no offer and no review.
	if doc, err := fx.store.Read(ctx, "local"); err != nil || len(doc.Offers) != 0 {
		t.Fatalf("the candidate was read with a write: %+v %v", doc.Offers, err)
	}

	// Another card waiting for its answer keeps the review away, and says why.
	waiting, err := fx.service.digest.ScanChip(ctx, "local", "documents")
	if err != nil || waiting.ID == "" || waiting.Status != FolderOfferPending {
		t.Fatalf("a card waiting on Home: %+v %v", waiting, err)
	}
	if got := fx.service.HireCandidate(ctx, fx.draft, fx.observation.ID); got != (FolderHireCandidate{Unavailable: FolderStructureReviewPending}) {
		t.Fatalf("while a card waits: %+v", got)
	}
	closeOffer := func(id string) {
		t.Helper()
		if _, err := fx.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
			for i := range d.Offers {
				if d.Offers[i].ID == id {
					d.Offers[i].Status = FolderOfferClosed
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	closeOffer(waiting.ID)

	// A card waiting on Home for this very song is no reason to refuse: the
	// conversation's review takes its place.
	fx.service.digest.deps.ResolveSelection = func(string) (string, error) { return fx.song, nil }
	home, err := fx.service.digest.ScanSelection(ctx, "local", "song-token")
	if err != nil || home.Status != FolderOfferPending {
		t.Fatalf("the song's card on Home: %+v %v", home, err)
	}
	if got := fx.service.HireCandidate(ctx, fx.draft, fx.observation.ID); got != (FolderHireCandidate{CandidateID: fx.candidate}) {
		t.Fatalf("with the same song's card on Home: %+v", got)
	}
	closeOffer(home.ID)

	// A folder the user said never to ask about again stays unasked.
	if _, err := fx.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
		d.Tombstones = append(d.Tombstones, FolderTombstone{Key: FolderKey(fx.song), Name: "Ori Music Demo", CreatedAt: fx.now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := fx.service.HireCandidate(ctx, fx.draft, fx.observation.ID); got != (FolderHireCandidate{}) {
		t.Fatalf("declined: %+v", got)
	}

	// An Ableton set is a project, but no reviewed tool sets it up.
	bounce := filepath.Join(fx.home, "Music", "Bounce")
	writeFolderFiles(t, bounce, "Bounce.als", "Samples/kick.wav")
	ableton := observeSelected(t, fx.folderDigestFixture, fx.service, fx.draft, bounce)
	if got := fx.service.HireCandidate(ctx, fx.draft, ableton.ID); got != (FolderHireCandidate{}) {
		t.Fatalf("unowned project: %+v", got)
	}
}

// The review this conversation already has open is retired by the next one it
// opens (Review), so it does not keep a newly attached folder's card away;
// a card on Home or in another conversation does.
func TestFolderHireCandidate_OnlyThisConversationsOwnReviewGivesWay(t *testing.T) {
	conversation := foldercontext.Target{UserID: "local", WorkspaceID: "hq-1", AgentName: "Atlas", ConversationID: "c-1"}
	own := &FolderOffer{Status: FolderOfferPending, ConversationReview: &FolderConversationReview{Target: conversation}}
	if !ownConversationReview(own, conversation) {
		t.Fatal("this conversation's own review gives way")
	}
	other := conversation
	other.ConversationID = "c-2"
	if ownConversationReview(own, other) {
		t.Fatal("another conversation's review waits for its answer")
	}
	draft := conversation
	draft.ConversationID, draft.DraftID = "", "draft-1"
	if ownConversationReview(own, draft) {
		t.Fatal("a draft has no review of its own")
	}
	if ownConversationReview(&FolderOffer{Status: FolderOfferPending}, conversation) {
		t.Fatal("a card on Home waits for its answer")
	}
}

func TestFolderBuildSetup_AGuardThatNoLongerVouchesStopsTheSetup(t *testing.T) {
	fx := newBuildSetupFixture(t)
	ctx := context.Background()
	plan, err := fx.service.BuildPlan(ctx, fx.draft, fx.observation.ID, fx.candidate, "")
	if err != nil || !plan.Ready {
		t.Fatalf("plan %+v %v", plan, err)
	}
	view, err := fx.service.ReviewBuild(ctx, fx.draft, "session-1", fx.observation.ID, fx.candidate, "")
	if err != nil {
		t.Fatal(err)
	}
	fx.mu.Lock()
	fx.guardErr = errors.New("the build was abandoned")
	fx.mu.Unlock()
	if _, err := fx.service.StartBuildSetup(ctx, "local", view.ID, plan.Digest, "build-setup-1", "", ""); err == nil {
		t.Fatal("a build that no longer vouches cannot start the run")
	}
	if fx.setup.runCount() != 0 {
		t.Fatal("nothing ran")
	}

	// And a pick that expired cannot be reviewed at all.
	fx.now = fx.now.Add(foldercontext.SelectionTTL)
	if _, err := fx.service.ReviewBuild(ctx, fx.draft, "session-1", fx.observation.ID, fx.candidate, ""); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("an expired pick: %v", err)
	}
}
