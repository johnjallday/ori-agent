package workspacecapability

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestSampleLibraryDefinitionIsInertAndHasNoGenericCompanion(t *testing.T) {
	definition := SampleLibraryDefinition()
	if definition.ID != workspace.CapabilitySampleLibrary || !definition.Requirements.AssistantProgramHomeOnly || definition.Companion != nil {
		t.Fatalf("definition = %+v", definition)
	}
	if definition.Automation.WatcherID != "" || definition.Automation.ScheduleID != "" {
		t.Fatalf("sample automation was declared: %+v", definition.Automation)
	}
}

func TestSampleLibraryInstallIsLimitedToDeclaringAssistantProgramHome(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := workspace.NewInMemoryStore()
	ordinary := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Ordinary"})
	if err := store.Save(ordinary); err != nil {
		t.Fatal(err)
	}
	service := NewService(registry, store)
	if _, err := service.Install(InstallRequest{WorkspaceID: ordinary.ID, CapabilityID: workspace.CapabilitySampleLibrary}); err == nil {
		t.Fatal("ordinary workspace installed the Home-only add-on")
	} else {
		var lifecycleErr *Error
		if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != CodeCapabilityUnavailable {
			t.Fatalf("ordinary install error = %v", err)
		}
	}

	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Home"})
	home.Kind = "group"
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramSchemaVersion,
		Key:           workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "example", ProgramID: "program"},
		Declaration: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "program", StationName: "Home",
			Roles: []workspace.AssistantProgramRoleSpec{{ID: "catalog", Label: "Catalog", Scope: workspace.AssistantRoleScopeHome, CapabilityID: workspace.CapabilitySampleLibrary}},
		},
	})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	result, err := service.Install(InstallRequest{WorkspaceID: home.ID, CapabilityID: workspace.CapabilitySampleLibrary})
	if err != nil || result.Record.ID != workspace.CapabilitySampleLibrary {
		t.Fatalf("Home install = %+v, %v", result, err)
	}
	if len(home.GetAgentInstances()) != 0 {
		t.Fatal("capability install created a companion or role")
	}
}
