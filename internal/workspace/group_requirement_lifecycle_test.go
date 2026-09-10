package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func lifecycleProgramDeclaration() *AssistantProgramDeclaration {
	return &AssistantProgramDeclaration{
		SchemaVersion: AssistantProgramSchemaVersion, ID: "neutral-program", StationName: "Neutral Home",
		Roles: []AssistantProgramRoleSpec{
			{ID: "home", Label: "Home", Scope: AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate."},
			{ID: "project", Label: "Project", Scope: AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Work."},
		},
		Stages:     []AssistantProgramStageSpec{{ID: "initial", Label: "Initial", AcceptedCompletionThreshold: 0}},
		Reflection: AssistantReflectionConfig{MinimumProjects: 2, CadenceHours: 24, MaxProjects: 4, MaxEventsPerProject: 4, MaxCandidates: 2, MaxEvidence: 2, Rubric: "Review."},
	}
}

func lifecycleSnapshot(policy, composition string, key *AssistantProgramKey, homeID, projectID string) *GroupRequirementSnapshot {
	digest := strings.Repeat("a", 64)
	snapshot := &GroupRequirementSnapshot{
		SchemaVersion: GroupRequirementSnapshotSchemaVersion, Policy: policy, SelectedComposition: composition,
		TemplateID: "plugin:neutral:project", TemplateRevision: digest, DefinitionDigest: digest,
		ProgramKey: key, HomeWorkspaceID: homeID, ReviewDigest: digest, OperationDigest: digest, AppliedAt: time.Now().UTC(),
	}
	if composition == GroupRequirementCompositionGrouped {
		snapshot.ProjectLinkID = AssistantProjectLinkID(homeID, projectID)
	}
	return snapshot
}

func TestRequiredGroupLifecycleRenameDisconnectAndOrdinaryGuards(t *testing.T) {
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentSnapshotStore(NewSyncStore(NewInMemoryStore(), folders), nil)
	programs := NewAssistantProgramStore(store)
	key := AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()
	declaration := lifecycleProgramDeclaration()
	home, _, err := programs.EnsureStation(key, declaration)
	if err != nil {
		t.Fatal(err)
	}
	project := NewWorkspace(CreateWorkspaceParams{Name: "Required Project"})
	project.ID = "required-project"
	project.OwnerUserID = "owner-1"
	project.ParentID = home.ID
	project.SharedData = map[string]any{"reviewed_grant": "unchanged"}
	project.MCPBindings = []MCPBinding{{ID: "files", ServerName: "filesystem", Enabled: true}}
	project.Tasks = []Task{{ID: "task-1", Description: "Preserve me", Status: TaskStatusPending}}
	project.SetTemplateProvenance(&TemplateProvenance{
		TemplateID:       "plugin:neutral:project",
		PluginOwner:      &PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1},
		AssistantProgram: declaration, GroupRequirement: lifecycleSnapshot("required", GroupRequirementCompositionGrouped, &key, home.ID, project.ID),
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if status := EvaluateGroupRequirementLifecycle(project, store.Get); status == nil || status.State != GroupRequirementStatusUnfulfilled {
		t.Fatalf("parent-only grouping was treated as membership: %#v", status)
	}
	linkedHome, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil || linkedHome.ID != home.ID {
		t.Fatalf("link: home=%#v err=%v", linkedHome, err)
	}
	project, _ = store.Get(project.ID)
	if status := EvaluateGroupRequirementLifecycle(project, store.Get); status == nil || status.State != GroupRequirementStatusReadyGrouped {
		t.Fatalf("healthy status = %#v", status)
	}
	if err := store.Update(home.ID, func(current *Workspace) error {
		current.Name = "Renamed Home"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	project, _ = store.Get(project.ID)
	if status := EvaluateGroupRequirementLifecycle(project, store.Get); status == nil || status.State != GroupRequirementStatusReadyGrouped {
		t.Fatalf("rename changed identity: %#v", status)
	}

	home, _ = store.Get(home.ID)
	review, err := programs.ReviewDisconnect(home.ID, project.ID, home.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := programs.CommitDisconnect(home.ID, review.Token, "disconnect-required"); err != nil {
		t.Fatal(err)
	}
	project, _ = store.Get(project.ID)
	provenance := project.GetTemplateProvenance()
	if provenance == nil || provenance.GroupRequirement == nil || provenance.GroupRequirement.Policy != "required" {
		t.Fatalf("disconnect erased Required snapshot: %#v", provenance)
	}
	if status := EvaluateGroupRequirementLifecycle(project, store.Get); status == nil || status.State != GroupRequirementStatusUnfulfilled {
		t.Fatalf("disconnected status = %#v", status)
	}
	if project.SharedData["reviewed_grant"] != "unchanged" || len(project.MCPBindings) != 1 || len(project.Tasks) != 1 {
		t.Fatalf("disconnect changed unrelated task/tool/grant state: %#v", project)
	}
	ordinary := NewWorkspace(CreateWorkspaceParams{Name: "Ordinary Group"})
	ordinary.ID, ordinary.Kind, ordinary.OwnerUserID = "ordinary-group", "group", "owner-1"
	if err := store.Save(ordinary); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveWorkspaceFolder(project.ID, ordinary.ID); !errors.Is(err, ErrGroupRequirementProtected) {
		t.Fatalf("detached Required project moved to arbitrary group: %v", err)
	}
	reconnectReview, err := programs.ReviewReconnect(project.ID)
	if err != nil || len(reconnectReview.Impact) == 0 {
		t.Fatalf("reconnect review = %#v err=%v", reconnectReview, err)
	}
	reconnected, err := programs.CommitReconnect(project.ID, reconnectReview.Token, "reconnect-required")
	if err != nil || reconnected.Replayed {
		failedProject, _ := store.Get(project.ID)
		failedHome, _ := store.Get(home.ID)
		t.Fatalf("reconnect = %#v err=%v project=%#v home=%#v", reconnected, err, failedProject, failedHome)
	}
	replayed, err := programs.CommitReconnect(project.ID, reconnectReview.Token, "reconnect-required")
	if err != nil || !replayed.Replayed {
		t.Fatalf("reconnect replay = %#v err=%v", replayed, err)
	}
	project, _ = store.Get(project.ID)
	if status := EvaluateGroupRequirementLifecycle(project, store.Get); status == nil || status.State != GroupRequirementStatusReadyGrouped {
		t.Fatalf("reconnected status = %#v", status)
	}
	home, _ = store.Get(home.ID)
	secondDisconnect, err := programs.ReviewDisconnect(home.ID, project.ID, home.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := programs.CommitDisconnect(home.ID, secondDisconnect.Token, "disconnect-required-again"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(project.ID); !errors.Is(err, ErrGroupRequirementProtected) {
		t.Fatalf("detached Required project deleted without lifecycle review: %v", err)
	}
	if err := store.DeleteReviewedGroupRequirementOperation(project.ID, strings.Repeat("b", 64), "reconcile_required"); !errors.Is(err, ErrGroupRequirementProtected) {
		t.Fatalf("wrong operation digest bypassed lifecycle protection: %v", err)
	}
	if err := store.DeleteReviewedGroupRequirementOperation(project.ID, provenance.GroupRequirement.OperationDigest, "succeeded"); !errors.Is(err, ErrGroupRequirementProtected) {
		t.Fatalf("completed operation bypassed lifecycle protection: %v", err)
	}
}

func TestReviewedHomeRemovalPreservesRequiredProjectAndExternalPath(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	programs := NewAssistantProgramStore(store)
	project := assistantProject(t, store, "Retained Required")
	project.ProjectPath = "/outside/managed/project.rpp"
	project.Tasks = []Task{{ID: "retained-task", Description: "Do not delete"}}
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	home, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := home.GetAssistantProgramState().Key
	project, _ = store.Get(project.ID)
	project.SetTemplateProvenance(&TemplateProvenance{
		TemplateID:       "plugin:neutral:project",
		GroupRequirement: lifecycleSnapshot("required", GroupRequirementCompositionGrouped, &key, home.ID, project.ID),
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	home, _ = store.Get(home.ID)
	review, err := programs.ReviewHomeRemoval(home.ID, home.GetAssistantProgramState().StateRevision)
	if err != nil || len(review.Impact) != 4 {
		t.Fatalf("Home removal review = %#v err=%v", review, err)
	}
	if _, err := programs.CommitHomeRemoval(home.ID, review.Token); err != nil {
		t.Fatal(err)
	}
	retained, err := store.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.ProjectPath != "/outside/managed/project.rpp" || len(retained.Tasks) != 1 || retained.GetTemplateProvenance().GroupRequirement == nil {
		t.Fatalf("Home removal changed retained state: %#v", retained)
	}
	if status := EvaluateGroupRequirementLifecycle(retained, store.Get); status == nil || status.State != GroupRequirementStatusUnfulfilled {
		t.Fatalf("Home removal status = %#v", status)
	}
}

func TestReviewedIncompleteGroupOperationCanRollbackExactOwnedShell(t *testing.T) {
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewSyncStore(NewInMemoryStore(), folders)
	project := NewWorkspace(CreateWorkspaceParams{Name: "Incomplete"})
	project.ID, project.OwnerUserID = "incomplete", "owner-1"
	key := AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()
	project.SetTemplateProvenance(&TemplateProvenance{
		TemplateID:       "plugin:neutral:project",
		GroupRequirement: lifecycleSnapshot("required", GroupRequirementCompositionGrouped, &key, "planned-home", project.ID),
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	digest := project.GetTemplateProvenance().GroupRequirement.OperationDigest
	home := NewWorkspace(CreateWorkspaceParams{Name: "Partial Home"})
	home.ID, home.Kind = "planned-home", "group"
	home.SetAssistantProgramState(&AssistantProgramState{Key: key, LinkedProjectIDs: []string{project.ID}})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteReviewedGroupRequirementOperation(project.ID, digest, "reconcile_required"); !errors.Is(err, ErrGroupRequirementProtected) {
		t.Fatalf("rollback removed a shell still referenced by Home: %v", err)
	}
	if err := store.Update(home.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.LinkedProjectIDs = nil
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteReviewedGroupRequirementOperation(project.ID, digest, "reconcile_required"); err != nil {
		t.Fatalf("exact incomplete operation-owned rollback failed: %v", err)
	}
	if _, err := store.Get(project.ID); err == nil {
		t.Fatal("operation-owned shell survived rollback")
	}
}

func TestGroupRequirementSnapshotSurvivesFolderStoreRestart(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	project := NewWorkspace(CreateWorkspaceParams{Name: "Restarted Standalone"})
	project.ID, project.OwnerUserID = "restarted-standalone", "owner-1"
	project.SetTemplateProvenance(&TemplateProvenance{
		TemplateID:       "user-template",
		GroupRequirement: lifecycleSnapshot("none", GroupRequirementCompositionStandalone, nil, "", project.ID),
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	loaded, err := restarted.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status := EvaluateGroupRequirementLifecycle(loaded, restarted.Get); status == nil || status.State != GroupRequirementStatusReadyStandalone {
		t.Fatalf("restart lost snapshot: %#v", status)
	}
}

func TestStandaloneGroupLifecycleKeepsOrdinaryOrganizationFlexible(t *testing.T) {
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewSyncStore(NewInMemoryStore(), folders)
	group := NewWorkspace(CreateWorkspaceParams{Name: "Ordinary Group"})
	group.ID, group.Kind, group.OwnerUserID = "ordinary", "group", "owner-1"
	if err := store.Save(group); err != nil {
		t.Fatal(err)
	}
	project := NewWorkspace(CreateWorkspaceParams{Name: "Standalone"})
	project.ID, project.OwnerUserID = "standalone", "owner-1"
	project.SetTemplateProvenance(&TemplateProvenance{
		TemplateID:       "user-template",
		GroupRequirement: lifecycleSnapshot("none", GroupRequirementCompositionStandalone, nil, "", project.ID),
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if status := EvaluateGroupRequirementLifecycle(project, store.Get); status == nil || status.State != GroupRequirementStatusReadyStandalone {
		t.Fatalf("standalone status = %#v", status)
	}
	if _, err := store.MoveWorkspaceFolder(project.ID, group.ID); err != nil {
		t.Fatalf("ordinary organization was blocked: %v", err)
	}
	moved, _ := store.Get(project.ID)
	if moved.GetAssistantProjectLink() != nil || moved.ParentID != group.ID {
		t.Fatalf("ordinary parent implied membership: %#v", moved)
	}
	if err := store.Update(project.ID, func(current *Workspace) error {
		current.Description = "Unrelated save"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	canonical, err := store.GetFolderWorkspace(project.ID)
	if err != nil || canonical.GetTemplateProvenance() == nil || canonical.GetTemplateProvenance().GroupRequirement == nil {
		t.Fatalf("ordinary save erased standalone snapshot: %#v err=%v", canonical, err)
	}
	if err := store.Delete(project.ID); err != nil {
		t.Fatalf("standalone delete was blocked: %v", err)
	}
}
