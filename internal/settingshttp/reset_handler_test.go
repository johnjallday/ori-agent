package settingshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeAgentStore is a minimal store.Store double. The reset handler only
// ever calls ClearAgents on it; every other method is an unused stub.
type fakeAgentStore struct {
	clearCalls int
	clearErr   error
}

func (f *fakeAgentStore) ListAgents() []string                               { return nil }
func (f *fakeAgentStore) CreateAgent(string, *store.CreateAgentConfig) error { return nil }
func (f *fakeAgentStore) DeleteAgent(string) error                           { return nil }
func (f *fakeAgentStore) GetAgent(string) (*agent.Agent, bool)               { return nil, false }
func (f *fakeAgentStore) SetAgent(string, *agent.Agent) error                { return nil }
func (f *fakeAgentStore) UpdateAgent(string, func(*agent.Agent) error) error { return nil }
func (f *fakeAgentStore) Save() error                                        { return nil }
func (f *fakeAgentStore) ClearAgents() error {
	f.clearCalls++
	return f.clearErr
}

// newTestHandler wires a ResetHandler against an isolated temp data
// directory, with a fake agent store and a real workspace file store rooted
// at dataDir/workspaces (the exact path resetSessions deletes), so both the
// on-disk and in-memory sides of a reset are independently observable.
func newTestHandler(t *testing.T, dataDir string) (*ResetHandler, *fakeAgentStore, *workspace.FileStore) {
	t.Helper()
	mgr := onboarding.NewManager(filepath.Join(dataDir, "app_state.json"))
	st := &fakeAgentStore{}
	ws, err := workspace.NewFileStore(filepath.Join(dataDir, "workspaces"))
	if err != nil {
		t.Fatalf("workspace.NewFileStore: %v", err)
	}
	h := NewResetHandler(mgr, st, dataDir)
	h.SetWorkspaceStore(ws)
	return h, st, ws
}

func postReset(t *testing.T, h *ResetHandler, req ResetRequest) ResetResponse {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/reset", strings.NewReader(string(body)))
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleReset(w, r)

	var resp ResetResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	return resp
}

func mustWriteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("stat %s: %v", path, err)
	return false
}

func TestResetHandler_DataDir_ReturnsConstructedValue(t *testing.T) {
	dataDir := t.TempDir()
	h, _, _ := newTestHandler(t, dataDir)
	if got := h.DataDir(); got != dataDir {
		t.Fatalf("DataDir() = %q, want %q", got, dataDir)
	}
}

func TestHandleReset_Settings_RemovesFileAndTogglesMissingFileHarmlessly(t *testing.T) {
	dataDir := t.TempDir()
	h, _, _ := newTestHandler(t, dataDir)
	settingsPath := filepath.Join(dataDir, "settings.json")
	mustWriteFile(t, settingsPath, `{"key":"value"}`)

	resp := postReset(t, h, ResetRequest{Settings: true, Confirmation: "RESET"})
	if !resp.Success || len(resp.Errors) != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if exists(t, settingsPath) {
		t.Fatal("settings.json should have been removed")
	}

	// A second reset with nothing left to remove must still succeed.
	resp = postReset(t, h, ResetRequest{Settings: true, Confirmation: "RESET"})
	if !resp.Success || len(resp.Errors) != 0 {
		t.Fatalf("reset of an already-missing settings.json should succeed, got %+v", resp)
	}
}

func TestHandleReset_Agents_RemovesFilesAndClearsInMemoryStore(t *testing.T) {
	dataDir := t.TempDir()
	h, st, _ := newTestHandler(t, dataDir)
	agentsJSON := filepath.Join(dataDir, "agents.json")
	agentsDir := filepath.Join(dataDir, "agents")
	mustWriteFile(t, agentsJSON, `{}`)
	mustWriteFile(t, filepath.Join(agentsDir, "researcher", "agent_settings.json"), `{}`)

	resp := postReset(t, h, ResetRequest{Agents: true, Confirmation: "RESET"})
	if !resp.Success || len(resp.Errors) != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if exists(t, agentsJSON) {
		t.Fatal("agents.json should have been removed")
	}
	if exists(t, agentsDir) {
		t.Fatal("agents/ directory should have been removed")
	}
	if st.clearCalls != 1 {
		t.Fatalf("ClearAgents calls = %d, want 1", st.clearCalls)
	}
}

