package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func requireResetNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestResetLifecycleEmptyGlobalStoreReadoptsLegacyCWDAgents(t *testing.T) {
	f := resetfixture.New(t)
	p := f.Paths()
	legacy, err := store.NewFileStore(filepath.Join(p.WorkDir, "agents.json"), types.Settings{})
	requireResetNoError(t, err)
	requireResetNoError(t, legacy.CreateAgent("Legacy Fixture", nil))
	for range 2 {
		reopened, err := createFileStore(filepath.Join(p.DataDir, "agents.json"), types.Settings{})
		requireResetNoError(t, err)
		if _, found := reopened.GetAgent("Legacy Fixture"); !found {
			t.Fatal("expected legacy CWD agents to be adopted into empty data root")
		}
		// Only an owned fixture profile tree is removed; no workspace/vault
		// files or live database handles are involved.
		requireResetNoError(t, os.RemoveAll(filepath.Join(p.DataDir, "agents")))
	}
}

func TestResetLifecycleLocalAllowlistBackfillRestoresRetainedAgentSnapshot(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	folders, err := workspace.NewFileStore(p.Workspaces)
	requireResetNoError(t, err)
	t.Cleanup(func() { requireResetNoError(t, folders.Close()) })
	agents, err := store.NewFileStore(filepath.Join(p.DataDir, "agents.json"), types.Settings{})
	requireResetNoError(t, err)
	agent, found := agents.GetAgent(resetfixture.AgentName)
	if !found {
		t.Fatal("seeded agent missing")
	}
	ws, err := folders.Get(resetfixture.WorkspaceID)
	requireResetNoError(t, err)
	ws.AgentInstances = []workspace.AgentInstance{{ID: "fixture-instance", Name: resetfixture.AgentName}}
	requireResetNoError(t, folders.Save(ws))
	requireResetNoError(t, folders.SaveWorkspaceAgent(ws.ID, resetfixture.AgentName, agent))
	empty, err := store.NewFileStore(filepath.Join(p.DataDir, "empty", "agents.json"), types.Settings{})
	requireResetNoError(t, err)
	allowlist := workspace.NewAllowlist(filepath.Join(p.DataDir, workspace.DefaultAllowlistFilename))
	workspace.RestoreAllowlistedWorkspaceAgents(folders, empty, allowlist)
	if len(empty.ListAgents()) != 0 {
		t.Fatal("empty allowlist unexpectedly restored snapshots")
	}
	workspace.BackfillLocalWorkspacesIntoAllowlist(folders, allowlist)
	workspace.RestoreAllowlistedWorkspaceAgents(folders, empty, allowlist)
	if _, found := empty.GetAgent(resetfixture.AgentName); !found {
		t.Fatal("expected physical workspace backfill to re-enable snapshot hydration")
	}
}

func TestResetLifecycleOperatorRootOverridesFreshInstallConsent(t *testing.T) {
	f := resetfixture.New(t)
	cfg := config.NewManagerWithSecretStore(filepath.Join(f.Paths().DataDir, "settings.json"), f.Secrets())
	requireResetNoError(t, cfg.Load())
	if shouldRunWorkspaceStartupMaintenance(cfg) || resolveWorkspaceRoot(cfg) != config.UnconfirmedWorkspaceRoot() {
		t.Fatal("fresh config did not choose inert staging")
	}
	t.Setenv("WORKSPACE_DIR", f.Paths().Workspaces)
	if !shouldRunWorkspaceStartupMaintenance(cfg) || resolveWorkspaceRoot(cfg) != f.Paths().Workspaces {
		t.Fatal("expected operator root to authorize startup adoption even without saved consent")
	}
}

func TestResetLifecycleBuilderRecompletesFirstDayAfterIntentionalQuestReset(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(f.Paths().DataDir, "sessions.db"), WALMode: true})
	requireResetNoError(t, err)
	t.Cleanup(func() { requireResetNoError(t, db.Close()) })
	relationships := personalassistant.NewSQLiteStore(db)
	_, err = relationships.CreateState(t.Context(), &personalassistant.State{
		UserID: userprofile.LocalUserID, AssistantID: "fixture-assistant",
		Status: personalassistant.StatusActive, DisplayName: resetfixture.AgentName,
		FirstAssignmentStatus: personalassistant.FirstAssignmentCompleted,
	})
	requireResetNoError(t, err)
	statePath := filepath.Join(f.Paths().DataDir, "app_state.json")
	mgr := onboarding.NewManager(statePath)
	engine := progression.New(mgr, progression.WithQuests(progression.PersonalAssistantQuests()))
	requireResetNoError(t, engine.Reset())
	if len(mgr.GetProgression().CompletedQuests) != 0 {
		t.Fatal("engine reset was not initially empty")
	}
	builder := &ServerBuilder{
		onboardingMgr: onboarding.NewManager(statePath), eventBus: workspace.NewEventBus(4, 4),
		personalAssistantService: personalassistant.NewService(relationships, nil, nil, nil),
	}
	t.Cleanup(builder.eventBus.Shutdown)
	builder.initializeProgression()
	if _, found := builder.onboardingMgr.GetProgression().CompletedQuests[progression.PersonalAssistantFirstDayQuestID]; !found {
		t.Fatal("characterization changed: builder no longer re-completes reset first-day quest")
	}
}

func TestResetLifecycleStartupProfileSeedRestoresRetainedAppIdentity(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(f.Paths().DataDir, "sessions.db"), WALMode: true})
	requireResetNoError(t, err)
	t.Cleanup(func() { requireResetNoError(t, db.Close()) })
	profiles := userprofile.NewSQLiteStore(db)
	mgr := onboarding.NewManager(filepath.Join(f.Paths().DataDir, "app_state.json"))
	mgr.SetUserStore(profiles)
	requireResetNoError(t, mgr.SeedLocalUserProfile(t.Context()))
	profile, err := profiles.Get(t.Context(), userprofile.LocalUserID)
	requireResetNoError(t, err)
	if profile.DisplayName != "Fixture User" {
		t.Fatal("expected old app-state name to seed a fresh database profile")
	}
}

func TestResetLifecycleRegistryDeletionReadoptsExternalMCPUnlessDisabled(t *testing.T) {
	f := resetfixture.New(t)
	const external = "[mcp_servers.fixture_external]\ncommand = \"fixture-only-command\"\n"
	requireResetNoError(t, f.WriteFile("home/.codex/config.toml", []byte(external)))
	for _, disabled := range []string{"false", "false", "true"} {
		t.Setenv(disableExternalMCPImportEnv, disabled)
		requireResetNoError(t, f.WriteFile("cwd/mcp_registry.json", []byte(`{"servers":[]}`)))
		builder := &ServerBuilder{}
		builder.initializeMCP()
		cfg, err := builder.mcpConfigManager.LoadGlobalConfig()
		requireResetNoError(t, err)
		found := false
		for _, item := range cfg.Servers {
			if item.Command != "fixture-only-command" {
				continue
			}
			found = true
			status, err := builder.mcpRegistry.GetServerStatus(item.Name)
			requireResetNoError(t, err)
			if status != mcp.StatusStopped {
				t.Fatal("external fixture command was started")
			}
		}
		if found != (disabled == "false") {
			t.Fatal("external import did not follow the startup import policy")
		}
		requireResetNoError(t, os.Remove(filepath.Join(f.Paths().WorkDir, "mcp_registry.json")))
	}
	retained, err := os.ReadFile(filepath.Join(f.Paths().Home, ".codex", "config.toml"))
	requireResetNoError(t, err)
	if string(retained) != external {
		t.Fatal("MCP import changed the external source config")
	}
}
