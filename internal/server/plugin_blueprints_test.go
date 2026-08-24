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
	contributed := []projecttemplates.Template{{
		ID: "plugin:owner:song", Name: "Plugin song", PluginOwner: owner,
	}}
	merged := mergeActivePluginBlueprints(existing, contributed)
	if len(merged) != 2 || merged[0].ID != "research" || merged[1].ID != "plugin:owner:song" {
		t.Fatalf("merged templates = %+v", merged)
	}
	if got := mergeActivePluginBlueprints(existing, nil); len(got) != 2 {
		t.Fatalf("inactive plugin removed a built-in: %+v", got)
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
		Name: "demo", Enabled: true, WorkspaceSurfaces: &plugin.SurfaceContribution{},
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

	got := activePluginBlueprintTemplates([]plugin.InstalledPlugin{disabled, unsupported, portableOnly, base})
	if len(got) != 1 || got[0].ID != "plugin:demo:starter" || got[0].PluginOwner == nil {
		t.Fatalf("active plugin blueprints = %+v", got)
	}
}
