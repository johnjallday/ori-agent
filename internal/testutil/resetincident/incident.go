// Package resetincident builds the reported reset incident with production
// code and exported APIs only, for host-level tests: a Start Fresh accepted in a
// disposable installation, the retained workspace emptied outside Ori, and the
// first recovery launch blocked at revision 3 and holding the installation lease.
//
// Nothing here copies a user's installation, receipt, workspace or credentials.
// Like internal/testutil/resetfixture it owns a newly created directory, uses
// in-memory secrets and never opens a listener or a real credential store.
package resetincident

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Incident is the blocked installation. Lease is the recovery process's lease,
// held open for the test's lifetime, as a host serving its recovery page does.
type Incident struct {
	Fixture     *resetfixture.Fixture
	DataDir     string
	OperationID string
	Lease       *resetstate.Lease
}

type lifecycle struct{ drain func(context.Context) error }

func (lifecycle) TryFence(context.Context) error { return nil }
func (l lifecycle) Drain(ctx context.Context) error {
	if l.drain == nil {
		return nil
	}
	return l.drain(ctx)
}

// New reproduces the incident. It skips when the platform has no installation
// lease. Callers get the lease still held; nothing else is running.
func New(t *testing.T) *Incident {
	t.Helper()
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	f := resetfixture.NewSeeded(t)
	paths := f.Paths()
	t.Chdir(paths.DataDir)
	root := paths.DataDir

	cfg := config.NewManagerWithSecretStore(filepath.Join(root, "settings.json"), f.Secrets())
	must(t, cfg.Load())
	must(t, cfg.SetTemplatesRoot(filepath.Join(root, "templates")))
	must(t, cfg.Save())
	t.Setenv("ORI_TEMPLATES_DIR", filepath.Join(root, "templates"))
	t.Setenv("WORKFLOW_TEMPLATES_DIR", filepath.Join(root, "workflow_templates"))
	agents := f.OpenAgents(t)
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "sessions.db"), WALMode: true})
	must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	folders, err := workspace.NewFileStore(paths.Workspaces)
	must(t, err)
	t.Cleanup(func() { _ = folders.Close() })
	uploads, err := sessionfiles.NewStore(filepath.Join(root, "session_files"))
	must(t, err)
	owners := &settingsreset.Owners{
		DataDir: root, Config: cfg, Agents: agents, Database: db, Uploads: uploads,
		Setup: onboarding.NewManager(filepath.Join(root, "app_state.json")), Workspaces: folders,
		Allowlist:      workspace.NewAllowlist(filepath.Join(root, workspace.DefaultAllowlistFilename)),
		Vaults:         vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: root, ManagedVaultRoot: paths.Vaults}),
		PluginPaths:    f.PluginResetPaths(),
		FreshTargets:   freshTargets(root),
		CheckLifecycle: func(context.Context) []settingsreset.Blocker { return nil },
	}
	planner := settingsreset.NewPlanner(func() settingsreset.Owners { return *owners })

	// Review and accept Start Fresh in the accepting process, then "quit" it.
	accepting, err := resetstate.Acquire(root)
	must(t, err)
	preview, err := planner.Create(t.Context(), settingsreset.IntentStartFresh, nil)
	must(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("Start Fresh review blockers: %+v", preview.Blockers)
	}
	request := settingsreset.ExecuteRequest{PreviewID: preview.ID, RequestID: "incident-request", Confirmation: "RESET"}
	if preview.AgentsFolder != nil {
		request.ConfirmAgentsFolder = preview.AgentsFolder.Path
	}
	staged, err := settingsreset.NewCoordinator(accepting, planner, lifecycle{drain: func(context.Context) error {
		return errors.Join(folders.Close(), db.Close())
	}}).Stage(t.Context(), request)
	must(t, err)
	if staged.State != settingsreset.StateAwaitingRestart {
		t.Fatalf("staged Start Fresh = %s", staged.State)
	}
	must(t, accepting.Close())

	// The person empties the folder they chose to keep, outside Ori.
	f.EmptyRetainedFolder(t, "projects", true)

	// The next process starts recovery and blocks.
	recovery, err := resetstate.Acquire(root)
	must(t, err)
	t.Cleanup(func() { _ = recovery.Close() })
	err = settingsreset.RecoverBeforeStores(t.Context(), recovery, settingsreset.RecoveryOptions{DataDir: root, SecretStore: f.Secrets()})
	if !errors.Is(err, settingsreset.ErrRecoveryIncomplete) {
		t.Fatalf("recovery after the workspace was emptied = %v, want ErrRecoveryIncomplete", err)
	}
	return &Incident{Fixture: f, DataDir: root, OperationID: staged.ID, Lease: recovery}
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// freshTargets mirrors the owner-resolved Start Fresh targets of a default
// installation under root.
func freshTargets(root string) []settingsreset.FreshTarget {
	plugins := filepath.Join(root, "plugins")
	at := func(category settingsreset.CategoryID, kind, path string) settingsreset.FreshTarget {
		return settingsreset.FreshTarget{Category: category, Kind: kind, Path: path}
	}
	return []settingsreset.FreshTarget{
		at(settingsreset.CategoryIdentityProgress, "first_run_state", filepath.Join(root, "app_state.json")),
		at(settingsreset.CategoryAppConfiguration, "model_categories", filepath.Join(root, "model_categories.json")),
		at(settingsreset.CategoryAppConfiguration, "location_zones", filepath.Join(root, "locations.json")),
		at(settingsreset.CategoryIntegrations, "connection_metadata", filepath.Join(root, "connections", "google.json")),
		at(settingsreset.CategoryIntegrations, "connection_consent", filepath.Join(root, "connections", "consent.json")),
		at(settingsreset.CategoryIntegrations, "mcp_registry", filepath.Join(root, "mcp_registry.json")),
		at(settingsreset.CategoryIntegrations, "mcp_search_sources", filepath.Join(root, "mcp_search_sources.json")),
		at(settingsreset.CategoryIntegrations, "mcp_search_cache", filepath.Join(root, "mcp_search_cache.json")),
		at(settingsreset.CategoryIntegrations, "plugin_registry", filepath.Join(plugins, "installed.json")),
		at(settingsreset.CategoryIntegrations, "plugin_marketplaces", filepath.Join(plugins, "marketplaces.json")),
		at(settingsreset.CategoryIntegrations, "plugin_clones", filepath.Join(plugins, "src")),
		at(settingsreset.CategoryIntegrations, "plugin_state", filepath.Join(plugins, "state")),
		at(settingsreset.CategoryIntegrations, "plugin_artifacts", filepath.Join(plugins, "artifacts")),
		at(settingsreset.CategoryIntegrations, "plugin_preview", filepath.Join(plugins, "preview")),
		at(settingsreset.CategoryTemplates, "project_templates", filepath.Join(root, "templates")),
		at(settingsreset.CategoryTemplates, "post_reset_project_templates", filepath.Join(root, "templates")),
		at(settingsreset.CategoryTemplates, "workflow_templates", filepath.Join(root, "workflow_templates")),
		at(settingsreset.CategoryActivity, "usage_records", filepath.Join(root, "usage_data")),
		at(settingsreset.CategoryActivity, "activity_logs", filepath.Join(root, "activity_logs")),
		at(settingsreset.CategoryActivity, "cli_event_logs", filepath.Join(root, "cli_agent_tasks")),
		at(settingsreset.CategoryRuntimeCache, "cli_mcp_configs", filepath.Join(root, "cli-mcp")),
	}
}
