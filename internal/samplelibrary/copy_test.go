package samplelibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type copyTestWorkspaceStore struct {
	workspace.Store
	paths map[string]string
}

func (s copyTestWorkspaceStore) GetFolderPath(id string) (string, error) { return s.paths[id], nil }

func TestReviewedCopyTargetsOneReciprocalChildAndPreservesSource(t *testing.T) {
	ctx := context.Background()
	service, store, workspaces, selections, homeID := newTestService(t)
	sourceRoot := t.TempDir()
	source := filepath.Join(sourceRoot, "kick.wav")
	if err := os.WriteFile(source, []byte("sample bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(sourceRoot)
	rootReview, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err := service.CommitRoot(ctx, homeID, rootReview.Token, token, "root")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Index(ctx, homeID, root.ID, "index", state.CatalogRevision, root.Revision); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Entries(ctx, homeID, root.ID, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", len(entries), err)
	}
	home, _ := workspaces.Get(homeID)
	program := home.GetAssistantProgramState()
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Child"})
	childRoot := t.TempDir()
	child.ProjectPath = "song"
	child.SharedData = map[string]any{}
	if err = os.Mkdir(filepath.Join(childRoot, "song"), 0750); err != nil {
		t.Fatal(err)
	}
	if err = workspace.SetProjectEntryPath(child.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(childRoot, "song", "song.rpp"), []byte("project"), 0600); err != nil {
		t.Fatal(err)
	}
	link := &workspace.AssistantProjectLink{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: workspace.AssistantProjectLinkID(homeID, child.ID), StationWorkspaceID: homeID, Key: program.Key, StateRevision: 1}
	child.SetAssistantProjectLink(link)
	if err = workspaces.Save(child); err != nil {
		t.Fatal(err)
	}
	program.LinkedProjectIDs = []string{child.ID}
	home.SetAssistantProgramState(program)
	if err = workspaces.Save(home); err != nil {
		t.Fatal(err)
	}
	service.workspaces = copyTestWorkspaceStore{Store: workspaces, paths: map[string]string{child.ID: childRoot}}
	collisionDir := filepath.Join(childRoot, "song", "Samples", "Ori Imports")
	if err = os.MkdirAll(collisionDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(collisionDir, "kick.wav"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewCopy(ctx, homeID, child.ID, []string{entries[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	canonicalSource, _ := filepath.EvalSymlinks(source)
	if len(review.Items) != 1 || review.Items[0].SourcePath != canonicalSource || !review.Items[0].CollisionResolved {
		t.Fatalf("review=%+v", review)
	}
	service.beforeCopyPromote = func() error { return ErrOperationFailed }
	if _, err = service.CommitCopy(ctx, homeID, child.ID, review.Token, "copy", []string{entries[0].ID}); err == nil {
		t.Fatal("interrupted copy succeeded")
	}
	service = NewService(store, service.workspaces, selections)
	result, err := service.CommitCopy(ctx, homeID, child.ID, review.Token, "copy", []string{entries[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Copies) != 1 {
		t.Fatalf("result=%+v", result)
	}
	copied := filepath.Join(childRoot, "song", "Samples", "Ori Imports", "kick (2).wav")
	data, err := os.ReadFile(copied)
	if err != nil || string(data) != "sample bytes" {
		t.Fatalf("copy=%q err=%v", data, err)
	}
	targetHash := sha256.Sum256(data)
	original, _ := os.ReadFile(filepath.Join(collisionDir, "kick.wav"))
	if string(original) != "existing" {
		t.Fatal("collision overwritten")
	}
	sourceData, _ := os.ReadFile(source)
	if string(sourceData) != "sample bytes" {
		t.Fatal("source changed")
	}
	sourceHash := sha256.Sum256(sourceData)
	if sourceHash != targetHash || result.Copies[0].SHA256 != hex.EncodeToString(sourceHash[:]) {
		t.Fatal("copy digest mismatch")
	}
	sourceFiles, err := os.ReadDir(sourceRoot)
	if err != nil || len(sourceFiles) != 1 {
		t.Fatalf("source sidecars=%d err=%v", len(sourceFiles), err)
	}
	var count int
	if err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM sample_library_child_copy WHERE child_workspace_id=?`, child.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("provenance=%d err=%v", count, err)
	}
	replayed, err := service.CommitCopy(ctx, homeID, child.ID, review.Token, "copy", []string{entries[0].ID})
	if err != nil || !replayed.Replayed {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
}

func TestCopyReviewRejectsUnlinkedChild(t *testing.T) {
	ctx := context.Background()
	service, _, workspaces, _, homeID := newTestService(t)
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Other"})
	_ = workspaces.Save(child)
	service.workspaces = copyTestWorkspaceStore{Store: workspaces, paths: map[string]string{child.ID: t.TempDir()}}
	if _, err := service.ReviewCopy(ctx, homeID, child.ID, []string{"entry"}); err == nil {
		t.Fatal("unlinked child accepted")
	}
}
