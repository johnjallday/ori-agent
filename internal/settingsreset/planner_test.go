package settingsreset

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func previewFixture(t *testing.T) (*resetfixture.Fixture, *Owners, *Planner) {
	t.Helper()
	f := resetfixture.NewSeeded(t)
	paths := f.Paths()
	t.Chdir(paths.DataDir) // supported host activation; tests can deliberately split it again
	cfg := config.NewManagerWithSecretStore(filepath.Join(paths.DataDir, "settings.json"), f.Secrets())
	mustPreview(t, cfg.Load())
	agents, err := store.NewFileStore(filepath.Join(paths.DataDir, "agents.json"), types.Settings{})
	mustPreview(t, err)
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(paths.DataDir, "sessions.db"), WALMode: true})
	mustPreview(t, err)
	t.Cleanup(func() { mustPreview(t, db.Close()) })
	folders, err := workspace.NewFileStore(paths.Workspaces)
	mustPreview(t, err)
	t.Cleanup(func() { mustPreview(t, folders.Close()) })
	uploads, err := sessionfiles.NewStore(filepath.Join(paths.DataDir, "session_files"))
	mustPreview(t, err)
	owners := &Owners{
		DataDir: paths.DataDir, Config: cfg, Agents: agents, Database: db, Uploads: uploads,
		Setup: onboarding.NewManager(filepath.Join(paths.DataDir, "app_state.json")), Workspaces: folders,
		Allowlist: workspace.NewAllowlist(filepath.Join(paths.DataDir, workspace.DefaultAllowlistFilename)),
		Vaults:    vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: paths.DataDir, ManagedVaultRoot: paths.Vaults}),
		// Only a read-only fixture readiness seam; no apply is exposed by Planner.
		CheckLifecycle: func(context.Context) []Blocker { return nil },
	}
	return f, owners, NewPlanner(func() Owners { return *owners })
}

func fixtureFreshTargets(root string) []FreshTarget {
	plugins := filepath.Join(root, "plugins")
	return []FreshTarget{
		{CategoryIdentityProgress, "first_run_state", filepath.Join(root, "app_state.json"), "", nil},
		{CategoryAppConfiguration, "model_categories", filepath.Join(root, "model_categories.json"), "", nil},
		{CategoryAppConfiguration, "location_zones", filepath.Join(root, "locations.json"), "", nil},
		{CategoryIntegrations, "connection_metadata", filepath.Join(root, "connections", "google.json"), "", nil},
		{CategoryIntegrations, "connection_consent", filepath.Join(root, "connections", "consent.json"), "", nil},
		{CategoryIntegrations, "mcp_registry", filepath.Join(root, "mcp_registry.json"), "", nil},
		{CategoryIntegrations, "mcp_search_sources", filepath.Join(root, "mcp_search_sources.json"), "", nil},
		{CategoryIntegrations, "mcp_search_cache", filepath.Join(root, "mcp_search_cache.json"), "", nil},
		{CategoryIntegrations, "plugin_registry", filepath.Join(plugins, "installed.json"), "", nil},
		{CategoryIntegrations, "plugin_marketplaces", filepath.Join(plugins, "marketplaces.json"), "", nil},
		{CategoryIntegrations, "plugin_clones", filepath.Join(plugins, "src"), "", nil},
		{CategoryIntegrations, "plugin_state", filepath.Join(plugins, "state"), "", nil},
		{CategoryIntegrations, "plugin_artifacts", filepath.Join(plugins, "artifacts"), "", nil},
		{CategoryIntegrations, "plugin_preview", filepath.Join(plugins, "preview"), "", nil},
		{CategoryTemplates, "project_templates", filepath.Join(root, "templates"), "", nil},
		{CategoryTemplates, "post_reset_project_templates", filepath.Join(root, "templates"), "", nil},
		{CategoryTemplates, "workflow_templates", filepath.Join(root, "workflow_templates"), "", nil},
		{CategoryActivity, "usage_records", filepath.Join(root, "usage_data"), "", nil},
		{CategoryActivity, "activity_logs", filepath.Join(root, "activity_logs"), "", nil},
		{CategoryActivity, "cli_event_logs", filepath.Join(root, "cli_agent_tasks"), "", nil},
		{CategoryRuntimeCache, "cli_mcp_configs", filepath.Join(root, "cli-mcp"), "", nil},
	}
}

