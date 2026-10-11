package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

// softwareFixture is a folder-digest fixture with the software fixtures built
// under its home, a recording creator, and a recording existing-project lookup.
type softwareFixture struct {
	*folderDigestFixture
	set     softwarefixture.Set
	creator *fakeFolderCreator
	// existing answers the lookup by canonical folder path; lookups records
	// every "path|identity" the service asked about.
	existing    map[string][]FolderExistingProject
	existingErr error
	lookups     []string
}

func newSoftwareFixture(t *testing.T) *softwareFixture {
	t.Helper()
	f := newFolderDigestFixture(t)
	root := filepath.Join(f.home, "Projects")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	set, err := softwarefixture.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { softwarefixture.Restore(set.Unreadable) })
	s := &softwareFixture{folderDigestFixture: f, set: set, creator: &fakeFolderCreator{}, existing: map[string][]FolderExistingProject{}}
	// Scans read the fixture's clock, so a conversation's selection is as old
	// as the service believes it is.
	f.service.deps.Scan = func(root string) (folderdigest.Result, error) {
		return folderdigest.Scan(root, folderdigest.Options{Now: func() time.Time { return f.now }})
	}
	f.service.deps.ScanObservation = func(root string) (folderdigest.Result, error) {
		return folderdigest.Scan(root, folderdigest.Options{CaptureTree: true, Now: func() time.Time { return f.now }})
	}
	f.service.deps.Creator = s.creator
	f.service.deps.BlueprintAvailable = func(id string) bool { return id == "code-project" }
	f.service.deps.ExistingProjects = func(_ context.Context, userID, path, identity string) ([]FolderExistingProject, error) {
		s.lookups = append(s.lookups, path+"|"+identity)
		if userID != "local" {
			return nil, nil
		}
		return s.existing[path], s.existingErr
	}
	return s
}

// pick shows the service one folder the way the native dialog does.
func (s *softwareFixture) pick(t *testing.T, dir string) FolderOfferView {
	t.Helper()
	s.service.deps.Picker = fakeFolderPicker{path: dir, chosen: true}
	view, err := s.service.ScanPicked(context.Background(), "local")
	if err != nil || view == nil {
		t.Fatalf("scan %s: %v", filepath.Base(dir), err)
	}
	return *view
}

func (s *softwareFixture) stored(t *testing.T, id string) FolderOffer {
	t.Helper()
	offer, err := s.service.StoredOffer(context.Background(), "local", id)
	if err != nil {
		t.Fatal(err)
	}
	return offer
}

func yesCreate(requestID string) FolderDecisionInput {
	return FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, Create: true, RequestID: requestID}
}

