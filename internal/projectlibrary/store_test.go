package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func libraryHome(t *testing.T) (*workspace.FileStore, Scope) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{OwnerUserID: "local", HomeID: "music-home", ProviderID: "music-project-management", ProgramID: "music-producer-assistant"}
	ws := &workspace.Workspace{ID: scope.HomeID, Name: "Music Home", OwnerUserID: scope.OwnerUserID,
		Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ws.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion:   workspace.AssistantProgramStateSchemaVersion,
		PluginAvailable: true,
		Key:             workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID},
	})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return store, scope
}

func initializeLibrary(t *testing.T, s *Store, scope Scope) OperationReceipt {
	t.Helper()
	review, err := s.ReviewInitialize(scope)
	if err != nil {
		t.Fatalf("review initialize: %v", err)
	}
	doc, replay, err := s.CommitInitialize(scope, review.Token, "init")
	if err != nil || replay || len(doc.Operations) != 1 {
		t.Fatalf("initialize: %+v replay=%v err=%v", doc, replay, err)
	}
	return doc.Operations[0]
}

func TestStore_ReadIsInertAndInitializationPersistsWithMarker(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	before, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(scope); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("read of legacy Home: %v", err)
	}
	after, err := file.Get(scope.HomeID)
	if err != nil || after.Version != before.Version || len(after.GetAssistantProgramState().ProjectLibrary) != 0 {
		t.Fatalf("read performed migration: %+v %v", after, err)
	}
	initial := initializeLibrary(t, s, scope)
	if _, err := NewStore(workspace.NewAgentSnapshotStore(file, nil)).Read(scope); err != nil {
		t.Fatalf("bare FileStore decorator incorrectly claimed a second mirror: %v", err)
	}
	folder, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(folder, workspace.WorkspaceConfigFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private Home record permissions: %+v %v", info, err)
	}
	if initial.Revision != 1 {
		t.Fatalf("initial receipt: %+v", initial)
	}
	legacy := workspace.NewAssistantPortfolioService(file)
	if _, err := legacy.List(scope.HomeID); !errors.Is(err, workspace.ErrAssistantPortfolioLibraryOwned) {
		t.Fatalf("legacy reader displayed stale editable state: %v", err)
	}
	if _, err := legacy.Review(scope.HomeID, "link", 0, workspace.AssistantPortfolioUpdate{Status: workspace.AssistantPortfolioStatusPlanning, ArchiveReviewState: workspace.AssistantArchiveReviewNotReady}); !errors.Is(err, workspace.ErrAssistantPortfolioLibraryOwned) {
		t.Fatalf("legacy writer bypassed authority switch: %v", err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := NewStore(reopened).Read(scope)
	if err != nil || persisted.Revision != 1 || len(persisted.Operations) != 1 || persisted.Operations[0] != initial {
		t.Fatalf("restart: %+v %v", persisted, err)
	}
	// The workspace clone must not alias the original raw JSON backing array.
	live, err := reopened.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	copyState := live.GetAssistantProgramState()
	copyState.ProjectLibrary[0] = 'x'
	if _, err := NewStore(reopened).Read(scope); err != nil {
		t.Fatalf("cloned workspace state was aliased: %v", err)
	}
}

func TestStore_LegacyReviewCannotCommitAcrossAuthoritySwitch(t *testing.T) {
	file, scope := libraryHome(t)
	project := &workspace.Workspace{ID: "linked", Name: "Linked song", Status: workspace.StatusActive,
		OwnerUserID: scope.OwnerUserID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	project.SetAssistantProjectLink(&workspace.AssistantProjectLink{
		ID: workspace.AssistantProjectLinkID(scope.HomeID, project.ID), StationWorkspaceID: scope.HomeID, Key: key,
		StateRevision: 1,
	})
	if err := file.Save(project); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = []string{project.ID}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	old := workspace.NewAssistantPortfolioService(file)
	update := workspace.AssistantPortfolioUpdate{Status: workspace.AssistantPortfolioStatusPlanning,
		ArchiveReviewState: workspace.AssistantArchiveReviewNotReady}
	review, err := old.Review(scope.HomeID, project.GetAssistantProjectLink().ID, 0, update)
	if err != nil {
		t.Fatal(err)
	}
	initializeLibrary(t, NewStore(file), scope)
	if _, err := old.Commit(scope.HomeID, review.Token, "old-key", update); !errors.Is(err, workspace.ErrAssistantPortfolioLibraryOwned) {
		t.Fatalf("old review committed after switch: %v", err)
	}
	if doc, err := NewStore(file).Read(scope); err != nil || len(doc.Entries) != 1 ||
		doc.Entries[0].Link == nil || doc.Entries[0].Fields.Revision != 0 {
		t.Fatalf("old review mutated managed document or initialization lost the exact link: %+v %v", doc, err)
	}
}

func TestStore_SplitHomeMirrorBlocksLegacyPortfolioWrites(t *testing.T) {
	file, scope := libraryHome(t)
	primary := workspace.NewInMemoryStore()
	legacyHome, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(legacyHome); err != nil {
		t.Fatal(err)
	}
	initializeLibrary(t, NewStore(file), scope) // Simulate crash after folder write, before primary save.
	mirrored := workspace.NewSyncStore(primary, file)
	if _, err := NewStore(mirrored).Read(scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("split marker not detected: %v", err)
	}
	// Production wraps SyncStore with AgentSnapshotStore. That decorator must
	// not hide the folder mirror from either the new or legacy authority gate.
	wrapped := workspace.NewAgentSnapshotStore(mirrored, nil)
	if _, err := NewStore(wrapped).Read(scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("decorated split marker not detected: %v", err)
	}
	legacy := workspace.NewAssistantPortfolioService(wrapped)
	if _, err := legacy.List(scope.HomeID); !errors.Is(err, workspace.ErrAssistantPortfolioLibraryOwned) {
		t.Fatalf("legacy read silently claimed split Home: %v", err)
	}
	update := workspace.AssistantPortfolioUpdate{Status: workspace.AssistantPortfolioStatusPlanning,
		ArchiveReviewState: workspace.AssistantArchiveReviewNotReady}
	if _, err := legacy.Review(scope.HomeID, "any-link", 0, update); !errors.Is(err, workspace.ErrAssistantPortfolioLibraryOwned) {
		t.Fatalf("legacy write silently claimed split Home: %v", err)
	}
}

func TestStore_SyncStoreRoundTripAndRollback(t *testing.T) {
	file, scope := libraryHome(t)
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := database.Open(context.Background(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	seed, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(seed); err != nil {
		t.Fatal(err)
	}
	syncStore := workspace.NewSyncStore(primary, file)
	initial := initializeLibrary(t, NewStore(syncStore), scope)
	for _, backing := range []workspace.Store{primary, file} {
		doc, readErr := NewStore(backing).Read(scope)
		if readErr != nil || doc.Operations[0] != initial {
			t.Fatalf("mirror lost marker or receipt: %+v %v", doc, readErr)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(context.Background(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	primary = session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(reopened, 10))
	if doc, readErr := NewStore(primary).Read(scope); readErr != nil || doc.Revision != 1 {
		t.Fatalf("DB restart lost authority marker: %+v %v", doc, readErr)
	}
	if _, readErr := NewStore(workspace.NewSyncStore(primary, file)).Read(scope); readErr != nil {
		t.Fatalf("sync mirrors disagreed after normal save: %v", readErr)
	}
	failed := workspace.NewSyncStore(&failingLibrarySave{Store: primary}, file)
	_, _, err = NewStore(failed).mutate(scope, 1, operation{key: "failed-write", action: "edit", digest: "input"},
		func(*Document) (string, error) { return "result", nil })
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("mirror write should fail: %v", err)
	}
	for _, backing := range []workspace.Store{primary, file} {
		doc, readErr := NewStore(backing).Read(scope)
		if readErr != nil || doc.Revision != 1 || len(doc.Operations) != 1 {
			t.Fatalf("failed write left split marker or receipt: %+v %v", doc, readErr)
		}
	}
	// Simulate a crash after the folder rename but before SQLite commit.
	// The old primary must neither expose nor overwrite the newer folder.
	if err := file.Update(scope.HomeID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		var updated Document
		if err := json.Unmarshal(state.ProjectLibrary, &updated); err != nil {
			return err
		}
		updated.Revision++
		state.ProjectLibrary, _ = json.Marshal(updated)
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(workspace.NewSyncStore(primary, file)).Read(scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("split mirrors were silently accepted: %v", err)
	}
	if _, _, err := NewStore(workspace.NewSyncStore(primary, file)).mutate(scope, 1,
		operation{key: "unsafe-retry", action: "edit", digest: "x"},
		func(*Document) (string, error) { t.Fatal("mutated a split mirror"); return "", nil }); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("split mirrors accepted a write: %v", err)
	}
}

type failingLibrarySave struct{ workspace.Store }

func (f *failingLibrarySave) Save(*workspace.Workspace) error { return os.ErrPermission }

func TestStore_RetryCASIsolationAndConcurrentClients(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	apply := func(rev int64, key, digest string) (OperationReceipt, bool, error) {
		return s.mutate(scope, rev, operation{key: key, action: "edit", digest: digest}, func(doc *Document) (string, error) {
			doc.Entries = append(doc.Entries, Entry{ID: newID(), Revision: 1,
				Link:   &ExactLink{WorkspaceID: "project", LinkID: "link", Revision: 1},
				Fields: Fields{DisplayName: key, Revision: 1}})
			return key, nil
		})
	}
	first, replay, err := apply(1, "one", "payload")
	if err != nil || replay {
		t.Fatalf("first: %+v %v %v", first, replay, err)
	}
	got, replay, err := apply(1, "one", "payload")
	if err != nil || !replay || got != first {
		t.Fatalf("retry: %+v %v %v", got, replay, err)
	}
	if _, _, err := apply(1, "one", "different"); !errors.Is(err, ErrConflict) {
		t.Fatalf("key reuse: %v", err)
	}
	if _, _, err := apply(1, "stale", "payload"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("parallel-%d", i)
			for attempt := 0; attempt < 100; attempt++ {
				current, err := s.Read(scope)
				if err != nil {
					failures <- err
					return
				}
				_, _, err = apply(current.Revision, key, "payload")
				if errors.Is(err, ErrConflict) {
					continue
				}
				if err != nil {
					failures <- err
				}
				return
			}
			failures <- errors.New("CAS retry budget exhausted")
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	doc, err := s.Read(scope)
	if err != nil || len(doc.Entries) != 17 || doc.Revision != 18 || len(doc.Operations) != 18 {
		t.Fatalf("lost update: entries=%d rev=%d ops=%d err=%v", len(doc.Entries), doc.Revision, len(doc.Operations), err)
	}
}

func TestStore_PersistsThousandRecordsBeyondLegacyEditCap(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	root := newID()
	scan := newID()
	at := time.Now().UTC()
	_, _, err := s.mutate(scope, 1, operation{key: "bulk-fixture", action: "catalog_fixture", digest: "stable"},
		func(doc *Document) (string, error) {
			path := t.TempDir()
			doc.Roots = append(doc.Roots, Root{ID: root, Path: path, FileIdentity: "fixture:root", Revision: 1, ApprovedAt: at})
			doc.Scans = append(doc.Scans, Scan{ID: scan, RootID: root, RootRevision: 1,
				RootDigest:   rootDigest(scope, "scan_source", path, "fixture:root", 1),
				ResultDigest: strings.Repeat("0", 64), Status: "complete", StartedAt: at, FinishedAt: &at})
			for i := 0; i < 1000; i++ {
				fields := Fields{DisplayName: fmt.Sprintf("Song %04d", i), Revision: 1}
				if i < 40 {
					fields.Status = "active"
					fields.Author = "test user"
				}
				doc.Entries = append(doc.Entries, Entry{ID: newID(), Revision: 1, Fields: fields,
					Observations: []Observation{{RootID: root, RelativeFolder: fmt.Sprintf("Song-%04d", i),
						FileIdentity: fmt.Sprintf("fixture:%d", i), Format: "reaper", ScanID: scan,
						ScannedAt: at, Availability: "available"}}})
			}
			return scan, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := NewStore(restarted).Read(scope)
	if err != nil || len(doc.Entries) != 1000 || len(doc.Operations) != 2 {
		t.Fatalf("large document restart: entries=%d operations=%d err=%v", len(doc.Entries), len(doc.Operations), err)
	}
	for i := 0; i < 40; i++ {
		if doc.Entries[i].Fields.Status != "active" {
			t.Fatalf("edited record %d disappeared", i)
		}
	}
}

func TestStore_CorruptAndFailedWriteFailClosed(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	failure := &failingLibraryUpdate{Store: file}
	_, _, err := NewStore(failure).mutate(scope, 1, operation{key: "failed", action: "edit", digest: "payload"}, func(doc *Document) (string, error) {
		return "not-saved", nil
	})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("failed write: %v", err)
	}
	doc, err := s.Read(scope)
	if err != nil || doc.Revision != 1 || len(doc.Operations) != 1 {
		t.Fatalf("failed save was exposed: %+v %v", doc, err)
	}
	for _, raw := range []json.RawMessage{[]byte("null"), []byte(`{"schema_version":99}`), []byte(`{"schema_version":1,"unexpected":true}`)} {
		if err := file.Update(scope.HomeID, func(ws *workspace.Workspace) error {
			state := ws.GetAssistantProgramState()
			state.ProjectLibrary = raw
			ws.SetAssistantProgramState(state)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Read(scope); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("corrupt doc read: %v", err)
		}
		if _, _, err := s.mutate(scope, 1, operation{key: "bad", action: "edit", digest: "x"}, func(*Document) (string, error) {
			t.Fatal("must never mutate corrupt doc")
			return "", nil
		}); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("corrupt doc write: %v", err)
		}
	}
}

type failingLibraryUpdate struct{ workspace.Store }

func (f *failingLibraryUpdate) Update(string, func(*workspace.Workspace) error) error {
	return os.ErrPermission
}

func TestStore_HomeStatusAndOwnerAreCheckedOnEveryReadAndWrite(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	other := scope
	other.OwnerUserID = "someone-else"
	if _, err := s.Read(other); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign read: %v", err)
	}
	// Simulate a lifecycle store returning a trashed/missing Home. Normal
	// workspace writes cannot change a protected Home's status directly.
	for _, status := range []workspace.WorkspaceStatus{workspace.StatusTrashed, workspace.StatusMissing} {
		closed := NewStore(&statusStore{Store: file, status: status})
		if _, err := closed.Read(scope); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("read under %s: %v", status, err)
		}
		if _, _, err := closed.mutate(scope, 1, operation{key: "closed", action: "edit", digest: "x"}, func(*Document) (string, error) {
			t.Fatal("mutated unavailable Home")
			return "", nil
		}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("write under %s: %v", status, err)
		}
	}
	if doc, err := s.Read(scope); err != nil || doc.Revision != 1 {
		t.Fatalf("restored Home lost its document: %+v %v", doc, err)
	}
}

type statusStore struct {
	workspace.Store
	status workspace.WorkspaceStatus
}

func (s *statusStore) Get(id string) (*workspace.Workspace, error) {
	ws, err := s.Store.Get(id)
	if err == nil {
		ws.Status = s.status
	}
	return ws, err
}

func (s *statusStore) Update(id string, fn func(*workspace.Workspace) error) error {
	return s.Store.Update(id, func(ws *workspace.Workspace) error {
		ws.Status = s.status
		return fn(ws)
	})
}

func TestStore_ModelRejectsTraversalAndUnexpectedValues(t *testing.T) {
	for _, name := range []string{"../other", "a/../other", "/absolute", "a//b", "a\\b", "a\x00b"} {
		if validRelative(name) {
			t.Errorf("accepted path %q", name)
		}
	}
	if (Fields{DisplayName: "Song", Status: "live_controlled"}).valid() {
		t.Fatal("unreviewed status accepted")
	}
	if !filepath.IsAbs(t.TempDir()) {
		t.Fatal("test roots must be absolute")
	}
}

func TestStore_InactiveRootIDsMustNamePersistedRoots(t *testing.T) {
	file, scope := libraryHome(t)
	initializeLibrary(t, NewStore(file), scope)
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibraryInactiveRoots = []string{"foreign-root"}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(file).Read(scope); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("forged inactive root bypassed library validation: %v", err)
	}
}
