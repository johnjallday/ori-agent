package downloadsjanitor

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// listableFakeStore is the package's fake workspace store plus List, which is
// what lets the service answer cross-workspace ownership questions.
type listableFakeStore struct {
	*fakeWorkspaceStore
}

func (s listableFakeStore) List() ([]string, error) {
	ids := make([]string, 0, len(s.workspaces))
	for id := range s.workspaces {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// twoWorkspaceService returns a service whose store can enumerate two
// workspaces, plus an isolated base directory to build folders under.
func twoWorkspaceService(t *testing.T) (*Service, listableFakeStore, string) {
	t.Helper()
	store, _ := newTestStore(t)
	workspaces := listableFakeStore{newFakeWorkspaceStore("ws-1", "ws-2")}
	return NewService(store, workspaces), workspaces, tempDirCanonical(t)
}

// TestConfirmSetup_RefusesAFolderAnotherWorkspaceManages is FR-49's core case.
// Two File Janitors on one folder would race to propose and act on the same
// files, and the journal could no longer say which install owned an outcome.
func TestConfirmSetup_RefusesAFolderAnotherWorkspaceManages(t *testing.T) {
	service, _, base := twoWorkspaceService(t)
	shared := mkdir(t, filepath.Join(base, "Downloads"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: shared}); err != nil {
		t.Fatalf("first setup: %v", err)
	}

	_, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-2", Path: shared})
	setupError := setupErrorFor(t, err)
	if setupError.Code != CodeFolderConflict {
		t.Fatalf("code = %q, want folder_conflict", setupError.Code)
	}
	// The user is told which workspace holds it, and offered a way forward.
	if setupError.ConflictWorkspaceID != "ws-1" {
		t.Fatalf("conflict workspace = %q, want ws-1", setupError.ConflictWorkspaceID)
	}
	if setupError.Repair != RepairChooseFolder {
		t.Fatalf("repair = %q", setupError.Repair)
	}

	// The rejected workspace is left completely unconfigured: no grant, no
	// directory reference, nothing to clean up.
	settings, err := service.store.LoadSettings("ws-2")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.IsSetUp() || settings.RootPath != "" || settings.DirectoryReferenceID != "" {
		t.Fatalf("a refused setup left state behind: %+v", settings)
	}
}

// TestConfirmSetup_RefusesNestedFolders covers the ancestor and descendant
// cases, which a plain equality check would miss entirely.
func TestConfirmSetup_RefusesNestedFolders(t *testing.T) {
	tests := []struct {
		name  string
		first string
		then  string
	}{
		{"second is inside the first", "Downloads", filepath.Join("Downloads", "Sub")},
		{"second contains the first", filepath.Join("Downloads", "Sub"), "Downloads"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, _, base := twoWorkspaceService(t)
			first := mkdir(t, filepath.Join(base, tc.first))
			second := mkdir(t, filepath.Join(base, tc.then))

			if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: first}); err != nil {
				t.Fatalf("first setup: %v", err)
			}
			_, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-2", Path: second})
			if got := setupErrorFor(t, err); got.Code != CodeFolderConflict {
				t.Fatalf("code = %q, want folder_conflict", got.Code)
			}
		})
	}
}

// TestConfirmSetup_ConflictSurvivesSymlinkAndSpellingTricks proves the check
// runs on the canonical root. Reaching the same folder through a link, or
// through a differently-spelled path, is still the same folder.
func TestConfirmSetup_ConflictSurvivesSymlinkAndSpellingTricks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	service, _, base := twoWorkspaceService(t)
	real := mkdir(t, filepath.Join(base, "Downloads"))
	link := filepath.Join(base, "Inbox")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: real}); err != nil {
		t.Fatalf("first setup: %v", err)
	}

	for _, spelling := range []string{
		link,
		real + string(filepath.Separator),
		filepath.Join(base, "Downloads", "..", "Downloads"),
	} {
		_, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-2", Path: spelling})
		if got := setupErrorFor(t, err); got.Code != CodeFolderConflict {
			t.Fatalf("spelling %q bypassed the conflict check: code = %q", spelling, got.Code)
		}
	}
}

