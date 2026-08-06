package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestShouldRunWorkspaceStartupMaintenance(t *testing.T) {
	newManager := func(t *testing.T) *config.Manager {
		t.Helper()
		manager := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
		if err := manager.Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		return manager
	}

	t.Run("unconfirmed first run stays inert", func(t *testing.T) {
		t.Setenv("WORKSPACE_DIR", "")
		if shouldRunWorkspaceStartupMaintenance(newManager(t)) {
			t.Fatal("unconfirmed first-run root must not run startup maintenance")
		}
	})

	t.Run("confirmed custom root is eligible", func(t *testing.T) {
		t.Setenv("WORKSPACE_DIR", "")
		manager := newManager(t)
		if err := manager.SetWorkspaceRoot(filepath.Join(t.TempDir(), "workspaces")); err != nil {
			t.Fatalf("SetWorkspaceRoot: %v", err)
		}
		if !shouldRunWorkspaceStartupMaintenance(manager) {
			t.Fatal("confirmed custom root must run startup maintenance")
		}
	})

	t.Run("confirmed built-in default is eligible", func(t *testing.T) {
		t.Setenv("WORKSPACE_DIR", "")
		manager := newManager(t)
		if err := manager.SetWorkspaceRoot(""); err != nil {
			t.Fatalf("SetWorkspaceRoot: %v", err)
		}
		if !shouldRunWorkspaceStartupMaintenance(manager) {
			t.Fatal("confirmed built-in root must run startup maintenance")
		}
	})

	t.Run("operator workspace override is eligible", func(t *testing.T) {
		t.Setenv("WORKSPACE_DIR", filepath.Join(t.TempDir(), "operator-workspaces"))
		if !shouldRunWorkspaceStartupMaintenance(newManager(t)) {
			t.Fatal("explicit WORKSPACE_DIR must run startup maintenance")
		}
	})

	t.Run("nil manager preserves alternate caller behavior", func(t *testing.T) {
		t.Setenv("WORKSPACE_DIR", "")
		if !shouldRunWorkspaceStartupMaintenance(nil) {
			t.Fatal("nil manager must preserve legacy startup maintenance")
		}
	})
}

func TestWorkspaceStartupMaintenance_RespectsFirstRunConsent(t *testing.T) {
	t.Run("unconfirmed staging is neither imported nor mutated", func(t *testing.T) {
		dataDir := prepareBuilderDataRoot(t)
		stagingRoot := config.UnconfirmedWorkspaceRoot()
		workspaceName := "Foreign Staging Workspace"
		workspaceFolder := seedFolderWorkspace(t, stagingRoot, workspaceName)
		legacySidecar := filepath.Join(workspaceFolder, "template-onboarding.json")
		if err := os.WriteFile(legacySidecar, []byte(`{"step":1}`), 0o600); err != nil {
			t.Fatalf("seed legacy sidecar: %v", err)
		}
		workspaceJSON := filepath.Join(workspaceFolder, "workspace.json")
		workspaceBefore := readBuilderFixture(t, workspaceJSON)
		sidecarBefore := readBuilderFixture(t, legacySidecar)

		payload := buildWorkspacePayload(t)
		if len(payload.Workspaces) != 0 || len(payload.Folders) != 0 {
			t.Fatalf("unconfirmed startup imported workspaces: %+v", payload.Workspaces)
		}
		if _, err := os.Stat(legacySidecar); err != nil {
			t.Fatalf("unconfirmed startup mutated staging sidecar: %v", err)
		}
		if got := readBuilderFixture(t, workspaceJSON); string(got) != string(workspaceBefore) {
			t.Fatal("unconfirmed startup rewrote workspace.json")
		}
		if got := readBuilderFixture(t, legacySidecar); string(got) != string(sidecarBefore) {
			t.Fatal("unconfirmed startup rewrote template-onboarding.json")
		}
		if got := config.DefaultDataDir(); got != dataDir {
			t.Fatalf("active data directory = %q, want %q", got, dataDir)
		}
	})

	t.Run("confirmed root still imports and maintains disk workspaces", func(t *testing.T) {
		dataDir := prepareBuilderDataRoot(t)
		confirmedRoot := filepath.Join(dataDir, "confirmed-workspaces")
		workspaceName := "Confirmed Disk Workspace"
		workspaceFolder := seedFolderWorkspace(t, confirmedRoot, workspaceName)
		legacySidecar := filepath.Join(workspaceFolder, "template-onboarding.json")
		if err := os.WriteFile(legacySidecar, []byte(`{"step":1}`), 0o600); err != nil {
			t.Fatalf("seed legacy sidecar: %v", err)
		}

		manager := config.NewManager(filepath.Join(dataDir, "settings.json"))
		if err := manager.Load(); err != nil {
			t.Fatalf("Load settings: %v", err)
		}
		if err := manager.SetWorkspaceRoot(confirmedRoot); err != nil {
			t.Fatalf("SetWorkspaceRoot: %v", err)
		}
		if err := manager.Save(); err != nil {
			t.Fatalf("Save settings: %v", err)
		}

		payload := buildWorkspacePayload(t)
		if !workspacePayloadContains(payload, workspaceName) {
			t.Fatalf("confirmed startup did not import %q: %+v", workspaceName, payload.Workspaces)
		}
		if _, err := os.Stat(legacySidecar); !os.IsNotExist(err) {
			t.Fatalf("confirmed startup did not run legacy maintenance; stat error = %v", err)
		}
	})
}

type workspacePayload struct {
	Folders    []workspacePayloadItem `json:"folders"`
	Workspaces []workspacePayloadItem `json:"workspaces"`
}

type workspacePayloadItem struct {
	Name string `json:"name"`
}

func prepareBuilderDataRoot(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	physicalDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(physicalDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalCWD); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	t.Setenv("HOME", filepath.Join(physicalDir, "home"))
	t.Setenv("ORI_DATA_DIR", physicalDir)
	t.Setenv("WORKSPACE_DIR", "")
	return physicalDir
}

func seedFolderWorkspace(t *testing.T, root, name string) string {
	t.Helper()
	store, err := workspace.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}
	folder, err := store.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	return folder
}

func buildWorkspacePayload(t *testing.T) workspacePayload {
	t.Helper()
	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder: %v", err)
	}
	builder.WithDesktopOpener(discardDesktopOpener{})
	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() {
		srv.Shutdown()
		if builder.sessionStore != nil {
			if err := builder.sessionStore.Close(); err != nil {
				t.Errorf("close session store: %v", err)
			}
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces?tree=true", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace API status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload workspacePayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode workspace API: %v", err)
	}
	return payload
}

func workspacePayloadContains(payload workspacePayload, name string) bool {
	for _, item := range payload.Workspaces {
		if item.Name == name {
			return true
		}
	}
	return false
}

func readBuilderFixture(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
}
