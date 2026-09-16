package workspace

import (
	"errors"
	"strings"
	"testing"
)

func TestAssistantProgramTopologyRejectsGenericMoveTrashAndDelete(t *testing.T) {
	store := NewInMemoryStore()
	project := assistantProject(t, store, "Protected")
	station, _, err := NewAssistantProgramStore(store).EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(current *Workspace) error {
		current.ParentID = "somewhere-else"
		return nil
	}); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("generic move error = %v", err)
	}
	if err := store.Update(project.ID, func(current *Workspace) error {
		current.Status = StatusTrashed
		return nil
	}); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("generic trash error = %v", err)
	}
	if err := store.Delete(project.ID); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("linked delete error = %v", err)
	}
	if err := store.Delete(station.ID); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("Home delete error = %v", err)
	}
}

func TestAssistantProgramFolderMoveRefusesLinkedChildBeforeDiskMutation(t *testing.T) {
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	project := assistantProject(t, folders, "Disk protected")
	station, _, err := NewAssistantProgramStore(folders).EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := folders.GetFolderPath(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := folders.MoveWorkspaceFolder(project.ID, ""); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("move error = %v", err)
	}
	after, err := folders.GetFolderPath(project.ID)
	if err != nil || after != before {
		t.Fatalf("protected folder moved: before=%q after=%q err=%v", before, after, err)
	}
	linked, _ := folders.Get(project.ID)
	if linked.ParentID != station.ID || linked.GetAssistantProjectLink() == nil {
		t.Fatalf("protected topology changed: %+v", linked)
	}
}

