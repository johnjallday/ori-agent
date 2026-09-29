package projectlibrary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type associationSelection struct{ path string }

func (r associationSelection) Resolve(string) (string, error) { return r.path, nil }

func connectExistingSong(t *testing.T, scope Scope, file *workspace.FileStore, plugins activationPlugins, folder string) string {
	t.Helper()
	creator := realActivationCreator(t, scope, file, plugins)(associationSelection{path: folder})
	connectionScope := projectconnection.Scope{OwnerUserID: scope.OwnerUserID, RunID: newID(), Template: plugins[1].ResolvedBlueprints[0].Template}
	input := projectconnection.Request{ModeID: projecttemplates.ProjectConnectionExistingProject,
		SelectionToken: "canonical-single-intake", EntryName: "Song.rpp", WorkspaceName: "Direct song", GroupComposition: "grouped"}
	preview, err := creator.Preview(t.Context(), connectionScope, input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := creator.Commit(t.Context(), connectionScope, input, preview.InputDigest, preview.OwnerDigest)
	if err != nil || result.ProjectWorkspaceID == "" || result.HomeWorkspaceID != scope.HomeID {
		t.Fatalf("canonical creator link: %+v %v", result, err)
	}
	return result.ProjectWorkspaceID
}

func TestLinkedAssociation_CanonicalSingleIntakeWithoutRootCreatesOnlyLinkMetadata(t *testing.T) {
	a, scope, _, file, _, plugins := activationFixture(t)
	outside := filepath.Join(t.TempDir(), "Single")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	fileName := filepath.Join(outside, "Song.rpp")
	if err := os.WriteFile(fileName, []byte("never parse this"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := fileDigest(t, fileName)
	childID := connectExistingSong(t, scope, file, plugins, outside)
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := a.library.PendingLinkedProjects(scope)
	if err != nil || pending.Total != 1 || pending.Rows[0].WorkspaceID != childID {
		t.Fatalf("direct intake was not offered for explicit association: %+v %v", pending, err)
	}
	review, err := a.library.ReviewLinkedProject(scope, childID, doc.Revision)
	if err != nil || !review.LinkOnly || review.EntryID != "" {
		t.Fatalf("ungranted project was treated as a discovery: %+v %v", review, err)
	}
	afterReview, err := a.library.Read(scope)
	if err != nil || len(afterReview.Entries) != len(doc.Entries) {
		t.Fatalf("review added a project early: %+v %v", afterReview, err)
	}
	accepted, err := a.library.CommitLinkedProject(scope, childID, review.Token, "direct-association")
	if err != nil || accepted.EntryID == "" || accepted.Replay {
		t.Fatalf("associate canonical child: %+v %v", accepted, err)
	}
	replay, err := a.library.CommitLinkedProject(scope, childID, review.Token, "direct-association")
	if err != nil || !replay.Replay || replay.EntryID != accepted.EntryID {
		t.Fatalf("replay duplicated an association: %+v %v", replay, err)
	}
	result, err := a.library.Read(scope)
	if err != nil || len(result.Roots) != len(doc.Roots) || len(result.Entries) != len(doc.Entries)+1 ||
		sessionEntry(result, accepted.EntryID).Link.WorkspaceID != childID || len(sessionEntry(result, accepted.EntryID).Observations) != 0 ||
		fileDigest(t, fileName) != before {
		t.Fatalf("association granted discovery or modified song: %+v %v", result, err)
	}
	if pending, err = a.library.PendingLinkedProjects(scope); err != nil || pending.Total != 0 {
		t.Fatalf("accepted link is still pending: %+v %v", pending, err)
	}
}

func TestLinkedAssociation_OfflineLinkOnlyDoesNotNeedFilesystemConsent(t *testing.T) {
	file, scope := libraryHome(t)
	library := NewStore(file).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return true })
	initializeLibrary(t, library, scope)
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Offline song"})
	child.OwnerUserID = scope.OwnerUserID
	child.SharedData = map[string]any{}
	child.DirectoryReferences = []workspace.DirectoryReference{{ID: "project-reference", WorkspaceID: child.ID,
		Path: filepath.Join(t.TempDir(), "unmounted"), Name: "Unavailable project"}}
	if err := workspace.SetProjectEntryLocator(child.SharedData, workspace.ProjectEntryLocator{
		SchemaVersion: workspace.ProjectEntryLocatorSchemaVersion, Kind: workspace.ProjectEntryDirectoryReference,
		DirectoryReferenceID: "project-reference", RelativePath: "Song.rpp"}); err != nil {
		t.Fatal(err)
	}
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID),
		SchemaVersion: 1, StationWorkspaceID: scope.HomeID, StateRevision: 1,
		Key: workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}})
	if err := file.Save(child); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = append(state.LinkedProjectIDs, child.ID)
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := library.ReviewLinkedProject(scope, child.ID, doc.Revision)
	if err != nil || !review.LinkOnly {
		t.Fatalf("offline project required filesystem or root grant: %+v %v", review, err)
	}
	result, err := library.CommitLinkedProject(scope, child.ID, review.Token, "offline-association")
	if err != nil || result.EntryID == "" {
		t.Fatalf("offline link-only metadata unavailable: %+v %v", result, err)
	}
	final, err := library.Read(scope)
	if err != nil || len(final.Roots) != 0 || len(final.Entries) != 1 || final.Entries[0].Link.WorkspaceID != child.ID {
		t.Fatalf("link-only grant created or history lost: %+v %v", final, err)
	}
}

