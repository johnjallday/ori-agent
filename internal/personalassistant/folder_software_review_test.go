package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// fakeSoftwareSetup stands in for the host's built-in software recipe. Its
// plan is built only from what the service hands it, the way the host builds
// one, and its lines never name the model: a changed model is a changed intent
// under the same words.
type fakeSoftwareSetup struct {
	mu        sync.Mutex
	available bool
	model     string
	recipe    string
	malformed bool
	planErr   error
	plans     int
	runs      []FolderSetupRequest
	finish    func() FolderSetupUpdate
}

func (f *fakeSoftwareSetup) SoftwareSetupAvailable() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.available
}

func (f *fakeSoftwareSetup) Plan(_ context.Context, req FolderSetupRequest) (FolderSetupPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plans++
	if f.planErr != nil {
		return FolderSetupPlan{}, f.planErr
	}
	if req.Software == nil || req.Software.Review == nil || req.Software.Review.EvidenceRevision == "" {
		return FolderSetupPlan{}, errors.New("no review to plan from")
	}
	review := req.Software.Review
	name, assistant := review.WorkspaceName, review.AssistantName
	if name == "" {
		name = req.Offer.Subject.Name
	}
	if assistant == "" {
		assistant = "Development Assistant"
	}
	intent := &FolderSoftwareIntent{
		Outcome: FolderSoftwareCreate, WorkspaceName: name, Template: "code-project", TemplateRevision: "template-1",
		SubjectKey: req.Offer.Subject.Key, SubjectIdentity: req.Software.Identity,
		Assistant: assistant, AssistantAction: FolderSoftwareCreate, Model: f.model, RosterRevision: "roster-" + f.model,
		Assessment: FolderSoftwareSeed, EvidenceRevision: review.EvidenceRevision,
		Scope: FolderSoftwareScopeReadOnly, Planning: true,
	}
	workspace := "Creates a Code project workspace named " + name
	if len(req.Software.Existing) == 1 {
		intent.Outcome, intent.WorkspaceID, intent.Template, intent.TemplateRevision = FolderSoftwareReuse, req.Software.Existing[0].WorkspaceID, "", ""
		workspace = "Uses your existing workspace " + req.Software.Existing[0].Name
	}
	plan := NewFolderSetupPlan([]FolderPlanLine{
		{Kind: FolderPlanWorkspace, Name: workspace},
		{Kind: FolderPlanFolder, Name: "Links " + req.Offer.Subject.Name + " where it is"},
		{Kind: FolderPlanAgents, Name: "Adds one development assistant, " + assistant},
		{Kind: FolderPlanTask, Name: "Queues a first assessment for you to start"},
	})
	recipe := f.recipe
	if recipe == "" {
		recipe = folderdigest.BuiltinSoftwareProject
	}
	plan.Intent = FolderSetupIntent{Recipe: recipe, RecipeVersion: 1, Software: intent}
	if f.malformed {
		plan.Intent.Software = nil
	}
	plan.Prerequisites = []FolderPrerequisite{{ID: "model", Label: "Assistant's model", State: FolderPrerequisiteConfigured, Required: true}}
	return plan.Stamped(), nil
}

func (f *fakeSoftwareSetup) Run(ctx context.Context, req FolderSetupRequest) error {
	f.mu.Lock()
	f.runs = append(f.runs, req)
	finish := f.finish
	f.mu.Unlock()
	lines := append([]FolderPlanLine(nil), req.Plan.Lines...)
	if err := req.Update(ctx, FolderSetupUpdate{Lines: lines, Status: FolderSetupRunning}); err != nil {
		return err
	}
	update := FolderSetupUpdate{Lines: lines, Status: FolderSetupDone}
	if finish != nil {
		update = finish()
		update.Lines = lines
	}
	return req.Update(ctx, update)
}

func (f *fakeSoftwareSetup) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