func mustPreview(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func hasBlocker(preview Preview, code string) bool {
	return slices.ContainsFunc(preview.Blockers, func(item Blocker) bool { return item.Code == code })
}

func treeBytes(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	files := make(map[string][sha256.Size]byte)
	mustPreview(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.HasSuffix(path, "-shm") {
			return nil
		}
		// SQLite SHM read-lock coordination is not application state. All actual
		// DB/WAL, settings, manifests and retained file bytes must stay unchanged.
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = sha256.Sum256(data)
		return nil
	}))
	return files
}

func TestStartFreshIsOneServerRecognizedIntentNotASelectionShortcut(t *testing.T) {
	f, owners, planner := previewFixture(t)
	root := f.Paths().DataDir
	mustPreview(t, owners.Config.SetTemplatesRoot(filepath.Join(root, "templates")))
	mustPreview(t, owners.Config.Save())
	t.Setenv("ORI_TEMPLATES_DIR", filepath.Join(root, "templates"))
	t.Setenv("WORKFLOW_TEMPLATES_DIR", filepath.Join(root, "workflow_templates"))
	owners.FreshTargets = fixtureFreshTargets(root)

	preview, err := planner.Create(t.Context(), IntentStartFresh, nil)
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("Start Fresh blockers = %+v", preview.Blockers)
	}
	selected, err := Selection(IntentStartFresh, nil)
	mustPreview(t, err)
	if !reflect.DeepEqual(preview.Selected, selected) || len(preview.Categories) != len(selected) {
		t.Fatalf("Start Fresh categories = %v", preview.Selected)
	}
	if _, err := planner.Create(t.Context(), IntentStartFresh, []CategoryID{CategorySettings, CategoryAgents, CategoryAppRecords, CategorySetupSteps}); !errors.Is(err, ErrInvalidSelection) {
		t.Fatal("selecting every narrow category became Start Fresh")
	}
}

func TestStartFreshBlocksExternalTemplateRootsInsteadOfBroadeningDeletion(t *testing.T) {
	f, owners, planner := previewFixture(t)
	owners.FreshTargets = fixtureFreshTargets(f.Paths().DataDir)
	for i := range owners.FreshTargets {
		if owners.FreshTargets[i].Kind == "project_templates" || owners.FreshTargets[i].Kind == "post_reset_project_templates" {
			owners.FreshTargets[i].Path = f.Paths().Templates
		}
		if owners.FreshTargets[i].Kind == "workflow_templates" {
			owners.FreshTargets[i].Path = filepath.Join(f.Paths().Root, "workflow-templates")
		}
	}
	preview, err := planner.Create(t.Context(), IntentStartFresh, nil)
	mustPreview(t, err)
	if !hasBlocker(preview, "target_outside_installation") {
		t.Fatalf("external template roots were not blocked: %+v", preview.Blockers)
	}
	f.AssertPreserved(t)
}

func TestPreviewRejectsInvalidSelectionBeforeReadingOwners(t *testing.T) {
	calls := 0
	planner := NewPlanner(func() Owners { calls++; return Owners{} })
	for _, item := range []struct {
		intent     Intent
		categories []CategoryID
	}{
		{IntentSelectedData, nil}, {IntentStartFresh, []CategoryID{CategorySettings}},
		{IntentSelectedData, []CategoryID{CategorySettings, CategorySettings}},
		{IntentSelectedData, []CategoryID{"/untrusted/path"}}, {"unknown", []CategoryID{CategoryAgents}},
	} {
		if _, err := planner.Create(t.Context(), item.intent, item.categories); !errors.Is(err, ErrInvalidSelection) {
			t.Fatal("accepted invalid selection:", err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input inspected runtime state")
	}
}

func TestPreviewReadsRealCountsAndPreservesEveryApplicationFile(t *testing.T) {
	f, owners, planner := previewFixture(t)
	before := treeBytes(t, f.Paths().Root)
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAppRecords})
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %+v", preview.Blockers)
	}
	if preview.ID == "" || preview.ScopeDigest == "" || preview.Restart.Mode != RestartProcessRelaunch {
		t.Fatal("missing confirmation/restart contract")
	}
	facts := preview.Categories[0].Facts
	for _, expected := range []struct {
		name  string
		count int64
	}{{"sessions", 1}, {"messages", 1}, {"vaults", 0}, {"workspace_plans", 0}} {
		index := slices.IndexFunc(facts, func(fact CountFact) bool { return fact.Name == expected.name })
		if index < 0 || facts[index].Count == nil || *facts[index].Count != expected.count {
			t.Fatal("incorrect authoritative count:", expected.name)
		}
	}
	if !strings.Contains(preview.Categories[0].Description, "profiles/HQ") || !strings.Contains(preview.Categories[0].Description, "vault registrations") {
		t.Fatal("shared database collateral scope hidden")
	}
	if !slices.ContainsFunc(preview.Categories[0].Retained, func(item Location) bool { return item.DisplayPath == owners.Workspaces.BasePath() }) {
		t.Fatal("retained workspace root omitted")
	}
	if !reflect.DeepEqual(before, treeBytes(t, f.Paths().Root)) {
		t.Fatal("read-only preview changed application files")
	}
}