func TestLinkedAssociation_MatchesExactAlreadyScannedEntryAndRejectsChangedSource(t *testing.T) {
	a, scope, _, file, tree, plugins := activationFixture(t)
	childID := connectExistingSong(t, scope, file, plugins, tree.single)
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := a.library.ReviewLinkedProject(scope, childID, doc.Revision)
	if err != nil || review.LinkOnly || review.EntryID != "single" {
		t.Fatalf("scanned source was not exactly matched: %+v %v", review, err)
	}
	result, err := a.library.CommitLinkedProject(scope, childID, review.Token, "exact-existing-entry")
	if err != nil || result.EntryID != "single" {
		t.Fatalf("exact scanned entry was not reused: %+v %v", result, err)
	}
	final, err := a.library.Read(scope)
	if err != nil || len(final.Entries) != len(doc.Entries) || sessionEntry(final, "single").Link.WorkspaceID != childID {
		t.Fatalf("association duplicated discovered entry: %+v %v", final, err)
	}
	if _, err := a.library.ReviewLinkedProject(scope, childID, final.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("a linked project was reviewed for duplicate adoption: %v", err)
	}
}

func TestLinkedAssociation_RefusesReplacementOfAnObservedFolderAfterReview(t *testing.T) {
	a, scope, _, file, tree, plugins := activationFixture(t)
	childID := connectExistingSong(t, scope, file, plugins, tree.single)
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := a.library.ReviewLinkedProject(scope, childID, doc.Revision)
	if err != nil || review.EntryID != "single" {
		t.Fatalf("review exact source: %+v %v", review, err)
	}
	moved := filepath.Join(tree.root, "Single-original")
	if err := os.Rename(tree.single, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tree.single, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.single, "Song.rpp"), []byte("different song"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.library.CommitLinkedProject(scope, childID, review.Token, "replaced-source"); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-named replacement inherited old observation: %v", err)
	}
	current, err := a.library.Read(scope)
	if err != nil || len(current.Entries) != len(doc.Entries) || sessionEntry(current, "single").Link != nil {
		t.Fatalf("refused association changed catalog: %+v %v", current, err)
	}
}