func TestAssistantProgramReviewedDisconnectPreservesChildAndIsIdempotent(t *testing.T) {
	store := NewInMemoryStore()
	project := assistantProject(t, store, "Reviewed")
	project.Tasks = []Task{{ID: "task-retained", Description: "Keep me"}}
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	programs := NewAssistantProgramStore(store)
	station, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := station.GetAssistantProgramState()
	review, err := programs.ReviewDisconnect(station.ID, project.ID, state.StateRevision)
	if err != nil || review.LinkID == "" || len(review.Impact) != 3 {
		t.Fatalf("review = %+v, %v", review, err)
	}
	receipt, err := programs.CommitDisconnect(station.ID, review.Token, "disconnect-once")
	if err != nil || receipt.ProjectWorkspaceID != project.ID || receipt.Replayed {
		t.Fatalf("receipt = %+v, %v", receipt, err)
	}
	replay, err := programs.CommitDisconnect(station.ID, review.Token, "disconnect-once")
	if err != nil || !replay.Replayed {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	retained, err := store.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.AssistantProjectLink != nil || retained.ParentID != "" || len(retained.Tasks) != 1 {
		t.Fatalf("retained child = %+v", retained)
	}
	updatedStation, _ := store.Get(station.ID)
	if len(updatedStation.GetAssistantProgramState().LinkedProjectIDs) != 0 {
		t.Fatal("disconnected project remained in Home membership")
	}
}

func TestAssistantProgramDisconnectRejectsChangedLink(t *testing.T) {
	store := NewInMemoryStore()
	project := assistantProject(t, store, "Race")
	programs := NewAssistantProgramStore(store)
	station, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := programs.ReviewDisconnect(station.ID, project.ID, station.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(current *Workspace) error {
		link := current.GetAssistantProjectLink()
		link.StateRevision++
		current.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := programs.CommitDisconnect(station.ID, review.Token, "stale-disconnect"); !errors.Is(err, ErrAssistantTopologyConflict) {
		t.Fatalf("stale disconnect error = %v", err)
	}
	retained, _ := store.Get(project.ID)
	if retained.GetAssistantProjectLink() == nil {
		t.Fatal("stale review disconnected the project")
	}
}

func TestAssistantProgramReviewedHomeRemovalPreservesChildren(t *testing.T) {
	store := NewInMemoryStore()
	first := assistantProject(t, store, "First retained")
	second := assistantProject(t, store, "Second retained")
	programs := NewAssistantProgramStore(store)
	station, _, err := programs.EnsureProjectStation(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := programs.EnsureProjectStation(second.ID); err != nil {
		t.Fatal(err)
	}
	station, _ = store.Get(station.ID)
	review, err := programs.ReviewHomeRemoval(station.ID, station.GetAssistantProgramState().StateRevision)
	if err != nil || review.LinkedProjectCount != 2 || len(review.Impact) != 3 {
		t.Fatalf("Home removal review = %+v, %v", review, err)
	}
	if !strings.Contains(review.Impact[0], "moves to the Trash") {
		t.Fatalf("review impact should promise the Trash on a store that can trash: %q", review.Impact[0])
	}
	receipt, err := programs.CommitHomeRemoval(station.ID, review.Token)
	if err != nil || receipt.RetainedProjects != 2 || !receipt.Trashed {
		t.Fatalf("Home removal receipt = %+v, %v", receipt, err)
	}
	// The Home is soft-deleted, not gone: the record stays, marked trashed,
	// with its live program state cleared (so it no longer answers as a
	// station) and stashed for a restore.
	trashed, err := store.Get(station.ID)
	if err != nil || trashed == nil || trashed.Status != StatusTrashed {
		t.Fatalf("trashed Home = %+v, %v", trashed, err)
	}
	if trashed.GetAssistantProgramState() != nil {
		t.Fatal("trashed Home kept its live program state")
	}
	if stash := removedAssistantProgramState(trashed); stash == nil || len(stash.LinkedProjectIDs) != 0 {
		t.Fatalf("trashed Home stash = %+v", stash)
	}
	for _, projectID := range []string{first.ID, second.ID} {
		project, getErr := store.Get(projectID)
		if getErr != nil || project.GetAssistantProjectLink() != nil || project.ParentID != "" {
			t.Fatalf("retained project %q = %+v, %v", projectID, project, getErr)
		}
	}
}

// A store that cannot trash (a bare FileStore has no soft-delete) still removes
// the Home permanently, and says so in both the review and the receipt.
func TestAssistantProgramHomeRemovalDeletesWhenTrashUnsupported(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := assistantProject(t, store, "Permanent")
	programs := NewAssistantProgramStore(store)
	station, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	station, _ = store.Get(station.ID)
	review, err := programs.ReviewHomeRemoval(station.ID, station.GetAssistantProgramState().StateRevision)
	if err != nil || len(review.Impact) != 3 {
		t.Fatalf("Home removal review = %+v, %v", review, err)
	}
	if strings.Contains(review.Impact[0], "Trash") {
		t.Fatalf("review impact promised a Trash the store does not have: %q", review.Impact[0])
	}
	receipt, err := programs.CommitHomeRemoval(station.ID, review.Token)
	if err != nil || receipt.Trashed {
		t.Fatalf("Home removal receipt = %+v, %v", receipt, err)
	}
	if _, err := store.Get(station.ID); err == nil {
		t.Fatal("permanently removed Home remained in the store")
	}
}

func TestAssistantProgramRestoreRemovedHomeReturnsAnEmptyHome(t *testing.T) {
	store := NewInMemoryStore()
	project := assistantProject(t, store, "Once linked")
	programs := NewAssistantProgramStore(store)
	station, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	station, _ = store.Get(station.ID)
	key := station.GetAssistantProgramState().Key
	roles := len(station.GetAssistantProgramState().HomeBindings.Bindings)
	review, err := programs.ReviewHomeRemoval(station.ID, station.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := programs.CommitHomeRemoval(station.ID, review.Token); err != nil {
		t.Fatal(err)
	}

	// While the Home sits in the Trash the key is free: a new Home for the
	// same program can be created instead of resolving to the trashed one.
	if _, err := programs.FindStation(key); !errors.Is(err, ErrAssistantStationNotFound) {
		t.Fatalf("FindStation while trashed = %v, want not found", err)
	}
	// Restoring the state is refused while the record is still trashed; the
	// workspace has to come back out of the Trash first.
	if restored, err := programs.RestoreRemovedHome(station.ID); err != nil || restored {
		t.Fatalf("RestoreRemovedHome while trashed = %v, %v", restored, err)
	}

	if err := store.Update(station.ID, func(current *Workspace) error {
		current.Status = StatusActive
		delete(current.SharedData, TrashSharedDataKey)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	restored, err := programs.RestoreRemovedHome(station.ID)
	if err != nil || !restored {
		t.Fatalf("RestoreRemovedHome = %v, %v", restored, err)
	}
	home, err := programs.FindStation(key)
	if err != nil || home == nil || home.ID != station.ID {
		t.Fatalf("restored Home lookup = %+v, %v", home, err)
	}
	state := home.GetAssistantProgramState()
	if state == nil || len(state.LinkedProjectIDs) != 0 || len(state.HomeBindings.Bindings) != roles || len(state.Topology.ReviewReceipts) != 0 {
		t.Fatalf("restored Home state = %+v", state)
	}
	if removedAssistantProgramState(home) != nil {
		t.Fatal("restore left the stash behind")
	}
	// The former project stayed standalone; nothing re-linked it.
	retained, _ := store.Get(project.ID)
	if retained.GetAssistantProjectLink() != nil || retained.ParentID != "" {
		t.Fatalf("restore re-linked the retained project: %+v", retained)
	}
	// A second restore is a no-op.
	if again, err := programs.RestoreRemovedHome(station.ID); err != nil || again {
		t.Fatalf("second RestoreRemovedHome = %v, %v", again, err)
	}
}

// If a new Home took the program key while the old one was in the Trash, the
// restore keeps the workspace as a plain group rather than creating two Homes
// that every station lookup would then find ambiguous.
func TestAssistantProgramRestoreRemovedHomeYieldsToANewerHome(t *testing.T) {
	store := NewInMemoryStore()
	project := assistantProject(t, store, "Replaced")
	programs := NewAssistantProgramStore(store)
	station, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	station, _ = store.Get(station.ID)
	review, err := programs.ReviewHomeRemoval(station.ID, station.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := programs.CommitHomeRemoval(station.ID, review.Token); err != nil {
		t.Fatal(err)
	}
	replacement, created, err := programs.EnsureProjectStation(project.ID)
	if err != nil || !created || replacement.ID == station.ID {
		t.Fatalf("replacement Home = %+v, created=%v, %v", replacement, created, err)
	}

	// The replacement took the old Home's canonical slug as well as its key, so
	// the old record comes back under a fresh slug (the restore API refuses a
	// slug collision outright; this is the path past that check).
	if err := store.Update(station.ID, func(current *Workspace) error {
		current.Status = StatusActive
		current.FolderSlug = current.FolderSlug + "-restored"
		delete(current.SharedData, TrashSharedDataKey)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	restored, err := programs.RestoreRemovedHome(station.ID)
	if err != nil || restored {
		t.Fatalf("RestoreRemovedHome with a newer Home = %v, %v", restored, err)
	}
	old, _ := store.Get(station.ID)
	if old.GetAssistantProgramState() != nil || removedAssistantProgramState(old) != nil {
		t.Fatalf("yielded Home should be a plain group with no stash: %+v", old)
	}
	if home, err := programs.FindStation(replacement.GetAssistantProgramState().Key); err != nil || home.ID != replacement.ID {
		t.Fatalf("newer Home lookup = %+v, %v", home, err)
	}
}

func TestAssistantProgramExplicitUnlinkShapePreservesChildData(t *testing.T) {
	store := NewInMemoryStore()
	project := assistantProject(t, store, "Retained")
	project.Tasks = []Task{{ID: "task-retained", Description: "Keep me"}}
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewAssistantProgramStore(store).EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(current *Workspace) error {
		current.SetAssistantProjectLink(nil)
		current.ParentID = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	retained, err := store.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.AssistantProjectLink != nil || retained.ParentID != "" || len(retained.Tasks) != 1 || retained.Tasks[0].ID != "task-retained" {
		t.Fatalf("retained child = %+v", retained)
	}
}