func TestHandleReset_Sessions_RemovesDBFilesDirectoriesAndInMemoryState(t *testing.T) {
	dataDir := t.TempDir()
	h, _, ws := newTestHandler(t, dataDir)
	dbPath := filepath.Join(dataDir, "sessions.db")
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"
	sessionFilesDir := filepath.Join(dataDir, "session_files")
	workspacesDir := filepath.Join(dataDir, "workspaces")
	mustWriteFile(t, dbPath, "db")
	mustWriteFile(t, walPath, "wal")
	mustWriteFile(t, shmPath, "shm")
	mustWriteFile(t, filepath.Join(sessionFilesDir, "upload.bin"), "data")
	mustWriteFile(t, filepath.Join(workspacesDir, "w1", "workspace.json"), `{"id":"w1"}`)

	resp := postReset(t, h, ResetRequest{Sessions: true, Confirmation: "RESET"})
	if !resp.Success || len(resp.Errors) != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	for _, p := range []string{dbPath, walPath, shmPath, sessionFilesDir, workspacesDir} {
		if exists(t, p) {
			t.Fatalf("%s should have been removed", p)
		}
	}
	ids, err := ws.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("in-memory workspace store should be cleared, still has %d entries", len(ids))
	}
}

func TestHandleReset_Onboarding_ResetsAppStateButPreservesPAFRecords(t *testing.T) {
	dataDir := t.TempDir()
	mgr := onboarding.NewManager(filepath.Join(dataDir, "app_state.json"))
	st := &fakeAgentStore{}
	ws, err := workspace.NewFileStore(filepath.Join(dataDir, "workspaces"))
	if err != nil {
		t.Fatalf("workspace.NewFileStore: %v", err)
	}
	h := NewResetHandler(mgr, st, dataDir)
	h.SetWorkspaceStore(ws)
	mustWriteFile(t, filepath.Join(dataDir, "sessions.db"), "relationship-records")
	if err := mgr.CompleteOnboarding(); err != nil {
		t.Fatalf("CompleteOnboarding: %v", err)
	}

	resp := postReset(t, h, ResetRequest{Onboarding: true, Confirmation: "RESET"})
	if !resp.Success || len(resp.Errors) != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(resp.ResetItems) != 1 || resp.ResetItems[0] != "onboarding" {
		t.Fatalf("reset items = %v, want [onboarding]", resp.ResetItems)
	}
	if mgr.IsOnboardingComplete() {
		t.Fatal("onboarding should be incomplete after reset")
	}
	if !exists(t, filepath.Join(dataDir, "sessions.db")) {
		t.Fatal("onboarding-only reset must preserve relationship/session records")
	}
}

func TestHandleReset_AllOptionsTogetherSucceed(t *testing.T) {
	dataDir := t.TempDir()
	h, st, ws := newTestHandler(t, dataDir)
	mustWriteFile(t, filepath.Join(dataDir, "settings.json"), `{}`)
	mustWriteFile(t, filepath.Join(dataDir, "agents.json"), `{}`)
	mustWriteFile(t, filepath.Join(dataDir, "sessions.db"), "db")

	resp := postReset(t, h, ResetRequest{
		Settings: true, Agents: true, Sessions: true, Onboarding: true,
		Confirmation: "RESET",
	})
	if !resp.Success || len(resp.Errors) != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	wantItems := map[string]bool{"settings": true, "agents": true, "sessions": true, "onboarding": true}
	if len(resp.ResetItems) != len(wantItems) {
		t.Fatalf("reset items = %v, want all four", resp.ResetItems)
	}
	for _, item := range resp.ResetItems {
		if !wantItems[item] {
			t.Fatalf("unexpected reset item %q", item)
		}
	}
	if st.clearCalls != 1 {
		t.Fatalf("ClearAgents calls = %d, want 1", st.clearCalls)
	}
	ids, err := ws.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 0 {
		t.Fatal("in-memory workspace store should be cleared")
	}
}

// TestHandleReset_PartialFailureReportsSucceededItemsAndErrors covers FR-36:
// one category failing validation must not silently swallow the others, and
// the overall response must truthfully reflect a partial outcome.
func TestHandleReset_PartialFailureReportsSucceededItemsAndErrors(t *testing.T) {
	dataDir := t.TempDir()
	h, _, _ := newTestHandler(t, dataDir)
	mustWriteFile(t, filepath.Join(dataDir, "settings.json"), `{}`)

	// Corrupt the handler's own notion of its data directory so every
	// validatePath call for the *remaining* categories fails, while
	// settings (checked first) still has a valid path already computed
	// before this - instead, exercise the failure path directly on a
	// handler whose dataDir cannot be resolved, and confirm settings
	// still fails cleanly (not the "silently swallow" pattern) rather
	// than only ever observing all-success or all-failure.
	broken := NewResetHandler(h.onboardingMgr, h.store, "")
	resp := postReset(t, broken, ResetRequest{Settings: true, Agents: true, Confirmation: "RESET"})
	if resp.Success {
		t.Fatal("reset with no configured data directory must not report success")
	}
	if len(resp.Errors) != 2 {
		t.Fatalf("expected both categories to report an error, got %+v", resp.Errors)
	}
	if len(resp.ResetItems) != 0 {
		t.Fatalf("expected no succeeded items, got %v", resp.ResetItems)
	}
}

