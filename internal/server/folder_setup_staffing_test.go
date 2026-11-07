package server

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Preflight may consult registry-only configuration but must never read or
// mutate builder sessions, resolve its assistant, or call its model.
type noStaffingPreflightBuildStore struct{}

func (noStaffingPreflightBuildStore) Read(context.Context, string) (personalassistant.WorkspaceBuildDocument, error) {
	panic("preflight read build state")
}
func (noStaffingPreflightBuildStore) Mutate(context.Context, string, func(*personalassistant.WorkspaceBuildDocument) error) (personalassistant.WorkspaceBuildDocument, error) {
	panic("preflight mutated build state")
}
func (noStaffingPreflightBuildStore) Now() time.Time { panic("preflight read build clock") }

func TestPortfolioReviewedHomeStaffingUsesCanonicalSourceAndOutcomeAcrossRestart(t *testing.T) {
	for _, action := range []string{"assign", "create", "stale_source"} {
		t.Run(action, func(t *testing.T) {
			workspaces := workspace.NewInMemoryStore()
			key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "fixture", ProgramID: "collection"}
			owner := workspace.AssistantProgramHomeOwner{PluginID: key.PluginID, PluginVersion: "1.0.0", ProgramID: key.ProgramID, HomeSchemaVersion: 1, HomeVersion: 1, PluginGeneration: 1, DeclarationDigest: strings.Repeat("a", 64), ComponentFingerprint: strings.Repeat("b", 64)}
			declaration := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: key.ProgramID, StationName: "Collection", Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}}, Roles: []workspace.AssistantProgramRoleSpec{{ID: "manager", Label: "Manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, Type: "tool_calling", SystemPrompt: "New role instructions", Description: "Coordinates collections"}}}
			home, _, err := workspace.NewAssistantProgramStore(workspaces).EnsureNamedIndependentStation(key, declaration, "Collection", owner)
			if err != nil {
				t.Fatal(err)
			}
			index := filepath.Join(t.TempDir(), "agents.json")
			profiles, err := store.NewFileStore(index, types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			if err := profiles.CreateAgent("Aster", &store.CreateAgentConfig{LLMProvider: "fixture", Model: "model", SystemPrompt: "AUTHORED_PRIVATE_CANARY"}); err != nil {
				t.Fatal(err)
			}
			b := &ServerBuilder{workspaceStore: workspaces, st: profiles}
			configured := true
			wire := func() {
				b.sessionHandler = sessionhttp.New(nil)
				b.sessionHandler.SetAgentStore(b.st)
				b.sessionHandler.SetWorkspaceBuild(sessionhttp.WorkspaceBuildDeps{
					Store: noStaffingPreflightBuildStore{},
					Assistant: func(context.Context, string) (*personalassistant.Projection, error) {
						panic("preflight read assistant")
					},
					Catalog: func(context.Context, string) ([]sessionhttp.WorkspaceBuildCatalogEntry, error) {
						panic("preflight read builder catalog")
					},
					ResolveModel: func(context.Context, string) (sessionhttp.WorkspaceBuildModel, error) {
						panic("preflight resolved recommender")
					},
					ProfileModelConfigured: func(provider, model string) bool { return configured && provider == "fixture" && model == "model" },
				})
				b.assistantStaffing = setupjourney.NewAssistantStaffingAdapter(workspaces, b.st, nil,
					func() (string, string) { t.Fatal("exact role resolved a new-agent default"); return "", "" },
					func(provider, model string) error {
						if provider != "fixture" || model != "model" {
							return setupjourney.ErrInvalid
						}
						return nil
					})
			}
			wire()
			consumer := portfolioHomeStaffing{builder: b}
			mode := action
			if action == "stale_source" {
				mode = "assign"
			}
			role := rosterstaffing.RoleIntent{RoleID: "manager", Scope: "home", Mode: mode, Name: "Aster"}
			count, want := 1, "assigned"
			if mode == "assign" {
				role.SourceWitness = store.ReadStaffingSnapshot(t.Context(), profiles).Profiles[0].Witness
			} else {
				role.Name, role.Provider, role.Model, role.Appearance = "Cedar", "fixture", "model", types.NewAgentAppearance()
				count, want = 2, "created"
			}
			if action == "stale_source" {
				if err := profiles.UpdateAgent("Aster", func(profile *agent.Agent) error {
					profile.Settings.SystemPrompt = "Replacement instructions"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := json.Marshal(store.LibraryDefinition(profiles, "Aster").Definition)
			destination, err := folderSetupHomeDestination(home)
			if err != nil {
				t.Fatal(err)
			}
			plan := (personalassistant.FolderSetupPlan{Destination: destination, Lines: []personalassistant.FolderPlanLine{{Kind: personalassistant.FolderPlanAgents, Name: role.Name}}, Intent: personalassistant.FolderSetupIntent{
				Portfolio: true, StaffsHome: true, Placement: "grouped", HomeTemplate: "plugin-home:fixture:collection", ProviderPlugin: "fixture", ProviderVersion: "1.0.0",
				HomeStaffing: &rosterstaffing.TeamIntent{Version: 1, Roles: []rosterstaffing.RoleIntent{role}},
			}}).Stamped()
			if plan.Digest == "" {
				t.Fatal("invalid exact plan fixture")
			}
			req := personalassistant.FolderSetupRequest{UserID: "local", Plan: plan}
			host := &folderSetupHost{builder: b}
			if err := host.validateSetupModel(req); err != nil {
				t.Fatal("exact selection consulted a default", err)
			}
			preflight := host.checkFolderHomeTeam(t.Context(), req, home)
			if (preflight != nil) != (action == "stale_source") {
				t.Fatal("pending source preflight", preflight)
			}
			digest := plan.Digest
			outcome, err := consumer.StaffReviewed(t.Context(), home.ID, role, digest)
			if action == "stale_source" {
				current, _ := workspaces.Get(home.ID)
				after, _ := json.Marshal(store.LibraryDefinition(profiles, "Aster").Definition)
				if err == nil || len(current.GetAgentInstances()) != 0 || len(profiles.ListAgents()) != 1 || !bytes.Equal(before, after) {
					t.Fatal("stale source bound, changed or allocated a profile", err)
				}
				return
			}
			if err != nil || outcome != want || len(profiles.ListAgents()) != count {
				t.Fatal("exact role failed", outcome, err)
			}
			after, _ := json.Marshal(store.LibraryDefinition(profiles, "Aster").Definition)
			if !bytes.Equal(before, after) {
				t.Fatal("authored saved profile was changed")
			}
			b.st, err = store.NewFileStore(index, types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			configured = false // completed observation needs neither a new source nor model availability
			wire()
			current, _ := workspaces.Get(home.ID)
			if err := host.checkFolderHomeTeam(t.Context(), req, current); err != nil {
				t.Fatal("completed preflight renewed source/configuration", err)
			}
			outcome, err = consumer.StaffReviewed(t.Context(), home.ID, role, digest)
			if err != nil || outcome != "already_present" || len(b.st.ListAgents()) != count {
				t.Fatal("restart repeated the role", outcome, err)
			}
			facts := foldersetup.HomeRoleOutcome{RoleID: role.RoleID, Name: role.Name, Action: role.Mode, Outcome: outcome, ChoiceDigest: role.StepDigest(digest, home.ID)}
			current, _ = workspaces.Get(home.ID)
			if !folderHomeRolePresent(current, facts) {
				t.Fatal("canonical outcome not observable")
			}
			reader := portfolioReceipts{host: &folderSetupHost{builder: b}, folder: "Collection", noun: "projects"}
			rows, err := reader.Receipt(t.Context(), home.ID, foldersetup.PortfolioReceiptFacts{HomeCreated: true, HomeRoles: []foldersetup.HomeRoleOutcome{facts}})
			if err != nil || len(rows) < 2 || rows[1].Name != role.Name || rows[1].Detail != "already present" || !rows[1].Assigned {
				t.Fatal("receipt invented a new hire on replay", err, rows)
			}
			if err := b.assistantStaffing.UnstaffRoleFromWorkspace(t.Context(), home.ID, role.RoleID); err != nil {
				t.Fatal(err)
			}
			current, _ = workspaces.Get(home.ID)
			if folderHomeRolePresent(current, facts) {
				t.Fatal("cleared role was reported present")
			}
			if err := host.checkFolderHomeTeam(t.Context(), req, current); err == nil {
				t.Fatal("cleared outcome passed preflight")
			}
			rows, err = reader.Receipt(t.Context(), home.ID, foldersetup.PortfolioReceiptFacts{HomeCreated: true, HomeRoles: []foldersetup.HomeRoleOutcome{facts}})
			if err != nil || len(rows) < 2 || rows[1].Assigned || rows[1].Detail != "not completed; review this role" {
				t.Fatal("receipt hid a subsequently cleared role", err, rows)
			}
			if _, err := consumer.StaffReviewed(t.Context(), home.ID, role, digest); err == nil {
				t.Fatal("old role choice refilled explicit clear")
			}
			if len(b.st.ListAgents()) != count {
				t.Fatal("retry created an additional profile")
			}
		})
	}
}
