package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type homePlacementPlugins []plugin.InstalledPlugin

func (p homePlacementPlugins) List() ([]plugin.InstalledPlugin, error) { return p, nil }

func TestHomePlacement_ObservesExactManagedHomeWithoutSetupOrCatalog(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := reviewedintegration.HomeProviders()[0]
	owner := workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.3.0", ProgramID: provider.ProgramID, HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64)}
	decl := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: provider.ProgramID, StationName: "Music Production Home", Roles: []workspace.AssistantProgramRoleSpec{{ID: "manager", Label: "Manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate reviewed work."}}}
	template := projecttemplates.Template{ID: "plugin-home:" + provider.PluginID + ":" + provider.ProgramID, Name: decl.StationName, AssistantProgram: decl, ProgramHomeOwner: &owner, GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: projecttemplates.GroupPolicyRequired, MissingHome: projecttemplates.MissingHomeOfferCreate, AssistantProgramID: provider.ProgramID, DefaultHomeName: decl.StationName}}
	programHome := projecttemplates.AssistantProgramHome{SchemaVersion: 1, Version: 1, ID: provider.ProgramID}
	owner.DeclarationDigest = projecttemplates.AssistantProgramHomeDigest(programHome)
	f.builder.sessionHandler.SetInstalledPluginLister(homePlacementPlugins{{Name: provider.PluginID, Version: owner.PluginVersion, Enabled: true, Generation: owner.PluginGeneration, ComponentFingerprint: owner.ComponentFingerprint,
		WorkspaceSurfaces: &plugin.SurfaceContribution{SchemaVersion: 1, Name: provider.PluginID, Version: owner.PluginVersion, Protocol: plugin.ProtocolRange{Min: 1, Max: 1}, RequiresHostFeatures: []string{plugin.HostFeatureIndependentProgramHomesV1}, AssistantProgramHomes: []projecttemplates.AssistantProgramHome{programHome}},
	}})
	f.builder.sessionHandler.SetGroupTemplateCatalog(func(string) ([]sessionhttp.GroupTemplateCatalogEntry, bool, error) {
		return []sessionhttp.GroupTemplateCatalogEntry{{Template: template, Readiness: blueprintreadiness.Ready(blueprintreadiness.OwnershipPlugin), Active: true}}, false, nil
	})
	f.builder.sessionHandler.SetProjectTemplateResolver(func(_, id string) (projecttemplates.Template, error) { return template, nil })
	host := &folderSetupHost{builder: f.builder}
	destination, err := host.newHomeDestination(t.Context(), "local", provider)
	if err != nil {
		t.Fatal(err)
	}
	review, err := f.builder.sessionHandler.ReviewPortfolioHome(t.Context(), provider.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.builder.sessionHandler.CommitPortfolioHome(t.Context(), review, "home-placement-operation")
	if err != nil {
		t.Fatal(err)
	}
	witness := &personalassistant.FolderSetupHomeOperation{Version: 1, OfferID: "source-offer", RequestID: "source-accept", PlanDigest: strings.Repeat("c", 64), GroupTemplateID: review.GroupTemplateID, Revision: review.Revision, Name: review.Name, ReviewToken: review.Token, IdempotencyKey: "home-placement-operation"}
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{ID: witness.OfferID, RequestID: witness.RequestID,
		Setup: &personalassistant.FolderSetupRun{PlanDigest: witness.PlanDigest, HomeID: id, HomeOperation: witness, Intent: personalassistant.FolderSetupIntent{Portfolio: true, CreatesHome: true, Destination: destination}}}}
	f.builder.sessionHandler.SetGroupTemplateCatalog(func(string) ([]sessionhttp.GroupTemplateCatalogEntry, bool, error) { panic("status rebuilt catalog") })
	before, err := f.builder.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	for i := 0; i < 3; i++ {
		result := host.ObserveSetupHome(t.Context(), req)
		if result.State != "verified" || result.HomeID != id || !result.Created {
			t.Fatalf("new Home proof = %+v", result)
		}
	}
	after, _ := f.builder.workspaceStore.Get(id)
	afterJSON, _ := json.Marshal(after)
	if !reflect.DeepEqual(beforeJSON, afterJSON) {
		t.Fatal("read changed Home or resources")
	}
	// Even when the progress write lost the Home ID, the original operation
	// can identify it. The service will keep this partial, not ready to place.
	req.Offer.Setup.HomeID = ""
	if got := host.ObserveSetupHome(t.Context(), req); got.HomeID != id || !got.Created {
		t.Fatalf("lost progress proof = %+v", got)
	}
	req.Offer.Setup.HomeID = "someone-elses"
	if got := host.ObserveSetupHome(t.Context(), req); got.State != "unavailable" {
		t.Fatalf("foreign ID = %+v", got)
	}
	req.Offer.Setup.HomeID = id
	req.Offer.Setup.HomeOperation = nil
	if got := host.ObserveSetupHome(t.Context(), req); got.State != "unproven" || got.Created {
		t.Fatalf("legacy ID became proof: %+v", got)
	}
	// Explicit reuse is canonical identity but never new-subject authority.
	existing, err := folderSetupHomeDestination(after)
	if err != nil {
		t.Fatal(err)
	}
	req.Offer.Setup.Intent.Destination = existing
	req.Offer.Setup.Intent.CreatesHome = false
	if got := host.ObserveSetupHome(t.Context(), req); got.State != "verified" || got.HomeID != id || got.Created {
		t.Fatalf("reuse = %+v", got)
	}
	req.UserID = "foreign"
	if got := host.ObserveSetupHome(t.Context(), req); got.State != "unavailable" || got.HomeID != "" {
		t.Fatalf("foreign owner = %+v", got)
	}
	req.UserID = "local"
	if err := f.builder.workspaceStore.Update(id, func(w *workspace.Workspace) error { w.Status = workspace.StatusTrashed; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := host.ObserveSetupHome(t.Context(), req); got.State != "unavailable" || got.HomeID != "" {
		t.Fatalf("trashed = %+v", got)
	}
}