// TestConfirmSetup_AllowsSiblingFolders is the negative control: the guard must
// not be so broad that two workspaces cannot each tidy their own folder.
func TestConfirmSetup_AllowsSiblingFolders(t *testing.T) {
	service, _, base := twoWorkspaceService(t)

	if _, err := service.ConfirmSetup(SetupRequest{
		WorkspaceID: "ws-1", Path: mkdir(t, filepath.Join(base, "Downloads")),
	}); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{
		WorkspaceID: "ws-2", Path: mkdir(t, filepath.Join(base, "Scans")),
	}); err != nil {
		t.Fatalf("a sibling folder should be allowed: %v", err)
	}
}

// TestConfirmSetup_ReconfiguringTheSameWorkspaceIsNotAConflict guards the
// obvious self-collision: a workspace re-confirming its own folder must not be
// told another workspace owns it.
func TestConfirmSetup_ReconfiguringTheSameWorkspaceIsNotAConflict(t *testing.T) {
	service, _, base := twoWorkspaceService(t)
	root := mkdir(t, filepath.Join(base, "Downloads"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("re-confirming the same folder must be allowed: %v", err)
	}
}

// TestConfirmSetup_ReleasedFolderBecomesAvailable proves ownership tracks the
// ACTIVE grant. A workspace that revoked access keeps its audit state but
// releases the folder, so another workspace may then claim it.
func TestConfirmSetup_ReleasedFolderBecomesAvailable(t *testing.T) {
	service, _, base := twoWorkspaceService(t)
	shared := mkdir(t, filepath.Join(base, "Downloads"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: shared}); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	if _, err := service.RevokeAccess(nil, "ws-1"); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-2", Path: shared}); err != nil {
		t.Fatalf("a released folder should be claimable: %v", err)
	}
}

// TestRelink_RefusesAFolderAnotherWorkspaceManagesWithoutTearingDown is the
// FR-56 ordering guarantee: a relink that cannot succeed must leave the old
// setup intact and running, rather than pausing this workspace and leaving it
// unconfigured.
func TestRelink_RefusesAFolderAnotherWorkspaceManagesWithoutTearingDown(t *testing.T) {
	service, _, base := twoWorkspaceService(t)
	mine := mkdir(t, filepath.Join(base, "Mine"))
	theirs := mkdir(t, filepath.Join(base, "Theirs"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: mine}); err != nil {
		t.Fatalf("setup ws-1: %v", err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-2", Path: theirs}); err != nil {
		t.Fatalf("setup ws-2: %v", err)
	}

	_, err := service.Relink(nil, RelinkRequest{WorkspaceID: "ws-1", Path: theirs})
	if got := setupErrorFor(t, err); got.Code != CodeFolderConflict {
		t.Fatalf("code = %q, want folder_conflict", got.Code)
	}

	// ws-1 still owns its original folder and is still configured.
	settings, err := service.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !settings.IsSetUp() {
		t.Fatal("a refused relink left the workspace unconfigured")
	}
	if settings.RootPath != mine {
		t.Fatalf("root = %q, want the original folder %q", settings.RootPath, mine)
	}
	if settings.Paused {
		t.Fatal("a refused relink left the workspace paused")
	}
}

// TestOwnershipCheck_DegradesWhenTheStoreCannotList documents the deliberate
// fallback: a store that cannot enumerate workspaces cannot answer the
// question, and refusing every setup on that basis would be worse than the race
// it prevents.
func TestOwnershipCheck_DegradesWhenTheStoreCannotList(t *testing.T) {
	store, _ := newTestStore(t)
	// The plain fake store has no List method.
	service := NewService(store, newFakeWorkspaceStore("ws-1", "ws-2"))
	base := tempDirCanonical(t)
	shared := mkdir(t, filepath.Join(base, "Downloads"))

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: shared}); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-2", Path: shared}); err != nil {
		t.Fatalf("without listing, the check is skipped rather than failing closed: %v", err)
	}
}
