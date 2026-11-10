package projectconnection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type nestedConnection struct {
	service *Service
	store   *workspace.SyncStore
	scope   Scope
	homeID  string
	albums  string // the anchored music folder the folder workspace covers
	song    string // a song folder inside it
}

func newNestedConnection(t *testing.T) *nestedConnection {
	t.Helper()
	service, store, _, _ := splitConnectionService(t)
	scope := Scope{OwnerUserID: "owner-1", RunID: "nested-run", Template: splitConnectionTemplate(t)}
	home, err := service.CreateHome(scope, "My Production")
	if err != nil || home.HomeID == "" {
		t.Fatalf("prepare Home: %+v %v", home, err)
	}
	albums := filepath.Join(t.TempDir(), "Albums")
	song := filepath.Join(albums, "Album-1")
	if err := os.MkdirAll(song, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(song, "Album-1.rpp"), []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &nestedConnection{service: service, store: store, scope: scope, homeID: home.HomeID, albums: albums, song: song}
}

// addFolderWorkspace saves the fed folder's workspace inside the Home,
// anchored to the music folder, as a group or (joined before nesting) a leaf.
func (f *nestedConnection) addFolderWorkspace(t *testing.T, kind string, tasks ...workspace.Task) string {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Albums"})
	ws.OwnerUserID, ws.Kind, ws.ParentID = "owner-1", kind, f.homeID
	ws.SharedData = map[string]any{workspace.AnchorDirectoryReferenceIDKey: "albums-anchor"}
	ws.Tasks = tasks
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{ID: "albums-anchor", Name: "Albums", Path: f.albums}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return ws.ID
}

func (f *nestedConnection) review(t *testing.T) (Request, Preview) {
	t.Helper()
	token, err := f.service.selections.(*pathselection.Store).Issue(f.song)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Album-1"}
	preview, err := f.service.Preview(context.Background(), f.scope, request)
	if err != nil {
		t.Fatal(err)
	}
	return request, preview
}

func (f *nestedConnection) commit(t *testing.T) CommitResult {
	t.Helper()
	request, preview := f.review(t)
	result, err := f.service.Commit(context.Background(), f.scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil || result.HomeWorkspaceID != f.homeID {
		t.Fatalf("commit: %+v %v", result, err)
	}
	return result
}

// FR4–FR6, FR11: the song lands inside the covering folder workspace, its
// station stays the Home, the Home lists it, and both mirrors agree.
func TestCommit_SongLandsInsideItsCoveringFolderWorkspace(t *testing.T) {
	f := newNestedConnection(t)
	albumsID := f.addFolderWorkspace(t, "group")
	request, preview := f.review(t)
	result, err := f.service.Commit(context.Background(), f.scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if result.ParentWorkspaceID != albumsID || result.PlacementNote != "" {
		t.Fatalf("result = %+v, want parent %q and no note", result, albumsID)
	}
	song, err := f.store.Get(result.ProjectWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	link := song.GetAssistantProjectLink()
	if song.ParentID != albumsID || link == nil || link.StationWorkspaceID != f.homeID {
		t.Fatalf("song = parent %q link %+v", song.ParentID, link)
	}
	home, err := f.store.Get(f.homeID)
	if err != nil || !containsProjectID(home.GetAssistantProgramState().LinkedProjectIDs, song.ID) {
		t.Fatalf("the Home does not list the nested song: %v", err)
	}
	if !workspace.AssistantProjectLinkMirrorsAgree(f.store, song) {
		t.Fatal("the nested song's mirrors disagree, so a reload would show needs_review")
	}
	if !f.service.Observe(f.scope, f.homeID, song.ID, projecttemplates.ProjectConnectionExistingProject) {
		t.Fatal("the nested song is not observed as connected")
	}
	// A retry of the same reviewed commit finds the same nested child.
	replay, err := f.service.Commit(context.Background(), f.scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil || replay.ProjectWorkspaceID != song.ID || replay.ParentWorkspaceID != albumsID {
		t.Fatalf("replay = %+v %v", replay, err)
	}
}

// FR2: a folder workspace that joined before nesting is promoted in place,
// inside the same set-up, before the song is created inside it.
func TestCommit_PromotesALeafFolderWorkspaceFirst(t *testing.T) {
	f := newNestedConnection(t)
	albumsID := f.addFolderWorkspace(t, "")
	result := f.commit(t)
	albums, err := f.store.Get(albumsID)
	if err != nil || albums.Kind != "group" || albums.ParentID != f.homeID {
		t.Fatalf("folder workspace after set-up: %+v %v", albums, err)
	}
	if result.ParentWorkspaceID != albumsID {
		t.Fatalf("song parent = %q, want the promoted folder workspace", result.ParentWorkspaceID)
	}
}

// Open question 1: when promotion is refused, the song goes directly into
// the Home and the receipt says why in one plain sentence.
func TestCommit_RefusedPromotionFallsBackToTheHomeWithOneReason(t *testing.T) {
	f := newNestedConnection(t)
	albumsID := f.addFolderWorkspace(t, "", workspace.Task{ID: "running", Description: "Sort", Status: workspace.TaskStatusInProgress})
	result := f.commit(t)
	if result.ParentWorkspaceID != f.homeID {
		t.Fatalf("song parent = %q, want the Home", result.ParentWorkspaceID)
	}
	if !strings.Contains(result.PlacementNote, "Album-1 went into your My Production, not into Albums") ||
		!strings.Contains(result.PlacementNote, "running ticket") || strings.Count(result.PlacementNote, ".") != 1 {
		t.Fatalf("placement note = %q", result.PlacementNote)
	}
	if albums, _ := f.store.Get(albumsID); albums == nil || albums.Kind == "group" {
		t.Fatalf("a refused promotion still changed the folder workspace: %+v", albums)
	}
}

// The destination is part of the review: a folder workspace that appears
// after the review is a changed review, not a silent new placement.
func TestCommit_ADestinationChangedSinceTheReviewIsRefused(t *testing.T) {
	f := newNestedConnection(t)
	request, flat := f.review(t)
	f.addFolderWorkspace(t, "group")
	if _, err := f.service.Commit(context.Background(), f.scope, request, flat.InputDigest, flat.OwnerDigest); !errors.Is(err, ErrChanged) {
		t.Fatalf("commit after a new folder workspace = %v, want ErrChanged", err)
	}
}

// FR19: a covering folder workspace changes only where a new song goes. A song
// folder that already has its own workspace is still refused before anything
// is placed or promoted.
func TestPreview_AnOwnedSongFolderIsStillRefusedUnderAFolderWorkspace(t *testing.T) {
	f := newNestedConnection(t)
	albumsID := f.addFolderWorkspace(t, "")
	owner := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album-1 (mine)"})
	owner.OwnerUserID = "owner-1"
	owner.SharedData = map[string]any{workspace.AnchorDirectoryReferenceIDKey: "own"}
	if err := owner.AddDirectoryReference(workspace.DirectoryReference{ID: "own", Name: "Album-1", Path: f.song}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(owner); err != nil {
		t.Fatal(err)
	}
	token, err := f.service.selections.(*pathselection.Store).Issue(f.song)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Album-1"}
	if _, err := f.service.Preview(context.Background(), f.scope, request); !errors.Is(err, ErrChanged) {
		t.Fatalf("an owned song folder = %v, want the existing refusal (ErrChanged)", err)
	}
	if albums, _ := f.store.Get(albumsID); albums == nil || albums.Kind == "group" {
		t.Fatal("a refused review promoted the folder workspace")
	}
}

// A song with no covering folder workspace keeps today's exact review digest.
func TestPreview_ASongForTheHomeKeepsItsDigest(t *testing.T) {
	f := newNestedConnection(t)
	_, before := f.review(t)
	outside := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Elsewhere"})
	outside.OwnerUserID, outside.Kind = "owner-1", "group"
	if err := f.store.Save(outside); err != nil {
		t.Fatal(err)
	}
	if _, after := f.review(t); after.OwnerDigest != before.OwnerDigest {
		t.Fatal("an unrelated group changed a Home-bound song's review digest")
	}
}
