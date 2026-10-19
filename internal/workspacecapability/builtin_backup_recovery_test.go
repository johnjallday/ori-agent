package workspacecapability

import (
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestBackupRecoveryDefinitionIsInertAndSeparate(t *testing.T) {
	def := BackupRecoveryDefinition()
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
	if def.ID != "backup-recovery" || def.Version != 1 || !def.Requirements.OrdinaryWorkspaceOnly || def.Requirements.AssistantProgramHomeOnly || def.Requirements.MaxInstallsPerWorkspace != 1 {
		t.Fatal("wrong capability identity/scope")
	}
	if def.Companion != nil || def.Automation != (AutomationDescriptor{}) || def.Setup.DirectoryRequirementKey != "" || len(def.Setup.LegacyAdapterIDs) != 0 || len(def.API.LegacyPrefixes) != 0 {
		t.Fatal("backup inherited Janitor/automation authority")
	}
	if def.Setup.AdapterID != "backup_recovery" || def.API.Prefix != "backup-recovery" || def.Console.PanelID != "backup-recovery" || def.Console.DefaultTab != "overview" || !reflect.DeepEqual(def.Console.Tabs, []string{"overview", "history", "recovery", "settings"}) {
		t.Fatal("stable contract identifiers drifted")
	}
	def.Console.Tabs[0] = "changed"
	if BackupRecoveryDefinition().Console.Tabs[0] != "overview" {
		t.Fatal("definition shares mutable values")
	}
}

type refusedBackupCompanion struct{ calls int }

func (p *refusedBackupCompanion) EnsureCompanionAgent(string, string) (string, bool, error) {
	p.calls++
	return "unexpected", true, nil
}

func TestBackupRecoveryInstallAndRetryGrantNothing(t *testing.T) {
	registry := mustBuiltinRegistry(t)
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Invented", Agents: []string{"Existing advisor"}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	service := NewService(registry, store)
	provisioner := &refusedBackupCompanion{}
	service.SetCompanionProvisioner(provisioner)
	first, err := service.Install(InstallRequest{WorkspaceID: ws.ID, CapabilityID: workspace.CapabilityBackupRecovery})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		again, err := service.Install(InstallRequest{WorkspaceID: ws.ID, CapabilityID: workspace.CapabilityBackupRecovery})
		if err != nil || !again.AlreadyInstalled || !again.Record.InstalledAt.Equal(first.Record.InstalledAt) || again.Status.State != StatusUnavailable || again.Status.Configured {
			t.Fatal("repeated install changed identity or claimed readiness", err)
		}
	}
	if _, ok := registry.Runtime(workspace.CapabilityBackupRecovery); ok {
		t.Fatal("catalog created a runtime")
	}
	_, err = service.AddCompanion(ws.ID, workspace.CapabilityBackupRecovery)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != CodeCompanionUnavailable || provisioner.calls != 0 {
		t.Fatal("backup reached the File Curator provisioner", err)
	}
	current, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.GetAgentInstances()) != 1 || len(current.DirectoryReferences) != 0 || len(current.GetMCPBindings()) != 0 || len(current.GetSkillBindings()) != 0 || len(current.GetInstalledCapabilities()) != 1 || len(first.Record.OwnedResources) != 0 {
		t.Fatal("install created a grant, extra agent or duplicate record")
	}
}

func TestBackupRecoveryCannotInstallIntoHomeGroupOrArchive(t *testing.T) {
	for _, scope := range []string{"group", "program-home", "archived"} {
		t.Run(scope, func(t *testing.T) {
			registry := mustBuiltinRegistry(t)
			store := workspace.NewInMemoryStore()
			ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Invented"})
			switch scope {
			case "group":
				ws.Kind = "group"
			case "program-home":
				ws.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramSchemaVersion, Key: workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "invented", ProgramID: "invented"}, Declaration: &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "invented", StationName: "Home"}})
			case "archived":
				ws.SetStatus("archived")
			}
			if err := store.Save(ws); err != nil {
				t.Fatal(err)
			}
			service := NewService(registry, store)
			if _, err := service.Install(InstallRequest{WorkspaceID: ws.ID, CapabilityID: workspace.CapabilityBackupRecovery}); err == nil {
				t.Fatal("unsupported workspace installed backup capability")
			}
			items, err := service.Catalog(ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range items {
				if item.Definition.ID == workspace.CapabilityBackupRecovery {
					found = true
					if item.Available || item.Installed {
						t.Fatal("catalog offered unsupported installation")
					}
				}
			}
			if !found {
				t.Fatal("unsupported capability vanished instead of explaining its scope")
			}
		})
	}
}

func TestBackupNeutralStatusesPreserveExistingPriority(t *testing.T) {
	old := []StatusState{StatusNeedsAttention, StatusSetupNeeded, StatusReviewReady, StatusPaused, StatusWatching}
	for i, a := range old {
		for _, b := range old[i:] {
			if got := HighestPriorityStatus(b, a); got != a {
				t.Fatal("legacy priority changed", a, b, got)
			}
		}
	}
	if HighestPriorityStatus(StatusReady, StatusMonitoring) != StatusMonitoring || HighestPriorityStatus(StatusMonitoring, StatusNeedsAttention) != StatusNeedsAttention {
		t.Fatal("neutral states hide an actionable state")
	}
}
