package workspace

import (
	"errors"
	"strings"
	"testing"
)

// A Required snapshot that records a Home provider without a project provider
// is what an interrupted creation left behind. It fails StructurallyValid but
// still declares Required, so it must stay protected from ordinary deletion
// while a reviewed, digest-bound deletion of the detached workspace succeeds.
func invalidLifecycleSnapshot(t *testing.T, key *AssistantProgramKey, homeID, projectID string) *GroupRequirementSnapshot {
	t.Helper()
	snapshot := lifecycleSnapshot("required", GroupRequirementCompositionGrouped, key, homeID, projectID)
	digest := strings.Repeat("c", 64)
	snapshot.HomeProvider = &AssistantProgramHomeOwner{
		PluginID: "neutral", PluginVersion: "0.3.0", ProgramID: "neutral-program",
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: digest,
		PluginGeneration: 1, ComponentFingerprint: digest,
	}
	if snapshot.StructurallyValid() {
		t.Fatal("fixture snapshot must be structurally invalid")
	}
	return snapshot
}

func TestReviewedRequiredDeletionAcceptsDetachedInvalidContract(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project := NewWorkspace(CreateWorkspaceParams{Name: "Ori Music Demo"})
	project.ID = "invalid-contract"
	project.OwnerUserID = "owner-1"
	key := AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()
	snapshot := invalidLifecycleSnapshot(t, &key, "music-home", project.ID)
	project.SetTemplateProvenance(&TemplateProvenance{TemplateID: "plugin:neutral:project", GroupRequirement: snapshot})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(project.ID); !errors.Is(err, ErrGroupRequirementProtected) {
		t.Fatalf("ordinary delete of an invalid Required record = %v, want protection", err)
	}
	if err := store.DeleteReviewedGroupRequirement(project.ID, strings.Repeat("b", 64)); !errors.Is(err, ErrGroupRequirementProtected) {
		t.Fatalf("reviewed delete with the wrong digest = %v, want protection", err)
	}
	if status := EvaluateGroupRequirementLifecycle(project, store.Get); status == nil || status.State != GroupRequirementStatusInvalid {
		t.Fatalf("lifecycle status = %#v, want %s", status, GroupRequirementStatusInvalid)
	}
	if err := store.DeleteReviewedGroupRequirement(project.ID, snapshot.OperationDigest); err != nil {
		t.Fatalf("reviewed delete of a detached invalid record: %v", err)
	}
	if _, err := store.Get(project.ID); err == nil {
		t.Fatal("reviewed deletion left the workspace registered")
	}
}

func TestReviewedRequiredDeletionKeepsProtectingConnectedInvalidContract(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()

	home := NewWorkspace(CreateWorkspaceParams{Name: "Music Home"})
	home.ID = "music-home"
	home.OwnerUserID = "owner-1"
	home.Kind = "group"
	home.SetAssistantProgramState(&AssistantProgramState{Key: key, LinkedProjectIDs: []string{"invalid-contract"}})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	project := NewWorkspace(CreateWorkspaceParams{Name: "Ori Music Demo"})
	project.ID = "invalid-contract"
	project.OwnerUserID = "owner-1"
	snapshot := invalidLifecycleSnapshot(t, &key, home.ID, project.ID)
	project.SetTemplateProvenance(&TemplateProvenance{TemplateID: "plugin:neutral:project", GroupRequirement: snapshot})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}

	// Listed by the Home: still a member from the Home's side.
	if err := store.DeleteReviewedGroupRequirement(project.ID, snapshot.OperationDigest); !errors.Is(err, ErrGroupRequirementProtected) {
		t.Fatalf("reviewed delete of a Home-listed project = %v, want protection", err)
	}

	// Carrying its own link: still a member from the project's side.
	home.SetAssistantProgramState(&AssistantProgramState{Key: key})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	project.SetAssistantProjectLink(&AssistantProjectLink{
		ID: AssistantProjectLinkID(home.ID, project.ID), StationWorkspaceID: home.ID, Key: key,
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	// The live link trips the Assistant Program guard first; either sentinel
	// is the same refusal.
	err = store.DeleteReviewedGroupRequirement(project.ID, snapshot.OperationDigest)
	if !errors.Is(err, ErrGroupRequirementProtected) && !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("reviewed delete of a linked project = %v, want protection", err)
	}
}
