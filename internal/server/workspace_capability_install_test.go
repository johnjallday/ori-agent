package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// TestFileJanitorInstallVerticalSlice exercises the whole Group 1 path against
// the real server: the real builder, the real workspace store (SyncStore over
// SQLite + the folder store), the real capability registry, and the real File
// Janitor runtime backed by the Janitor service.
//
// It is the check that distinguishes a wired feature from a set of passing unit
// tests: catalog -> install -> catalog, with the install surviving a reload from
// the canonical workspace record and the status coming from the Janitor service
// rather than anything persisted.
func TestFileJanitorInstallVerticalSlice(t *testing.T) {
	builder := newBuiltTestBuilder(t)
	handler := builder.server.Handler()

	if builder.workspaceStore == nil {
		t.Fatal("workspace store was not wired")
	}

	const workspaceID = "ws-capability-slice"
	seed := &workspace.Workspace{
		ID:          workspaceID,
		Name:        "Research Notes",
		FolderSlug:  "research-notes",
		OwnerUserID: "local",
		Status:      workspace.StatusActive,
		SharedData:  map[string]any{},
		Tasks: []workspace.Task{
			{ID: "task-1", Description: "Pre-existing task", Status: workspace.TaskStatusPending},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := builder.workspaceStore.Save(seed); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	get := func(method, path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: status = %d, body = %s", method, path, rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
		return payload
	}

	// 1. Catalog before install: File Janitor offered, not installed.
	catalog := get(http.MethodGet, "/api/workspaces/"+workspaceID+"/capabilities")
	items := catalog["capabilities"].([]any)
	if len(items) != len(workspacecapability.BuiltinDefinitions()) {
		t.Fatalf("expected every compiled capability, got %d", len(items))
	}
	item := items[0].(map[string]any)
	if item["installed"] != false {
		t.Fatalf("File Janitor reported installed before install: %v", item)
	}

	// 2. Install.
	installed := get(http.MethodPost, "/api/workspaces/"+workspaceID+"/capabilities/file-janitor/install")
	if installed["already_installed"] != false {
		t.Fatalf("first install reported already_installed")
	}
	status := installed["status"].(map[string]any)
	if status["state"] != string(workspacecapability.StatusSetupNeeded) {
		t.Fatalf("status = %v, want setup_needed from the real Janitor runtime", status["state"])
	}
	if status["configured"] != false {
		t.Fatal("a freshly installed capability must not report itself configured")
	}

	// 3. Catalog after install: installed, still setup_needed (FR-21).
	catalog = get(http.MethodGet, "/api/workspaces/"+workspaceID+"/capabilities")
	item = catalog["capabilities"].([]any)[0].(map[string]any)
	if item["installed"] != true {
		t.Fatal("catalog does not report the capability as installed")
	}
	status = item["status"].(map[string]any)
	if status["state"] != string(workspacecapability.StatusSetupNeeded) {
		t.Fatalf("catalog status = %v, want setup_needed", status["state"])
	}

	// 4. The install survives on the canonical workspace record, and installing
	//    granted no folder access and started no automation (FR-20).
	stored, err := builder.workspaceStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !stored.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatalf("install did not persist: %+v", stored.GetInstalledCapabilities())
	}
	if len(stored.DirectoryReferences) != 0 {
		t.Fatalf("install created a directory reference: %+v", stored.DirectoryReferences)
	}
	if len(stored.MCPBindings) != 0 {
		t.Fatalf("install created an MCP binding: %+v", stored.MCPBindings)
	}
	if len(stored.AgentInstances) != 0 {
		t.Fatalf("install created an agent: %+v", stored.AgentInstances)
	}
	if len(stored.Tasks) != 1 || stored.Tasks[0].ID != "task-1" {
		t.Fatalf("install disturbed the workspace's tasks: %+v", stored.Tasks)
	}
	if stored.Name != "Research Notes" {
		t.Fatalf("install renamed the workspace: %q", stored.Name)
	}

	// 5. Repeating the install is idempotent over the real stack (FR-9).
	repeat := get(http.MethodPost, "/api/workspaces/"+workspaceID+"/capabilities/file-janitor/install")
	if repeat["already_installed"] != true {
		t.Fatal("repeat install did not report already_installed")
	}
	stored, err = builder.workspaceStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := len(stored.GetInstalledCapabilities()); got != 1 {
		t.Fatalf("expected exactly one install record after a repeat, got %d", got)
	}
}
