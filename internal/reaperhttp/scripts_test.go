package reaperhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/reaper"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// pinStub is an in-memory PinService double. It mutates the SAME testStore
// workspace record ListScripts reads PinnedReaperScripts from (as
// workspace.ReaperPinService does via store.Update against the real store),
// so pin state set up directly on the stub is visible through the handler
// exactly like production wiring.
type pinStub struct {
	store *testStore
	err   error
}

func newPinStub(store *testStore) *pinStub {
	return &pinStub{store: store}
}

func (p *pinStub) pinned(workspaceID string) []string {
	ws, ok := p.store.workspaces[workspaceID]
	if !ok {
		return nil
	}
	return ws.PinnedReaperScripts
}

func (p *pinStub) setPinned(workspaceID string, ids []string) {
	p.store.workspaces[workspaceID].PinnedReaperScripts = ids
}

func (p *pinStub) Pin(workspaceID, scriptID string) error {
	if p.err != nil {
		return p.err
	}
	if slices.Contains(p.pinned(workspaceID), scriptID) {
		return nil
	}
	p.setPinned(workspaceID, append(p.pinned(workspaceID), scriptID))
	return nil
}

func (p *pinStub) Unpin(workspaceID, scriptID string) error {
	if p.err != nil {
		return p.err
	}
	current := p.pinned(workspaceID)
	out := make([]string, 0, len(current))
	for _, id := range current {
		if id != scriptID {
			out = append(out, id)
		}
	}
	p.setPinned(workspaceID, out)
	return nil
}

func (p *pinStub) Reorder(workspaceID string, orderedScriptIDs []string) error {
	if p.err != nil {
		return p.err
	}
	p.setPinned(workspaceID, orderedScriptIDs)
	return nil
}

func scriptTestHandler(t *testing.T) (*http.ServeMux, *pinStub) {
	t.Helper()
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	library := reaper.NewLibraryAt(filepath.Join(root, "Ori Scripts", "reaper"))
	catalog := reaper.NewCatalogWithKeyboardConfig("")
	catalog.SetLibrary(library)
	reader := &stateReader{state: reaper.State{Connected: true, PlayState: "stopped", Tracks: []reaper.Track{}}}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, catalog)
	handler.SetScriptServices(library, &scriptRunnerStub{})
	pins := newPinStub(store)
	handler.SetPinService(pins)
	mux := http.NewServeMux()
	handler.Register(mux)
	return mux, pins
}

func createTestScript(t *testing.T, mux *http.ServeMux, filename string) {
	t.Helper()
	body := `{"filename":"` + filename + `","name":"n","description":"d","needs_confirmation":false,"code":"return 1\\n"}`
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/scripts", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create %s = %d %s", filename, recorder.Code, recorder.Body.String())
	}
}

func TestPinScriptPersistsAndAppearsInList(t *testing.T) {
	mux, pins := scriptTestHandler(t)
	createTestScript(t, mux, "a.lua")

	pinRecorder := httptest.NewRecorder()
	mux.ServeHTTP(pinRecorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/scripts/custom:a.lua/pin", nil))
	if pinRecorder.Code != http.StatusOK {
		t.Fatalf("pin = %d %s", pinRecorder.Code, pinRecorder.Body.String())
	}
	if got := pins.pinned("mine"); len(got) != 1 || got[0] != "custom:a.lua" {
		t.Fatalf("pins.pinned(mine) = %v", got)
	}

	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/scripts", nil))
	var response ScriptListResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.PinnedScriptIDs) != 1 || response.PinnedScriptIDs[0] != "custom:a.lua" {
		t.Fatalf("pinned_script_ids = %v", response.PinnedScriptIDs)
	}
}

func TestPinScriptRejectsUnknownScript(t *testing.T) {
	mux, pins := scriptTestHandler(t)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/scripts/custom:never-created.lua/pin", nil))
	if recorder.Code == http.StatusOK {
		t.Fatalf("pin of unknown script = %d, want an error", recorder.Code)
	}
	if len(pins.pinned("mine")) != 0 {
		t.Fatalf("pins.pinned(mine) = %v, want nothing pinned", pins.pinned("mine"))
	}
}

func TestUnpinScriptRemovesFromList(t *testing.T) {
	mux, pins := scriptTestHandler(t)
	createTestScript(t, mux, "a.lua")
	pins.setPinned("mine", []string{"custom:a.lua"})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/mine/reaper/scripts/custom:a.lua/pin", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("unpin = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(pins.pinned("mine")) != 0 {
		t.Fatalf("pins.pinned(mine) = %v, want empty", pins.pinned("mine"))
	}
}

func TestReorderPinnedScriptsPersistsNewOrder(t *testing.T) {
	mux, pins := scriptTestHandler(t)
	createTestScript(t, mux, "a.lua")
	createTestScript(t, mux, "b.lua")
	pins.setPinned("mine", []string{"custom:a.lua", "custom:b.lua"})

	body := `{"ordered_script_ids":["custom:b.lua","custom:a.lua"]}`
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/workspaces/mine/reaper/pinned-scripts", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("reorder = %d %s", recorder.Code, recorder.Body.String())
	}
	got := pins.pinned("mine")
	if len(got) != 2 || got[0] != "custom:b.lua" || got[1] != "custom:a.lua" {
		t.Fatalf("pins.pinned(mine) = %v", got)
	}
}

func TestReorderPinnedScriptsRejectsMismatchedSetAsConflict(t *testing.T) {
	mux, pins := scriptTestHandler(t)
	pins.err = errors.New("reorder must include exactly the currently pinned scripts")

	body := `{"ordered_script_ids":["custom:a.lua"]}`
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/workspaces/mine/reaper/pinned-scripts", strings.NewReader(body)))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("reorder = %d %s, want 409", recorder.Code, recorder.Body.String())
	}
}

func TestListScriptsPrunesPinIDsForDeletedScripts(t *testing.T) {
	mux, pins := scriptTestHandler(t)
	createTestScript(t, mux, "a.lua")
	// "b.lua" is pinned but was never created in the library — simulates a
	// pin surviving a delete elsewhere (task 1.3's read-time prune).
	pins.setPinned("mine", []string{"custom:a.lua", "custom:b.lua"})

	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/workspaces/mine/reaper/scripts", nil))
	var response ScriptListResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.PinnedScriptIDs) != 1 || response.PinnedScriptIDs[0] != "custom:a.lua" {
		t.Fatalf("pinned_script_ids = %v, want only the still-resolving script", response.PinnedScriptIDs)
	}
}

func TestPinEndpointsUnavailableWithoutPinService(t *testing.T) {
	root := t.TempDir()
	store := &testStore{root: root, workspaces: map[string]*workspace.Workspace{}}
	store.workspaces["mine"] = reaperHTTPWorkspace(t, root, "mine", userprofile.LocalUserID)
	library := reaper.NewLibraryAt(filepath.Join(root, "Ori Scripts", "reaper"))
	catalog := reaper.NewCatalogWithKeyboardConfig("")
	catalog.SetLibrary(library)
	reader := &stateReader{state: reaper.State{Connected: true, Tracks: []reaper.Track{}}}
	handler := NewHandler(store, testUser(userprofile.LocalUserID), reader, catalog)
	handler.SetScriptServices(library, &scriptRunnerStub{})
	// Deliberately no SetPinService call.
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/mine/reaper/scripts/custom:a.lua/pin", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("pin without service = %d, want 503", recorder.Code)
	}
}
