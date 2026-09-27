package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type activationCreatorTest struct {
	store    *workspace.FileStore
	resolver projectconnection.SelectionResolver
	homeID   string
	commits  *int
	result   projectconnection.CommitResult
	// A real creator refuses to preview a newly created project as an empty
	// folder; simulate that to catch a recovery path that re-previews first.
	refusePreviewAfterCommit bool
}

func (c *activationCreatorTest) HomePreparation(projectconnection.Scope) (projectconnection.HomePreparation, error) {
	return projectconnection.HomePreparation{Exists: true, HomeID: c.homeID}, nil
}
func (c *activationCreatorTest) Preview(_ context.Context, _ projectconnection.Scope, request projectconnection.Request) (projectconnection.Preview, error) {
	if c.refusePreviewAfterCommit && c.result.ProjectWorkspaceID != "" {
		return projectconnection.Preview{}, projectconnection.ErrChanged
	}
	path, err := c.resolver.Resolve(request.SelectionToken)
	if err != nil {
		return projectconnection.Preview{}, err
	}
	if filepath.Base(path) != "Single" || request.EntryName != "Song.rpp" || request.GroupComposition != "grouped" {
		return projectconnection.Preview{}, projectconnection.ErrInvalid
	}
	return projectconnection.Preview{
		InputDigest: strings.Repeat("a", 64), OwnerDigest: strings.Repeat("b", 64),
		Projection: projectconnection.Projection{EntryName: request.EntryName},
	}, nil
}
func (c *activationCreatorTest) Commit(ctx context.Context, scope projectconnection.Scope, request projectconnection.Request, input, owner string) (projectconnection.CommitResult, error) {
	preview, err := c.Preview(ctx, scope, request)
	if err != nil || input != preview.InputDigest || owner != preview.OwnerDigest {
		return projectconnection.CommitResult{}, projectconnection.ErrChanged
	}
	*c.commits++
	child := &workspace.Workspace{ID: "activation-child", Name: request.WorkspaceName,
		OwnerUserID: scope.OwnerUserID, ParentID: c.homeID}
	home, err := c.store.Get(c.homeID)
	if err != nil {
		return projectconnection.CommitResult{}, err
	}
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(c.homeID, child.ID),
		StationWorkspaceID: c.homeID, Key: home.GetAssistantProgramState().Key, StateRevision: 1})
	if err := c.store.Save(child); err != nil {
		return projectconnection.CommitResult{}, err
	}
	if err := c.store.Update(c.homeID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.LinkedProjectIDs = append(state.LinkedProjectIDs, child.ID)
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		return projectconnection.CommitResult{}, err
	}
	c.result = projectconnection.CommitResult{HomeWorkspaceID: c.homeID, ProjectWorkspaceID: child.ID,
		ModeID: projecttemplates.ProjectConnectionExistingProject}
	return c.result, nil
}
func (c *activationCreatorTest) Observe(_ projectconnection.Scope, home, project string, mode projecttemplates.ProjectConnectionMode) bool {
	return c.result.HomeWorkspaceID == home && c.result.ProjectWorkspaceID == project && mode == projecttemplates.ProjectConnectionExistingProject
}
func (c *activationCreatorTest) ObservedResult(_ projectconnection.Scope, home, _ string) (projectconnection.CommitResult, bool) {
	return c.result, c.result.HomeWorkspaceID == home && c.result.ProjectWorkspaceID != ""
}

func TestActivation_ReviewedSelectionAndCanonicalReceiptNeverUseBrowserPath(t *testing.T) {
	a, scope, _, file, tree, _ := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	commits := 0
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		return &activationCreatorTest{store: file, resolver: resolver, homeID: scope.HomeID, commits: &commits}
	})
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Review(t.Context(), scope, "alternates", doc.Revision, "", "Take A"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous file auto-selected: %v", err)
	}
	if _, err := service.Review(t.Context(), scope, "single", doc.Revision, "../Song.rpp", "Song"); !errors.Is(err, ErrConflict) {
		t.Fatalf("browser-supplied path was used: %v", err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil || review.ProjectFile != "Song.rpp" || review.BlueprintID == "" || len(review.ProjectRoleLabels) != 1 || commits != 0 {
		t.Fatalf("inert reviewed creator preview: %+v commits=%d err=%v", review, commits, err)
	}
	if _, err := service.Commit(t.Context(), Scope{OwnerUserID: "foreign", HomeID: scope.HomeID,
		ProviderID: scope.ProviderID, ProgramID: scope.ProgramID}, "single", review.Token, "commit-song"); err == nil {
		t.Fatal("foreign owner committed a selected project")
	}
	result, err := service.Commit(t.Context(), scope, "single", review.Token, "commit-song")
	if err != nil || result.WorkspaceID != "activation-child" || result.LinkID == "" || result.Replay || commits != 1 {
		t.Fatalf("one exact project attach: %+v commits=%d err=%v", result, commits, err)
	}
	replay, err := service.Commit(t.Context(), scope, "single", review.Token, "commit-song")
	if err != nil || !replay.Replay || replay.WorkspaceID != result.WorkspaceID || commits != 1 {
		t.Fatalf("creator replay was not inert: %+v commits=%d err=%v", replay, commits, err)
	}
	if _, err := service.Commit(t.Context(), scope, "single", review.Token, "different-key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("same review accepted a different creator key: %v", err)
	}
	saved, err := a.library.Read(scope)
	if err != nil || len(saved.Entries) != 3 || sessionEntry(saved, "single").Link == nil ||
		fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("catalog association or source changed: %+v %v", saved, err)
	}
}

