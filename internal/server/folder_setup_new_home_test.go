package server

import (
	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"strings"
	"testing"
)

func TestFolderDestination_NewHomeIsCanonicalReadOnlyAndRejectsUnreceiptedCreation(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := reviewedintegration.HomeProviders()[0]
	owner := workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.1.1", ProgramID: provider.ProgramID, HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64)}
	declaration := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: provider.ProgramID, StationName: "Named new Music Home", Roles: []workspace.AssistantProgramRoleSpec{{ID: "producer", Label: "Producer", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate reviewed work."}}}
	template := projecttemplates.Template{ID: "plugin-home:" + provider.PluginID + ":" + provider.ProgramID, Name: "Named new Music Home", AssistantProgram: declaration, ProgramHomeOwner: &owner, GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: projecttemplates.GroupPolicyRequired, AssistantProgramID: provider.ProgramID, DefaultHomeName: declaration.StationName}}
	f.builder.sessionHandler.SetGroupTemplateCatalog(func(string) ([]sessionhttp.GroupTemplateCatalogEntry, bool, error) {
		return []sessionhttp.GroupTemplateCatalogEntry{{Template: template, Readiness: blueprintreadiness.Ready(blueprintreadiness.OwnershipPlugin), Active: true}}, false, nil
	})
	host := &folderSetupHost{builder: f.builder}
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	destination, err := host.newHomeDestination(t.Context(), "local", provider)
	if err != nil || destination.Name != declaration.StationName || destination.Status != "new" || destination.WorkspaceID != "" || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		_, catalog := f.call(t, "GET", "/api/workspaces/group-templates", nil)
		t.Fatal("new Home invented or created during read", destination, err, catalog)
	}
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{ID: "offer"}, Plan: personalassistant.FolderSetupPlan{Destination: destination, Digest: "reviewed"}}
	if err := host.ValidateSetupDestination(t.Context(), req); err != nil {
		t.Fatal("canonical new destination refused", err)
	}
	owner.PluginGeneration++
	if err := host.ValidateSetupDestination(t.Context(), req); err == nil {
		t.Fatal("changed provider evidence accepted")
	}
	owner.PluginGeneration--
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: provider.PluginID, ProgramID: provider.ProgramID}
	if _, _, err := workspace.NewAssistantProgramStore(f.builder.workspaceFileStore).EnsureNamedIndependentStation(key, declaration, destination.Name, owner); err != nil {
		t.Fatal(err)
	}
	if err := host.ValidateSetupDestination(t.Context(), req); err == nil {
		t.Fatal("same-name newly created Home adopted without canonical run receipt")
	}
}
