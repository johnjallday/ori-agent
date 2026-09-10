package projecttemplates_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type noUserQuestSkillGrants struct{}

func (noUserQuestSkillGrants) Available(string) bool       { return false }
func (noUserQuestSkillGrants) Grant(string, string) error  { return nil }
func (noUserQuestSkillGrants) Revoke(string, string) error { return nil }

// TestUserSetupQuestEligibleFixtureUsesTypedUserOwnership proves canonical
// Home/project creation accepts the local attachment without fabricating plugin
// provenance.
func TestUserSetupQuestEligibleFixtureUsesTypedUserOwnership(t *testing.T) {
	template, err := projecttemplates.LoadFolder(filepath.Join("testdata", "user-setup-quest-eligible"))
	if err != nil {
		t.Fatal(err)
	}
	if template.Builtin || template.PluginOwner != nil {
		t.Fatalf("fixture must remain user-owned without fabricated plugin provenance: %+v", template.PluginOwner)
	}
	if template.ProjectConnection == nil ||
		!template.ProjectConnection.Supports(projecttemplates.ProjectConnectionExistingProject) ||
		!template.ProjectConnection.Supports(projecttemplates.ProjectConnectionNewProject) ||
		template.ProjectEntry == nil || !template.HasSkeleton {
		t.Fatalf("project connection fixture is not constructible: %+v", template)
	}
	if template.AssistantProgram == nil || template.AssistantProgram.SchemaVersion != workspace.AssistantProgramSchemaVersion ||
		template.AssistantProgramError != "" {
		t.Fatalf("assistant program fixture is unusable: program=%+v error=%q", template.AssistantProgram, template.AssistantProgramError)
	}
	if template.RuntimeRequirements == nil || template.SetupWizard == nil ||
		template.RuntimeRequirementsError != "" || template.SetupWizardError != "" {
		t.Fatalf("file-only wizard fixture is unusable: runtime=%q wizard=%q", template.RuntimeRequirementsError, template.SetupWizardError)
	}
	if len(template.RuntimeRequirements.OperatingModes) != 1 || template.RuntimeRequirements.OperatingModes[0].ID != "file_only" {
		t.Fatalf("fixture does not expose an honest file-only mode: %+v", template.RuntimeRequirements)
	}
	draft := projecttemplates.DefaultUserSetupQuestDraft()
	quest, err := projecttemplates.NewUserSetupQuest(template, nil, draft)
	if err != nil {
		t.Fatal(err)
	}
	template.UserSetupQuest = quest
	template.UserSetupQuestRevision = projecttemplates.UserSetupQuestRevision(quest)

	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
	owner := projectconnection.NewService(store, pathselection.NewStore())
	scope := projectconnection.Scope{OwnerUserID: "local", RunID: "characterization-run", Template: template}
	request := projectconnection.Request{
		ModeID: projecttemplates.ProjectConnectionNewProject, WorkspaceName: "Local project", ProjectName: "Local project",
	}
	preview, err := owner.Preview(context.Background(), scope, request)
	if err != nil || preview.Projection.ModeID != projecttemplates.ProjectConnectionNewProject {
		t.Fatalf("user-owned project preview: preview=%+v error=%v", preview, err)
	}
	result, err := owner.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil || result.HomeWorkspaceID == "" || result.ProjectWorkspaceID == "" {
		t.Fatalf("user-owned project commit: result=%+v error=%v", result, err)
	}
	createdProject, err := store.Get(result.ProjectWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	createdProvenance := createdProject.GetTemplateProvenance()
	if createdProvenance == nil || createdProvenance.PluginOwner != nil || createdProvenance.UserTemplateOwner == nil ||
		createdProvenance.UserTemplateOwner.AttachmentID != quest.AttachmentID {
		t.Fatalf("created project provenance=%+v", createdProvenance)
	}

	runtime := runtimecapability.NewService(store, runtimecapability.NewRegistry())
	wizard := setupwizard.NewService(store, setupwizard.NewRegistry())
	wizard.SetRuntimeService(runtime)
	if _, err := wizard.Confirm(context.Background(), createdProject.ID, "mode", setupwizard.StepAction{Type: setupwizard.ActionConfirm, Option: "file_only"}); err != nil {
		t.Fatal(err)
	}
	if _, err := wizard.Confirm(context.Background(), createdProject.ID, "summary", setupwizard.StepAction{Type: setupwizard.ActionConfirm}); err != nil {
		t.Fatal(err)
	}
	profiles, err := agentstore.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{Provider: "openai", Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatal(err)
	}
	staffing := setupjourney.NewAssistantStaffingAdapter(store, profiles, noUserQuestSkillGrants{},
		func() (string, string) { return "openai", "gpt-4o-mini" }, func(string, string) error { return nil })
	if err := staffing.StaffFromReviewedWorkspaceSetup(context.Background(), createdProject.ID, "Music Coordinator", "", ""); err != nil {
		t.Fatal(err)
	}
	createdProject, err = store.GetFolderWorkspace(createdProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	createdHome, err := store.GetFolderWorkspace(result.HomeWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeStatus, err := runtime.Status(context.Background(), createdProject.ID)
	if err != nil || runtimeStatus.SelectedModeID != "file_only" || len(runtimeStatus.Requirements) != 0 {
		t.Fatalf("file-only state=%+v error=%v", runtimeStatus, err)
	}
	if len(createdHome.GetAssistantProgramState().HomeBindings.Bindings) != 1 ||
		len(createdProject.GetAssistantProjectLink().ProjectBindings.Bindings) != 1 {
		t.Fatalf("user-owned scoped staffing: home=%+v project=%+v", createdHome.GetAssistantProgramState(), createdProject.GetAssistantProjectLink())
	}

	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Existing local project"})
	project.OwnerUserID = "local"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: template.ID, AssistantProgram: template.AssistantProgram,
		RuntimeRequirements: template.RuntimeRequirements, SetupWizard: template.SetupWizard,
		UserTemplateOwner: &workspace.UserTemplateOwner{
			TemplateID: template.ID, AttachmentID: quest.AttachmentID, QuestID: quest.Declaration.ID,
			DefinitionDigest: projecttemplates.UserSetupQuestDefinitionDigest(quest),
			ExecutionDigest:  projecttemplates.UserSetupQuestExecutionDigest(template),
		},
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	home, created, err := workspace.NewAssistantProgramStore(store).EnsureProjectStation(project.ID)
	if err != nil || created || home == nil || home.GetAssistantProgramState().Key.PluginID != "" ||
		home.GetAssistantProgramState().Key.TemplateID != template.ID {
		t.Fatalf("typed user-owned Assistant Program: home=%+v created=%v error=%v", home, created, err)
	}
}