func TestActivation_RecoversExactCreatorRunBeforeRepreviewAfterInterruptedCatalogWrite(t *testing.T) {
	a, scope, _, file, tree, _ := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	commits := 0
	creator := &activationCreatorTest{store: file, homeID: scope.HomeID, commits: &commits, refusePreviewAfterCommit: true}
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		creator.resolver = resolver
		return creator
	})
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	doc, err = a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	savedReview, ok := findReview(doc, review.Token, "activate_project")
	if !ok {
		t.Fatal("review receipt not saved")
	}
	creatorScope := projectconnection.Scope{OwnerUserID: scope.OwnerUserID, RunID: savedReview.TargetID}
	result, err := creator.Commit(t.Context(), creatorScope, activationRequest(review.Token, *savedReview.Activation),
		savedReview.Activation.CreatorInputDigest, savedReview.Activation.CreatorOwnerDigest)
	if err != nil || commits != 1 || result.ProjectWorkspaceID == "" {
		t.Fatalf("canonical creator succeeded before catalog association: %+v %v", result, err)
	}
	// Reconstruct the service against persisted state as on restart. The
	// deterministic run and its exact link survive; the Home entry did not
	// receive its library receipt yet. A second preview would now fail.
	service = NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		creator.resolver = resolver
		return creator
	})
	recovered, err := service.Commit(t.Context(), scope, "single", review.Token, "recovered-run")
	if err != nil || commits != 1 || recovered.WorkspaceID != result.ProjectWorkspaceID || recovered.LinkID == "" {
		t.Fatalf("interrupted catalog association was not reconciled: %+v commits=%d err=%v", recovered, commits, err)
	}
	if _, err := service.Commit(t.Context(), scope, "single", review.Token, "new-run"); !errors.Is(err, ErrConflict) {
		t.Fatalf("other key reused consumed review: %v", err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("creator duplicated project or modified source: %v %v", ids, err)
	}
}