func TestLinkedAssociation_SQLitePrimaryRejectsChangedChildMirrorAndRetriesFinalWrite(t *testing.T) {
	_, scope, _, file, _, plugins := activationFixture(t)
	outside := filepath.Join(t.TempDir(), "SQLite-song")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "Song.rpp"), []byte("private song"), 0o600); err != nil {
		t.Fatal(err)
	}
	childID := connectExistingSong(t, scope, file, plugins, outside)
	db, err := database.Open(context.Background(), &database.Config{Path: filepath.Join(t.TempDir(), "ori.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	for _, id := range []string{scope.HomeID, childID} {
		item, getErr := file.Get(id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if err := primary.Save(item); err != nil {
			t.Fatal(err)
		}
	}
	synced := workspace.NewSyncStore(primary, file)
	library := NewStore(synced).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return true })
	pending, err := library.PendingLinkedProjects(scope)
	if err != nil || pending.Total != 1 || pending.Rows[0].WorkspaceID != childID {
		t.Fatalf("portable child source absent in SQLite-primary read: %+v %v", pending, err)
	}
	base, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := library.ReviewLinkedProject(scope, childID, pending.Revision)
	if err != nil || !review.LinkOnly {
		t.Fatalf("review split-store link: %+v %v", review, err)
	}
	child, err := file.Get(childID)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Update(childID, func(item *workspace.Workspace) error {
		link := item.GetAssistantProjectLink()
		link.StateRevision++
		item.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := library.CommitLinkedProject(scope, childID, review.Token, "sqlite-associate"); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("split child mirror was accepted: %v", err)
	}
	if pending, err := library.PendingLinkedProjects(scope); !errors.Is(err, ErrMirrorDiverged) || pending.Total != 0 {
		t.Fatalf("split child mirror was misreported as an empty pending shelf: %+v %v", pending, err)
	}
	// Test-only exact restoration of the folder mirror (same version as the
	// primary); a bumping Save would leave the fence tripped on version drift.
	if err := file.RestoreMirrorRecord(child); err != nil {
		t.Fatal(err)
	}
	if pending, err := library.PendingLinkedProjects(scope); err != nil || pending.Total != 1 || pending.Rows[0].WorkspaceID != childID {
		t.Fatalf("restored child did not return as a pending exact link: %+v %v", pending, err)
	}
	broken := NewStore(workspace.NewSyncStore(&failingLibrarySave{Store: primary}, file)).WithProviderEvidence(
		func(_ Scope, _ *workspace.Workspace) bool { return true })
	if _, err := broken.CommitLinkedProject(scope, childID, review.Token, "sqlite-associate"); err == nil {
		t.Fatal("SQLite write failure was not reported")
	}
	accepted, err := library.CommitLinkedProject(scope, childID, review.Token, "sqlite-associate")
	if err != nil || accepted.EntryID == "" {
		t.Fatalf("retry after split-store failure: %+v %v", accepted, err)
	}
	result, err := library.Read(scope)
	if err != nil || len(result.Entries) != len(base.Entries)+1 || len(result.Roots) != len(base.Roots) ||
		sessionEntry(result, accepted.EntryID).Link.WorkspaceID != childID {
		t.Fatalf("split store invented an extra grant or lost link: %+v %v", result, err)
	}
}

func TestLinkedAssociation_RecoversFinalHomeWriteFailureWithoutGrantingASecondRoot(t *testing.T) {
	a, scope, _, file, _, plugins := activationFixture(t)
	outside := filepath.Join(t.TempDir(), "Another")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "Song.rpp"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	childID := connectExistingSong(t, scope, file, plugins, outside)
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := a.library.ReviewLinkedProject(scope, childID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := &failActivationHomeWrite{Store: file, homeID: scope.HomeID, pending: true}
	a.library = NewStore(interrupted).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return true })
	if _, err := a.library.CommitLinkedProject(scope, childID, review.Token, "direct-repair"); err == nil || interrupted.pending {
		t.Fatalf("Home write interruption did not surface: %v", err)
	}
	path, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	library := NewStore(reopened).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return true })
	result, err := library.CommitLinkedProject(scope, childID, review.Token, "direct-repair")
	if err != nil || result.EntryID == "" {
		t.Fatalf("failed association was not recoverable: %+v %v", result, err)
	}
	accepted, err := library.Read(scope)
	if err != nil || len(accepted.Roots) != len(doc.Roots) || len(accepted.Entries) != len(doc.Entries)+1 {
		t.Fatalf("repair added a grant or duplicate: %+v %v", accepted, err)
	}
}