func TestPreviewUsesOwnerPathInsteadOfDataDirectoryGuess(t *testing.T) {
	f, owners, planner := previewFixture(t)
	actual := filepath.Join(f.Paths().WorkDir, "custom-settings.json")
	mustPreview(t, f.WriteFile("cwd/custom-settings.json", []byte(`{}`)))
	owners.Config = config.NewManagerWithSecretStore(actual, f.Secrets())
	mustPreview(t, owners.Config.Load())
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySettings})
	mustPreview(t, err)
	if preview.Categories[0].Removed[0].DisplayPath != actual || !hasBlocker(preview, "target_outside_installation") {
		t.Fatal("preview substituted a default or authorized an outside owner")
	}
	fact := slices.IndexFunc(preview.Categories[0].Facts, func(item CountFact) bool {
		return item.Name == "Ori-saved provider/search key slots"
	})
	if fact < 0 || preview.Categories[0].Facts[fact].Count == nil || *preview.Categories[0].Facts[fact].Count != 4 {
		t.Fatal("owned credential presence was hidden or exposed as secret content")
	}
	encoded, err := json.Marshal(preview)
	mustPreview(t, err)
	for _, key := range []vault.SecretKey{vault.SecretKeyOpenAIAPIKey, vault.SecretKeyAnthropicAPIKey, vault.SecretKeyGeminiAPIKey, vault.SecretKeyBraveAPIKey} {
		secret, err := f.Secrets().Get(key)
		mustPreview(t, err)
		if strings.Contains(string(encoded), secret) {
			t.Fatal("preview exposed a credential value")
		}
	}
}

func TestPreviewBlocksSharedRelativeCredentialNamespaceWithoutReadingValues(t *testing.T) {
	f, owners, planner := previewFixture(t)
	owners.Config = config.NewManagerWithSecretStore("settings.json", f.Secrets())
	mustPreview(t, owners.Config.Load())
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySettings})
	mustPreview(t, err)
	if !hasBlocker(preview, "credential_namespace_ambiguous") {
		t.Fatalf("shared credential namespace was authorized: %+v", preview.Blockers)
	}
	index := slices.IndexFunc(preview.Categories[0].Facts, func(item CountFact) bool {
		return item.Name == "Ori-saved provider/search key slots"
	})
	if index < 0 || preview.Categories[0].Facts[index].Count != nil {
		t.Fatal("ambiguous shared credentials were reported as an owned count")
	}
}

func TestPreviewScopeBindingAllowsNewCountsButRejectsNewTargets(t *testing.T) {
	f, owners, planner := previewFixture(t)
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents})
	mustPreview(t, err)
	mustPreview(t, owners.Agents.CreateAgent("Added after count", nil))
	validated, err := planner.Validate(t.Context(), preview.ID)
	mustPreview(t, err)
	if validated.ScopeDigest != preview.ScopeDigest || *validated.Categories[0].Facts[0].Count != 2 {
		t.Fatal("ordinary count changes invalidated scope or were hidden")
	}
	// Mutating the public payload must not rewrite the held confirmation.
	preview.Selected[0] = CategorySettings
	preview.Categories[0].Removed[0].DisplayPath = f.Paths().Vaults
	_, err = planner.Validate(t.Context(), preview.ID)
	mustPreview(t, err)
	other, err := store.NewFileStore(filepath.Join(f.Paths().DataDir, "other", "agents.json"), types.Settings{})
	mustPreview(t, err)
	owners.Agents = other
	if _, err := planner.Validate(t.Context(), preview.ID); !errors.Is(err, ErrScopeChanged) {
		t.Fatal("material owner change reused confirmation:", err)
	}
}

