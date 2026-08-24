package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

func TestTrustedWorkspaceProjectEntryRejectsTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "song")
	if err := os.Mkdir(project, 0o750); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(project, "song.rpp")
	if err := os.WriteFile(entry, []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	ws.ProjectPath = "song"
	ws.SharedData = map[string]any{}
	if err := workspace.SetProjectEntryPath(ws.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	if got := trustedWorkspaceProjectEntry(root, ws); got != entry {
		t.Fatalf("entry = %q", got)
	}
	outside := filepath.Join(t.TempDir(), "outside.rpp")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, entry); err != nil {
		t.Fatal(err)
	}
	if got := trustedWorkspaceProjectEntry(root, ws); got != "" {
		t.Fatalf("symlink entry = %q", got)
	}
}

func TestWireWorkspaceSurfacesRestoresInstalledPluginThroughOnlyGenericRoutes(t *testing.T) {
	root := t.TempDir()
	store, err := workspace.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Surface Demo Workspace"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve repository root")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	t.Setenv("ORI_DATA_DIR", t.TempDir())
	pluginHandler := pluginhttp.NewHandler(nil, nil, t.TempDir(), t.TempDir())
	installed, err := pluginHandler.Manager().Install(
		filepath.Join(repositoryRoot, "examples", "plugins", "workspace-surface-demo"),
		"",
		func(plugin.TrustReport) bool { return true },
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := pluginHandler.Manager().SetEnabled(installed.Name, true); err != nil {
		t.Fatal(err)
	}
	owner := workspace.CapabilityOwner{
		Kind: workspace.CapabilityOwnerPlugin, PluginID: installed.Name, PluginVersion: installed.Version,
	}
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID: "demo-tools", Version: 1, InstalledAt: installed.InstalledAt,
		Source: workspace.InstallSourceInPlace, Owner: &owner,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	capabilityRegistry, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}

	builder := &ServerBuilder{
		workspaceStore: store, workspaceFileStore: store, pluginHandler: pluginHandler,
		workspaceCapabilityRegistry: capabilityRegistry,
	}
	builder.wireWorkspaceSurfaces()
	if builder.workspaceSurfaceHandler == nil || builder.workspaceSurfaceRegistry == nil {
		t.Fatal("workspace surface handler/registry was not wired")
	}
	if surfaces := builder.workspaceSurfaceRegistry.Surfaces(); len(surfaces) != 1 || surfaces[0].Owner.ID != "workspace-surface-demo" {
		t.Fatalf("registered surfaces = %#v", surfaces)
	}

	mux := http.NewServeMux()
	builder.workspaceSurfaceHandler.Register(mux)
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/surfaces", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"key":"plugin:workspace-surface-demo:demo-tools:main"`) {
		t.Fatalf("catalog response = %d %s", response.Code, response.Body.String())
	}

	// A plugin-looking path is not a route. Only the generic broker/catalog
	// patterns above can reach the registered contribution.
	pluginRoute := httptest.NewRecorder()
	mux.ServeHTTP(pluginRoute, httptest.NewRequest(http.MethodGet, "/api/workspace-surface-demo/status", nil))
	if pluginRoute.Code != http.StatusNotFound {
		t.Fatalf("plugin-specific route status = %d, want 404", pluginRoute.Code)
	}
}

func TestWireWorkspaceSurfacesLeavesRegistryEmptyWithoutInstalledPlugin(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	builder := &ServerBuilder{workspaceStore: store, workspaceFileStore: store}
	builder.wireWorkspaceSurfaces()
	if builder.workspaceSurfaceHandler == nil {
		t.Fatal("generic handler should be wired even with no installed surfaces")
	}
	if surfaces := builder.workspaceSurfaceRegistry.Surfaces(); len(surfaces) != 0 {
		t.Fatalf("unexpected default surfaces = %#v", surfaces)
	}
}
