package workspacecapabilityhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// installedHandler returns a handler over a workspace that already has File
// Janitor installed, plus a second workspace owned by somebody else.
func installedHandler(t *testing.T) (*Handler, *memStore) {
	t.Helper()
	mine := ownedWorkspace("ws-1", userprofile.LocalUserID)
	if _, err := mine.AddInstalledCapability(workspace.InstalledCapability{
		ID:      workspace.CapabilityFileJanitor,
		Version: 1,
		Source:  workspace.InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}
	store := newMemStore(mine, ownedWorkspace("ws-other", "someone-else"))
	h, _ := newTestHandler(t, store, stubUserProvider{id: userprofile.LocalUserID})
	return h, store
}

func serve(t *testing.T, h *Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := do(t, newTestMux(t, h), method, path, body)
	if rec.Body.Len() == 0 {
		return rec, map[string]any{}
	}
	return rec, decode(t, rec)
}

// The dry run must change nothing. It is a GET for exactly that reason: the
// confirmation a user reads is derived from the same resolution the removal
// will perform, and asking what would happen must never be the thing that makes
// it happen (FR-24, FR-25).
func TestRemovalSummary_ChangesNothing(t *testing.T) {
	h, store := installedHandler(t)

	rec, body := serve(t, h, http.MethodGet,
		"/api/workspaces/ws-1/capabilities/file-janitor/removal", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	removal, _ := body["removal"].(map[string]any)
	if removal["installed"] != true {
		t.Fatalf("removal summary = %v, want installed", removal)
	}
	if removal["moves_files"] != false {
		t.Error("the summary must state that removal moves no files")
	}

	ws, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("asking what removal would do must not remove anything")
	}
}

func TestRemoveCapability_RemovesAndReportsIt(t *testing.T) {
	h, store := installedHandler(t)

	rec, body := serve(t, h, http.MethodDelete,
		"/api/workspaces/ws-1/capabilities/file-janitor", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body["removed"] != true {
		t.Fatalf("removed = %v, want true", body["removed"])
	}

	ws, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("the capability is still installed")
	}
}

// Repeating a removal is success. A retry after a partial failure has to be
// able to finish (FR-15).
func TestRemoveCapability_IsIdempotentOverHTTP(t *testing.T) {
	h, _ := installedHandler(t)

	if rec, _ := serve(t, h, http.MethodDelete,
		"/api/workspaces/ws-1/capabilities/file-janitor", ""); rec.Code != http.StatusOK {
		t.Fatalf("first removal: %s", rec.Body.String())
	}
	rec, body := serve(t, h, http.MethodDelete,
		"/api/workspaces/ws-1/capabilities/file-janitor", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a repeated removal", rec.Code)
	}
	if body["already_removed"] != true {
		t.Errorf("already_removed = %v, want true", body["already_removed"])
	}
}

// The companion decision is explicit and defaults to off: uninstalling a
// capability is not consent to delete an agent (FR-27).
func TestRemoveCapability_DefaultsToKeepingTheCompanion(t *testing.T) {
	h, _ := installedHandler(t)

	rec, body := serve(t, h, http.MethodDelete,
		"/api/workspaces/ws-1/capabilities/file-janitor", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body["companion_removed"] != false {
		t.Errorf("companion_removed = %v, want false when nothing asked for it", body["companion_removed"])
	}
}

// Removal is workspace-scoped like every other endpoint here: a workspace owned
// by someone else is reported as not found rather than forbidden, so the API
// does not confirm that another user's workspace exists (FR-140).
func TestRemoveCapability_RefusesAnotherUsersWorkspace(t *testing.T) {
	h, _ := installedHandler(t)

	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodDelete, "/api/workspaces/ws-other/capabilities/file-janitor"},
		{http.MethodGet, "/api/workspaces/ws-other/capabilities/file-janitor/removal"},
	} {
		rec, _ := serve(t, h, target.method, target.path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", target.method, target.path, rec.Code)
		}
	}
}
