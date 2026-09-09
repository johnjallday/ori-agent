package projecttemplates_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestUserSetupQuestEligibleFixtureCharacterizesPluginOnlyOwnerGates records the
// pre-feature seam: a local template can already satisfy every portable target
// declaration, but canonical Home/project creation still refuses it solely
// because those owners currently require plugin provenance.
func TestUserSetupQuestEligibleFixtureCharacterizesPluginOnlyOwnerGates(t *testing.T) {
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

	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
	owner := projectconnection.NewService(store, pathselection.NewStore())
	scope := projectconnection.Scope{OwnerUserID: "local", RunID: "characterization-run", Template: template}
	_, err = owner.Preview(context.Background(), scope, projectconnection.Request{
		ModeID: projecttemplates.ProjectConnectionNewProject, WorkspaceName: "Local project", ProjectName: "Local project",
	})
	if !errors.Is(err, projectconnection.ErrUnavailable) {
		t.Fatalf("plugin-only project owner gate changed: error=%v", err)
	}

	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Existing local project"})
	project.OwnerUserID = "local"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: template.ID, AssistantProgram: template.AssistantProgram,
		RuntimeRequirements: template.RuntimeRequirements, SetupWizard: template.SetupWizard,
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.NewAssistantProgramStore(store).EnsureProjectStation(project.ID); !errors.Is(err, workspace.ErrAssistantProgramUnavailable) {
		t.Fatalf("plugin-only Assistant Program provenance gate changed: error=%v", err)
	}
}
