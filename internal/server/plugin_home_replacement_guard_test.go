package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHomeReplacementGuard_ExistingHomeNeedsReviewBeforeNewGuidanceCanReplaceProvider(t *testing.T) {
	store := workspace.NewInMemoryStore()
	owner := workspace.AssistantProgramHomeOwner{PluginID: "music-project-management", PluginVersion: "0.1.0",
		ProgramID: "music-producer-assistant", ComponentFingerprint: strings.Repeat("a", 64),
		HomeSchemaVersion: 1, HomeVersion: 1, PluginGeneration: 4}
	station := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Existing Home"})
	station.Kind = "group"
	station.OwnerUserID = "local"
	station.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
		Key:          workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: owner.PluginID, ProgramID: owner.ProgramID},
		HomeProvider: &owner, PluginAvailable: true})
	if err := store.Save(station); err != nil {
		t.Fatal(err)
	}
	current := plugin.InstalledPlugin{Name: owner.PluginID, Version: owner.PluginVersion,
		ComponentFingerprint: owner.ComponentFingerprint,
		WorkspaceSurfaces: &plugin.SurfaceContribution{AssistantProgramHomes: []projecttemplates.AssistantProgramHome{{
			SchemaVersion: 1, Version: 1, ID: owner.ProgramID,
		}}}}
	if err := refuseUnreviewedHomeReplacement(store, current, "0.1.0", owner.ComponentFingerprint); err != nil {
		t.Fatalf("same reviewed bytes were blocked: %v", err)
	}
	for _, tc := range []struct{ name, version, fingerprint string }{
		{"new skill and role bytes", "0.1.1", strings.Repeat("b", 64)},
		{"version change", "0.1.1", owner.ComponentFingerprint},
		{"changed guidance under same version", "0.1.0", strings.Repeat("c", 64)},
	} {
		if err := refuseUnreviewedHomeReplacement(store, current, tc.version, tc.fingerprint); err == nil ||
			!strings.Contains(err.Error(), "reviewed Home/child upgrade") {
			t.Errorf("%s stranded Home without clear refusal: %v", tc.name, err)
		}
	}
	if err := refuseUnreviewedHomeReplacement(nil, current, "0.1.1", strings.Repeat("b", 64)); err == nil {
		t.Fatal("unavailable Home store was treated as no affected Homes")
	}
	if err := refuseUnreviewedHomeReplacement(workspace.NewInMemoryStore(), current, "0.1.1", strings.Repeat("b", 64)); err != nil {
		t.Fatalf("new install with no existing Home was blocked: %v", err)
	}
}
