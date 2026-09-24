package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type folderLinkerFixture struct {
	linker   folderWorkspaceLinker
	sessions session.HybridStore
	files    *workspace.FileStore
	root     string
}

func newFolderLinkerFixture(t *testing.T) *folderLinkerFixture {
	t.Helper()
	root := t.TempDir()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewHybridStoreWithDB(db, 10)
	t.Cleanup(func() { _ = sessions.Close() })
	files, err := workspace.NewFileStore(filepath.Join(root, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	handler := sessionhttp.New(sessions)
	handler.SetWorkspaceStore(files)
	return &folderLinkerFixture{
		linker:   folderWorkspaceLinker{files: files, sessions: sessions, tasks: handler},
		sessions: sessions, files: files, root: root,
	}
}

// seed creates one workspace owned by the local user in both stores, as the
// Create Workspace modal does for a "show me a folder" offer.
func (f *folderLinkerFixture) seed(t *testing.T, id, name, offerID string, kind session.WorkspaceKind) {
	t.Helper()
	const owner = "local"
	shared := map[string]any{}
	if offerID != "" {
		shared[projecttemplates.FolderOfferIDKey] = offerID
	}
	slug := strings.ToLower(name)
	ws := &session.Workspace{ID: id, Name: name, Kind: kind, FolderSlug: slug, OwnerUserID: owner, SharedData: shared}
	if err := f.sessions.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	folderShared := map[string]any{}
	for k, v := range shared {
		folderShared[k] = v
	}
	if err := f.files.Save(&workspace.Workspace{
		ID: id, Name: name, Kind: string(kind), FolderSlug: slug, OwnerUserID: owner,
		SharedData: folderShared, Status: workspace.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *folderLinkerFixture) folder(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(f.root, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFolderWorkspaceLinker_AttachesOnceAndSeedsTheFirstTask(t *testing.T) {
	f := newFolderLinkerFixture(t)
	ctx := context.Background()
	f.seed(t, "ws-thesis", "Thesis", "offer-1", session.WorkspaceKindWorkspace)
	thesis := f.folder(t, "Thesis")
	req := personalassistant.FolderLinkRequest{
		UserID: "local", WorkspaceID: "ws-thesis", OfferID: "offer-1",
		Name: "Thesis", Path: thesis, Shape: "manuscript",
	}

	result, err := f.linker.LinkFolder(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.DirectoryID == "" || result.Route != "/workspaces/thesis" || !result.FirstTaskSeeded {
		t.Fatalf("result=%+v", result)
	}

	folderWS, err := f.files.Get("ws-thesis")
	if err != nil {
		t.Fatal(err)
	}
	if len(folderWS.DirectoryReferences) != 1 || folderWS.DirectoryReferences[0].Path != thesis || folderWS.DirectoryReferences[0].Name != "Thesis" {
		t.Fatalf("directory references=%+v", folderWS.DirectoryReferences)
	}
	if got, _ := folderWS.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); got != result.DirectoryID {
		t.Fatalf("primary directory=%q want %q", got, result.DirectoryID)
	}
	if folderWS.ProjectPath != "" {
		t.Fatalf("project_path must stay workspace-relative and unused, got %q", folderWS.ProjectPath)
	}
	if len(folderWS.Tasks) != 1 || folderWS.Tasks[0].Description != "Summarize the current draft and its open sections" || folderWS.Tasks[0].Status != workspace.TaskStatusPending {
		t.Fatalf("tasks=%+v", folderWS.Tasks)
	}
	row, err := f.sessions.GetWorkspace(ctx, "ws-thesis")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := row.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); got != result.DirectoryID {
		t.Fatalf("session primary directory=%q", got)
	}
	if !strings.Contains(string(row.DirectoryReferencesJSON), thesis) {
		t.Fatalf("session directory references=%s", row.DirectoryReferencesJSON)
	}

	// A replayed resolve links nothing twice and seeds no second task.
	again, err := f.linker.LinkFolder(ctx, req)
	if err != nil || again.DirectoryID != result.DirectoryID {
		t.Fatalf("replay=%+v err=%v", again, err)
	}
	folderWS, _ = f.files.Get("ws-thesis")
	if len(folderWS.DirectoryReferences) != 1 || len(folderWS.Tasks) != 1 {
		t.Fatalf("replay changed the workspace: %d refs %d tasks", len(folderWS.DirectoryReferences), len(folderWS.Tasks))
	}

	// A different folder on an already-linked workspace is refused.
	other := req
	other.Path = f.folder(t, "Other")
	if _, err := f.linker.LinkFolder(ctx, other); !errors.Is(err, personalassistant.ErrFolderWorkspaceRefused) {
		t.Fatalf("second folder err=%v", err)
	}
}

func TestFolderWorkspaceLinker_ShownFolderSupersedesABlueprintScaffold(t *testing.T) {
	f := newFolderLinkerFixture(t)
	ctx := context.Background()
	f.seed(t, "ws-scaffold", "Scaffold", "offer-1", session.WorkspaceKindWorkspace)
	// The writing-project blueprint scaffolds a starter project inside the
	// workspace folder and marks it primary before the offer is resolved.
	folderPath, err := f.files.GetFolderPath("ws-scaffold")
	if err != nil {
		t.Fatal(err)
	}
	scaffold := filepath.Join(folderPath, "scaffold")
	if err := os.MkdirAll(scaffold, 0o755); err != nil {
		t.Fatal(err)
	}
	folderWS, _ := f.files.Get("ws-scaffold")
	scaffoldID, err := projecttemplates.EnsureProjectDirectoryReference(folderWS, "Scaffold", folderPath, "scaffold")
	if err != nil {
		t.Fatal(err)
	}
	projecttemplates.SetPrimaryDirectoryID(folderWS.SharedData, scaffoldID)
	if err := f.files.Save(folderWS); err != nil {
		t.Fatal(err)
	}

	thesis := f.folder(t, "Thesis")
	result, err := f.linker.LinkFolder(ctx, personalassistant.FolderLinkRequest{
		UserID: "local", WorkspaceID: "ws-scaffold", OfferID: "offer-1", Name: "Thesis", Path: thesis, Shape: "manuscript",
	})
	if err != nil {
		t.Fatal(err)
	}
	folderWS, _ = f.files.Get("ws-scaffold")
	if got, _ := folderWS.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); got != result.DirectoryID || got == scaffoldID {
		t.Fatalf("primary=%q want the shown folder %q (scaffold %q)", got, result.DirectoryID, scaffoldID)
	}
	if len(folderWS.DirectoryReferences) != 2 {
		t.Fatalf("references=%+v", folderWS.DirectoryReferences)
	}
	if len(folderWS.Tasks) != 1 {
		t.Fatalf("tasks=%+v", folderWS.Tasks)
	}

	// An outside folder that is not a scaffold stays: another outside folder
	// on top of it is refused.
	f.seed(t, "ws-linked", "Linked", "offer-2", session.WorkspaceKindWorkspace)
	linkedWS, _ := f.files.Get("ws-linked")
	elsewhere := f.folder(t, "Elsewhere")
	if _, err := projecttemplates.AttachLinkedDirectory(linkedWS, "Elsewhere", elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := f.files.Save(linkedWS); err != nil {
		t.Fatal(err)
	}
	if _, err := f.linker.LinkFolder(ctx, personalassistant.FolderLinkRequest{
		UserID: "local", WorkspaceID: "ws-linked", OfferID: "offer-2", Name: "Thesis", Path: thesis, Shape: "manuscript",
	}); !errors.Is(err, personalassistant.ErrFolderWorkspaceRefused) {
		t.Fatalf("outside primary err=%v", err)
	}
}

func TestFolderWorkspaceLinker_RefusesWorkspacesNotCreatedForTheOffer(t *testing.T) {
	f := newFolderLinkerFixture(t)
	ctx := context.Background()
	thesis := f.folder(t, "Thesis")
	f.seed(t, "ws-other-offer", "Other", "offer-99", session.WorkspaceKindWorkspace)
	f.seed(t, "ws-no-offer", "Plain", "", session.WorkspaceKindWorkspace)
	// The session store only accepts owners it knows, so the ownership check
	// is exercised from the other side: a local workspace and a request from
	// someone else.
	f.seed(t, "ws-foreign", "Foreign", "offer-1", session.WorkspaceKindWorkspace)
	f.seed(t, "ws-group", "Group", "offer-1", session.WorkspaceKindGroup)
	for _, scenario := range []struct {
		workspaceID string
		userID      string
		want        error
	}{
		{"ws-other-offer", "local", personalassistant.ErrFolderWorkspaceRefused},
		{"ws-no-offer", "local", personalassistant.ErrFolderWorkspaceRefused},
		{"ws-foreign", "someone-else", personalassistant.ErrFolderWorkspaceRefused},
		{"ws-group", "local", personalassistant.ErrFolderWorkspaceRefused},
		{"ws-missing", "local", personalassistant.ErrFolderWorkspaceNotFound},
	} {
		_, err := f.linker.LinkFolder(ctx, personalassistant.FolderLinkRequest{
			UserID: scenario.userID, WorkspaceID: scenario.workspaceID, OfferID: "offer-1", Name: "Thesis", Path: thesis, Shape: "manuscript",
		})
		if !errors.Is(err, scenario.want) {
			t.Errorf("%s: err=%v want %v", scenario.workspaceID, err, scenario.want)
		}
		if folderWS, getErr := f.files.Get(scenario.workspaceID); getErr == nil && len(folderWS.DirectoryReferences) != 0 {
			t.Errorf("%s: a refused link still attached the folder", scenario.workspaceID)
		}
	}
}

func TestFolderWorkspaceLinker_FirstTaskPerShapeAndAbsentWithoutTaskStore(t *testing.T) {
	f := newFolderLinkerFixture(t)
	ctx := context.Background()
	for i, scenario := range []struct {
		shape string
		want  string
	}{
		{"corpus", "Build the sources index from the documents already in this folder: one line per document with a citation and a one-paragraph summary"},
		{"code", "List the open TODOs and the last thing changed"},
		{"", "Tell me what is in this folder and what looks most active"},
	} {
		id := "ws-" + scenario.shape + "-x"
		f.seed(t, id, "Shape"+string(rune('A'+i)), "offer-"+id, session.WorkspaceKindWorkspace)
		path := f.folder(t, "folder-"+id)
		if _, err := f.linker.LinkFolder(ctx, personalassistant.FolderLinkRequest{
			UserID: "local", WorkspaceID: id, OfferID: "offer-" + id, Name: "F", Path: path, Shape: folderdigest.Shape(scenario.shape),
		}); err != nil {
			t.Fatal(err)
		}
		folderWS, _ := f.files.Get(id)
		if len(folderWS.Tasks) != 1 || folderWS.Tasks[0].Description != scenario.want {
			t.Errorf("%q: tasks=%+v", scenario.shape, folderWS.Tasks)
		}
	}

	// Without a task store to seed through, the link still succeeds and no
	// first task is claimed.
	noTasks := folderWorkspaceLinker{files: f.files, sessions: f.sessions}
	f.seed(t, "ws-plain", "Plain", "offer-plain", session.WorkspaceKindWorkspace)
	result, err := noTasks.LinkFolder(ctx, personalassistant.FolderLinkRequest{
		UserID: "local", WorkspaceID: "ws-plain", OfferID: "offer-plain", Name: "P", Path: f.folder(t, "Plain"), Shape: "manuscript",
	})
	if err != nil || result.FirstTaskSeeded {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if folderWS, _ := f.files.Get("ws-plain"); len(folderWS.Tasks) != 0 || len(folderWS.DirectoryReferences) != 1 {
		t.Fatalf("plain workspace: %d tasks %d refs", len(folderWS.Tasks), len(folderWS.DirectoryReferences))
	}
}
