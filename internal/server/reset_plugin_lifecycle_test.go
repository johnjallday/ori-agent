package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestProductionPluginResetJourneyThroughHTTPAndRelaunch is the real end-to-end
// path for the Installed plugins category: the production HTTP preview and
// confirmation endpoints, the production fence/drain lifecycle, a genuine
// process-boundary relaunch through settingsreset.BeforeStores, and the durable
// result read back through the production operation endpoint.
//
// The installation, HOME, personal skills root, plugin registry, and MCP
// registry are all fixture-owned temporary directories. No real plugin is
// resolved, downloaded, or executed, and nothing under the developer's own HOME
// is read or written.
func TestProductionPluginResetJourneyThroughHTTPAndRelaunch(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("native reset admission lease unavailable")
	}
	f := resetfixture.NewSeeded(t)
	paths := f.Paths()
	t.Chdir(paths.DataDir)
	t.Setenv("ORI_DATA_DIR", paths.DataDir)
	pluginPaths := f.PluginResetPaths()
	f.SeedPlugins(t,
		resetfixture.PluginSeed{
			Name: "demo-managed", Version: "1.4.0", Enabled: true, Managed: true,
			MCPServers: []string{"tools"}, Skills: []string{"demo-managed-skill"},
			Surfaces: true, Artifacts: true,
		},
		resetfixture.PluginSeed{Name: "demo-linked", Version: "0.2.0", Skills: []string{"demo-linked-skill"}},
	)
	linkedSource := filepath.Join(paths.Root, "external", "plugin-demo-linked")
	linkedBefore := hashTree(t, linkedSource)
	marketplacesBefore := hashTree(t, pluginPaths.MarketplacesPath())
	userSkillBefore := hashTree(t, filepath.Join(f.PersonalSkillsRoot(), resetfixture.UserAuthoredSkill))

	lease, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	gate := lease.WorkGate()
	cfg := config.NewManagerWithSecretStore(filepath.Join(paths.DataDir, "settings.json"), f.Secrets())
	if err := cfg.Load(); err != nil {
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
	planner := settingsreset.NewPlanner(func() settingsreset.Owners {
		return settingsreset.Owners{
			DataDir: paths.DataDir, Config: cfg, Agents: agents,
			Setup:    onboarding.NewManager(filepath.Join(paths.DataDir, "app_state.json")),
			Database: db, Uploads: uploads, Workspaces: folders,
			Allowlist:   workspace.NewAllowlist(filepath.Join(paths.DataDir, workspace.DefaultAllowlistFilename)),
			Vaults:      vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: paths.DataDir, ManagedVaultRoot: paths.Vaults}),
			PluginPaths: pluginPaths,
			CheckLifecycle: func(context.Context) []settingsreset.Blocker {
				if gate.Snapshot().Known {
					return nil
				}
				return []settingsreset.Blocker{{Code: "lifecycle_unavailable", Message: "fixture lifecycle unavailable"}}
			},
		}
	})
	srv := &Server{resetWork: gate, resetLease: lease, Storage: &StorageSystemFacade{SessionStore: sessions}, workspaceFileStore: folders}
	handler := settingshttp.NewResetHandler(nil, agents, paths.DataDir)
	handler.SetPreviewPlanner(planner)
	handler.SetCoordinator(settingsreset.NewCoordinator(lease, planner, newServerResetLifecycle(srv)))
	server := f.StartHTTP(t, resetRoutes(handler))

	// 1. Review the authoritative scope over the real preview endpoint.
	preview := decodeResetJSON[settingsreset.Preview](t, server, http.MethodGet,
		"/api/reset/preview?intent=selected_data&category=installed_plugins", nil, http.StatusOK)
	if len(preview.Blockers) != 0 {
		t.Fatalf("preview blocked: %+v", preview.Blockers)
	}
	if len(preview.Categories) != 1 || preview.Categories[0].ID != settingsreset.CategoryInstalledPlugins {
		t.Fatalf("preview scope = %+v", preview.Categories)
	}
	names := make([]string, 0, len(preview.Categories[0].Items))
	for _, item := range preview.Categories[0].Items {
		names = append(names, item.Name)
	}
	if !slices.Equal(names, []string{"demo-linked", "demo-managed"}) {
		t.Fatalf("preview did not name the exact inventory: %v", names)
	}
	if preview.Restart.Mode != settingsreset.RestartProcessRelaunch {
		t.Fatal("plugin reset did not require a full process relaunch")
	}

	// 2. Confirm over the real execution endpoint.
	body := strings.NewReader(`{"preview_id":"` + preview.ID + `","request_id":"plugin-demo-request","confirmation":"RESET"}`)
	staged := decodeResetJSON[settingshttp.ResetResponse](t, server, http.MethodPost, "/api/reset", body, http.StatusAccepted)
	if staged.Operation == nil || staged.Operation.State != settingsreset.StateAwaitingRestart {
		t.Fatalf("confirmation did not stage a relaunch: %+v", staged)
	}
	if !staged.RequiresRestart || staged.Success {
		t.Fatal("confirmation claimed completion before the relaunch")
	}
	// Nothing is deleted by the accepting process.
	for _, present := range []string{
		filepath.Join(pluginPaths.CloneDir, "demo-managed-repo"),
		filepath.Join(f.PersonalSkillsRoot(), "demo-managed-skill"),
		pluginPaths.RegistryPath(),
	} {
		if _, err := os.Lstat(present); err != nil {
			t.Errorf("the accepting process already removed %s: %v", present, err)
		}
	}

	// 3. Stop the process and relaunch through the production pre-store path.
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := settingsreset.BeforeStores(t.Context(), paths.DataDir)
	if err != nil {
		t.Fatalf("pre-store recovery: %v", err)
	}
	if recovered == nil || recovered.WorkGate().Snapshot().Fenced {
		t.Fatal("pre-store recovery did not authorize normal runtime construction")
	}
	if err := recovered.Close(); !errors.Is(err, resetstate.ErrPinned) {
		t.Fatalf("verified host did not retain process ownership: %v", err)
	}

	// 4. Read the durable result back through the production operation endpoint.
	relaunched := settingshttp.NewResetHandler(nil, agents, paths.DataDir)
	relaunched.SetCoordinator(settingsreset.NewCoordinator(recovered, nil, nil))
	relaunchedServer := f.StartHTTP(t, resetRoutes(relaunched))
	final := decodeResetJSON[settingshttp.ResetResponse](t, relaunchedServer, http.MethodGet,
		"/api/reset/operations/"+staged.Operation.ID, nil, http.StatusOK)
	if !final.Success || final.Operation == nil || final.Operation.State != settingsreset.StateCompleted {
		t.Fatalf("recovered operation did not verify complete: %+v", final)
	}
	outcomes := map[string]settingsreset.Outcome{}
	for _, result := range final.Operation.Results {
		for _, item := range result.Items {
			outcomes[item.Name] = item.Outcome
		}
	}
	if outcomes["demo-managed"] != settingsreset.OutcomeCompleted || outcomes["demo-linked"] != settingsreset.OutcomeCompleted {
		t.Fatalf("per-plugin outcomes were not reported: %v", outcomes)
	}

	// 5. Owned components are gone; every preserved sentinel is unchanged.
	for _, gone := range []string{
		filepath.Join(pluginPaths.CloneDir, "demo-managed-repo"),
		filepath.Join(pluginPaths.ArtifactsRoot(), "demo-managed"),
		filepath.Join(pluginPaths.StateRoot(), plugin.ResetStateNamespace("demo-managed")),
		filepath.Join(pluginPaths.StateRoot(), "demo-managed"),
		filepath.Join(f.PersonalSkillsRoot(), "demo-managed-skill"),
		filepath.Join(f.PersonalSkillsRoot(), "demo-linked-skill"),
		pluginPaths.PreviewRoot(),
	} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("reviewed plugin component survived: %s (%v)", gone, err)
		}
	}
	empty, err := plugin.ResetRegistryEmpty(pluginPaths)
	if err != nil || !empty {
		t.Fatalf("installed records remain: %v %v", empty, err)
	}
	if !equalTrees(linkedBefore, hashTree(t, linkedSource)) {
		t.Error("the linked plugin source was modified")
	}
	if !equalTrees(marketplacesBefore, hashTree(t, pluginPaths.MarketplacesPath())) {
		t.Error("selected plugin reset removed marketplace registrations")
	}
	if !equalTrees(userSkillBefore, hashTree(t, filepath.Join(f.PersonalSkillsRoot(), resetfixture.UserAuthoredSkill))) {
		t.Error("a personal skill Ori did not install was modified")
	}
	registry, err := os.ReadFile(pluginPaths.MCPRegistry) // #nosec G304 -- fixture-owned temporary tree
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(registry), "unrelated-user-server") || strings.Contains(string(registry), "demo-managed/tools") {
		t.Errorf("MCP registrations were not pruned exactly: %s", registry)
	}
	f.AssertPreserved(t)
}

func resetRoutes(handler *settingshttp.ResetHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/reset/preview", handler.GetResetPreview)
	mux.HandleFunc("/api/reset", handler.HandleReset)
	mux.HandleFunc("/api/reset/operations/", handler.GetOperation)
	return mux
}

func decodeResetJSON[T any](t *testing.T, server *resetfixture.HTTPServer, method, path string, body io.Reader, want int) T {
	t.Helper()
	response, err := server.Do(t.Context(), method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s status = %d, want %d: %s", method, path, response.StatusCode, want, payload)
	}
	var decoded T
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode %s %s: %v: %s", method, path, err, payload)
	}
	return decoded
}

func hashTree(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	result := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path) // #nosec G304 -- fixture-owned temporary tree
		if err != nil {
			return err
		}
		result[relative] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatalf("hash %s: %v", root, err)
	}
	return result
}

func equalTrees(before, after map[string][sha256.Size]byte) bool {
	if len(before) != len(after) {
		return false
	}
	for path, digest := range before {
		if after[path] != digest {
			return false
		}
	}
	return true
}