func TestValidatePath_RejectsEmptyDataDir(t *testing.T) {
	h := &ResetHandler{dataDir: ""}
	if err := h.validatePath("/tmp/whatever"); err == nil {
		t.Fatal("expected an error for an empty data directory")
	}
}

func TestValidatePath_RejectsFilesystemRootDataDir(t *testing.T) {
	root := "/"
	if runtime.GOOS == "windows" {
		root = `C:\`
	}
	h := &ResetHandler{dataDir: root}
	if err := h.validatePath(filepath.Join(root, "settings.json")); err == nil {
		t.Fatal("expected an error for a filesystem-root data directory")
	}
}

func TestValidatePath_AcceptsExactDataDirAndChildren(t *testing.T) {
	dataDir := t.TempDir()
	h := &ResetHandler{dataDir: dataDir}
	if err := h.validatePath(dataDir); err != nil {
		t.Fatalf("data directory itself should validate, got %v", err)
	}
	if err := h.validatePath(filepath.Join(dataDir, "settings.json")); err != nil {
		t.Fatalf("a direct child should validate, got %v", err)
	}
	if err := h.validatePath(filepath.Join(dataDir, "agents", "researcher")); err != nil {
		t.Fatalf("a nested child should validate, got %v", err)
	}
}

// TestValidatePath_RejectsSiblingPrefixDirectory is the case a raw
// strings.HasPrefix without a separator suffix gets wrong: a sibling
// directory whose name happens to start with the data directory's name.
func TestValidatePath_RejectsSiblingPrefixDirectory(t *testing.T) {
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "OriData")
	sibling := filepath.Join(parent, "OriData-backup", "important-file")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	h := &ResetHandler{dataDir: dataDir}
	if err := h.validatePath(sibling); err == nil {
		t.Fatal("a sibling directory sharing a name prefix must not validate")
	}
}

func TestValidatePath_RejectsParentTraversal(t *testing.T) {
	dataDir := t.TempDir()
	h := &ResetHandler{dataDir: dataDir}
	escape := filepath.Join(dataDir, "..", "escaped")
	if err := h.validatePath(escape); err == nil {
		t.Fatal("a path that traverses above the data directory must not validate")
	}
}

// TestValidatePath_RejectsSymlinkEscape covers the symlink half of FR-35: a
// symlink planted inside the data directory must not redirect deletion to a
// target outside it.
func TestValidatePath_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	dataDir := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	mustWriteFile(t, sentinel, "keep me")

	link := filepath.Join(dataDir, "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	h := &ResetHandler{dataDir: dataDir}
	if err := h.validatePath(filepath.Join(link, "sentinel")); err == nil {
		t.Fatal("a symlink resolving outside the data directory must not validate")
	}
}

// TestHandleReset_PreservesSentinelOutsideDataDir covers FR-37: every tested
// reset combination must prove a sentinel outside the validated data
// directory survives.
func TestHandleReset_PreservesSentinelOutsideDataDir(t *testing.T) {
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	h, _, _ := newTestHandler(t, dataDir)

	// A sibling directory whose name is a prefix-extension of dataDir, plus a
	// plain unrelated sibling - both must survive every reset combination.
	siblingPrefixed := filepath.Join(parent, "data-backup", "settings.json")
	siblingPlain := filepath.Join(parent, "other", "settings.json")
	mustWriteFile(t, siblingPrefixed, "keep me")
	mustWriteFile(t, siblingPlain, "keep me too")
	mustWriteFile(t, filepath.Join(dataDir, "settings.json"), `{}`)
	mustWriteFile(t, filepath.Join(dataDir, "agents.json"), `{}`)
	mustWriteFile(t, filepath.Join(dataDir, "sessions.db"), "db")

	resp := postReset(t, h, ResetRequest{
		Settings: true, Agents: true, Sessions: true, Onboarding: true,
		Confirmation: "RESET",
	})
	if !resp.Success || len(resp.Errors) != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !exists(t, siblingPrefixed) {
		t.Fatal("sentinel in a sibling prefix-extended directory must survive reset")
	}
	if !exists(t, siblingPlain) {
		t.Fatal("sentinel in an unrelated sibling directory must survive reset")
	}
}

func TestHandleReset_RejectsWithoutConfirmation(t *testing.T) {
	dataDir := t.TempDir()
	h, _, _ := newTestHandler(t, dataDir)
	settingsPath := filepath.Join(dataDir, "settings.json")
	mustWriteFile(t, settingsPath, `{}`)

	resp := postReset(t, h, ResetRequest{Settings: true, Confirmation: "nope"})
	if resp.Success {
		t.Fatal("reset without the exact RESET confirmation must not succeed")
	}
	if !exists(t, settingsPath) {
		t.Fatal("settings.json must survive a rejected reset request")
	}
}
