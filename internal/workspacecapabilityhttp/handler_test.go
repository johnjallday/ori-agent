package workspacecapabilityhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

type memStore struct {
	mu         sync.Mutex
	workspaces map[string]*workspace.Workspace
}

func newMemStore(ws ...*workspace.Workspace) *memStore {
	s := &memStore{workspaces: make(map[string]*workspace.Workspace, len(ws))}
	for _, w := range ws {
		s.workspaces[w.ID] = w
	}
	return s
}

func (s *memStore) Get(id string) (*workspace.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.workspaces[id]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	return clone(ws), nil
}

func (s *memStore) Update(id string, fn func(*workspace.Workspace) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.workspaces[id]
	if !ok {
		return errors.New("workspace not found")
	}
	working := clone(ws)
	if err := fn(working); err != nil {
		return err
	}
	s.workspaces[id] = working
	return nil
}

func clone(ws *workspace.Workspace) *workspace.Workspace {
	data, err := ws.ToJSON()
	if err != nil {
		panic(err)
	}
	out, err := workspace.FromJSON(data)
	if err != nil {
		panic(err)
	}
	return out
}

type stubUserProvider struct {
	id  string
	err error
}

func (s stubUserProvider) CurrentUserID(context.Context) (string, error) { return s.id, s.err }

type stubRuntime struct{ status workspacecapability.Status }

func (s stubRuntime) CapabilityStatus(string) (workspacecapability.Status, error) {
	return s.status, nil
}

func ownedWorkspace(id, owner string) *workspace.Workspace {
	return &workspace.Workspace{
		ID:          id,
		Name:        "Inbox",
		OwnerUserID: owner,
		Status:      workspace.StatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

func newTestHandler(t *testing.T, store *memStore, provider stubUserProvider) (*Handler, *workspacecapability.Service) {
	t.Helper()
	registry, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	svc := workspacecapability.NewService(registry, store)
	return NewHandler(svc, store, provider), svc
}

func newTestMux(t *testing.T, h *Handler) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return payload
}

func TestListCapabilities_ReturnsCatalogForOwnedWorkspace(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-1", "local"))
	h, _ := newTestHandler(t, store, stubUserProvider{id: "local"})
	mux := newTestMux(t, h)

	rec := do(t, mux, http.MethodGet, "/api/workspaces/ws-1/capabilities", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	payload := decode(t, rec)
	capabilities, ok := payload["capabilities"].([]any)
	if !ok {
		t.Fatalf("catalog response is missing capabilities: %v", payload)
	}
	if len(capabilities) != len(workspacecapability.BuiltinDefinitions()) {
		t.Fatalf("expected %d catalog entries, got %d", len(workspacecapability.BuiltinDefinitions()), len(capabilities))
	}

	seen := map[string]bool{}
	for _, itemRaw := range capabilities {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			t.Fatalf("catalog item has unexpected type: %T", itemRaw)
		}
		if item["installed"] != false {
			t.Fatalf("expected not installed, got %v", item["installed"])
		}
		definition, ok := item["definition"].(map[string]any)
		if !ok {
			t.Fatal("definition is missing or invalid")
		}
		id, _ := definition["id"].(string)
		if id == "" {
			t.Fatalf("catalog item has empty id: %v", definition)
		}
		if seen[id] {
			t.Fatalf("catalog listed duplicate capability %q", id)
		}
		seen[id] = true
	}

	item := (func() map[string]any {
		for _, itemRaw := range capabilities {
			item := itemRaw.(map[string]any)
			definition := item["definition"].(map[string]any)
			if definition["id"] == workspace.CapabilityFileJanitor {
				return item
			}
		}
		return nil
	})()
	if item == nil {
		t.Fatalf("file janitor missing from catalog; got %v", seen)
	}
	definition := item["definition"].(map[string]any)
	if definition["id"] != workspace.CapabilityFileJanitor {
		t.Fatalf("unexpected capability id: %v", definition["id"])
	}
	// The catalog item must carry the safety copy the user needs before
	// installing (FR-18).
	display := definition["display"].(map[string]any)
	if display["name"] != "File Janitor" {
		t.Fatalf("unexpected display name: %v", display["name"])
	}
	if highlights, ok := display["highlights"].([]any); !ok || len(highlights) == 0 {
		t.Fatal("catalog item is missing its safety highlights")
	}
}

func TestInstallCapability_PersistsAndReturnsDerivedStatus(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-1", "local"))
	h, svc := newTestHandler(t, store, stubUserProvider{id: "local"})
	if err := svc.Registry().BindRuntime(workspace.CapabilityFileJanitor, stubRuntime{
		status: workspacecapability.Status{State: workspacecapability.StatusSetupNeeded, Detail: "Choose a folder"},
	}); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}
	mux := newTestMux(t, h)

	rec := do(t, mux, http.MethodPost, "/api/workspaces/ws-1/capabilities/file-janitor/install", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	payload := decode(t, rec)
	if payload["already_installed"] != false {
		t.Fatalf("first install reported already_installed: %v", payload["already_installed"])
	}

	// Persisted install metadata (FR-5).
	record := payload["record"].(map[string]any)
	if record["id"] != workspace.CapabilityFileJanitor {
		t.Fatalf("unexpected record id: %v", record["id"])
	}
	if record["version"].(float64) != float64(workspacecapability.FileJanitorDefinitionVersion) {
		t.Fatalf("unexpected record version: %v", record["version"])
	}
	if record["source"] != workspace.InstallSourceInPlace {
		t.Fatalf("unexpected record source: %v", record["source"])
	}
	if record["installed_at"] == nil {
		t.Fatal("record is missing installed_at")
	}

	// Derived health, not a stored status string (FR-6).
	status := payload["status"].(map[string]any)
	if status["state"] != string(workspacecapability.StatusSetupNeeded) {
		t.Fatalf("status = %v, want setup_needed", status["state"])
	}

	stored, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !stored.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		t.Fatal("install was not persisted")
	}
}

