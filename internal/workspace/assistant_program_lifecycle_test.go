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
