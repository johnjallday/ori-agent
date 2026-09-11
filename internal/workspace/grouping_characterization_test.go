package workspace

import "testing"

func TestOrdinaryParentageDoesNotCreateAssistantProgramMembership(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	home := NewWorkspace(CreateWorkspaceParams{Name: "Ordinary Group"})
	home.Kind = "group"
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	project := NewWorkspace(CreateWorkspaceParams{Name: "Ordinary Project"})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveWorkspaceFolder(project.ID, home.ID); err != nil {
		t.Fatal(err)
	}

	grouped, err := store.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if grouped.ParentID != home.ID {
		t.Fatalf("ParentID = %q, want %q", grouped.ParentID, home.ID)
	}
	if grouped.GetAssistantProjectLink() != nil {
		t.Fatal("ordinary group move invented Assistant Program membership")
	}
	storedHome, err := store.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedHome.GetAssistantProgramState() != nil {
		t.Fatal("ordinary group move converted its parent into an Assistant Program Home")
	}
}

func TestAssistantProgramLookupDoesNotAdoptSameNamedOrdinaryGroup(t *testing.T) {
	store := NewInMemoryStore()
	ordinary := NewWorkspace(CreateWorkspaceParams{Name: "Project Guide Home"})
	ordinary.Kind = "group"
	ordinary.OwnerUserID = "owner-1"
	if err := store.Save(ordinary); err != nil {
		t.Fatal(err)
	}

	key := AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "project-guide"}
	station, created, err := NewAssistantProgramStore(store).EnsureStation(key, neutralAssistantDeclaration())
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("same-named ordinary group was adopted as an Assistant Program Home")
	}
	if station.ID == ordinary.ID {
		t.Fatal("Assistant Program identity depended on a mutable display name")
	}
	if state := station.GetAssistantProgramState(); state == nil || state.Key.Normalize() != key.Normalize() {
		t.Fatalf("created Home has wrong stable identity: %#v", state)
	}
}