func realActivationCreator(t *testing.T, scope Scope, file interface {
	workspace.Store
	GetFolderPath(string) (string, error)
}, installed activationPlugins) ActivationCreatorFactory {
	t.Helper()
	homeDecl := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion,
		ID: scope.ProgramID, StationName: "Music Home",
		Roles: []workspace.AssistantProgramRoleSpec{{ID: "portfolio_manager", Label: "Portfolio Manager",
			Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate music."}}}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		home.Kind = "group"
		state := home.GetAssistantProgramState()
		state.Declaration = homeDecl
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	grouping := grouprequirements.NewService(file, grouprequirements.NewMemoryStore())
	grouping.SetIndependentHomeResolver(func(owner string, template projecttemplates.Template, requireHome bool) (grouprequirements.IndependentHomeResolution, error) {
		if owner != scope.OwnerUserID || template.PluginOwner == nil || template.PluginOwner.PluginID != "reaper-plugin" {
			return grouprequirements.IndependentHomeResolution{}, grouprequirements.ErrIndependentHomeInvalid
		}
		project := template.AssistantProject
		projectOwner := &workspace.AssistantProjectProviderOwner{PluginID: "reaper-plugin", PluginVersion: "0.9.0",
			BlueprintID: "reaper-song", BlueprintVersion: 10, ProjectTeamID: project.ID,
			ProjectTeamSchema: project.SchemaVersion, ProjectTeamVersion: project.Version,
			ProjectTeamDigest: projecttemplates.AssistantProjectDigest(project), PluginGeneration: installed[1].EvidenceGeneration(),
			ComponentFingerprint: installed[1].ComponentFingerprint}
		if !requireHome {
			return grouprequirements.IndependentHomeResolution{ProjectOwner: projectOwner}, nil
		}
		home, err := file.Get(scope.HomeID)
		if err != nil {
			return grouprequirements.IndependentHomeResolution{}, err
		}
		return grouprequirements.IndependentHomeResolution{Key: home.GetAssistantProgramState().Key,
			Declaration: homeDecl, Owner: home.GetAssistantProgramState().HomeProvider, ProjectOwner: projectOwner}, nil
	})
	return func(resolver projectconnection.SelectionResolver) ActivationCreator {
		creator := projectconnection.NewService(file, resolver)
		creator.SetGroupRequirementService(grouping)
		return creator
	}
}

type failActivationHomeWrite struct {
	workspace.Store
	homeID  string
	pending bool
}

func (s *failActivationHomeWrite) Update(id string, fn func(*workspace.Workspace) error) error {
	if id == s.homeID && s.pending {
		s.pending = false
		return errors.New("injected Home catalog persistence interruption")
	}
	return s.Store.Update(id, fn)
}

// Pause only after the real shared creator has durably made its child. The
// outer Home library write has not yet happened, so a racing queue Skip must
// wait rather than persisting "skipped" and stranding that child.
type pauseAfterActivationCreator struct {
	ActivationCreator
	created chan struct{}
	release chan struct{}
}

func (p *pauseAfterActivationCreator) Commit(ctx context.Context, scope projectconnection.Scope, request projectconnection.Request, input, owner string) (projectconnection.CommitResult, error) {
	result, err := p.ActivationCreator.Commit(ctx, scope, request, input, owner)
	if err == nil {
		close(p.created)
		<-p.release
	}
	return result, err
}

func TestActivationQueue_CreatorAndSkipSerializeAcrossDurableChildBoundary(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	queue, _, err := a.library.StartActivationQueue(scope, []string{"single", "alternates"}, "race-start")
	if err != nil {
		t.Fatal(err)
	}
	baseCreator := realActivationCreator(t, scope, file, installed)
	created, release := make(chan struct{}), make(chan struct{})
	releaseCreator := sync.OnceFunc(func() { close(release) })
	defer releaseCreator()
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		return &pauseAfterActivationCreator{ActivationCreator: baseCreator(resolver), created: created, release: release}
	})
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		value ActivationResult
		err   error
	}
	creatorDone := make(chan result, 1)
	go func() {
		value, commitErr := service.Commit(t.Context(), scope, "single", review.Token, "race-creator")
		creatorDone <- result{value, commitErr}
	}()
	select {
	case <-created:
	case <-time.After(10 * time.Second):
		t.Fatal("creator did not persist its child")
	}
	ids, err := file.List()
	if err != nil || len(ids) != 2 {
		t.Fatalf("creator has not committed exactly one child: %v %v", ids, err)
	}
	skipStarted := make(chan struct{})
	skipDone := make(chan error, 1)
	go func() {
		close(skipStarted)
		_, _, skipErr := a.library.ProgressActivationQueue(scope, queue.ID, "single", "skip", "racing-skip", 1)
		skipDone <- skipErr
	}()
	<-skipStarted
	select {
	case skipErr := <-skipDone:
		t.Fatalf("Skip overtook the already committed child: %v", skipErr)
	case <-time.After(100 * time.Millisecond):
	}
	releaseCreator()
	select {
	case committed := <-creatorDone:
		if committed.err != nil || committed.value.WorkspaceID == "" {
			t.Fatalf("creator lost its Home association: %+v", committed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("creator hung after allowing Home association")
	}
	select {
	case skipErr := <-skipDone:
		if !errors.Is(skipErr, ErrConflict) {
			t.Fatalf("a now-connected song was skipped: %v", skipErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Skip remained blocked after creator returned")
	}
	view, err := a.library.CurrentActivationQueue(scope)
	if err != nil || view.Queue == nil || view.Queue.Index != 0 || len(view.Queue.Skipped) != 0 {
		t.Fatalf("queue claimed the connected song was skipped: %+v %v", view, err)
	}
	progress, replay, err := a.library.ProgressActivationQueue(scope, queue.ID, "single", "connected", "race-connected", 1)
	if err != nil || replay || progress.Index != 1 || len(progress.Connected) != 1 {
		t.Fatalf("separate linked receipt did not advance: %+v %t %v", progress, replay, err)
	}
	ids, err = file.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("race duplicated the child or changed the source: %v %v", ids, err)
	}
}

func TestActivationQueue_SkipBeforeCreatorRefusesStaleReview(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	queue, _, err := a.library.StartActivationQueue(scope, []string{"single", "alternates"}, "skip-first-start")
	if err != nil {
		t.Fatal(err)
	}
	service := NewActivationService(a, realActivationCreator(t, scope, file, installed))
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.library.ProgressActivationQueue(scope, queue.ID, "single", "skip", "skip-first", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(t.Context(), scope, "single", review.Token, "stale-after-skip"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale review created a child after Skip: %v", err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 1 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("Skip-before-creator created a child or changed source: %v %v", ids, err)
	}
}

func TestActivation_RealCreatorRecoversAfterHomeWriteFailsAndStoreRestarts(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	creator := realActivationCreator(t, scope, file, installed)
	service := NewActivationService(a, creator)
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	originalLibrary := a.library
	interrupted := &failActivationHomeWrite{Store: file, homeID: scope.HomeID, pending: true}
	a.library = NewStore(interrupted).WithProviderEvidence(originalLibrary.providerEvidence)
	if _, err := service.Commit(t.Context(), scope, "single", review.Token, "resume-real-creator"); err == nil || interrupted.pending {
		t.Fatalf("commit did not stop after canonical creator success: %v", err)
	}
	if doc, err := originalLibrary.Read(scope); err != nil || sessionEntry(doc, "single").Link != nil {
		t.Fatalf("failed Home write associated the child: %+v %v", doc, err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 2 {
		t.Fatalf("canonical creator did not preserve exactly one child: %v %v", ids, err)
	}
	root, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := workspace.NewFileStore(filepath.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	library := NewStore(restarted).WithProviderEvidence(originalLibrary.providerEvidence)
	// The original confirmation crossed the creator boundary, but its Home
	// review expired while the catalog write was interrupted. An exact-run
	// retry must repair the link, never preview another child.
	library.now = func() time.Time { return review.ExpiresAt.Add(time.Second) }
	a.library, a.roots.library, a.owners = library, library, restarted
	service = NewActivationService(a, realActivationCreator(t, scope, restarted, installed))
	result, err := service.Commit(t.Context(), scope, "single", review.Token, "resume-real-creator")
	if err != nil || result.WorkspaceID == "" || result.LinkID == "" {
		t.Fatalf("creator success was not reconciled after restart: %+v %v", result, err)
	}
	replay, err := service.Commit(t.Context(), scope, "single", review.Token, "resume-real-creator")
	if err != nil || !replay.Replay || replay.WorkspaceID != result.WorkspaceID {
		t.Fatalf("recovered receipt did not replay: %+v %v", replay, err)
	}
	ids, err = restarted.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("recovery duplicated a workspace or changed source: %v %v", ids, err)
	}
	// Expiration alone must never permit a first creator commit. The other
	// catalog-only folder still has no exact run to recover.
	doc, err = library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	unused, err := service.Review(t.Context(), scope, "alternates", doc.Revision, "Take A.rpp", "Another song")
	if err != nil {
		t.Fatal(err)
	}
	library.now = func() time.Time { return unused.ExpiresAt.Add(time.Second) }
	if _, err := service.Commit(t.Context(), scope, "alternates", unused.Token, "expired-without-child"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired review created a new child: %v", err)
	}
	ids, err = restarted.List()
	if err != nil || len(ids) != 2 {
		t.Fatalf("expired review changed workspace count: %v %v", ids, err)
	}
}

func TestActivation_SQLitePrimarySplitHomeMirrorRefusesCreatorRepairUntilExplicitReconciliation(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "activation.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close SQLite activation fixture: %v", err)
		}
	}()
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(home); err != nil {
		t.Fatal(err)
	}
	synced := workspace.NewSyncStore(primary, file)
	original := a.library
	library := NewStore(synced).WithProviderEvidence(original.providerEvidence)
	a.library, a.roots.library, a.owners = library, library, synced
	queue, _, err := library.StartActivationQueue(scope, []string{"single", "alternates"}, "sqlite-activation-queue")
	if err != nil {
		t.Fatal(err)
	}
	creatorFactory := realActivationCreator(t, scope, synced, installed)
	created, release := make(chan struct{}), make(chan struct{})
	releaseCreator := sync.OnceFunc(func() { close(release) })
	defer releaseCreator()
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		return &pauseAfterActivationCreator{ActivationCreator: creatorFactory(resolver), created: created, release: release}
	})
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, commitErr := service.Commit(t.Context(), scope, "single", review.Token, "sqlite-exact-creator")
		done <- commitErr
	}()
	select {
	case <-created:
	case <-time.After(15 * time.Second):
		t.Fatal("SQLite-backed creator did not durably link one child")
	}
	// Emulate a crash between the folder-mirror and SQLite-primary Home writes.
	// Only the library document differs; the independently durable reciprocal
	// child and Home membership are identical and must never be undone here.
	if err := file.Update(scope.HomeID, func(folderHome *workspace.Workspace) error {
		state := folderHome.GetAssistantProgramState()
		var changed Document
		if err := json.Unmarshal(state.ProjectLibrary, &changed); err != nil {
			return err
		}
		changed.Revision++
		if !changed.valid(scope) {
			return ErrCorrupt
		}
		encoded, err := json.Marshal(changed)
		if err != nil {
			return err
		}
		state.ProjectLibrary = encoded
		folderHome.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	releaseCreator()
	select {
	case commitErr := <-done:
		if !errors.Is(commitErr, ErrMirrorDiverged) && !errors.Is(commitErr, ErrUnavailable) {
			t.Fatalf("creator repair claimed success across divergent Home mirrors: %v", commitErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("creator hung on split Home mirrors")
	}
	if _, err := library.PendingLinkedProjects(scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("split Home exposed pending-link navigation: %v", err)
	}
	folderPath, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopenedFile, err := workspace.NewFileStore(filepath.Dir(folderPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(workspace.NewSyncStore(primary, reopenedFile)).Read(scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("fresh FileStore silently accepted a persistent split Home: %v", err)
	}
	if _, _, err := library.ProgressActivationQueue(scope, queue.ID, "single", "skip", "split-creator-skip", 1); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("split Home allowed Skip to invalidate exact creator: %v", err)
	}
	ids, err := synced.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("split Home changed child count or project source: %v %v", ids, err)
	}
	primaryHome, err := primary.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	primaryDoc, err := NewStore(primary).Read(scope)
	if err != nil || sessionEntry(primaryDoc, "single").Link != nil || primaryDoc.Queue.Index != 0 {
		t.Fatalf("SQLite primary silently adopted the split folder receipt: %+v %v", primaryDoc, err)
	}
	// Test-only operator reconciliation from the unchanged primary: production
	// deliberately offers no automatic winner or migration for split mirrors.
	if err := file.Update(scope.HomeID, func(folderHome *workspace.Workspace) error {
		state := folderHome.GetAssistantProgramState()
		state.ProjectLibrary = append([]byte(nil), primaryHome.GetAssistantProgramState().ProjectLibrary...)
		folderHome.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := library.PendingLinkedProjects(scope)
	if err != nil || pending.Total != 1 {
		t.Fatalf("exact child not reviewable after external repair: %+v %v", pending, err)
	}
	result, err := service.Commit(t.Context(), scope, "single", review.Token, "sqlite-exact-creator")
	if err != nil || result.WorkspaceID != pending.Rows[0].WorkspaceID {
		t.Fatalf("exact confirmed run failed to repair without duplication: %+v %v", result, err)
	}
	view, err := library.CurrentActivationQueue(scope)
	if err != nil || view.Queue == nil || view.Queue.Index != 0 {
		t.Fatalf("queue order lost during exact repair: %+v %v", view, err)
	}
	if _, _, err := library.ProgressActivationQueue(scope, queue.ID, "single", "connected", "sqlite-exact-connected", 1); err != nil {
		t.Fatalf("reviewed connection did not advance its own queue receipt: %v", err)
	}
	ids, err = synced.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("recovery duplicated child or edited project source: %v %v", ids, err)
	}
}

// Fail only the first SQLite Home save after the creator has linked its child.
// SyncStore has already written the new folder mirror by that point and must
// roll it back rather than leaving a deceptively successful catalog receipt.
type failSQLiteHomeAssociation struct {
	workspace.Store
	homeID    string
	failNext  bool
	triggered bool
}

func (f *failSQLiteHomeAssociation) Save(item *workspace.Workspace) error {
	if item.ID == f.homeID && f.failNext {
		f.failNext = false
		f.triggered = true
		return os.ErrPermission
	}
	return f.Store.Save(item)
}

func TestActivation_SQLitePrimaryFailedFinalSaveRollsBackFolderAndRetriesExactRun(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "failed-final-write.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close SQLite fixture: %v", err)
		}
	}()
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(home); err != nil {
		t.Fatal(err)
	}
	failing := &failSQLiteHomeAssociation{Store: primary, homeID: scope.HomeID}
	synced := workspace.NewSyncStore(failing, file)
	original := a.library
	library := NewStore(synced).WithProviderEvidence(original.providerEvidence)
	a.library, a.roots.library, a.owners = library, library, synced
	queue, _, err := library.StartActivationQueue(scope, []string{"single", "alternates"}, "failed-save-queue")
	if err != nil {
		t.Fatal(err)
	}
	factory := realActivationCreator(t, scope, synced, installed)
	created, release := make(chan struct{}), make(chan struct{})
	releaseCreator := sync.OnceFunc(func() { close(release) })
	defer releaseCreator()
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		return &pauseAfterActivationCreator{ActivationCreator: factory(resolver), created: created, release: release}
	})
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	prior, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() {
		_, commitErr := service.Commit(t.Context(), scope, "single", review.Token, "failed-sqlite-final-write")
		completed <- commitErr
	}()
	select {
	case <-created:
	case <-time.After(15 * time.Second):
		t.Fatal("canonical creator did not persist its child")
	}
	failing.failNext = true // No SQLite write happens until the paused creator is released.
	releaseCreator()
	select {
	case commitErr := <-completed:
		if commitErr == nil || !failing.triggered {
			t.Fatalf("final SQLite write failure was hidden: %v triggered=%t", commitErr, failing.triggered)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Home write failure deadlocked")
	}
	for _, backing := range []workspace.Store{primary, file, synced} {
		saved, readErr := NewStore(backing).Read(scope)
		if readErr != nil || saved.Revision != prior.Revision || sessionEntry(saved, "single").Link != nil ||
			saved.Queue == nil || saved.Queue.ID != queue.ID || saved.Queue.Index != 0 {
			t.Fatalf("failed SQLite save left a split or connected Home: %+v %v", saved, readErr)
		}
	}
	folderPath, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopenedFile, err := workspace.NewFileStore(filepath.Dir(folderPath))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewStore(workspace.NewSyncStore(primary, reopenedFile)).Read(scope)
	if err != nil || fresh.Revision != prior.Revision || sessionEntry(fresh, "single").Link != nil {
		t.Fatalf("fresh folder handle retained a failed catalog association: %+v %v", fresh, err)
	}
	pending, err := library.PendingLinkedProjects(scope)
	if err != nil || pending.Total != 1 || len(pending.Rows) != 1 {
		t.Fatalf("independently durable child was not reviewable: %+v %v", pending, err)
	}
	ids, err := synced.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("final write failure duplicated child or changed source: %v %v", ids, err)
	}
	result, err := service.Commit(t.Context(), scope, "single", review.Token, "failed-sqlite-final-write")
	if err != nil || result.WorkspaceID != pending.Rows[0].WorkspaceID {
		t.Fatalf("confirmed run could not retry after rollback: %+v %v", result, err)
	}
	if _, _, err := library.ProgressActivationQueue(scope, queue.ID, "single", "skip", "skip-linked-after-retry", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("Skip misreported a linked child after recovery: %v", err)
	}
	if _, _, err := library.ProgressActivationQueue(scope, queue.ID, "single", "connected", "connected-after-retry", 1); err != nil {
		t.Fatalf("exact connected receipt failed: %v", err)
	}
	for _, backing := range []workspace.Store{primary, file, synced} {
		saved, readErr := NewStore(backing).Read(scope)
		if readErr != nil || sessionEntry(saved, "single").Link == nil || saved.Queue.Index != 1 || len(saved.Queue.Connected) != 1 {
			t.Fatalf("retry failed to mirror one confirmed link/queue receipt: %+v %v", saved, readErr)
		}
	}
	ids, err = synced.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("retry duplicated child or edited project source: %v %v", ids, err)
	}
}