func TestPreviewDetectsSchemaChangeAndUnknownDomain(t *testing.T) {
	_, owners, planner := previewFixture(t)
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAppRecords})
	mustPreview(t, err)
	_, err = owners.Database.ExecContext(t.Context(), `CREATE TABLE unknown_reset_domain (id TEXT)`)
	mustPreview(t, err)
	if _, err := planner.Validate(t.Context(), preview.ID); !errors.Is(err, ErrScopeChanged) {
		t.Fatal("schema change reused confirmation:", err)
	}
	changed, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAppRecords})
	mustPreview(t, err)
	if !hasBlocker(changed, "unclassified_database_domain") {
		t.Fatal("unknown database domain silently omitted")
	}
}

func TestPreviewBlocksUnclassifiedVaultMaterialAndUnreadableDatabase(t *testing.T) {
	_, owners, planner := previewFixture(t)
	_, err := owners.Database.ExecContext(t.Context(), `ALTER TABLE vaults ADD COLUMN unknown_key_copy TEXT`)
	mustPreview(t, err)
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAppRecords})
	mustPreview(t, err)
	if !hasBlocker(preview, "unclassified_vault_column") {
		t.Fatal("unknown vault material was assumed disposable")
	}
	mustPreview(t, owners.Database.Close())
	preview, err = planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAppRecords})
	mustPreview(t, err)
	if !hasBlocker(preview, "database_inspection_unavailable") {
		t.Fatal("closed database inspection was treated as empty")
	}
}

func TestPreviewUnavailableOwnerNeverMeansZero(t *testing.T) {
	_, owners, planner := previewFixture(t)
	owners.Database = nil
	owners.CheckLifecycle = nil
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAppRecords})
	mustPreview(t, err)
	facts := preview.Categories[0].Facts
	index := slices.IndexFunc(facts, func(fact CountFact) bool { return fact.Name == "sessions" })
	if index < 0 || facts[index].Count != nil || !hasBlocker(preview, "database_owner_unavailable") || !hasBlocker(preview, "lifecycle_unavailable") {
		t.Fatal("missing owners were hidden")
	}
	if _, err := planner.Validate(t.Context(), preview.ID); !errors.Is(err, ErrPreviewBlocked) {
		t.Fatal("blocked preview validated:", err)
	}
}

func TestPreviewProtectsCatalogOnlyNestedVaultAndOperatorRoots(t *testing.T) {
	f, owners, planner := previewFixture(t)
	path := filepath.Join(f.Paths().DataDir, "agents", "retained.orivault", "vault.db")
	mustPreview(t, f.WriteFile("data/agents/retained.orivault/vault.db", []byte("opaque retained fixture")))
	_, err := owners.Database.ExecContext(t.Context(), `INSERT INTO vaults (id, name, file_path, created_at, updated_at) VALUES ('retained', 'Fixture', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, path)
	mustPreview(t, err)
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents})
	mustPreview(t, err)
	if !hasBlocker(preview, "retained_path_overlap") {
		t.Fatal("nested catalog-only vault was not protected")
	}
	t.Setenv("WORKSPACE_DIR", f.Paths().Workspaces)
	preview, err = planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAppRecords})
	mustPreview(t, err)
	if !hasBlocker(preview, "operator_workspace_root") {
		t.Fatal("operator forced reattachment was not blocked")
	}
}

func TestPreviewExpiryCapacityAndAllSelectionRemainBounded(t *testing.T) {
	_, _, planner := previewFixture(t)
	clock := time.Now()
	planner.now = func() time.Time { return clock }
	categories := []CategoryID{CategorySetupSteps, CategoryAppRecords, CategoryAgents, CategorySettings}
	preview, err := planner.Create(t.Context(), IntentSelectedData, categories)
	mustPreview(t, err)
	if preview.Intent != IntentSelectedData || len(preview.Selected) != 4 {
		t.Fatal("selecting all escalated into Start Fresh")
	}
	for i := 1; i < maxPreviews; i++ {
		_, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents})
		mustPreview(t, err)
	}
	if _, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents}); !errors.Is(err, ErrPreviewLimit) {
		t.Fatal("confirmation cache was not bounded")
	}
	clock = clock.Add(previewLifetime)
	if _, err := planner.Validate(t.Context(), preview.ID); !errors.Is(err, ErrPreviewExpired) {
		t.Fatal("expired confirmation reused")
	}
	_, err = planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents})
	mustPreview(t, err)
	if len(planner.plans) != 1 {
		t.Fatal("expired previews were not retired")
	}
}
