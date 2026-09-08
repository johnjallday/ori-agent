package samplelibrary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func catalogProgramState() *workspace.AssistantProgramState {
	return &workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramSchemaVersion, StateRevision: 1, Key: workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "fixture", ProgramID: "catalog-program"}, Declaration: &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "catalog-program", Roles: []workspace.AssistantProgramRoleSpec{{ID: "catalog", Scope: workspace.AssistantRoleScopeHome, CapabilityID: workspace.CapabilitySampleLibrary}}}, PluginAvailable: true}
}

func newTestService(t *testing.T) (*Service, *Store, *workspace.InMemoryStore, *pathselection.Store, string) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspaces := workspace.NewInMemoryStore()
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Home"})
	home.SetAssistantProgramState(catalogProgramState())
	home.SetInstalledCapabilities([]workspace.InstalledCapability{{ID: workspace.CapabilitySampleLibrary, Version: 1, InstalledAt: time.Now().UTC()}})
	if err := workspaces.Save(home); err != nil {
		t.Fatal(err)
	}
	selections := pathselection.NewStore()
	store := NewStore(db)
	return NewService(store, workspaces, selections), store, workspaces, selections, home.ID
}

func TestConnectRootIsConsentBoundAndDoesNotScanOrGrantRuntimeRoot(t *testing.T) {
	ctx := context.Background()
	service, store, workspaces, selections, homeID := newTestService(t)
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "kick.wav"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := selections.Issue(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, canonicalErr := filepath.EvalSymlinks(rootPath)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if review.ExactPath != canonicalRoot || review.Limits.Entries != MaxEntries {
		t.Fatalf("bad review: %+v", review)
	}
	// Review authority is durable even though the exact path remains only in the
	// trusted picker store. Reconstructing the service simulates a process restart.
	service = NewService(store, workspaces, selections)
	state, root, err := service.CommitRoot(ctx, homeID, review.Token, token, "connect-1")
	if err != nil {
		t.Fatal(err)
	}
	replayedState, replayedRoot, err := service.CommitRoot(ctx, homeID, review.Token, token, "connect-1")
	if err != nil || replayedRoot.ID != root.ID || replayedState.CatalogRevision != state.CatalogRevision {
		t.Fatalf("idempotent replay = %+v %+v %v", replayedState, replayedRoot, err)
	}
	if state.CatalogRevision != 1 || root.Generation != 0 || root.Completeness != "not_indexed" {
		t.Fatalf("unexpected connected state: %+v %+v", state, root)
	}
	entries, err := store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(entries) != 0 {
		t.Fatalf("connect scanned entries: %v %#v", err, entries)
	}
	ws, err := workspaces.Get(homeID)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ws.GetDirectoryReference(root.DirectoryReferenceID)
	capability, _ := workspace.FindInstalledCapability(ws.GetInstalledCapabilities(), workspace.CapabilitySampleLibrary)
	if _, recorded := capability.Owns(workspace.ResourceDirectoryReference, root.DirectoryReferenceID); !recorded {
		t.Fatal("capability did not own its sample reference")
	}
	if err != nil || ref.Purpose != "sample_library" {
		t.Fatalf("missing capability reference: %+v %v", ref, err)
	}
	if _, err = ws.ListDirectoryFiles(ref.ID); err == nil {
		t.Fatal("sample root was available through generic listing")
	}
}

func TestIndexIsExplicitBoundedAndReplacesGeneration(t *testing.T) {
	ctx := context.Background()
	service, store, _, selections, homeID := newTestService(t)
	rootPath := t.TempDir()
	for name := range map[string]bool{"A.WAV": true, "bass.flac": true, "notes.txt": true, ".hidden.mp3": true} {
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(rootPath, "A.WAV"), filepath.Join(rootPath, "alias.wav")); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(rootPath)
	review, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err := service.CommitRoot(ctx, homeID, review.Token, token, "connect")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Index(ctx, homeID, root.ID, "index-1", state.CatalogRevision, root.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Root.Completeness != "complete" || result.Receipt.Indexed != 2 {
		t.Fatalf("unexpected scan: %+v", result)
	}
	replay, err := service.Index(ctx, homeID, root.ID, "index-1", state.CatalogRevision, root.Revision)
	if err != nil || replay.Receipt.OperationID != result.Receipt.OperationID {
		t.Fatalf("scan replay=%+v err=%v", replay, err)
	}
	entries, err := store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%#v err=%v", entries, err)
	}
	if entries[0].RelativeLocator != "A.WAV" || entries[1].RelativeLocator != "bass.flac" {
		t.Fatalf("unexpected deterministic entries: %#v", entries)
	}
	search, err := service.Search(ctx, homeID, "BASS", 200)
	if err != nil || len(search.Entries) != 1 || search.Entries[0].Filename != "bass.flac" || !search.Complete {
		t.Fatalf("search=%+v err=%v", search, err)
	}
	if err := os.Remove(filepath.Join(rootPath, "bass.flac")); err != nil {
		t.Fatal(err)
	}
	result, err = service.Index(ctx, homeID, root.ID, "index-2", result.State.CatalogRevision, result.Root.Revision)
	if err != nil {
		t.Fatal(err)
	}
	entries, err = store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(entries) != 1 {
		t.Fatalf("generation was not replaced: %#v %v", entries, err)
	}
}

func TestRootReviewRejectsSymlinksAndOverlapsAcrossHomes(t *testing.T) {
	ctx := context.Background()
	service, _, workspaces, selections, homeID := newTestService(t)
	rootPath := t.TempDir()
	link := filepath.Join(t.TempDir(), "samples")
	if err := os.Symlink(rootPath, link); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(link)
	if _, err := service.ReviewRoot(ctx, homeID, token); !errors.Is(err, ErrInvalidRoot) {
		t.Fatalf("symlink err=%v", err)
	}
	token, _ = selections.Issue(rootPath)
	review, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.CommitRoot(ctx, homeID, review.Token, token, "one"); err != nil {
		t.Fatal(err)
	}
	other := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Other Home"})
	other.SetAssistantProgramState(catalogProgramState())
	other.SetInstalledCapabilities([]workspace.InstalledCapability{{ID: workspace.CapabilitySampleLibrary, Version: 1, InstalledAt: time.Now().UTC()}})
	if err = workspaces.Save(other); err != nil {
		t.Fatal(err)
	}
	token, _ = selections.Issue(rootPath)
	if _, err = service.ReviewRoot(ctx, other.ID, token); !errors.Is(err, ErrRootConflict) {
		t.Fatalf("cross-home overlap err=%v", err)
	}
}

func TestIndexRecoversExpiredDurableClaimWithoutAssumingSuccess(t *testing.T) {
	ctx := context.Background()
	service, store, _, selections, homeID := newTestService(t)
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "a.wav"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(rootPath)
	review, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err := service.CommitRoot(ctx, homeID, review.Token, token, "connect")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if _, err = store.db.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,root_id,operation_kind,idempotency_key,input_digest,status,created_at,expires_at) VALUES('stale',?,?,'index','stale',?,'claimed',?,?)`, homeID, root.ID, digestStrings("stale"), past, past); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Index(ctx, homeID, root.ID, "fresh", state.CatalogRevision, root.Revision); err != nil {
		t.Fatalf("recover stale claim: %v", err)
	}
	var status string
	if err = store.db.QueryRowContext(ctx, `SELECT status FROM sample_library_operation_receipt WHERE operation_id='stale'`).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("stale claim status=%q err=%v", status, err)
	}
}

func TestIndexCommitsHonestPartialGenerationAtInjectedBound(t *testing.T) {
	ctx := context.Background()
	service, store, _, selections, homeID := newTestService(t)
	rootPath := t.TempDir()
	for _, name := range []string{"a.wav", "b.wav", "c.wav"} {
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	service.SetLimitsForTest(Limits{Depth: 2, Visited: 2, Entries: 100, Directories: 10, WallTime: ProductionLimits().WallTime})
	token, _ := selections.Issue(rootPath)
	review, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err := service.CommitRoot(ctx, homeID, review.Token, token, "connect")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Index(ctx, homeID, root.ID, "partial", state.CatalogRevision, root.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Root.Completeness != "partial" || result.Receipt.ReasonCode != "sample_scan_partial" {
		t.Fatalf("expected partial: %+v", result)
	}
	entries, err := store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(entries) != 2 {
		t.Fatalf("partial retained unsafe count: %d %v", len(entries), err)
	}
}