func TestActivation_NormalSingleIntakeWinsAfterLibraryReviewWithoutDuplicatingChild(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	service := NewActivationService(a, realActivationCreator(t, scope, file, installed))
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	childID := connectExistingSong(t, scope, file, installed, tree.single)
	if _, err := service.Commit(t.Context(), scope, "single", review.Token, "late-library-creator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("library review claimed an independently connected folder: %v", err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("competing creator duplicated child or edited source: %v %v", ids, err)
	}
	pending, err := a.library.PendingLinkedProjects(scope)
	if err != nil || pending.Total != 1 || pending.Rows[0].WorkspaceID != childID {
		t.Fatalf("independent child lacked a reviewable exact Home link: %+v %v", pending, err)
	}
	linkReview, err := a.library.ReviewLinkedProject(scope, childID, pending.Revision)
	if err != nil || linkReview.EntryID != "single" || linkReview.LinkOnly {
		t.Fatalf("normal intake was not matched to its scanned song: %+v %v", linkReview, err)
	}
	if _, err := a.library.CommitLinkedProject(scope, childID, linkReview.Token, "normal-intake-won"); err != nil {
		t.Fatal(err)
	}
	final, err := a.library.Read(scope)
	if err != nil || sessionEntry(final, "single") == nil || sessionEntry(final, "single").Link == nil ||
		sessionEntry(final, "single").Link.WorkspaceID != childID || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("explicit owner reconciliation lost the song or source: %+v %v", final, err)
	}
	ids, err = file.List()
	if err != nil || len(ids) != 2 {
		t.Fatalf("reconciliation created a second project: %v %v", ids, err)
	}
}

