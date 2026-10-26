package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalassistanthttp"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// pinnedHomeFixture is one Home whose provider is the installed 0.3.0 release.
func pinnedHomeFixture(t *testing.T) (workspace.Store, plugin.InstalledPlugin, *workspace.Workspace) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	owner := workspace.AssistantProgramHomeOwner{PluginID: "music-project-management", PluginVersion: "0.3.0",
		ProgramID: "music-producer-assistant", ComponentFingerprint: strings.Repeat("a", 64), HomeSchemaVersion: 1, HomeVersion: 1}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Production Home"})
	home.Kind = "group"
	home.OwnerUserID = "local"
	home.FolderSlug = "music-production-home"
	home.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
		Key:          workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: owner.PluginID, ProgramID: owner.ProgramID},
		HomeProvider: &owner, PluginAvailable: true})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	installed := plugin.InstalledPlugin{Name: owner.PluginID, Version: owner.PluginVersion, ComponentFingerprint: owner.ComponentFingerprint,
		WorkspaceSurfaces: &plugin.SurfaceContribution{AssistantProgramHomes: []projecttemplates.AssistantProgramHome{{
			SchemaVersion: 1, Version: 1, ID: owner.ProgramID,
		}}}}
	return store, installed, home
}

// The preview names the Home the replacement guard would refuse for, so a plan
// never promises an update that cannot be made.
func TestHomeUpgradeBlocking_NamesTheHomeTheGuardWouldRefuseFor(t *testing.T) {
	store, installed, home := pinnedHomeFixture(t)
	refusal := homeUpgradeBlocking(store, installed, "0.4.0")
	if refusal == nil || refusal.HomeName != "Music Production Home" ||
		refusal.HomePath != "/workspaces/music-production-home/assistant?upgrade=review" || strings.Contains(refusal.HomePath, home.ID) {
		t.Fatalf("refusal = %+v", refusal)
	}
	// The guard itself refuses the same update.
	if err := refuseUnreviewedHomeReplacement(store, installed, "0.4.0", strings.Repeat("b", 64), nil); !errors.Is(err, plugin.ErrHomeUpgradeRequired) {
		t.Fatalf("the guard allowed what the preview said it would refuse: %v", err)
	}
	if refusal := homeUpgradeBlocking(store, installed, "0.3.0"); refusal != nil {
		t.Fatalf("a Home already on that release blocked it: %+v", refusal)
	}
	if refusal := homeUpgradeBlocking(workspace.NewInMemoryStore(), installed, "0.4.0"); refusal != nil {
		t.Fatalf("no Home, yet one blocked the update: %+v", refusal)
	}
	other := installed
	other.WorkspaceSurfaces = nil
	if refusal := homeUpgradeBlocking(store, other, "0.4.0"); refusal != nil {
		t.Fatalf("a plugin that provides no Home was blocked: %+v", refusal)
	}
	// Unreadable Homes are not reported from a preview; the guard still decides.
	if refusal := homeUpgradeBlocking(nil, installed, "0.4.0"); refusal != nil {
		t.Fatalf("an unreadable store named a Home: %+v", refusal)
	}
}

func TestProviderPlugin_AHomeOnTheInstalledReleaseHoldsThePlanBack(t *testing.T) {
	provider, ok := reviewedintegration.HomeProviderFor("music-project-management")
	if !ok {
		t.Fatal("reviewed Home provider fixture is missing")
	}
	home := personalassistant.FolderHomeLink{Name: "Music Production Home", Route: "/workspaces/music-production-home/assistant?upgrade=review"}
	preview := personalassistanthttp.FolderHomeProviderPreview{PluginID: provider.PluginID, Installed: true, Update: true,
		Version: "0.4.0", InstalledVersion: "0.3.0", HomeUpgrade: &home}
	row, err := providerPlugin(provider, preview)
	if row.State != personalassistant.FolderVisualHomeUpgrade || row.InstalledVersion != "0.3.0" || row.Version != "0.4.0" {
		t.Fatalf("the requirement row does not say the Home must move first: %+v", row)
	}
	var refusal interface {
		SetupPreviewReason() string
		SetupHomeUpgrade() (string, personalassistant.FolderHomeLink)
	}
	if !errors.As(err, &refusal) || !errors.Is(err, errSetupUnavailable) || refusal.SetupPreviewReason() != personalassistant.FolderStopHomeUpgrade {
		t.Fatalf("err = %v", err)
	}
	message, named := refusal.SetupHomeUpgrade()
	if named != home || !strings.Contains(message, "Music Production Home") || !strings.Contains(message, "0.3.0") || !strings.Contains(message, "0.4.0") {
		t.Fatalf("message = %q, home = %+v", message, named)
	}
	var unavailable folderSetupUnavailable
	if !errors.As(err, &unavailable) || unavailable.availability != personalassistant.FolderAvailabilityPlanUnavailable {
		t.Fatalf("the refusal is not logged or worded as an unavailable plan: %+v", unavailable)
	}

	// With no Home in the way the same preview is an ordinary update.
	preview.HomeUpgrade = nil
	facts, err := providerPlugin(provider, preview)
	if err != nil || facts.State != personalassistant.FolderInstallUpdate || facts.InstalledVersion != "0.3.0" || facts.Version != "0.4.0" {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
}
