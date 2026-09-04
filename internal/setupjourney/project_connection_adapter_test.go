package setupjourney

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type staticProjectTemplateResolver struct{ template projecttemplates.Template }

func (r staticProjectTemplateResolver) ResolveProjectTemplate(context.Context, ReadScope) (projecttemplates.Template, error) {
	return r.template, nil
}

func setupJourneyProjectAdapter(t *testing.T) (*ProjectConnectionAdapter, *pathselection.Store, *workspace.SyncStore) {
	t.Helper()
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
	selections := pathselection.NewStore()
	templateRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(templateRoot, "{{name}}.project"), []byte("template"), 0o600); err != nil {
		t.Fatal(err)
	}
	template := projecttemplates.Template{
		ID: "plugin:neutral:project", Name: "Project", Path: templateRoot, HasSkeleton: true,
		ProjectEntry: &projecttemplates.ProjectEntry{RelativePath: "{{name}}.project"},
		ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{
			SchemaVersion:  projecttemplates.ProjectConnectionSchemaVersion,
			SupportedModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionNewProject, projecttemplates.ProjectConnectionExistingProject},
			AttachExisting: &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".project"}},
		},
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1},
		AssistantProgram: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "guide", StationName: "Guide Home", DefaultPrimaryName: "Guide", HireTitle: "Hire",
			Roles: []workspace.AssistantProgramRoleSpec{
				{ID: "home", Label: "Home", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate."},
				{ID: "project", Label: "Project", Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Work."},
			},
			Stages:     []workspace.AssistantProgramStageSpec{{ID: "initial", Label: "Initial", AcceptedCompletionThreshold: 0}},
			Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 2, CadenceHours: 24, MaxProjects: 4, MaxEventsPerProject: 4, MaxCandidates: 2, MaxEvidence: 2, Rubric: "Review."},
		},
	}
	return NewProjectConnectionAdapter(projectconnection.NewService(store, selections), staticProjectTemplateResolver{template}), selections, store
}

func TestProjectConnectionAdapterReviewsCommitsAndReconcilesExactSelection(t *testing.T) {
	adapter, selections, _ := setupJourneyProjectAdapter(t)
	external := t.TempDir()
	entry := filepath.Join(external, "Existing.project")
	if err := os.WriteFile(entry, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(external)
	raw, _ := json.Marshal(projectconnection.Request{
		ModeID:         projecttemplates.ProjectConnectionExistingProject,
		SelectionToken: token, WorkspaceName: "Existing Project",
	})
	scope := ReadScope{OwnerUserID: "owner-1", RunKind: RunKindRoot, RunID: "journey-run"}
	material, err := adapter.Review(context.Background(), scope, ActionReviewExistingProject, raw)
	if err != nil {
		t.Fatal(err)
	}
	if material.CommitAction != ActionConnectExistingProject || material.ProjectConnection == nil || material.ProjectConnection.SelectedFolder != external {
		t.Fatalf("review material = %+v", material)
	}
	prepared, err := adapter.PrepareCommit(context.Background(), scope, ActionConnectExistingProject, raw)
	if err != nil || prepared.OwnerRevisionDigest != material.OwnerRevisionDigest || prepared.DisclosureDigest != material.DisclosureDigest {
		t.Fatalf("prepared material = %+v err=%v", prepared, err)
	}
	result, err := adapter.Commit(context.Background(), scope, ActionConnectExistingProject, raw, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if result.HomeWorkspaceID == "" || result.ProjectWorkspaceID == "" || result.SelectedModeID != "" {
		t.Fatalf("canonical result = %+v", result)
	}
	scope.HomeWorkspaceID = result.HomeWorkspaceID
	scope.ProjectWorkspaceID = result.ProjectWorkspaceID
	observed, err := adapter.Read(context.Background(), scope)
	if err != nil || !observed.Complete || !adapter.ConsequenceObserved(ActionConnectExistingProject, observed) {
		t.Fatalf("observed = %+v err=%v", observed, err)
	}
}

func TestProjectConnectionAdapterRejectsUnknownInputAndActionModeConfusion(t *testing.T) {
	adapter, _, _ := setupJourneyProjectAdapter(t)
	scope := ReadScope{OwnerUserID: "owner-1", RunKind: RunKindRoot, RunID: "run"}
	unknown := json.RawMessage(`{"mode_id":"new_project","workspace_name":"New","project_name":"New","command":"run"}`)
	if _, err := adapter.Review(context.Background(), scope, ActionReviewNewProject, unknown); err == nil {
		t.Fatal("unknown executable field was accepted")
	}
	wrongMode := json.RawMessage(`{"mode_id":"existing_project","workspace_name":"Existing","selection_token":"opaque"}`)
	if _, err := adapter.Review(context.Background(), scope, ActionReviewNewProject, wrongMode); err == nil {
		t.Fatal("review action accepted a different connection mode")
	}
}
