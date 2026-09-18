package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Creation's host-feature check and the one install validates against must be
// the same list. When they were two lists, adding a feature made a plugin that
// requires it install cleanly and then vanish from the blueprint picker at
// creation, with nothing to explain why.
func TestCreationAcceptsEveryHostFeatureInstallDoes(t *testing.T) {
	for _, feature := range plugin.HostFeatures() {
		if !pluginHostFeaturesAvailable([]string{feature}) {
			t.Errorf("install advertises %q but creation refuses it", feature)
		}
	}
	if !pluginHostFeaturesAvailable(plugin.HostFeatures()) {
		t.Error("creation refuses the complete advertised feature set")
	}
	if pluginHostFeaturesAvailable([]string{"a_feature_this_build_does_not_have"}) {
		t.Error("creation accepted an unknown host feature")
	}
	if pluginHostFeaturesAvailable([]string{plugin.HostFeatureSetupQuestsV2, plugin.HostFeatureSetupQuestsV2}) {
		t.Error("creation accepted a duplicated host feature")
	}
	// The reviewed REAPER integration is the concrete case: it requires the
	// newest feature, so a drifted list would hide its blueprint at creation.
	if !pluginHostFeaturesAvailable([]string{
		plugin.HostFeatureAssistantProgramV1, plugin.HostFeatureSpecialistSetupJourneyV1,
		plugin.HostFeatureSetupQuestsV2, plugin.HostFeatureTemplateGroupRequirementsV1,
		plugin.HostFeatureBlueprintInputsV1,
	}) {
		t.Error("creation refuses a plugin that declares inputs, so its blueprints would be inactive")
	}
}

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

type staticInstalledPlugins struct {
	installed []plugin.InstalledPlugin
	err       error
}

func (s staticInstalledPlugins) List() ([]plugin.InstalledPlugin, error) { return s.installed, s.err }

// Derived catalog views (Group Templates) consume the lifecycle decision
// explicitly rather than inferring it from a template's display fields.
func TestBlueprintCatalogSnapshotCarriesExplicitActiveState(t *testing.T) {
	blueprint := func(pluginID string) plugin.ResolvedBlueprint {
		owner := &workspace.PluginTemplateOwner{PluginID: pluginID, PluginVersion: "1.0.0", BlueprintID: "starter", BlueprintVersion: 1}
		return plugin.ResolvedBlueprint{
			ID: "starter", QualifiedID: "plugin:" + pluginID + ":starter", Version: 1,
			Template: projecttemplates.Template{ID: "plugin:" + pluginID + ":starter", Name: pluginID, PluginOwner: owner},
		}
	}
	installed := func(name string, enabled bool) plugin.InstalledPlugin {
		return plugin.InstalledPlugin{
			Name: name, Version: "1.0.0", Enabled: enabled,
			WorkspaceSurfaces: &plugin.SurfaceContribution{
				Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
			},
			ResolvedBlueprints: []plugin.ResolvedBlueprint{blueprint(name)},
			ResolvedArtifacts:  []plugin.ResolvedArtifact{{Available: true}},
		}
	}

	snapshot, err := buildBlueprintCatalogSnapshot(t.TempDir(), nil, staticInstalledPlugins{installed: []plugin.InstalledPlugin{
		installed("enabled", true), installed("disabled", false),
	}}, "local")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DependencyStateUnavailable {
		t.Fatal("readable plugin state reported unavailable")
	}
	active, seenEnabled, seenDisabled := snapshot.Active, false, false
	for _, entry := range snapshot.Entries {
		switch entry.ID {
		case "plugin:enabled:starter":
			seenEnabled = true
		case "plugin:disabled:starter":
			seenDisabled = true
		}
	}
	if !seenEnabled || !seenDisabled || !active["plugin:enabled:starter"] || active["plugin:disabled:starter"] {
		t.Fatalf("snapshot entries/active = %d/%v", len(snapshot.Entries), active)
	}

	unreadable, err := buildBlueprintCatalogSnapshot(t.TempDir(), nil, staticInstalledPlugins{err: errTestPluginListUnavailable}, "local")
	if err != nil {
		t.Fatal(err)
	}
	if !unreadable.DependencyStateUnavailable {
		t.Fatal("unreadable plugin state must be reported, not treated as nothing installed")
	}
	for id := range unreadable.Active {
		if strings.HasPrefix(id, "plugin:") {
			t.Fatalf("unreadable plugin state produced plugin entry %q", id)
		}
	}
}

var errTestPluginListUnavailable = errors.New("plugin store unavailable")

// TestBlueprintCatalogSnapshotOmitsRetiredBuiltinBehindInertCandidate exercises
// the retirement end to end at the snapshot level: a real library holding the
// retired downloads-janitor built-in, plus an inert plugin candidate that
// owns the same blueprint ID. The snapshot must list the plugin's candidate
// (inactive, since its plugin is disabled) and never the built-in, because
// ListLibrary already excludes retired templates before the merge runs
// (FR-7).
func TestBlueprintCatalogSnapshotOmitsRetiredBuiltinBehindInertCandidate(t *testing.T) {
	root := t.TempDir()
	if err := projecttemplates.EnsureLibrary(root); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	owner := &workspace.PluginTemplateOwner{PluginID: "tidy", PluginVersion: "1.0.0", BlueprintID: "downloads-janitor", BlueprintVersion: 1}
	inertPlugin := plugin.InstalledPlugin{
		Name: "tidy", Version: "1.0.0", Enabled: false, // disabled: pluginBlueprintsActive returns false
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			Protocol: plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
		},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{
			ID: "downloads-janitor", QualifiedID: "plugin:tidy:downloads-janitor", Version: 1,
			Template: projecttemplates.Template{ID: "plugin:tidy:downloads-janitor", Name: "Tidy", PluginOwner: owner},
		}},
		ResolvedArtifacts: []plugin.ResolvedArtifact{{Available: true}},
	}

	snapshot, err := buildBlueprintCatalogSnapshot(root, nil, staticInstalledPlugins{installed: []plugin.InstalledPlugin{inertPlugin}}, "local")
	if err != nil {
		t.Fatal(err)
	}
	var sawCandidate bool
	for _, entry := range snapshot.Entries {
		if entry.ID == "downloads-janitor" {
			t.Fatalf("snapshot must omit the retired built-in downloads-janitor, got %+v", snapshot.Entries)
		}
		if entry.ID == "plugin:tidy:downloads-janitor" {
			sawCandidate = true
			if snapshot.Active[entry.ID] {
				t.Fatalf("the plugin's disabled candidate must be inactive: %+v", snapshot.Active)
			}
		}
	}
	if !sawCandidate {
		t.Fatalf("snapshot must list the inert plugin candidate for downloads-janitor: %+v", snapshot.Entries)
	}
}
