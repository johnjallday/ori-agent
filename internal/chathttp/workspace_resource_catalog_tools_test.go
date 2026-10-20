package chathttp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/resourcecatalog"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// catalogToolProvider answers like the REAPER plugin from the contract
// vectors: every approved source holds one entry named after it.
type catalogToolProvider struct{ declaration *resourcecatalog.Declaration }

func (p catalogToolProvider) Status(*workspace.Workspace) resourcecatalog.ProviderStatus {
	return resourcecatalog.ProviderStatus{Available: true, Declaration: p.declaration,
		Catalog: resourcecatalog.CatalogRef{PluginID: "reaper-plugin", ID: p.declaration.ID, Version: p.declaration.Version}}
}

func (catalogToolProvider) Sources(context.Context, *workspace.Workspace) ([]byte, error) {
	return os.ReadFile(filepath.Join("..", "resourcecatalog", "testdata", "contract", "sources.valid.json"))
}

func (catalogToolProvider) Inventory(_ context.Context, _ *workspace.Workspace, input []byte) ([]byte, error) {
	var request resourcecatalog.InventoryRequest
	if err := json.Unmarshal(input, &request); err != nil {
		return nil, err
	}
	kind := map[string]string{"project_templates": "project_template", "track_templates": "track_template", "audio_plugins": "effect"}[request.SourceID]
	if kind == "" {
		kind = "script"
	}
	return json.Marshal(map[string]any{
		"schema_version": 1, "installation_id": request.InstallationID, "source_id": request.SourceID, "state": "ok",
		"entries":     []map[string]any{{"native_id": request.SourceID + "/Vocal chain", "kind": kind, "name": "Vocal chain", "facts": []any{}}},
		"next_cursor": "", "coverage": map[string]any{"visited": 1, "skipped": []any{}},
	})
}

