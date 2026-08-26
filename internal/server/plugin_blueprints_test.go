package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestPluginBlueprintSupersedesMatchingBuiltinOnlyWhileActive(t *testing.T) {
	owner := &workspace.PluginTemplateOwner{PluginID: "owner", BlueprintID: "song", BlueprintVersion: 1}
	existing := []projecttemplates.Template{
		{ID: "song", Name: "Built-in song", Builtin: true},
		{ID: "research", Name: "Research", Builtin: true},
	}
	contributed := []pluginBlueprintCandidate{{
		Template: projecttemplates.Template{ID: "plugin:owner:song", Name: "Plugin song", PluginOwner: owner},
		Active:   true,
	}}
	merged := mergePluginBlueprintCandidates(existing, contributed)
	if len(merged) != 2 || merged[0].ID != "research" || merged[1].ID != "plugin:owner:song" {
		t.Fatalf("merged templates = %+v", merged)
	}
	if got := mergePluginBlueprintCandidates(existing, nil); len(got) != 2 {
		t.Fatalf("inactive plugin removed a built-in: %+v", got)
	}
}

// An inert candidate explains itself in the catalog but must not displace a
// built-in the app still ships — installing a plugin that cannot run here
// would otherwise take a working blueprint away.
func TestInertPluginBlueprintOnlySupersedesARetiredBuiltin(t *testing.T) {
	shipped := &workspace.PluginTemplateOwner{PluginID: "owner", BlueprintID: "research-project", BlueprintVersion: 1}
	retired := &workspace.PluginTemplateOwner{PluginID: "owner", BlueprintID: "song-production", BlueprintVersion: 1}
	if !projecttemplates.IsBuiltinStarterID("research-project") || projecttemplates.IsBuiltinStarterID("song-production") {
		t.Skip("embedded starter catalog no longer matches this fixture's assumptions")
	}
	existing := []projecttemplates.Template{
		{ID: "research-project", Name: "Research Project", Builtin: true},
		{ID: "song-production", Name: "Song Production", Builtin: true},
	}
	inert := []pluginBlueprintCandidate{
		{Template: projecttemplates.Template{ID: "plugin:owner:research-project", PluginOwner: shipped}},
		{Template: projecttemplates.Template{ID: "plugin:owner:song-production", PluginOwner: retired}},
	}

	merged := mergePluginBlueprintCandidates(existing, inert)
	ids := make(map[string]bool, len(merged))
	for _, template := range merged {
		ids[template.ID] = true
	}
	if !ids["research-project"] {
		t.Fatalf("an inert candidate displaced a shipped built-in: %+v", merged)
	}
	if ids["song-production"] {
		t.Fatalf("a retired built-in survived its plugin-owned replacement: %+v", merged)
	}
	if !ids["plugin:owner:research-project"] || !ids["plugin:owner:song-production"] {
		t.Fatalf("inert candidates were dropped from the catalog: %+v", merged)
	}
}

func TestActivePluginBlueprintCatalogRequiresEnabledCompatibleOwner(t *testing.T) {
	owner := &workspace.PluginTemplateOwner{
		PluginID: "demo", PluginVersion: "1.0.0", BlueprintID: "starter", BlueprintVersion: 1,
	}
	blueprint := plugin.ResolvedBlueprint{
		ID: "starter", QualifiedID: "plugin:demo:starter", Version: 1,
		Template: projecttemplates.Template{ID: "plugin:demo:starter", Name: "Demo", PluginOwner: owner},
	}
	base := plugin.InstalledPlugin{
		Name: "demo", Enabled: true,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
		},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{blueprint},
		ResolvedArtifacts:  []plugin.ResolvedArtifact{{Available: true}},
	}
	disabled := base
	disabled.Enabled = false
	unsupported := base
	unsupported.Name = "unsupported"
	unsupported.ResolvedArtifacts = []plugin.ResolvedArtifact{{Available: false, Unavailable: "platform_unsupported"}}
	portableOnly := base
	portableOnly.Name = "portable"
	portableOnly.WorkspaceSurfaces = nil
	incompatible := base
	incompatible.Name = "incompatible"
	incompatible.WorkspaceSurfaces = &plugin.SurfaceContribution{
		Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion + 1, Max: plugin.SurfaceProtocolVersion + 1},
	}

	got := activePluginBlueprintTemplates([]plugin.InstalledPlugin{disabled, unsupported, portableOnly, incompatible, base})
	if len(got) != 1 || got[0].ID != "plugin:demo:starter" || got[0].PluginOwner == nil {
		t.Fatalf("active plugin blueprints = %+v", got)
	}

	// Every one of those records still contributes a visible candidate, so the
	// user can see the blueprint and why it is not ready.
	candidates := candidatePluginBlueprintTemplates([]plugin.InstalledPlugin{disabled, unsupported, portableOnly, incompatible, base})
	if len(candidates) != 5 {
		t.Fatalf("candidate blueprints = %d, want 5: %+v", len(candidates), candidates)
	}
	activeCount := 0
	for _, candidate := range candidates {
		if candidate.Active {
			activeCount++
			continue
		}
		// An inert candidate must be uninstantiable by construction.
		if candidate.Template.Path != "" || candidate.Template.HasSkeleton {
			t.Fatalf("inert candidate carries a skeleton: %+v", candidate.Template)
		}
	}
	if activeCount != 1 {
		t.Fatalf("active candidates = %d, want 1", activeCount)
	}
}