func (f *fakeSoftwareSetup) lastRun() FolderSetupRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[len(f.runs)-1]
}

func (f *fakeSoftwareSetup) set(change func(*fakeSoftwareSetup)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// inspect stands in for the bounded inspector: its revision follows the
// README's bytes, so a changed project is a changed review.
func (s *softwareFixture) inspect(_ context.Context, path string) (FolderSoftwareInspection, error) {
	s.inspected = append(s.inspected, path)
	content, err := os.ReadFile(filepath.Join(path, "README.md"))
	review := FolderSoftwareReview{InspectedAt: s.now, Stack: []string{"Go"}}
	if err == nil {
		review.Files = []string{"README.md"}
	} else {
		content = []byte("no readme")
		review.Gaps = []FolderSoftwareGap{{Code: "no_readme"}}
	}
	sum := sha256.Sum256(content)
	review.EvidenceRevision = hex.EncodeToString(sum[:])
	return FolderSoftwareInspection{Review: review, Evidence: FolderSoftwareEvidence{
		Purpose: "What " + filepath.Base(path) + " says it is.", PurposeSource: "README.md",
		Commands: []FolderSoftwareCommand{{Kind: "test", Command: "make test", Source: "Makefile", Declared: true}},
	}}, nil
}

// verify stands in for the host's canonical re-read of a finished setup.
func (s *softwareFixture) verify(_ context.Context, userID string, offer FolderOffer, path, identity string) (FolderCreateResult, error) {
	s.verified = append(s.verified, userID+"|"+path+"|"+identity)
	if s.verifyErr != nil {
		return FolderCreateResult{}, s.verifyErr
	}
	name := offer.Setup.Intent.Software.WorkspaceName
	return FolderCreateResult{
		WorkspaceID: "ws-" + strings.ToLower(name), Route: "/workspaces/" + strings.ToLower(strings.ReplaceAll(name, " ", "-")),
		Created: offer.Setup.Intent.Software.Outcome == FolderSoftwareCreate,
		Receipt: []FolderReceiptRow{{Kind: "workspace", Name: name}, {Kind: "folder", Name: offer.Subject.Name, Detail: "linked as primary"}},
	}, nil
}

// review presses Review setup and returns the offer with its plan.
func (s *softwareFixture) review(t *testing.T, offerID string) FolderOfferView {
	t.Helper()
	view, err := s.service.ReviewSoftware(context.Background(), "local", offerID, FolderSoftwareReviewInput{RequestID: "review-" + offerID + "-" + time.Now().Format("150405.000000000")})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if view.Plan == nil || view.Plan.Digest == "" || view.Software == nil || view.Software.Review == nil {
		t.Fatalf("a review returned no plan: plan=%+v software=%+v reason=%q", view.Plan, view.Software, view.SetupUnavailableReason)
	}
	return view
}

// setUp presses Set up on a reviewed offer, waits for the run to settle, and
// returns what the host's run was given.
func (s *softwareFixture) setUp(t *testing.T, reviewed FolderOfferView) FolderSetupRequest {
	t.Helper()
	before := s.setup.runCount()
	if _, err := s.service.StartSetup(context.Background(), "local", reviewed.ID, FolderSetupInput{RequestID: "setup-" + reviewed.ID, PlanDigest: reviewed.Plan.Digest}); err != nil {
		t.Fatalf("set up: %v", err)
	}
	waitFor(t, "the software setup to settle", func() bool {
		offer := s.stored(t, reviewed.ID)
		return s.setup.runCount() > before && !s.service.isRunning(reviewed.ID) &&
			(offer.Status == FolderOfferResolved || (offer.Setup != nil && offer.Setup.Status == FolderSetupStopped))
	})
	return s.setup.lastRun()
}

// Showing a folder reads nothing. Review setup is the one click that reads the
// project's files, and even it creates nothing and runs nothing.
func TestFolderSoftwareReview_OnlyTheReviewClickInspectsAndNothingIsCreated(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	view := s.pick(t, s.set.GoNode)
	for range 3 {
		if _, err := s.service.CurrentOffer(ctx, "local", view.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.inspected) != 0 || view.Plan != nil || view.Software.Review != nil {
		t.Fatalf("showing the folder inspected it or planned a setup: inspected=%v plan=%+v", s.inspected, view.Plan)
	}
	if view.CreateAvailable {
		t.Fatal("the generic creator is still offered for a software project")
	}

	reviewed := s.review(t, view.ID)
	if len(s.inspected) != 1 || s.inspected[0] != s.set.GoNode {
		t.Fatalf("the review inspected %v, want the selected folder once", s.inspected)
	}
	review := reviewed.Software.Review
	if !review.EvidenceHeld || review.Purpose != "What widget says it is." || len(review.Commands) != 1 ||
		review.EvidenceRevision == "" || strings.Join(review.Files, ",") != "README.md" {
		t.Fatalf("review = %+v", review)
	}
	if len(reviewed.Plan.Lines) != 4 || len(reviewed.Plan.Prerequisites) != 1 || !reviewed.Software.SetupAvailable {
		t.Fatalf("plan = %+v available=%t", reviewed.Plan, reviewed.Software.SetupAvailable)
	}
	noPathIn(t, s.home, reviewed)
	if s.setup.runCount() != 0 || len(s.creator.requests) != 0 || len(s.verified) != 0 {
		t.Fatal("a review ran, created or verified something")
	}
	// Reading the offer again does not read the project again.
	if _, err := s.service.CurrentOffer(ctx, "local", view.ID); err != nil || len(s.inspected) != 1 {
		t.Fatalf("a read of the offer inspected the project: %v %v", s.inspected, err)
	}

	// The excerpts are held in memory only. After a restart the stored facts and
	// the plan remain; the excerpts are gone until the user reviews again.
	stored := s.stored(t, view.ID)
	if stored.SoftwareReview == nil || stored.SoftwareReview.EvidenceRevision != review.EvidenceRevision {
		t.Fatalf("the review was not stored: %+v", stored.SoftwareReview)
	}
	restarted := s.newService()
	restarted.deps.Setup, restarted.deps.InspectSoftware, restarted.deps.SoftwareProject = s.setup, s.inspect, s.verify
	restarted.deps.ExistingProjects = s.service.deps.ExistingProjects
	restarted.rememberPath(view.ID, s.set.GoNode) // the user picked the folder again
	after, err := restarted.CurrentOffer(ctx, "local", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Offer.Software.Review == nil || after.Offer.Software.Review.EvidenceHeld || after.Offer.Software.Review.FolderSoftwareEvidence != nil ||
		after.Offer.Plan == nil || after.Offer.Plan.Digest != reviewed.Plan.Digest {
		t.Fatalf("after a restart: review=%+v plan=%+v", after.Offer.Software.Review, after.Offer.Plan)
	}
}

// A changed name is a changed plan, an invalid name is refused, and there is
// nothing to adjust before a review exists.
func TestFolderSoftwareReview_AdjustingNamesChangesThePlanAndNothingElse(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	view := s.pick(t, s.set.GoNode)
	name := "Widget service"
	if _, err := s.service.AdjustSoftware(ctx, "local", view.ID, FolderSoftwareReviewInput{RequestID: "early", WorkspaceName: &name}); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("adjusting before a review: %v", err)
	}
	reviewed := s.review(t, view.ID)
	inspections := len(s.inspected)

	adjusted, err := s.service.AdjustSoftware(ctx, "local", view.ID, FolderSoftwareReviewInput{RequestID: "rename", WorkspaceName: &name})
	if err != nil {
		t.Fatal(err)
	}
	if adjusted.Plan == nil || adjusted.Plan.Digest == reviewed.Plan.Digest || !strings.Contains(adjusted.Plan.Lines[0].Name, "Widget service") {
		t.Fatalf("the renamed plan: %+v", adjusted.Plan)
	}
	assistant := "Pat"
	renamed, err := s.service.AdjustSoftware(ctx, "local", view.ID, FolderSoftwareReviewInput{RequestID: "assistant", AssistantName: &assistant})
	if err != nil || renamed.Plan.Digest == adjusted.Plan.Digest || !strings.Contains(renamed.Plan.Lines[2].Name, "Pat") ||
		!strings.Contains(renamed.Plan.Lines[0].Name, "Widget service") {
		t.Fatalf("the assistant's name: %+v %v", renamed.Plan, err)
	}
	// The same request again returns the same review.
	if replay, err := s.service.AdjustSoftware(ctx, "local", view.ID, FolderSoftwareReviewInput{RequestID: "assistant", AssistantName: &assistant}); err != nil || replay.Plan.Digest != renamed.Plan.Digest {
		t.Fatalf("replayed adjustment: %v", err)
	}
	// An empty name returns to the suggestion.
	empty := ""
	back, err := s.service.AdjustSoftware(ctx, "local", view.ID, FolderSoftwareReviewInput{RequestID: "back", WorkspaceName: &empty, AssistantName: &empty})
	if err != nil || back.Plan.Digest != reviewed.Plan.Digest {
		t.Fatalf("returning to the suggestion: %v", err)
	}
	for _, bad := range []string{"a/b", "../up", "line\nbreak", strings.Repeat("x", 121)} {
		value := bad
		if _, err := s.service.AdjustSoftware(ctx, "local", view.ID, FolderSoftwareReviewInput{RequestID: "bad-" + bad[:1], WorkspaceName: &value}); !errors.Is(err, ErrValidation) {
			t.Errorf("the name %q: %v", bad, err)
		}
	}
	if _, err := s.service.AdjustSoftware(ctx, "local", view.ID, FolderSoftwareReviewInput{RequestID: "nothing"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("an adjustment of nothing: %v", err)
	}
	if _, err := s.service.AdjustSoftware(ctx, "someone-else", view.ID, FolderSoftwareReviewInput{RequestID: "foreign", WorkspaceName: &name}); err == nil {
		t.Fatal("another user adjusted this user's review")
	}
	if len(s.inspected) != inspections || s.setup.runCount() != 0 || len(s.creator.requests) != 0 {
		t.Fatal("adjusting a name read the project, ran a setup or created something")
	}
	// A review pressed again keeps the user's names.
	if _, err := s.service.AdjustSoftware(ctx, "local", view.ID, FolderSoftwareReviewInput{RequestID: "keep", WorkspaceName: &name}); err != nil {
		t.Fatal(err)
	}
	if again := s.review(t, view.ID); !strings.Contains(again.Plan.Lines[0].Name, "Widget service") {
		t.Fatalf("a second review lost the user's name: %+v", again.Plan.Lines[0])
	}
}

// The digest the user confirms covers the intent behind the words. A different
// model under identical lines is a different review, and the old confirmation
// starts nothing.
func TestFolderSoftwareReview_ChangedIntentUnderTheSameWordsIsStale(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	view := s.pick(t, s.set.GoNode)
	reviewed := s.review(t, view.ID)

	s.setup.set(func(f *fakeSoftwareSetup) { f.model = "another-model" })
	current, err := s.service.CurrentOffer(ctx, "local", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range current.Offer.Plan.Lines {
		if line != reviewed.Plan.Lines[i] {
			t.Fatalf("the fixture's wording changed with the model: %+v", line)
		}
	}
	if current.Offer.Plan.Digest == reviewed.Plan.Digest {
		t.Fatal("a changed model kept the digest of the review that was shown")
	}
	stale, err := s.service.StartSetup(ctx, "local", view.ID, FolderSetupInput{RequestID: "stale", PlanDigest: reviewed.Plan.Digest})
	if !errors.Is(err, ErrFolderPlanChanged) || stale.Plan == nil || stale.Plan.Digest != current.Offer.Plan.Digest {
		t.Fatalf("a stale confirmation: %v plan=%+v", err, stale.Plan)
	}
	if s.setup.runCount() != 0 || s.stored(t, view.ID).Status != FolderOfferPending {
		t.Fatal("a stale confirmation started a run or decided the offer")
	}
	// Prerequisites are read fresh and are not part of what is confirmed.
	bare := NewFolderSetupPlan(reviewed.Plan.Lines)
	bare.Intent = FolderSetupIntent{Recipe: folderdigest.BuiltinSoftwareProject, RecipeVersion: 1, Software: &FolderSoftwareIntent{}}
	with := bare
	with.Prerequisites = []FolderPrerequisite{{ID: "github", State: FolderPrerequisiteAbsent}}
	if bare.Stamped().Digest != with.Stamped().Digest {
		t.Fatal("an optional prerequisite changed the digest")
	}
	if bare.Stamped().Digest == NewFolderSetupPlan(reviewed.Plan.Lines).Stamped().Digest {
		t.Fatal("a recipe's intent is not in its digest")
	}
}

// Set up is an explicit click, so the project's files are read once more. A
// review made on files that have since changed is not confirmed; the card gets
// the review of the project as it is now.
func TestFolderSoftwareReview_AProjectChangedSinceTheReviewNeedsAFreshOne(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	view := s.pick(t, s.set.Clone)
	reviewed := s.review(t, view.ID)
	if err := os.WriteFile(filepath.Join(s.set.Clone, "README.md"), []byte("# Widget\n\nRewritten since the review.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, err := s.service.StartSetup(ctx, "local", view.ID, FolderSetupInput{RequestID: "old-review", PlanDigest: reviewed.Plan.Digest})
	if !errors.Is(err, ErrFolderPlanChanged) {
		t.Fatalf("a review of changed files: %v", err)
	}
	if stale.Software.Review.EvidenceRevision == reviewed.Software.Review.EvidenceRevision || stale.Plan == nil || stale.Plan.Digest == reviewed.Plan.Digest {
		t.Fatalf("the card was not given the fresh review: %+v", stale.Software.Review)
	}
	if s.setup.runCount() != 0 {
		t.Fatal("a stale review started a run")
	}
	// The fresh review, confirmed, runs.
	run := s.setUp(t, stale)
	if run.Plan.Intent.Software.EvidenceRevision != stale.Software.Review.EvidenceRevision {
		t.Fatalf("the run was held to evidence %q", run.Plan.Intent.Software.EvidenceRevision)
	}
}

func TestFolderSoftwareSetup_RunsTheReviewedIntentAndResolvesOnlyWhenVerified(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	var outcomes []string
	s.service.deps.OnOutcome = func(_ context.Context, _ string, offer FolderOffer) {
		outcomes = append(outcomes, offer.Outcome.WorkspaceID)
	}
	view := s.pick(t, s.set.GoNode)
	reviewed := s.review(t, view.ID)
	identity := s.stored(t, view.ID).SubjectIdentity

	run := s.setUp(t, reviewed)
	intent := run.Plan.Intent
	if run.Path != s.set.GoNode || run.Software == nil || run.Software.Identity != identity ||
		intent.Recipe != folderdigest.BuiltinSoftwareProject || intent.Software == nil ||
		intent.Software.SubjectIdentity != identity || intent.Software.Outcome != FolderSoftwareCreate ||
		intent.Integration != "" || intent.Provider != "" {
		t.Fatalf("run request: path=%q software=%+v intent=%+v", run.Path, run.Software, intent)
	}
	stored := s.stored(t, view.ID)
	if stored.Status != FolderOfferResolved || stored.Outcome == nil || stored.Outcome.WorkspaceID != "ws-widget" ||
		stored.Outcome.Route != "/workspaces/widget" || stored.Outcome.Existing || len(stored.Outcome.Receipt) != 2 {
		t.Fatalf("resolved offer: %+v outcome=%+v", stored.Status, stored.Outcome)
	}
	if len(s.verified) != 1 || s.verified[0] != "local|"+s.set.GoNode+"|"+identity || len(outcomes) != 1 {
		t.Fatalf("verified=%v outcomes=%v", s.verified, outcomes)
	}
	if len(s.creator.requests) != 0 {
		t.Fatal("the generic creator was used")
	}
	// The same click again returns the result and starts nothing.
	again, err := s.service.StartSetup(ctx, "local", view.ID, FolderSetupInput{RequestID: "setup-" + view.ID, PlanDigest: reviewed.Plan.Digest})
	if err != nil || again.Status != FolderOfferResolved || s.setup.runCount() != 1 {
		t.Fatalf("replayed click: %v status=%s runs=%d", err, again.Status, s.setup.runCount())
	}
	// The modal's path cannot claim a workspace for a software offer.
	other := s.pick(t, s.set.RustPython)
	if _, err := s.service.Resolve(ctx, "local", other.ID, FolderResolveInput{WorkspaceID: "ws-any", RequestID: "modal"}); !errors.Is(err, ErrFolderOfferDecided) && !errors.Is(err, ErrFolderWorkspaceRefused) {
		t.Fatalf("the modal's resolve on a software offer: %v", err)
	}
}

// A run that says it finished is not believed until the workspace is re-read
// from canonical state. One that cannot be verified never resolves the offer.
func TestFolderSoftwareSetup_AnUnverifiedRunDoesNotResolveTheOffer(t *testing.T) {
	s := newSoftwareFixture(t)
	s.verifyErr = errors.New("no workspace is bound to this folder")
	view := s.pick(t, s.set.GoNode)
	s.setUp(t, s.review(t, view.ID))
	stored := s.stored(t, view.ID)
	if stored.Status != FolderOfferAwaitingOutcome || stored.Outcome == nil || stored.Outcome.WorkspaceID != "" ||
		stored.Setup == nil || stored.Setup.Status != FolderSetupStopped || stored.Setup.StopReason != FolderStopFailed {
		t.Fatalf("an unverified run: status=%s setup=%+v outcome=%+v", stored.Status, stored.Setup, stored.Outcome)
	}

	// A run that stops (no model yet) keeps what it finished and is resumable
	// with the digest it was confirmed on.
	s2 := newSoftwareFixture(t)
	s2.setup.set(func(f *fakeSoftwareSetup) {
		f.finish = func() FolderSetupUpdate {
			return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel}
		}
	})
	view = s2.pick(t, s2.set.GoNode)
	reviewed := s2.review(t, view.ID)
	s2.setUp(t, reviewed)
	stopped := s2.stored(t, view.ID)
	if stopped.Setup.StopReason != FolderStopNeedsModel || stopped.Status != FolderOfferAwaitingOutcome || len(s2.verified) != 0 {
		t.Fatalf("a stopped run: %+v verified=%v", stopped.Setup, s2.verified)
	}
	s2.setup.set(func(f *fakeSoftwareSetup) { f.finish = nil })
	if _, err := s2.service.StartSetup(context.Background(), "local", view.ID, FolderSetupInput{RequestID: "resume", PlanDigest: stopped.Setup.PlanDigest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resumed setup to resolve", func() bool { return s2.stored(t, view.ID).Status == FolderOfferResolved })
	if s2.setup.runCount() != 2 || len(s2.inspected) != 2 {
		// One inspection for the review and one for the first Set up; a resumed
		// run is held to the plan it was confirmed on and reads nothing again.
		t.Fatalf("resumed run: runs=%d inspections=%d", s2.setup.runCount(), len(s2.inspected))
	}
}

func TestFolderSoftwareSetup_RefusedStartsRunNothing(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	start := func(offerID, digest, request string) error {
		_, err := s.service.StartSetup(ctx, "local", offerID, FolderSetupInput{RequestID: request, PlanDigest: digest})
		return err
	}

	// Without a review there is no plan to confirm.
	unreviewed := s.pick(t, s.set.GoNode)
	if err := start(unreviewed.ID, strings.Repeat("0", 64), "no-review"); !errors.Is(err, ErrFolderReviewRequired) {
		t.Fatalf("set up without a review: %v", err)
	}
	// Several projects need a choice first.
	container := s.pick(t, s.set.Container)
	if err := start(container.ID, strings.Repeat("0", 64), "no-choice"); !errors.Is(err, ErrFolderOfferChoice) {
		t.Fatalf("set up without a choice: %v", err)
	}
	if _, err := s.service.ReviewSoftware(ctx, "local", container.ID, FolderSoftwareReviewInput{RequestID: "review-no-choice"}); !errors.Is(err, ErrFolderOfferChoice) {
		t.Fatalf("a review without a choice: %v", err)
	}

	// A folder replaced after the review is not the folder that was reviewed.
	clone := s.pick(t, s.set.Clone)
	reviewed := s.review(t, clone.ID)
	if err := softwarefixtureReplace(s.set.Clone); err != nil {
		t.Fatal(err)
	}
	if err := start(clone.ID, reviewed.Plan.Digest, "replaced"); !errors.Is(err, ErrFolderPathLost) {
		t.Fatalf("set up on a replaced folder: %v", err)
	}

	// A host that cannot carry the recipe out offers no Set up and starts none.
	rust := s.pick(t, s.set.RustPython)
	ready := s.review(t, rust.ID)
	s.setup.set(func(f *fakeSoftwareSetup) { f.available = false })
	off, err := s.service.CurrentOffer(ctx, "local", rust.ID)
	if err != nil || off.Offer.Software.SetupAvailable {
		t.Fatalf("an unavailable host still offers Set up: %v", err)
	}
	if err := start(rust.ID, ready.Plan.Digest, "unavailable"); !errors.Is(err, ErrFolderOutcomeUnavailable) {
		t.Fatalf("set up on an unavailable host: %v", err)
	}
	s.setup.set(func(f *fakeSoftwareSetup) { f.available = true })

	// An intent the service does not recognize is refused, not run as something
	// else: an unknown recipe, and a recipe with no promise behind it.
	for name, change := range map[string]func(*fakeSoftwareSetup){
		"unknown recipe":  func(f *fakeSoftwareSetup) { f.recipe = "format_disk" },
		"missing promise": func(f *fakeSoftwareSetup) { f.malformed = true },
	} {
		s.setup.set(change)
		current, err := s.service.CurrentOffer(ctx, "local", rust.ID)
		if err != nil || current.Offer.Plan == nil {
			t.Fatalf("%s: no plan to confirm: %v", name, err)
		}
		if err := start(rust.ID, current.Offer.Plan.Digest, "bad-"+name[:3]); err == nil {
			t.Errorf("%s was started", name)
		}
		s.setup.set(func(f *fakeSoftwareSetup) { f.recipe, f.malformed = "", false })
	}
	if s.setup.runCount() != 0 || len(s.verified) != 0 || len(s.creator.requests) != 0 {
		t.Fatalf("a refused start ran something: runs=%d verified=%v", s.setup.runCount(), s.verified)
	}
	for _, id := range []string{unreviewed.ID, container.ID, clone.ID, rust.ID} {
		if status := s.stored(t, id).Status; status != FolderOfferPending && status != FolderOfferLater {
			t.Errorf("offer %s was decided by a refused start: %s", id, status)
		}
	}
}

// A workspace linked to the folder some other way can be completed by a
// review; one already set up for it cannot be taken over.
func TestFolderSoftwareReview_ExistingWorkspacesDecideWhetherThereIsAPlan(t *testing.T) {
	s := newSoftwareFixture(t)
	legacy := FolderExistingProject{WorkspaceID: "ws-legacy", Name: "Old widget", Route: "/workspaces/old-widget"}
	s.existing[s.set.GoNode] = []FolderExistingProject{legacy}
	view := s.pick(t, s.set.GoNode)
	reviewed := s.review(t, view.ID)
	if !strings.Contains(reviewed.Plan.Lines[0].Name, "Uses your existing workspace Old widget") {
		t.Fatalf("a workspace linked another way: %+v", reviewed.Plan.Lines[0])
	}
	run := s.setUp(t, reviewed)
	if intent := run.Plan.Intent.Software; intent.Outcome != FolderSoftwareReuse || intent.WorkspaceID != "ws-legacy" {
		t.Fatalf("reuse intent: %+v", intent)
	}
	if stored := s.stored(t, view.ID); stored.Outcome == nil || !stored.Outcome.Existing {
		t.Fatalf("a reused workspace was reported as created: %+v", stored.Outcome)
	}

	for name, existing := range map[string][]FolderExistingProject{
		"already set up":     {{WorkspaceID: "ws-a", Name: "Gauge", Route: "/workspaces/gauge", Software: true}},
		"replaced directory": {{WorkspaceID: "ws-a", Name: "Gauge", Route: "/workspaces/gauge", Software: true, Replaced: true}},
		"several":            {{WorkspaceID: "ws-a", Name: "A", Route: "/workspaces/a"}, {WorkspaceID: "ws-b", Name: "B", Route: "/workspaces/b"}},
	} {
		s.existing[s.set.RustPython] = existing
		offer := s.pick(t, s.set.RustPython)
		got, err := s.service.ReviewSoftware(context.Background(), "local", offer.ID, FolderSoftwareReviewInput{RequestID: "review-" + name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Plan != nil || len(got.Software.Existing) != len(existing) {
			t.Errorf("%s: a plan was offered over %d existing workspaces", name, len(got.Software.Existing))
		}
	}
}

// softwarefixtureReplace is softwarefixture.Replace, named here so this file's
// imports stay small.
func softwarefixtureReplace(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module replaced\n"), 0o600)
}

// Music's one-card plans are untouched: a plan with no recipe has the digest
// it always had, and a run with no recipe needs none.
func TestFolderSetupPlan_IntegrationPlansKeepTheirDigestAndValidation(t *testing.T) {
	lines := oneCardLines()
	plan := NewFolderSetupPlan(lines)
	plan.Intent = FolderSetupIntent{Integration: FolderInstallInstall, IntegrationPlugin: "ori-reaper", IntegrationVersion: "0.9.0"}
	if plan.Stamped().Digest != FolderPlanDigest(lines) {
		t.Fatal("an integration plan's digest changed")
	}
	run := FolderSetupRun{PlanDigest: plan.Stamped().Digest, Lines: lines, Status: FolderSetupRunning, Intent: plan.Intent}
	if err := validateFolderSetupRun(run); err != nil {
		t.Fatalf("an integration run no longer validates: %v", err)
	}
	for name, intent := range map[string]FolderSetupIntent{
		"unknown recipe":         {Recipe: "something_else", RecipeVersion: 1},
		"recipe with no intent":  {Recipe: folderdigest.BuiltinSoftwareProject, RecipeVersion: 1},
		"intent with no recipe":  {Software: &FolderSoftwareIntent{}},
		"version with no recipe": {RecipeVersion: 2},
		"malformed software":     {Recipe: folderdigest.BuiltinSoftwareProject, RecipeVersion: 1, Software: &FolderSoftwareIntent{Outcome: "delete"}},
	} {
		run.Intent = intent
		if err := validateFolderSetupRun(run); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
