package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFolderHomeStaffingProducerKeepsUndecidedAndOriginalSourcesInExistingPlan(t *testing.T) {
	f := newDraftServerFixture(t)
	b := f.builder
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"}
	owner := workspace.AssistantProgramHomeOwner{PluginID: key.PluginID, PluginVersion: "0.4.0", ProgramID: key.ProgramID, HomeSchemaVersion: 1, HomeVersion: 1, PluginGeneration: 1, DeclarationDigest: strings.Repeat("a", 64), ComponentFingerprint: strings.Repeat("b", 64)}
	declaration := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: key.ProgramID, StationName: "Collection", Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}}, Roles: []workspace.AssistantProgramRoleSpec{{ID: "manager", Label: "Manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, Type: "tool_calling", Description: "Coordinates collections", SystemPrompt: "PRIVATE_ROLE_CANARY"}}}
	home, _, err := workspace.NewAssistantProgramStore(b.workspaceStore).EnsureNamedIndependentStation(key, declaration, "Collection", owner)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.CreateAgent("Aster", &store.CreateAgentConfig{LLMProvider: "fixture", Model: "model", SystemPrompt: "PRIVATE_PROFILE_CANARY"}); err != nil {
		t.Fatal(err)
	}
	b.st = profiles
	b.sessionHandler.SetAgentStore(profiles)
	b.sessionHandler.SetWorkspaceBuild(sessionhttp.WorkspaceBuildDeps{Store: noStaffingPreflightBuildStore{},
		Assistant:              func(context.Context, string) (*personalassistant.Projection, error) { panic("plan read recommender") },
		ProfileModelConfigured: func(provider, model string) bool { return provider == "fixture" && model == "model" },
		Catalog: func(context.Context, string) ([]sessionhttp.WorkspaceBuildCatalogEntry, error) {
			panic("plan read builder catalog")
		},
		ResolveModel: func(context.Context, string) (sessionhttp.WorkspaceBuildModel, error) {
			panic("plan resolved recommender")
		},
	})
	destination, err := folderSetupHomeDestination(home)
	if err != nil {
		t.Fatal(err)
	}
	base := (personalassistant.FolderSetupPlan{Destination: destination, Lines: []personalassistant.FolderPlanLine{{Kind: personalassistant.FolderPlanAgents, Name: "Adds the Home agents"}},
		Intent: personalassistant.FolderSetupIntent{Portfolio: true, StaffsHome: true, Placement: "grouped", HomeTemplate: "plugin-home:" + key.PluginID + ":" + key.ProgramID, ProviderPlugin: key.PluginID, ProviderVersion: owner.PluginVersion}}).Stamped()
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{ID: "offer", Status: personalassistant.FolderOfferPending, Portfolio: &personalassistant.FolderPortfolioEvidence{ProviderKey: "music_project_management"}}}
	host := &folderSetupHost{builder: b}
	pending, err := host.withFolderHomeStaffingReview(t.Context(), req, base)
	if err != nil || pending.HomeStaffing == nil || pending.HomeStaffing.State != "needs_choice" || !pending.Intent.RequiresHomeStaffing || pending.Intent.HomeStaffing != nil || pending.Digest != "" {
		t.Fatal("undecided Home was confirmable", err)
	}
	source := store.ReadStaffingSnapshot(t.Context(), profiles).Profiles[0].Witness
	req.Offer.HomeStaffingReview = &personalassistant.FolderHomeStaffingReview{Version: 1, Revision: 1, ContextDigest: pending.HomeStaffing.ContextDigest,
		Team: &rosterstaffing.TeamIntent{Version: 1, Roles: []rosterstaffing.RoleIntent{{RoleID: "manager", Scope: "home", Mode: "assign", Name: "Aster", SourceWitness: source}}}}
	ready, err := host.withFolderHomeStaffingReview(t.Context(), req, base)
	if err != nil || ready.HomeStaffing.State != "ready" || ready.Digest == "" || ready.Intent.HomeStaffing.Roles[0].SourceWitness != source {
		t.Fatal("exact original team not projected", err)
	}
	current, err := b.workspaceStore.Get(home.ID)
	if err != nil || len(current.GetAgentInstances()) != 0 || len(profiles.ListAgents()) != 1 {
		t.Fatal("plan performed staffing", err)
	}
	if err := profiles.UpdateAgent("Aster", func(profile *agent.Agent) error { profile.Settings.SystemPrompt = "EXPLICIT_SOURCE_EDIT"; return nil }); err != nil {
		t.Fatal(err)
	}
	stale, err := host.withFolderHomeStaffingReview(t.Context(), req, base)
	if err != nil || stale.HomeStaffing.State != "stale" || len(stale.HomeStaffing.Choices) != 1 || stale.HomeStaffing.Choices[0].Name != "Aster" || stale.Digest != "" || stale.Intent.HomeStaffing != nil {
		t.Fatal("read renewed or lost a changed source", err)
	}
	req.Offer.ID, req.Offer.HomeStaffingReview = "", nil
	preview, err := host.withFolderHomeStaffingReview(t.Context(), req, base)
	if err != nil || preview.HomeStaffing.State != "needs_review" || preview.HomeStaffing.ContextDigest != "" || preview.Digest != "" {
		t.Fatal("provisional build minted canonical review authority", err)
	}
	req.Offer.Setup = &personalassistant.FolderSetupRun{}
	legacy, err := host.withFolderHomeStaffingReview(t.Context(), req, base)
	if err != nil || legacy.Digest != base.Digest || legacy.Intent.RequiresHomeStaffing || legacy.HomeStaffing != nil {
		t.Fatal("accepted legacy run gained new authority", err)
	}
}
