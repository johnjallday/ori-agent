package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// musicHomeCarrierPlugin is the Music plugin as far as the catalog sees it:
// one program Home ("Music Production Home") and no project blueprint.
func musicHomeCarrierPlugin(t *testing.T) plugin.InstalledPlugin {
	t.Helper()
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "music-producer-assistant",
		StationName: "Music Production Home", DefaultPrimaryName: "Portfolio Manager", HireTitle: "Staff this Home",
		Roles:      []projecttemplates.AssistantProgramHomeRole{{ID: "portfolio_manager", Label: "Portfolio Manager", Required: true, Primary: true, SystemPrompt: "Coordinate bounded work."}},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation", AcceptedCompletionThreshold: 0}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168, MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
	}
	if err := projecttemplates.NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatal(err)
	}
	return plugin.InstalledPlugin{
		Name: "music-project-management", Version: "0.1.0", Generation: 1, Enabled: true,
		ComponentFingerprint: strings.Repeat("a", 64),
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			Protocol:              plugin.ProtocolRange{Min: 1, Max: 1},
			RequiresHostFeatures:  []string{plugin.HostFeatureIndependentProgramHomesV1},
			AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home},
		},
	}
}

// On 2026-10-19 an assisted build chose "Music Production Home" as the
// blueprint for a REAPER song folder. That template is the Home carrier the
// Group creator makes groups from, not a workspace blueprint: the build
// catalog must never contain one, while the Group creator keeps it.
func TestWorkspaceBuildCatalog_NeverOffersAHomeCarrier(t *testing.T) {
	root := t.TempDir()
	if err := projecttemplates.EnsureLibrary(root); err != nil {
		t.Fatal(err)
	}
	snapshot, err := buildBlueprintCatalogSnapshot(root, nil, staticInstalledPlugins{installed: []plugin.InstalledPlugin{musicHomeCarrierPlugin(t)}}, "local")
	if err != nil {
		t.Fatal(err)
	}
	carrier := "plugin-home:music-project-management:music-producer-assistant"
	if !snapshot.Active[carrier] {
		t.Fatalf("the fixture's Home carrier is in the shared snapshot and active: %v", snapshot.Active)
	}

	build := workspaceBuildCatalogEntries(snapshot)
	if len(build) == 0 {
		t.Fatal("the build catalog still offers the library's workspace blueprints")
	}
	for _, entry := range build {
		if entry.Template.ProgramHomeOwner != nil || entry.Template.ID == carrier {
			t.Fatalf("the build catalog offers the Home carrier %q", entry.Template.ID)
		}
		if !snapshot.Active[entry.Template.ID] {
			t.Fatalf("the build catalog offers the inactive %q", entry.Template.ID)
		}
	}

	groups := groupTemplateCatalogEntries(snapshot)
	found := false
	for _, entry := range groups {
		found = found || (entry.Template.ID == carrier && entry.Active && entry.Template.ProgramHomeOwner != nil)
	}
	if !found || len(groups) != len(snapshot.Entries) {
		t.Fatalf("the Group creator's catalog keeps every entry, the carrier included: found=%v %d/%d", found, len(groups), len(snapshot.Entries))
	}
}
