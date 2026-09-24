package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// TestStartFreshRemovesAPluginInstalledThroughTheRealHostPath is the Start
// Fresh counterpart to the selective journey: the plugin is installed through
// the production plugin manager (registering its MCP server; its skill stays in
// the plugin's own folder), a Workspace Surface state
// record is written for it, and Start Fresh then completes through a real
// relaunch without the retired manual-uninstall blocker.
//
// The plugin bundle is a domain-neutral local directory written by this test.
// Nothing is downloaded, cloned, or executed, and every root is fixture-owned.
func TestStartFreshRemovesAPluginInstalledThroughTheRealHostPath(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("native reset admission lease unavailable")
	}
	f := resetfixture.NewSeeded(t)
	paths := f.Paths()
	t.Chdir(paths.DataDir)
	t.Setenv("ORI_DATA_DIR", paths.DataDir)
	t.Setenv("ORI_TEMPLATES_DIR", filepath.Join(paths.DataDir, "templates"))
	t.Setenv("WORKFLOW_TEMPLATES_DIR", filepath.Join(paths.DataDir, "workflow_templates"))

	// A synthetic plugin bundle installed from a local directory. Its source
	// folder is linked, not a managed clone, so its bytes must survive.
	source := filepath.Join(paths.Root, "external", "fresh-host-plugin")
	writeFixtureFile(t, filepath.Join(source, ".claude-plugin", "plugin.json"),
		`{"name":"fresh-host-plugin","version":"1.0.0","description":"synthetic reset fixture"}`)
	writeFixtureFile(t, filepath.Join(source, ".mcp.json"),
		`{"tools":{"command":"never-executed-by-this-test","args":[]}}`)
	writeFixtureFile(t, filepath.Join(source, "skills", "fresh-host-skill", "SKILL.md"),
		"---\nname: fresh-host-skill\ndescription: synthetic reset fixture skill\n---\n")

	pluginsDir := filepath.Join(paths.DataDir, "plugins")
	skillsRoot := f.PersonalSkillsRoot()
	mcpConfig := mcp.NewConfigManager(paths.DataDir)
	pluginHandler := pluginhttp.NewHandler(mcpConfig, mcp.NewRegistry(), pluginsDir)
	installed, err := pluginHandler.Manager().Install(source, plugin.FormatClaude, func(plugin.TrustReport) bool { return true })
	if err != nil {
		t.Fatalf("install the synthetic plugin through the real host path: %v", err)
	}
	if len(installed.Skills) != 1 || len(installed.MCPServers) != 1 {
		t.Fatalf("the real install did not register the expected components: %+v", installed)
	}
	// The skill is read in place from the plugin's folder: nothing is copied
	// into the fixture's ~/.agents/skills.
	if _, err := os.Lstat(filepath.Join(skillsRoot, installed.Skills[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the real install copied the skill into ~/.agents/skills: %v", err)
	}
	// A plugin-backed Workspace Surface state record, written through the same
	// store the live surface runtime uses.
	state := workspacesurface.NewStateStore(filepath.Join(pluginsDir, "state"))
	if _, err := state.Set(installed.Name, resetfixture.WorkspaceID, "display", 1, "0", json.RawMessage(`{"panel":"open"}`)); err != nil {
		t.Fatalf("write plugin-backed surface state: %v", err)
	}
	sourceBefore := hashTree(t, source)
	userSkillBefore := hashTree(t, filepath.Join(skillsRoot, resetfixture.UserAuthoredSkill))

	lease, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	gate := lease.WorkGate()
	cfg := config.NewManagerWithSecretStore(filepath.Join(paths.DataDir, "settings.json"), f.Secrets())
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetTemplatesRoot(filepath.Join(paths.DataDir, "templates")); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(paths.DataDir, "sessions.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewHybridStoreWithDB(db, 10)
	session.SetAdmissionGate(sessions, gate)
	agents, err := store.NewFileStore(filepath.Join(paths.DataDir, "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	folders, err := workspace.NewFileStore(paths.Workspaces)
	if err != nil {
		t.Fatal(err)
	}
	uploads, err := sessionfiles.NewStore(filepath.Join(paths.DataDir, "session_files"))
	if err != nil {
		t.Fatal(err)
	}
	freshTargets := []settingsreset.FreshTarget{
		{Category: settingsreset.CategoryIdentityProgress, Kind: "first_run_state", Path: filepath.Join(paths.DataDir, "app_state.json")},
		{Category: settingsreset.CategoryAppConfiguration, Kind: "model_categories", Path: filepath.Join(paths.DataDir, "model_categories.json")},
		{Category: settingsreset.CategoryAppConfiguration, Kind: "location_zones", Path: filepath.Join(paths.DataDir, "locations.json")},
		{Category: settingsreset.CategoryIntegrations, Kind: "connection_metadata", Path: filepath.Join(paths.DataDir, "connections", "google.json")},
		{Category: settingsreset.CategoryIntegrations, Kind: "connection_consent", Path: filepath.Join(paths.DataDir, "connections", "consent.json")},
		{Category: settingsreset.CategoryIntegrations, Kind: "mcp_registry", Path: mcpConfig.PersistencePath()},
		{Category: settingsreset.CategoryIntegrations, Kind: "mcp_search_sources", Path: filepath.Join(paths.DataDir, "mcp_search_sources.json")},
		{Category: settingsreset.CategoryIntegrations, Kind: "mcp_search_cache", Path: filepath.Join(paths.DataDir, "mcp_search_cache.json")},
		{Category: settingsreset.CategoryTemplates, Kind: "project_templates", Path: filepath.Join(paths.DataDir, "templates")},
		{Category: settingsreset.CategoryTemplates, Kind: "post_reset_project_templates", Path: config.ResolveTemplatesRoot("")},
		{Category: settingsreset.CategoryTemplates, Kind: "workflow_templates", Path: filepath.Join(paths.DataDir, "workflow_templates")},
		{Category: settingsreset.CategoryActivity, Kind: "usage_records", Path: filepath.Join(paths.DataDir, "usage_data")},
		{Category: settingsreset.CategoryActivity, Kind: "activity_logs", Path: filepath.Join(paths.DataDir, "activity_logs")},
		{Category: settingsreset.CategoryActivity, Kind: "cli_event_logs", Path: filepath.Join(paths.DataDir, "cli_agent_tasks")},
		{Category: settingsreset.CategoryRuntimeCache, Kind: "cli_mcp_configs", Path: filepath.Join(paths.DataDir, "cli-mcp")},
	}
	// The managed plugin roots come from the live manager, exactly as the
	// production builder supplies them.
	for kind, path := range pluginHandler.Manager().FreshPersistencePaths() {
		freshTargets = append(freshTargets, settingsreset.FreshTarget{
			Category: settingsreset.CategoryIntegrations, Kind: kind, Path: path,
		})
	}
	planner := settingsreset.NewPlanner(func() settingsreset.Owners {
		return settingsreset.Owners{
			DataDir: paths.DataDir, Config: cfg, Agents: agents,
			Setup:    onboarding.NewManager(filepath.Join(paths.DataDir, "app_state.json")),
			Database: db, Uploads: uploads, Workspaces: folders,
			Allowlist:    workspace.NewAllowlist(filepath.Join(paths.DataDir, workspace.DefaultAllowlistFilename)),
			Vaults:       vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: paths.DataDir, ManagedVaultRoot: paths.Vaults}),
			PluginPaths:  resetPluginPaths(paths.DataDir),
			FreshTargets: freshTargets,
			CheckLifecycle: func(context.Context) []settingsreset.Blocker {
				if gate.Snapshot().Known {
					return nil
				}
				return []settingsreset.Blocker{{Code: "lifecycle_unavailable", Message: "fixture lifecycle unavailable"}}
			},
		}
	})
	preview, err := planner.Create(t.Context(), settingsreset.IntentStartFresh, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, blocker := range preview.Blockers {
		if blocker.Code == "external_plugin_skills_present" {
			t.Fatal("a plugin installed through the real host path still triggers the retired blocker")
		}
	}
	if len(preview.Blockers) != 0 {
		t.Fatalf("Start Fresh blocked: %+v", preview.Blockers)
	}

	srv := &Server{resetWork: gate, resetLease: lease, Storage: &StorageSystemFacade{SessionStore: sessions}, workspaceFileStore: folders}
	operation, err := settingsreset.NewCoordinator(lease, planner, newServerResetLifecycle(srv)).Stage(t.Context(), settingsreset.ExecuteRequest{
		PreviewID: preview.ID, RequestID: "start-fresh-real-plugin", Confirmation: "RESET",
	})
	if err != nil || operation.State != settingsreset.StateAwaitingRestart {
		t.Fatalf("stage Start Fresh = %+v, %v", operation, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	// Relaunch: a new lease on the same installation, recovered through the same
	// pre-store entry point the production host calls, before any application
	// store is constructed.
	//
	// The one substitution is the credential backend. Start Fresh always includes
	// the Settings category, and settingsreset.BeforeStores opens the real native
	// secret store; the fixture contract is to inject its memory store instead, so
	// no Keychain entry is read or deleted. Everything else — lease handoff,
	// scope revalidation, ordering, and verification — is the production path.
	// The selective plugin journey exercises BeforeStores itself.
	relaunched, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relaunched.Close() })
	if err := settingsreset.RecoverBeforeStores(t.Context(), relaunched, settingsreset.RecoveryOptions{
		DataDir: paths.DataDir, SecretStore: f.Secrets(),
	}); err != nil {
		blocked, statusErr := settingsreset.NewCoordinator(relaunched, nil, nil).Status(t.Context(), operation.ID)
		t.Fatalf("pre-store Start Fresh recovery: %v (state %s, %v)", err, blocked.State, statusErr)
	}
	final, err := settingsreset.NewCoordinator(relaunched, nil, nil).Status(t.Context(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !final.VerifiedComplete() || len(final.CompletedCategories()) != 9 {
		t.Fatalf("Start Fresh did not verify complete: %+v", final)
	}
	outcomes := map[string]settingsreset.Outcome{}
	for _, result := range final.Results {
		for _, item := range result.Items {
			outcomes[item.Name] = item.Outcome
		}
	}
	if outcomes[installed.Name] != settingsreset.OutcomeCompleted {
		t.Fatalf("Start Fresh did not report the plugin's own outcome: %v", outcomes)
	}

	// Exact plugin cleanup ran; the broader policy removed the managed roots
	// and marketplaces.
	for _, gone := range []string{
		filepath.Join(pluginsDir, "state", plugin.ResetStateNamespace(installed.Name)),
		filepath.Join(pluginsDir, "installed.json"),
		filepath.Join(pluginsDir, "marketplaces.json"),
		mcpConfig.PersistencePath(),
	} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Start Fresh left %s: %v", gone, err)
		}
	}
	// The linked source folder and the user's own skill are untouched.
	if !equalTrees(sourceBefore, hashTree(t, source)) {
		t.Error("Start Fresh modified the linked plugin source folder")
	}
	if !equalTrees(userSkillBefore, hashTree(t, filepath.Join(skillsRoot, resetfixture.UserAuthoredSkill))) {
		t.Error("Start Fresh modified a personal skill Ori did not install")
	}
	if _, err := os.Lstat(skillsRoot); err != nil {
		t.Errorf("Start Fresh removed the shared personal skills root: %v", err)
	}
	f.AssertPreserved(t)
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