// noPathIn fails when a browser-bound value carries a filesystem location.
func noPathIn(t *testing.T, home string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{home, "folder_key", "identity", `"key"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the view leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestFolderSoftware_ARecognizedProjectIsDescribedAndNothingIsCreated(t *testing.T) {
	s := newSoftwareFixture(t)
	for _, tc := range []struct {
		name       string
		dir        string
		marker     string
		repository string
	}{
		{"ordinary checkout", s.set.GoNode, "Go module", "directory"},
		{"linked worktree", s.set.Worktree, "Go module", "file"},
		{"rust and python", s.set.RustPython, "Rust crate", "directory"},
		{"no version control", s.set.NonGit, "Node.js package", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := softwarefixture.Hash(tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			view := s.pick(t, tc.dir)
			if view.Verdict != "project" || !view.Subject.IsRoot || view.Subject.Shape != "code" {
				t.Fatalf("offer = %+v", view)
			}
			software := view.Software
			if software == nil || software.Recipe != "software_project" || software.Marker != tc.marker || software.Repository != tc.repository {
				t.Fatalf("software = %+v", software)
			}
			if software.NeedsCandidate || len(software.Candidates) != 0 || len(software.Existing) != 0 || software.ExistingUnavailable {
				t.Fatalf("one project needs no choice and has no workspace yet: %+v", software)
			}
			if view.Enclosing != nil {
				t.Fatalf("a folder under no repository reported one: %+v", view.Enclosing)
			}
			noPathIn(t, s.home, view)
			stored := s.stored(t, view.ID)
			// A repository's packages, docs and scripts are not queued as projects.
			if len(stored.Queue) != 0 {
				t.Fatalf("the project's subfolders were queued as offers: %+v", stored.Queue)
			}
			if stored.SubjectIdentity == "" {
				t.Fatal("the folder the offer is about was not witnessed")
			}
			after, err := softwarefixture.Hash(tc.dir)
			if err != nil || after != before {
				t.Fatalf("showing the folder changed it: %v", err)
			}
		})
	}
	if len(s.creator.requests) != 0 {
		t.Fatalf("showing folders created %d workspaces", len(s.creator.requests))
	}
}

func TestFolderSoftware_SeveralProjectsNeedAChoiceBeforeAnythingIsSetUp(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	view := s.pick(t, s.set.Container)
	software := view.Software
	if software == nil || !software.NeedsCandidate || len(software.Candidates) != 2 || !software.Candidates[0].Current || software.Candidates[1].Current {
		t.Fatalf("software = %+v", software)
	}
	if view.CreateAvailable {
		t.Fatal("a setup was offered before the user chose a project")
	}
	noPathIn(t, s.home, view)

	// A yes cannot stand in for the choice.
	if _, err := s.service.Decide(ctx, "local", view.ID, yesCreate("req-guess")); !errors.Is(err, ErrFolderOfferChoice) {
		t.Fatalf("a yes without a choice: %v", err)
	}
	// A candidate the offer never listed is refused, whatever it names.
	for _, forged := range []string{"api", s.set.ContainerSite, "../site", strings.Repeat("a", 16)} {
		if _, err := s.service.ChooseCandidate(ctx, "local", view.ID, FolderCandidateInput{CandidateID: forged, RequestID: "req-forged-" + forged[:1]}); !errors.Is(err, ErrValidation) {
			t.Errorf("forged candidate %q: %v", forged, err)
		}
	}
	// Another user cannot choose for this one.
	if _, err := s.service.ChooseCandidate(ctx, "someone-else", view.ID, FolderCandidateInput{CandidateID: software.Candidates[1].ID, RequestID: "req-foreign"}); err == nil {
		t.Fatal("another user chose this user's candidate")
	}
	if len(s.creator.requests) != 0 {
		t.Fatalf("a refused request created a workspace: %+v", s.creator.requests)
	}

	second := software.Candidates[1]
	chosen, err := s.service.ChooseCandidate(ctx, "local", view.ID, FolderCandidateInput{CandidateID: second.ID, RequestID: "req-choose"})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Subject.Name != second.Name || chosen.Software == nil || chosen.Software.NeedsCandidate || len(chosen.Software.Candidates) != 0 {
		t.Fatalf("after the choice: %+v software=%+v", chosen.Subject, chosen.Software)
	}
	if !chosen.CreateAvailable {
		t.Fatal("the chosen project cannot be set up")
	}
	// The same click again changes nothing.
	replay, err := s.service.ChooseCandidate(ctx, "local", view.ID, FolderCandidateInput{CandidateID: second.ID, RequestID: "req-choose"})
	if err != nil || replay.Subject.Name != second.Name {
		t.Fatalf("replayed choice: %+v %v", replay.Subject, err)
	}
	stored := s.stored(t, view.ID)
	var queuedProjects []string
	for _, queued := range stored.Queue {
		if queued.Kind == FolderChoiceProject {
			queuedProjects = append(queuedProjects, queued.Name)
		}
	}
	if len(queuedProjects) != 1 || queuedProjects[0] != software.Candidates[0].Name || stored.SubjectIdentity == "" {
		t.Fatalf("the project not chosen should stay queued: %+v", stored.Queue)
	}
	// A settled choice is not reopened by a second, different request.
	if _, err := s.service.ChooseCandidate(ctx, "local", view.ID, FolderCandidateInput{CandidateID: software.Candidates[0].ID, RequestID: "req-again"}); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("a second choice: %v", err)
	}

	// Setup now links exactly the chosen folder, not the folder that held it.
	if _, err := s.service.Decide(ctx, "local", view.ID, yesCreate("req-setup")); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(s.set.Container, second.Name)
	if len(s.creator.requests) != 1 || s.creator.requests[0].Path != want || s.creator.requests[0].Name != second.Name {
		t.Fatalf("create request = %+v, want the folder %q", s.creator.requests, second.Name)
	}
}

func TestFolderSoftware_ASubfolderOfARepositoryIsExplainedAndNotBroadened(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()

	// A package inside a checkout stands as a project, and says what it is in.
	web := s.pick(t, filepath.Join(s.set.GoNode, "web"))
	if web.Software == nil || web.Enclosing == nil || web.Enclosing.Levels != 1 || web.Enclosing.Name != "widget" {
		t.Fatalf("package in a checkout: software=%+v enclosing=%+v", web.Software, web.Enclosing)
	}
	noPathIn(t, s.home, web)
	if _, err := s.service.Decide(ctx, "local", web.ID, yesCreate("req-web")); err != nil {
		t.Fatal(err)
	}
	if got := s.creator.requests[len(s.creator.requests)-1].Path; got != filepath.Join(s.set.GoNode, "web") {
		t.Fatalf("setting up the package linked %q, not the package", got)
	}

	// A folder with no marker of its own is not offered as software; the card
	// can still say which repository it belongs to.
	inner := s.pick(t, filepath.Join(s.set.GoNode, "internal", "count"))
	if inner.Software != nil || inner.Enclosing == nil || inner.Enclosing.Levels != 2 || inner.Enclosing.Name != "widget" {
		t.Fatalf("unmarked subfolder: software=%+v enclosing=%+v", inner.Software, inner.Enclosing)
	}

	// The check stops below the home folder, however a home is laid out.
	if err := os.MkdirAll(filepath.Join(s.home, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if loose := s.pick(t, s.set.NonGit); loose.Enclosing != nil {
		t.Fatalf("the check reached the home folder: %+v", loose.Enclosing)
	}
}

func TestFolderSoftware_AnExistingWorkspaceIsOpenedAndNeverDuplicated(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	widget := FolderExistingProject{WorkspaceID: "ws-widget", Name: "Widget", Route: "/workspaces/widget", Software: true}
	s.existing[s.set.GoNode] = []FolderExistingProject{widget}

	view := s.pick(t, s.set.GoNode)
	if view.Software == nil || len(view.Software.Existing) != 1 || view.Software.Existing[0] != widget {
		t.Fatalf("existing = %+v", view.Software)
	}
	if view.CreateAvailable {
		t.Fatal("a second workspace was offered for a folder that has one")
	}
	noPathIn(t, s.home, view)
	refused, err := s.service.Decide(ctx, "local", view.ID, yesCreate("req-dup"))
	if !errors.Is(err, ErrFolderProjectExists) || refused.Software == nil || len(refused.Software.Existing) != 1 {
		t.Fatalf("a yes on a covered folder: %v %+v", err, refused.Software)
	}
	// The modal's path makes the same yes without create; it is refused too.
	if _, err := s.service.Decide(ctx, "local", view.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "req-modal"}); !errors.Is(err, ErrFolderProjectExists) {
		t.Fatalf("a modal yes on a covered folder: %v", err)
	}
	if len(s.creator.requests) != 0 || s.stored(t, view.ID).Status != FolderOfferPending {
		t.Fatal("the refused yes created or decided something")
	}
	// The lookup was asked about the exact directory, with its witness.
	identity := s.stored(t, view.ID).SubjectIdentity
	if len(s.lookups) == 0 || s.lookups[0] != s.set.GoNode+"|"+identity {
		t.Fatalf("lookups = %v, want the canonical folder and its witness", s.lookups)
	}

	// A sibling worktree and a clone of the same repository are other folders.
	for _, other := range []string{s.set.Worktree, s.set.SiblingWorktree, s.set.Clone} {
		view := s.pick(t, other)
		if len(view.Software.Existing) != 0 || !view.CreateAvailable {
			t.Fatalf("%s was treated as the checkout that has a workspace: %+v", filepath.Base(other), view.Software)
		}
	}

	// Several workspaces on one folder are a choice, not a guess.
	s.existing[s.set.RustPython] = []FolderExistingProject{
		{WorkspaceID: "ws-a", Name: "Gauge", Route: "/workspaces/gauge"},
		{WorkspaceID: "ws-b", Name: "Gauge tools", Route: "/workspaces/gauge-tools"},
	}
	several := s.pick(t, s.set.RustPython)
	if len(several.Software.Existing) != 2 || several.CreateAvailable {
		t.Fatalf("several workspaces: %+v", several.Software)
	}

	// A host that cannot say fails closed: no workspace is promised.
	s.existingErr = errors.New("the workspace store is unreadable")
	unknown := s.pick(t, s.set.NonGit)
	if !unknown.Software.ExistingUnavailable || unknown.CreateAvailable {
		t.Fatalf("an unreadable lookup: %+v", unknown.Software)
	}
	if _, err := s.service.Decide(ctx, "local", unknown.ID, yesCreate("req-unknown")); !errors.Is(err, ErrFolderOutcomeUnavailable) {
		t.Fatalf("a yes with an unreadable lookup: %v", err)
	}
	if len(s.creator.requests) != 0 {
		t.Fatalf("workspaces were created: %+v", s.creator.requests)
	}
}

func TestFolderSoftware_AReplacedOrMissingFolderNeedsAFreshPick(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()

	view := s.pick(t, s.set.Clone)
	if view.NeedsPick || !view.CreateAvailable {
		t.Fatalf("a fresh pick: %+v", view)
	}
	// The folder is deleted and another is made at the same path.
	if err := softwarefixture.Replace(s.set.Clone); err != nil {
		t.Fatal(err)
	}
	current, err := s.service.CurrentOffer(ctx, "local", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Offer.NeedsPick || current.Offer.CreateAvailable {
		t.Fatalf("a replaced folder kept its offer: %+v", current.Offer)
	}
	if _, err := s.service.Decide(ctx, "local", view.ID, yesCreate("req-replaced")); !errors.Is(err, ErrFolderPathLost) {
		t.Fatalf("a yes on a replaced folder: %v", err)
	}

	gone := s.pick(t, s.set.NonGit)
	if err := os.RemoveAll(s.set.NonGit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.service.Decide(ctx, "local", gone.ID, yesCreate("req-gone")); !errors.Is(err, ErrFolderPathLost) {
		t.Fatalf("a yes on a missing folder: %v", err)
	}
	if len(s.creator.requests) != 0 {
		t.Fatalf("workspaces were created: %+v", s.creator.requests)
	}

	// Picking the folder again is a new offer about the directory that is there.
	again := s.pick(t, s.set.Clone)
	if again.ID == view.ID || again.NeedsPick || !again.CreateAvailable {
		t.Fatalf("a fresh pick of the replaced folder: %+v", again)
	}
}

// A more specific kind of project keeps its own offer, and a declined software
// project is only that folder's no.
func TestFolderSoftware_OtherShapesAndDeclinesAreUnchanged(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	documents, err := s.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if documents.Subject.Name != "Thesis" || documents.Software != nil {
		t.Fatalf("a manuscript was described as software: %+v", documents)
	}

	view := s.pick(t, s.set.GoNode)
	if _, err := s.service.Decide(ctx, "local", view.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req-no"}); err != nil {
		t.Fatal(err)
	}
	doc, err := s.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.DomainDeclines) != 0 {
		t.Fatalf("one project's no became a domain-wide no: %+v", doc.DomainDeclines)
	}
	other := s.pick(t, s.set.RustPython)
	if other.Software == nil || !other.CreateAvailable {
		t.Fatalf("a different project lost its offer after a no: %+v", other)
	}
}

// softwareObservation attaches one fixture folder to a conversation.
func softwareObservation(t *testing.T, s *softwareFixture, dir string) (*FolderObservationService, foldercontext.Target, *foldercontext.Observation) {
	t.Helper()
	binding, err := s.store.Binding(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	target := foldercontext.Target{UserID: "local", WorkspaceID: binding.HQWorkspaceID, AgentName: "Atlas", DraftID: "draft-software"}
	service := NewFolderObservationService(s.service)
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil },
		func(foldercontext.Target) (func(), bool) { return func() {}, true })
	s.service.deps.Picker = fakeFolderPicker{path: dir, chosen: true}
	observation, err := service.Observe(context.Background(), target, "picker", "")
	if err != nil || observation == nil {
		t.Fatalf("observe: %v", err)
	}
	return service, target, observation
}

func candidateNamed(t *testing.T, observation *foldercontext.Observation, name string) string {
	t.Helper()
	for _, project := range observation.Projects {
		if project.Name == name {
			return project.ID
		}
	}
	t.Fatalf("candidate %q was not disclosed", name)
	return ""
}

func TestFolderSoftware_ConversationOptionsDescribeWithoutScanningOrCreating(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	service, target, observation := softwareObservation(t, s, s.set.Container)
	before, err := s.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	ids := s.ids

	options := service.ReviewOptions(ctx, target, observation.ID)
	types := map[string]string{}
	for _, option := range options {
		types[option.CandidateID] = option.WorkspaceType
	}
	for _, name := range []string{"api", "site"} {
		if got := types[candidateNamed(t, observation, name)]; got != "Code project workspace" {
			t.Errorf("%s is offered as %q", name, got)
		}
	}
	noPathIn(t, s.home, options)
	after, err := s.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version || s.ids != ids || len(s.creator.requests) != 0 {
		t.Fatal("describing the options wrote an offer, allocated an id, or created a workspace")
	}

	// An explicit review is about the one candidate the user named.
	offer, err := service.Review(ctx, target, "conversation-software", observation.ID, candidateNamed(t, observation, "site"))
	if err != nil {
		t.Fatal(err)
	}
	if offer.Subject.Name != "site" || offer.Software == nil || offer.Software.NeedsCandidate || len(offer.Software.Candidates) != 0 {
		t.Fatalf("reviewed candidate: %+v software=%+v", offer.Subject, offer.Software)
	}
	if len(s.creator.requests) != 0 {
		t.Fatal("a review created a workspace")
	}
}

// A conversation reopened after a restart shows what was observed, as history.
// The server holds no selection for it, so it describes no option, reviews no
// candidate, looks up no workspace, and creates nothing until the user picks
// the folder again.
func TestFolderSoftware_AHistoricalConversationHasNoSelectionAuthority(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	service, target, observation := softwareObservation(t, s, s.set.Container)
	api := candidateNamed(t, observation, "api")
	if len(service.ReviewOptions(ctx, target, observation.ID)) == 0 {
		t.Fatal("a live selection described no option")
	}
	before, err := s.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	lookups := len(s.lookups)

	// The process restarts: observations are held in memory only.
	restarted := NewFolderObservationService(s.service)
	restarted.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil },
		func(foldercontext.Target) (func(), bool) { return func() {}, true })
	if options := restarted.ReviewOptions(ctx, target, observation.ID); len(options) != 0 {
		t.Fatalf("a historical observation described options: %+v", options)
	}
	if _, err := restarted.Review(ctx, target, "conversation-old", observation.ID, api); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("a historical observation was reviewed: %v", err)
	}
	if _, err := restarted.PreviewReview(ctx, target, observation.ID, api); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("a historical observation was previewed: %v", err)
	}
	if done, err := restarted.CompletedProject(ctx, target, observation.ID, api); done != nil || !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("a historical observation named a workspace: %+v %v", done, err)
	}
	// Another conversation's target cannot use this one's selection either.
	foreign := target
	foreign.DraftID = "draft-other"
	if options := service.ReviewOptions(ctx, foreign, observation.ID); len(options) != 0 {
		t.Fatalf("another conversation used this selection: %+v", options)
	}
	after, err := s.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version || len(s.lookups) != lookups || len(s.creator.requests) != 0 {
		t.Fatal("a historical observation wrote an offer, looked up a workspace, or created one")
	}
}

func TestFolderSoftware_ConversationPointsAtTheOneWorkspaceThatCoversTheFolder(t *testing.T) {
	s := newSoftwareFixture(t)
	ctx := context.Background()
	service, target, observation := softwareObservation(t, s, s.set.Container)
	api, site := candidateNamed(t, observation, "api"), candidateNamed(t, observation, "site")

	if done, err := service.CompletedProject(ctx, target, observation.ID, api); err != nil || done != nil {
		t.Fatalf("a folder with no workspace was reported as set up: %+v %v", done, err)
	}

	s.existing[s.set.ContainerAPI] = []FolderExistingProject{{WorkspaceID: "ws-api", Name: "API", Route: "/workspaces/api", Software: true}}
	done, err := service.CompletedProject(ctx, target, observation.ID, api)
	if err != nil || done == nil || done.Outcome == nil || done.Outcome.WorkspaceID != "ws-api" || !done.Outcome.Existing || done.Subject.Name != "api" {
		t.Fatalf("the covering workspace was not reported: %+v %v", done, err)
	}
	noPathIn(t, s.home, done)
	// The other project in the same folder is untouched by it.
	if other, err := service.CompletedProject(ctx, target, observation.ID, site); err != nil || other != nil {
		t.Fatalf("a different project inherited the workspace: %+v %v", other, err)
	}

	// Several workspaces, or one made for a directory since replaced, are left
	// to the review card rather than guessed at here.
	s.existing[s.set.ContainerAPI] = append(s.existing[s.set.ContainerAPI], FolderExistingProject{WorkspaceID: "ws-api-2", Name: "API again", Route: "/workspaces/api-2"})
	if many, err := service.CompletedProject(ctx, target, observation.ID, api); err != nil || many != nil {
		t.Fatalf("one of several workspaces was chosen: %+v %v", many, err)
	}
	s.existing[s.set.ContainerAPI] = []FolderExistingProject{{WorkspaceID: "ws-api", Name: "API", Route: "/workspaces/api", Software: true, Replaced: true}}
	if replaced, err := service.CompletedProject(ctx, target, observation.ID, api); err != nil || replaced != nil {
		t.Fatalf("a workspace for a replaced directory was reused: %+v %v", replaced, err)
	}

	// Linking a supporting folder is a different operation and never matches.
	s.existing[s.set.ContainerAPI] = []FolderExistingProject{{WorkspaceID: "ws-api", Name: "API", Route: "/workspaces/api", Software: true}}
	supporting := WithFolderReviewPlacement(ctx, FolderReviewPlacement{Operation: FolderOperationSupport, DestinationID: "ws-other"})
	if support, err := service.CompletedProject(supporting, target, observation.ID, api); err != nil || support != nil {
		t.Fatalf("a supporting link was answered with the project workspace: %+v %v", support, err)
	}
	if len(s.creator.requests) != 0 {
		t.Fatal("reading existing workspaces created one")
	}
}
