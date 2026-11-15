package server

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func folderAgentFacesFixture(t *testing.T) (*ServerBuilder, string, *types.AgentAppearance) {
	t.Helper()
	workspaces := workspace.NewInMemoryStore()
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "fixture", ProgramID: "collection"}
	owner := workspace.AssistantProgramHomeOwner{PluginID: key.PluginID, PluginVersion: "1.0.0", ProgramID: key.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, PluginGeneration: 1, DeclarationDigest: strings.Repeat("a", 64), ComponentFingerprint: strings.Repeat("b", 64)}
	decl := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: key.ProgramID,
		StationName: "Collection", Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}}, Roles: []workspace.AssistantProgramRoleSpec{{ID: "manager", Label: "Manager", Required: true,
			Primary: true, Scope: workspace.AssistantRoleScopeHome, Role: "orchestrator", Type: "tool_calling", SystemPrompt: "Coordinates work"}}}
	home, _, err := workspace.NewAssistantProgramStore(workspaces).EnsureNamedIndependentStation(key, decl, "Collection", owner)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	b := &ServerBuilder{workspaceStore: workspaces, st: profiles}
	b.assistantStaffing = setupjourney.NewAssistantStaffingAdapter(workspaces, profiles, nil,
		func() (string, string) { return "fixture", "model" }, func(string, string) error { return nil })
	ch := charactercatalog.MustLoad().Working()[0]
	appearance := types.NewAgentAppearance()
	appearance.SetCharacter(string(ch.ID), ch.EntryVersion)
	return b, home.ID, appearance
}