func linkCatalogChild(t *testing.T, store *workspace.FileStore, home *workspace.Workspace, id, kind string) {
	t.Helper()
	state := home.GetAssistantProgramState()
	key := state.Key.Normalize()
	blueprint := map[string]string{"": "reaper-song", workspace.AssistantWorkspaceKindResource: "reaper-studio"}[kind]
	owner := &workspace.AssistantProjectProviderOwner{
		PluginID: "reaper-plugin", PluginVersion: "0.11.0", BlueprintID: blueprint, BlueprintVersion: 1,
		ProjectTeamID: blueprint + "-team", ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: strings.Repeat("c", 64),
		PluginGeneration: 5, ComponentFingerprint: strings.Repeat("d", 64), WorkspaceKind: kind,
	}
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
	child.ID, child.OwnerUserID = id, "local"
	child.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "plugin:reaper-plugin:" + blueprint, AssistantProgram: state.Declaration,
		AssistantProjectRoles: []workspace.AssistantProgramRoleSpec{{ID: "assistant", Label: "Assistant",
			Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Work here."}},
		GroupRequirement: &workspace.GroupRequirementSnapshot{
			SchemaVersion: workspace.GroupRequirementSnapshotSchemaVersion, Policy: "required", SelectedComposition: workspace.GroupRequirementCompositionGrouped,
			TemplateID: "plugin:reaper-plugin:" + blueprint, DefinitionDigest: strings.Repeat("e", 64), ReviewDigest: strings.Repeat("f", 64),
			OperationDigest: strings.Repeat("1", 64), AppliedAt: time.Now().UTC(), ProgramKey: &key,
			HomeWorkspaceID: home.ID, ProjectLinkID: workspace.AssistantProjectLinkID(home.ID, id),
			HomeProvider: state.HomeProvider, ProjectProvider: owner,
		},
	})
	if err := store.Save(child); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.NewAssistantProgramStore(store).EnsureProjectStation(id); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(id, func(current *workspace.Workspace) error {
		current.AgentInstances = []workspace.AgentInstance{{ID: "inst-" + id, Name: "Assistant", RoleID: "assistant"}}
		link := current.GetAssistantProjectLink()
		link.ProjectBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{{RoleID: "assistant", AgentInstanceID: "inst-" + id, AgentName: "Assistant"}}}
		current.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkspaceAgent(id, "Assistant", &agent.Agent{Status: types.AgentStatusActive}); err != nil {
		t.Fatal(err)
	}
}

func TestResourceCatalogTools_ALinkedSongSeesThemAndAnUnboundAgentDoesNot(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	homeOwner := workspace.AssistantProgramHomeOwner{
		PluginID: "music-project-management", PluginVersion: "0.4.0", ProgramID: "music-producer-assistant", HomeSchemaVersion: 1, HomeVersion: 1,
		DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 3, ComponentFingerprint: strings.Repeat("b", 64),
	}
	declaration := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "music-producer-assistant", StationName: "Music Production Home",
		Roles: []workspace.AssistantProgramRoleSpec{{ID: "portfolio_manager", Label: "Portfolio Manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate."}}}
	home, _, err := workspace.NewAssistantProgramStore(store).EnsureNamedIndependentStation(
		workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: homeOwner.PluginID, ProgramID: homeOwner.ProgramID}, declaration, "Music Production Home", homeOwner)
	if err != nil {
		t.Fatal(err)
	}
	linkCatalogChild(t, store, home, "studio-one", workspace.AssistantWorkspaceKindResource)
	linkCatalogChild(t, store, home, "song-one", "")
	raw, err := os.ReadFile(filepath.Join("..", "resourcecatalog", "testdata", "contract", "declaration.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalogDeclaration, err := resourcecatalog.DecodeDeclaration(raw)
	if err != nil {
		t.Fatal(err)
	}
	service := resourcecatalog.NewService(resourcecatalog.Deps{
		Workspaces: store, Folders: store, DataDir: t.TempDir(), Provider: catalogToolProvider{declaration: catalogDeclaration},
		HomeProviderAvailable: func(*workspace.Workspace) bool { return true },
	})
	review, err := service.ReviewSources(context.Background(), "local", "studio-one")
	if err != nil {
		t.Fatal(err)
	}
	op, err := service.CommitSources(context.Background(), "local", "studio-one", review.Token, "approve", []string{"project_templates", "reaper_scripts"})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		current, err := service.Operation("local", "studio-one", op.ID)
		if err == nil && current.State != resourcecatalog.OperationRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inspection never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}

	toolNames := func(provider *WorkspaceToolProvider) map[string]bool {
		names := map[string]bool{}
		for _, tool := range provider.Tools() {
			names[tool.Definition().Name] = true
		}
		return names
	}
	song := NewWorkspaceToolProvider(nil, store, "song-one")
	song.SetExecutingAgent("Assistant")
	song.SetExecutingInstanceID("inst-song-one")
	song.SetResourceCatalogs(func() *resourcecatalog.Service { return service })
	names := toolNames(song)
	if !names["resource_catalog_search"] || !names["resource_catalog_detail"] {
		t.Fatalf("a bound song assistant has no catalog tools: %v", names)
	}
	out, err := song.resourceCatalogSearchTool().Call(context.Background(), `{"query":"vocal","limit":5}`)
	if err != nil || !strings.Contains(out, `"Vocal chain"`) || !strings.Contains(out, "/catalog?entry=") || !strings.Contains(out, resourcecatalog.DataNotice) {
		t.Fatalf("search = %s, %v", out, err)
	}
	if _, err := song.resourceCatalogSearchTool().Call(context.Background(), `{"query":"vocal","workspace_id":"studio-one"}`); err == nil {
		t.Fatal("a tool argument named a workspace")
	}

	unbound := NewWorkspaceToolProvider(nil, store, "song-one")
	unbound.SetExecutingAgent("Assistant")
	unbound.SetExecutingInstanceID("not-an-instance")
	unbound.SetResourceCatalogs(func() *resourcecatalog.Service { return service })
	if names := toolNames(unbound); names["resource_catalog_search"] {
		t.Fatal("an unbound caller was offered the catalog tools")
	}
	if _, err := unbound.resourceCatalogSearchTool().Call(context.Background(), `{"query":"vocal"}`); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unbound call err = %v", err)
	}
	home2 := NewWorkspaceToolProvider(nil, store, home.ID)
	home2.SetExecutingAgent("Assistant")
	home2.SetExecutingInstanceID("inst-song-one")
	home2.SetResourceCatalogs(func() *resourcecatalog.Service { return service })
	if names := toolNames(home2); names["resource_catalog_search"] {
		t.Fatal("the Home was offered the catalog tools")
	}
}