// TestInstallCapability_IsIdempotentOverHTTP covers FR-9 at the API boundary:
// a repeated POST is a success reporting the unchanged record, not a 409 and
// not a second install.
func TestInstallCapability_IsIdempotentOverHTTP(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-1", "local"))
	h, _ := newTestHandler(t, store, stubUserProvider{id: "local"})
	mux := newTestMux(t, h)

	first := do(t, mux, http.MethodPost, "/api/workspaces/ws-1/capabilities/file-janitor/install", "")
	if first.Code != http.StatusOK {
		t.Fatalf("first install status = %d", first.Code)
	}
	firstRecord := decode(t, first)["record"].(map[string]any)

	second := do(t, mux, http.MethodPost, "/api/workspaces/ws-1/capabilities/file-janitor/install", `{"source":"blueprint"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("repeat install status = %d, body = %s", second.Code, second.Body.String())
	}
	payload := decode(t, second)
	if payload["already_installed"] != true {
		t.Fatal("repeat install did not report already_installed")
	}
	secondRecord := payload["record"].(map[string]any)
	if secondRecord["source"] != firstRecord["source"] {
		t.Fatalf("repeat install rewrote provenance: %v -> %v", firstRecord["source"], secondRecord["source"])
	}

	stored, _ := store.Get("ws-1")
	if got := len(stored.GetInstalledCapabilities()); got != 1 {
		t.Fatalf("expected one install record, got %d", got)
	}
}

// TestEndpoints_RejectWorkspaceOwnedBySomeoneElse covers FR-140. An unowned
// workspace reports 404, not 403, so the API does not confirm that another
// user's workspace exists.
func TestEndpoints_RejectWorkspaceOwnedBySomeoneElse(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-other", "someone-else"))
	h, _ := newTestHandler(t, store, stubUserProvider{id: "local"})
	mux := newTestMux(t, h)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"list", http.MethodGet, "/api/workspaces/ws-other/capabilities"},
		{"install", http.MethodPost, "/api/workspaces/ws-other/capabilities/file-janitor/install"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, mux, tc.method, tc.path, "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
			}
			if strings.Contains(strings.ToLower(rec.Body.String()), "forbidden") {
				t.Fatal("response confirms the workspace exists")
			}
		})
	}

	stored, _ := store.Get("ws-other")
	if len(stored.GetInstalledCapabilities()) != 0 {
		t.Fatal("an unauthorized request installed a capability")
	}
}

func TestEndpoints_RejectUnknownWorkspace(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-1", "local"))
	h, _ := newTestHandler(t, store, stubUserProvider{id: "local"})
	mux := newTestMux(t, h)

	for _, path := range []string{
		"/api/workspaces/ws-missing/capabilities",
		"/api/workspaces/ws-missing/capabilities/file-janitor/install",
	} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/install") {
			method = http.MethodPost
		}
		rec := do(t, mux, method, path, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}

func TestEndpoints_RejectWhenCurrentUserCannotBeResolved(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-1", "local"))
	h, _ := newTestHandler(t, store, stubUserProvider{err: errors.New("no session")})
	mux := newTestMux(t, h)

	rec := do(t, mux, http.MethodGet, "/api/workspaces/ws-1/capabilities", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when the caller cannot be identified", rec.Code)
	}
}

// TestInstallCapability_RejectsUnknownCapabilityID is the fail-closed guarantee
// at the API boundary (FR-14): a client-supplied ID can only select a compiled
// definition.
func TestInstallCapability_RejectsUnknownCapabilityID(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-1", "local"))
	h, _ := newTestHandler(t, store, stubUserProvider{id: "local"})
	mux := newTestMux(t, h)

	for _, id := range []string{"made-up", "..%2Fetc%2Fpasswd", "file-janitor-evil"} {
		rec := do(t, mux, http.MethodPost, "/api/workspaces/ws-1/capabilities/"+id+"/install", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("capability %q: status = %d, want 404 (body %s)", id, rec.Code, rec.Body.String())
		}
	}

	stored, _ := store.Get("ws-1")
	if len(stored.GetInstalledCapabilities()) != 0 {
		t.Fatalf("an unknown capability id was installed: %+v", stored.GetInstalledCapabilities())
	}
}

func TestEndpoints_RejectWrongMethod(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-1", "local"))
	h, _ := newTestHandler(t, store, stubUserProvider{id: "local"})
	mux := newTestMux(t, h)

	// The catalog is read-only over GET; installing requires POST. The mux's
	// method-scoped patterns are the request protection here.
	if rec := do(t, mux, http.MethodPost, "/api/workspaces/ws-1/capabilities", ""); rec.Code == http.StatusOK {
		t.Fatal("POST to the catalog endpoint should not succeed")
	}
	if rec := do(t, mux, http.MethodGet, "/api/workspaces/ws-1/capabilities/file-janitor/install", ""); rec.Code == http.StatusOK {
		t.Fatal("GET to the install endpoint should not succeed")
	}

	stored, _ := store.Get("ws-1")
	if len(stored.GetInstalledCapabilities()) != 0 {
		t.Fatal("a wrong-method request installed a capability")
	}
}

// TestHandler_UnavailableServiceDoesNotPanic covers FR-145: a capability whose
// dependencies failed to wire reports 503 rather than taking the workspace API
// down.
func TestHandler_UnavailableServiceDoesNotPanic(t *testing.T) {
	h := NewHandler(nil, newMemStore(ownedWorkspace("ws-1", "local")), stubUserProvider{id: "local"})
	mux := newTestMux(t, h)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/ws-1/capabilities"},
		{http.MethodPost, "/api/workspaces/ws-1/capabilities/file-janitor/install"},
	} {
		rec := do(t, mux, tc.method, tc.path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d, want 503", tc.method, tc.path, rec.Code)
		}
	}
}

func TestListCapabilities_ReportsInstalledRecordAndStatus(t *testing.T) {
	store := newMemStore(ownedWorkspace("ws-1", "local"))
	h, svc := newTestHandler(t, store, stubUserProvider{id: "local"})
	if err := svc.Registry().BindRuntime(workspace.CapabilityFileJanitor, stubRuntime{
		status: workspacecapability.Status{
			State:             workspacecapability.StatusReviewReady,
			Detail:            "12 ready for review",
			ReviewCount:       12,
			FolderDisplayName: "Downloads",
			Configured:        true,
		},
	}); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}
	mux := newTestMux(t, h)

	if rec := do(t, mux, http.MethodPost, "/api/workspaces/ws-1/capabilities/file-janitor/install", ""); rec.Code != http.StatusOK {
		t.Fatalf("install status = %d", rec.Code)
	}

	rec := do(t, mux, http.MethodGet, "/api/workspaces/ws-1/capabilities", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	item := decode(t, rec)["capabilities"].([]any)[0].(map[string]any)
	if item["installed"] != true {
		t.Fatal("catalog does not report the capability as installed")
	}
	status := item["status"].(map[string]any)
	if status["state"] != string(workspacecapability.StatusReviewReady) {
		t.Fatalf("status = %v", status["state"])
	}
	if status["review_count"].(float64) != 12 {
		t.Fatalf("review_count = %v", status["review_count"])
	}
	if status["folder_display_name"] != "Downloads" {
		t.Fatalf("folder_display_name = %v", status["folder_display_name"])
	}
}
