package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWireWorkspaceSurfacesRegistersOnlyGenericRoutes(t *testing.T) {
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
	t.Setenv(workspaceSurfaceDemoEnv, "1")
	t.Setenv(workspaceSurfaceDemoRootEnv, filepath.Join(repositoryRoot, "internal", "workspacesurfacedemo", "testdata", "plugin"))
	t.Setenv("ORI_DATA_DIR", t.TempDir())

	builder := &ServerBuilder{workspaceStore: store, workspaceFileStore: store}
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

func TestWireWorkspaceSurfacesLeavesRegistryEmptyWithoutExplicitDemoOptIn(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(workspaceSurfaceDemoEnv, "")
	builder := &ServerBuilder{workspaceStore: store, workspaceFileStore: store}
	builder.wireWorkspaceSurfaces()
	if builder.workspaceSurfaceHandler == nil {
		t.Fatal("generic handler should be wired even with no installed surfaces")
	}
	if surfaces := builder.workspaceSurfaceRegistry.Surfaces(); len(surfaces) != 0 {
		t.Fatalf("unexpected default surfaces = %#v", surfaces)
	}
}
