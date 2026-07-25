package downloadsjanitorhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeStore struct {
	root       string
	workspaces map[string]*workspace.Workspace
}

func (f *fakeStore) Get(id string) (*workspace.Workspace, error) {
	ws, ok := f.workspaces[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return ws, nil
}

func (f *fakeStore) Update(id string, fn func(*workspace.Workspace) error) error {
	ws, ok := f.workspaces[id]
	if !ok {
		return errors.New("not found")
	}
	return fn(ws)
}

func (f *fakeStore) GetFolderPath(workspaceID string) (string, error) {
	if _, ok := f.workspaces[workspaceID]; !ok {
		return "", errors.New("not found")
	}
	dir := filepath.Join(f.root, workspaceID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

type fixedUser string

func (u fixedUser) CurrentUserID(context.Context) (string, error) { return string(u), nil }

func newTestHandler(t *testing.T, owners map[string]string) (*Handler, *fakeStore) {
	t.Helper()
	store := &fakeStore{root: t.TempDir(), workspaces: map[string]*workspace.Workspace{}}
	for id, owner := range owners {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
		ws.ID = id
		ws.OwnerUserID = owner
		store.workspaces[id] = ws
	}
	service := downloadsjanitor.NewService(downloadsjanitor.NewStore(store), store)
	return NewHandler(service, store, fixedUser(userprofile.LocalUserID)), store
}

func serve(t *testing.T, h *Handler, method, target string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("response is not JSON (%d): %s", rec.Code, rec.Body.String())
		}
	}
	return rec, decoded
}

func inboxFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Inbox")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGetStatus_ReportsSetupRequiredBeforeSetup(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})

	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	readiness, _ := status["readiness"].(map[string]any)
	if readiness["state"] != string(downloadsjanitor.ReadinessSetupRequired) {
		t.Fatalf("state = %v, want setup_required", readiness["state"])
	}
}

func TestConfirmSetup_ConfiguresTheWorkspace(t *testing.T) {
	h, store := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	root := inboxFixture(t)

	payload, _ := json.Marshal(map[string]string{"path": root})
	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	settings, _ := status["settings"].(map[string]any)
	if settings["root_path"] != filepath.Clean(root) {
		t.Fatalf("root_path = %v, want %q", settings["root_path"], root)
	}
	if settings["content_mode"] != string(downloadsjanitor.ContentModeMetadataOnly) {
		t.Fatalf("content_mode = %v, want metadata_only", settings["content_mode"])
	}
	if len(store.workspaces["ws-1"].DirectoryReferences) != 1 {
		t.Fatal("setup should have linked exactly one folder")
	}
}

func TestConfirmSetup_RejectsAFileWithAStableCode(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	file := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]string{"path": file})
	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	apiErr, _ := body["error"].(map[string]any)
	if apiErr == nil {
		apiErr = body
	}
	if apiErr["code"] != downloadsjanitor.CodeNotADirectory {
		t.Fatalf("error code = %v, want %q (body: %s)", apiErr["code"], downloadsjanitor.CodeNotADirectory, rec.Body.String())
	}
}

// A workspace owned by another user must be indistinguishable from one that
// does not exist, and must never be configured through this API.
func TestEndpoints_RejectAnotherUsersWorkspace(t *testing.T) {
	h, store := newTestHandler(t, map[string]string{"ws-other": "someone-else"})
	root := inboxFixture(t)

	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-other/downloads-janitor", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want 404", rec.Code)
	}

	payload, _ := json.Marshal(map[string]string{"path": root})
	rec, _ = serve(t, h, http.MethodPost, "/api/workspaces/ws-other/downloads-janitor/setup", string(payload))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST setup = %d, want 404", rec.Code)
	}
	if len(store.workspaces["ws-other"].DirectoryReferences) != 0 {
		t.Fatal("another user's workspace must not be configured")
	}
}

func TestEndpoints_UnknownWorkspaceIs404(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/nope/downloads-janitor", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetReadiness_ReturnsComponentChecks(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	root := inboxFixture(t)
	payload, _ := json.Marshal(map[string]string{"path": root})
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload)); rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %s", rec.Body.String())
	}

	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/readiness", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	readiness, _ := body["readiness"].(map[string]any)
	checks, _ := readiness["checks"].([]any)
	if len(checks) != len(downloadsjanitor.RequiredComponents) {
		t.Fatalf("expected one check per required component, got %d", len(checks))
	}
}

func TestHandler_WithoutAServiceReports503(t *testing.T) {
	h := NewHandler(nil, nil, nil)
	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// Setup is the only endpoint that accepts a path at all; reading status must
// never take one, so a client cannot steer the Janitor by querystring.
func TestGetStatus_IgnoresClientSuppliedPaths(t *testing.T) {
	h, store := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor?path=/etc", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(store.workspaces["ws-1"].DirectoryReferences) != 0 {
		t.Fatal("reading status must not link any folder")
	}
}
