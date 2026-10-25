package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFolderRecommendation_CanonicalLocalDestinationAndProviderStates(t *testing.T) {
	provider := reviewedintegration.HomeProviders()[0]
	files, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = files.Close() })
	plugins := t.TempDir()
	store := plugin.NewStore(plugins)
	b := &ServerBuilder{workspaceFileStore: files, workspaceStore: files, pluginHandler: pluginhttp.NewHandler(nil, nil, plugins)}
	b.reviewedIntegrationReader = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
		t.Fatal("discovery consulted a release or prerequisite")
		return setupjourney.CanonicalStepRead{}, nil
	})
	host := &folderSetupHost{builder: b}
	// A matching display name supplies no provider ownership.
	if err := files.Save(&workspace.Workspace{ID: "unrelated", Name: "Music Production Home", OwnerUserID: "local", Kind: "group", Status: workspace.StatusActive}); err != nil {
		t.Fatal(err)
	}
	got := host.ReadFolderRecommendation(t.Context(), "local", provider.Key)
	if got.State != "absent" || got.Readiness != "provider_missing" {
		t.Fatalf("namesake adopted or absence concealed: %+v", got)
	}
	owner := workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.4.0", ProgramID: provider.ProgramID, HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64)}
	decl := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: provider.ProgramID, StationName: "My Music", Roles: []workspace.AssistantProgramRoleSpec{{ID: "producer", Label: "Producer", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate reviewed music work."}}}
	home, _, err := workspace.NewAssistantProgramStore(files).EnsureNamedIndependentStation(workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: provider.PluginID, ProgramID: provider.ProgramID}, decl, "My Music", owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version string
		enabled bool
		want    string
	}{
		{"0.4.0", false, "provider_disabled"},
		{"0.0.1", true, "provider_outdated"},
		{"0.4.0", true, "destination_incompatible"}, // version alone is not compatible contribution evidence
	} {
		if err := store.Put(plugin.InstalledPlugin{Name: provider.PluginID, Version: tc.version, Enabled: tc.enabled}); err != nil {
			t.Fatal(err)
		}
		got = host.ReadFolderRecommendation(t.Context(), "local", provider.Key)
		if got.State != "existing" || got.Name != home.Name || got.Readiness != tc.want {
			t.Fatalf("provider metadata: %+v, want %s", got, tc.want)
		}
	}
	b.workspaceStore = destinationReadStore{Store: files, read: func(string) (*workspace.Workspace, error) { return nil, errors.New("read failed") }}
	got = host.ReadFolderRecommendation(t.Context(), "local", provider.Key)
	if got.State != "unknown" || got.Readiness != "unavailable" {
		t.Fatalf("failed live read became absence: %+v", got)
	}
	b.workspaceStore = files
	duplicate := &workspace.Workspace{ID: "duplicate", Name: "Another Music", Kind: "group", OwnerUserID: "local", Status: workspace.StatusActive}
	duplicate.SetAssistantProgramState(home.GetAssistantProgramState())
	if err := files.Save(duplicate); err != nil {
		t.Fatal(err)
	}
	got = host.ReadFolderRecommendation(t.Context(), "local", provider.Key)
	if got.State != "unknown" || got.Readiness != "destination_ambiguous" {
		t.Fatalf("ambiguous destination guessed: %+v", got)
	}
}
