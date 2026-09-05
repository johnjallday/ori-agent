package workspace

import (
	"errors"
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
	receipt, err := programs.CommitHomeRemoval(station.ID, review.Token)
	if err != nil || receipt.RetainedProjects != 2 {
		t.Fatalf("Home removal receipt = %+v, %v", receipt, err)
	}
	if _, err := store.Get(station.ID); err == nil {
		t.Fatal("removed Home remained in the store")
	}
	for _, projectID := range []string{first.ID, second.ID} {
		project, getErr := store.Get(projectID)
		if getErr != nil || project.GetAssistantProjectLink() != nil || project.ParentID != "" {
			t.Fatalf("retained project %q = %+v, %v", projectID, project, getErr)
		}
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