func TestActivation_UnrelatedHomeReviewInvalidatesCreatorBeforeCreation(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	service := NewActivationService(a, realActivationCreator(t, scope, file, installed))
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	action := "Focus on lyrics"
	patch := FieldsPatch{NextAction: &action}
	fieldReview, err := a.library.ReviewFields(scope, "alternates", 0, patch, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.library.CommitFields(scope, "alternates", fieldReview.Token, "different-song-edit", 0, patch, scope.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(t.Context(), scope, "single", review.Token, "stale-creator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reviewed creator crossed a changed Home revision: %v", err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 1 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("stale review created a child or edited the source: %v %v", ids, err)
	}
	doc, err = a.library.Read(scope)
	if err != nil || sessionEntry(doc, "single").Link != nil || sessionEntry(doc, "alternates").Fields.NextAction != action {
		t.Fatalf("unrelated user edit was lost or stale link attached: %+v %v", doc, err)
	}
}

// A tab can close after the canonical creator succeeds but before Ori saves
// the catalog association. Losing the tab's activation token/key must not
// strand its child: the independently durable reciprocal link is reviewable
// through the Home's pending-link shelf without a new creator request.
func TestActivation_LostBrowserKeyRecoversThroughExplicitHomeLinkReview(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	service := NewActivationService(a, realActivationCreator(t, scope, file, installed))
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	activationReview, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	original := a.library
	broken := &failActivationHomeWrite{Store: file, homeID: scope.HomeID, pending: true}
	a.library = NewStore(broken).WithProviderEvidence(original.providerEvidence)
	if _, err := service.Commit(t.Context(), scope, "single", activationReview.Token, "lost-browser-key"); err == nil || broken.pending {
		t.Fatalf("did not fail strictly after canonical creator: %v", err)
	}
	// Another user saves an unrelated Home note before the browser can retry.
	// The old creator review is stale, yet a fresh exact-link owner review must
	// still recover the one independently durable child without losing notes.
	action := "Review the alternate melody"
	patch := FieldsPatch{NextAction: &action}
	fieldReview, err := original.ReviewFields(scope, "alternates", 0, patch, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := original.CommitFields(scope, "alternates", fieldReview.Token, "unrelated-after-child", 0, patch, scope.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(t.Context(), scope, "single", activationReview.Token, "lost-browser-key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale creator review unexpectedly wrote a Home association: %v", err)
	}
	path, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := workspace.NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	library := NewStore(restarted).WithProviderEvidence(original.providerEvidence)
	pending, err := library.PendingLinkedProjects(scope)
	if err != nil || pending.Total != 1 || len(pending.Rows) != 1 {
		t.Fatalf("lost confirmation stranded linked child: %+v %v", pending, err)
	}
	// Browser state is intentionally absent here. The user selects exactly
	// the Home-listed reciprocal link and confirms a fresh association review.
	linkReview, err := library.ReviewLinkedProject(scope, pending.Rows[0].WorkspaceID, pending.Revision)
	if err != nil || linkReview.EntryID != "single" || linkReview.LinkOnly {
		t.Fatalf("failed to match the unchanged scanned source: %+v %v", linkReview, err)
	}
	result, err := library.CommitLinkedProject(scope, pending.Rows[0].WorkspaceID, linkReview.Token, "new-review-after-lost-key")
	if err != nil || result.EntryID != "single" {
		t.Fatalf("user re-review did not reconcile the Home: %+v %v", result, err)
	}
	page, err := library.PendingLinkedProjects(scope)
	if err != nil || page.Total != 0 {
		t.Fatalf("associated child still appeared in pending shelf: %+v %v", page, err)
	}
	ids, err := restarted.List()
	if err != nil || len(ids) != 2 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("lost-key repair created a child or touched the project file: %v %v", ids, err)
	}
	final, err := library.Read(scope)
	if err != nil || sessionEntry(final, "alternates").Fields.NextAction != action {
		t.Fatalf("link recovery overwrote another Home edit: %+v %v", final, err)
	}
}

func TestActivation_RealCreatorPreviewsAndAttachesOnePinnedSongWithoutStaffing(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	service := NewActivationService(a, realActivationCreator(t, scope, file, installed))
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatalf("real creator preview: %v", err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 1 {
		t.Fatalf("review created a project: %v %v", ids, err)
	}
	result, err := service.Commit(t.Context(), scope, "single", review.Token, "real-creator-song")
	if err != nil {
		t.Fatalf("real creator commit: %v", err)
	}
	if result.WorkspaceID == "" || result.LinkID != workspace.AssistantProjectLinkID(scope.HomeID, result.WorkspaceID) {
		t.Fatalf("project did not attach to this Home: %+v", result)
	}
	child, err := file.Get(result.WorkspaceID)
	if err != nil || child.ParentID != scope.HomeID || child.OwnerUserID != scope.OwnerUserID ||
		len(child.GetAgentInstances()) != 0 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("unexpected creator consequence: child=%+v err=%v", child, err)
	}
	replay, err := service.Commit(t.Context(), scope, "single", review.Token, "real-creator-song")
	if err != nil || !replay.Replay || replay.WorkspaceID != child.ID {
		t.Fatalf("real creator replay: %+v %v", replay, err)
	}
	ids, err = file.List()
	if err != nil || len(ids) != 2 {
		t.Fatalf("creator duplicated Home or project: %v %v", ids, err)
	}
}

func TestActivation_SameNamedConnectedRootsKeepTheirOwnReviewedSource(t *testing.T) {
	a, scope, roots, file, tree, _ := activationFixture(t)
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	original := doc.Roots[0]
	other := filepath.Join(t.TempDir(), filepath.Base(tree.root))
	if err := os.MkdirAll(filepath.Join(other, "Single"), 0o750); err != nil {
		t.Fatal(err)
	}
	otherSong := filepath.Join(other, "Single", "Song.rpp")
	if err := os.WriteFile(otherSong, []byte("separate source"), 0o600); err != nil {
		t.Fatal(err)
	}
	picker := roots.picker.(*testRootPicker)
	picker.path, err = filepath.EvalSymlinks(other)
	if err != nil {
		t.Fatal(err)
	}
	picked, err := roots.Pick(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	rootReview, err := roots.Review(scope, picked, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	otherRoot, _, err := roots.Commit(scope, rootReview.Token, "grant-identical-basename")
	if err != nil || otherRoot.ID == original.ID || filepath.Base(otherRoot.Path) != filepath.Base(original.Path) {
		t.Fatalf("two exact roots were not independently approved: %+v %v", otherRoot, err)
	}
	commits := 0
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		return &activationCreatorTest{store: file, resolver: resolver, homeID: scope.HomeID, commits: &commits}
	})
	doc, err = a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	doc, err = a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := findReview(doc, review.Token, "activate_project")
	if !ok || bound.Activation == nil || bound.Activation.RootID != original.ID || bound.Activation.RootID == otherRoot.ID {
		t.Fatalf("review adopted a same-named but unrelated root: %+v", bound)
	}
	originalHash := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	otherHash := fileDigest(t, otherSong)
	if result, err := service.Commit(t.Context(), scope, "single", review.Token, "single-origin-only"); err != nil || result.LinkID == "" || commits != 1 {
		t.Fatalf("reviewed origin could not attach: %+v %v", result, err)
	}
	if fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != originalHash || fileDigest(t, otherSong) != otherHash {
		t.Fatal("connecting one root modified source files")
	}
}

func TestActivation_RevocationAndSameNamedReplacementRefuseReviewedCreator(t *testing.T) {
	for _, pathChange := range []string{"replace-folder", "replace-file", "replace-regular-file", "revoke"} {
		t.Run(pathChange, func(t *testing.T) {
			a, scope, roots, file, tree, _ := activationFixture(t)
			commits := 0
			service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
				return &activationCreatorTest{store: file, resolver: resolver, homeID: scope.HomeID, commits: &commits}
			})
			doc, err := a.library.Read(scope)
			if err != nil {
				t.Fatal(err)
			}
			review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
			if err != nil {
				t.Fatal(err)
			}
			switch pathChange {
			case "replace-folder":
				if err := os.Rename(tree.single, tree.single+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(tree.single, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tree.single, "Song.rpp"), []byte("replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "replace-file":
				if err := os.Rename(filepath.Join(tree.single, "Song.rpp"), filepath.Join(tree.single, "Song.rpp-old")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(tree.single, "Song.rpp-old"), filepath.Join(tree.single, "Song.rpp")); err != nil {
					t.Fatal(err)
				}
			case "replace-regular-file":
				if err := os.Rename(filepath.Join(tree.single, "Song.rpp"), filepath.Join(tree.single, "Song.rpp-old")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tree.single, "Song.rpp"), []byte("same name, different inode"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "revoke":
				doc, err = a.library.Read(scope)
				if err != nil {
					t.Fatal(err)
				}
				revoke, err := roots.ReviewRevoke(scope, doc.Roots[0].ID, doc.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = roots.CommitRevoke(scope, doc.Roots[0].ID, revoke.Token, "revoke-before-creator"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.Commit(t.Context(), scope, "single", review.Token, "new-project"); err == nil || commits != 0 {
				t.Fatalf("stale creator changed state: err=%v commits=%d", err, commits)
			}
			ids, err := file.List()
			if err != nil || len(ids) != 1 || ids[0] != scope.HomeID {
				t.Fatalf("stale selection created a child: %v %v", ids, err)
			}
		})
	}
}
