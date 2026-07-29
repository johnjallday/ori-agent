package setupwizardhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeStore struct {
	workspaces map[string]*workspace.Workspace
}

func (f *fakeStore) Get(id string) (*workspace.Workspace, error) {
	ws, ok := f.workspaces[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return ws, nil
}

func (f *fakeStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	return f.Get(id)
}

func (f *fakeStore) Update(id string, fn func(*workspace.Workspace) error) error {
	ws, ok := f.workspaces[id]
	if !ok {
		return errors.New("not found")
	}
	return fn(ws)
}

type fixedUser string

func (u fixedUser) CurrentUserID(context.Context) (string, error) { return string(u), nil }

// readyAdapter is a domain adapter that reports whatever the test needs and
// counts the actions it was asked to perform.
type readyAdapter struct {
	ready    bool
	confirms int
}

func (a *readyAdapter) ID() string { return "downloads_janitor" }

func (a *readyAdapter) Evaluate(context.Context, setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	return setupwizard.StepReadiness{Ready: a.ready}, nil
}

func (a *readyAdapter) Confirm(context.Context, setupwizard.StepRequest, setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	a.confirms++
	a.ready = true
	return setupwizard.StepReadiness{Ready: true}, nil
}

func testWizard() *workspace.SetupWizard {
	return &workspace.SetupWizard{
		Version: workspace.SetupWizardSchemaVersion,
		Title:   "Set up Downloads Janitor",
		Steps: []workspace.SetupWizardStep{
			{ID: "folder", Kind: workspace.SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true, Adapter: "downloads_janitor"},
			{ID: "summary", Kind: workspace.SetupStepKindSummary, Required: false},
		},
	}
}

// newTestHandler builds a handler over two workspaces: "ws-mine" (owned by the
// local user, wizard-enabled) and "ws-theirs" (someone else's).
func newTestHandler(t *testing.T) (*Handler, *readyAdapter, *fakeStore) {
	t.Helper()
	store := &fakeStore{workspaces: map[string]*workspace.Workspace{}}
	for id, owner := range map[string]string{"ws-mine": userprofile.LocalUserID, "ws-theirs": "someone-else"} {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
		ws.ID = id
		ws.OwnerUserID = owner
		ws.SetTemplateProvenance(&workspace.TemplateProvenance{
			TemplateID:            "downloads-janitor",
			TemplateName:          "Downloads Janitor",
			Builtin:               true,
			DirectoryRequirements: []workspace.DirectoryRequirement{{Key: "downloads-root", Label: "Downloads folder"}},
			SetupWizard:           testWizard(),
		})
		store.workspaces[id] = ws
	}
	plain := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "ws-plain"})
	plain.ID = "ws-plain"
	store.workspaces["ws-plain"] = plain

	adapter := &readyAdapter{}
	registry := setupwizard.NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := setupwizard.NewService(store, registry)
	return NewHandler(service, store, fixedUser(userprofile.LocalUserID)), adapter, store
}

func serve(t *testing.T, h *Handler, method, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(method, target, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var decoded map[string]any
	// ServeMux answers a method mismatch itself, in plain text; every response
	// this package writes is JSON.
	if rec.Body.Len() > 0 && strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("response is not JSON (%d): %s", rec.Code, rec.Body.String())
		}
	}
	return rec, decoded
}

func setupPayload(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	setup, ok := body["setup"].(map[string]any)
	if !ok {
		t.Fatalf("response carries no setup status: %+v", body)
	}
	return setup
}

func TestGetStatus_ReportsSetupRequiredBeforeAnythingIsDone(t *testing.T) {
	handler, adapter, _ := newTestHandler(t)

	rec, body := serve(t, handler, http.MethodGet, "/api/workspaces/ws-mine/setup-wizard", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	setup := setupPayload(t, body)
	if setup["state"] != workspace.SetupWizardStateNotStarted {
		t.Fatalf("expected not_started, got %v", setup["state"])
	}
	if setup["auto_open"] != true {
		t.Fatalf("a never-opened wizard should auto-open once: %+v", setup)
	}
	if setup["current_step_id"] != "folder" {
		t.Fatalf("expected to resume at the first required step, got %v", setup["current_step_id"])
	}
	if adapter.confirms != 0 {
		t.Fatal("reading status must perform no domain action")
	}
}

func TestEndpoints_RejectAnotherUsersWorkspace(t *testing.T) {
	handler, adapter, _ := newTestHandler(t)

	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/workspaces/ws-theirs/setup-wizard", ""},
		{http.MethodPost, "/api/workspaces/ws-theirs/setup-wizard/open", ""},
		{http.MethodPost, "/api/workspaces/ws-theirs/setup-wizard/dismiss", ""},
		{http.MethodPost, "/api/workspaces/ws-theirs/setup-wizard/recheck", ""},
		{http.MethodPost, "/api/workspaces/ws-theirs/setup-wizard/complete", ""},
		{http.MethodPost, "/api/workspaces/ws-theirs/setup-wizard/steps/folder/confirm", `{"option":"x"}`},
		{http.MethodPost, "/api/workspaces/ws-theirs/setup-wizard/steps/summary/skip", ""},
	} {
		rec, _ := serve(t, handler, tc.method, tc.target, tc.body)
		// Not-found, not forbidden: the API must not confirm that another
		// user's workspace exists.
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", tc.method, tc.target, rec.Code)
		}
	}
	if adapter.confirms != 0 {
		t.Fatal("a rejected request must never reach the domain")
	}
}

func TestEndpoints_UnknownWorkspaceIs404(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	rec, _ := serve(t, handler, http.MethodGet, "/api/workspaces/nope/setup-wizard", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGetStatus_WorkspaceWithoutWizardIsNotApplicable(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	rec, body := serve(t, handler, http.MethodGet, "/api/workspaces/ws-plain/setup-wizard", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	setup := setupPayload(t, body)
	if setup["applicable"] != false || setup["state"] != workspace.SetupWizardStateNotApplicable {
		t.Fatalf("expected a not_applicable status, got %+v", setup)
	}
}

func TestLifecycle_OpenDismissConfirmComplete(t *testing.T) {
	handler, adapter, _ := newTestHandler(t)

	if rec, body := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/open", ""); rec.Code != http.StatusOK {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	} else if setupPayload(t, body)["auto_open"] != false {
		t.Fatal("opening should spend the one auto-open")
	}

	rec, body := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/dismiss", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body.String())
	}
	setup := setupPayload(t, body)
	if setup["dismissed"] != true || setup["state"] == workspace.SetupWizardStateReady {
		t.Fatalf("dismissal must be recorded without implying readiness: %+v", setup)
	}

	// Completing before the required step passes is refused with a stable code.
	rec, body = serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/complete", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code, _ := body["code"].(string); code != "invalid_action" {
		t.Fatalf("expected a stable invalid_action code, got %+v", body)
	}

	rec, _ = serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/steps/folder/confirm", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}
	if adapter.confirms != 1 {
		t.Fatalf("expected exactly one domain action, got %d", adapter.confirms)
	}

	rec, body = serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/complete", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body.String())
	}
	setup = setupPayload(t, body)
	if setup["state"] != workspace.SetupWizardStateReady {
		t.Fatalf("expected ready, got %+v", setup)
	}
	// Every response is the freshly evaluated status, so a client never has to
	// re-fetch to find out what its own action did.
	if setup["auto_open"] != false || setup["completed_at"] == nil {
		t.Fatalf("a completed wizard should report its completion: %+v", setup)
	}
}

func TestConfirmStep_RejectsAnythingOutsideTheClosedBody(t *testing.T) {
	handler, adapter, _ := newTestHandler(t)

	// The one field a client may send is a short option token. Everything else
	// is refused rather than ignored, so nothing can be smuggled past this
	// boundary and silently dropped.
	for name, body := range map[string]string{
		"filesystem path":  `{"path":"/etc/passwd"}`,
		"adapter override": `{"adapter":"rm_rf"}`,
		"endpoint":         `{"url":"https://evil.test"}`,
		"payload":          `{"option":"file_only","extra":{"cmd":"rm -rf /"}}`,
	} {
		rec, _ := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/steps/folder/confirm", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", name, rec.Code)
		}
	}
	if adapter.confirms != 0 {
		t.Fatal("a rejected body must never reach the domain")
	}

	// An over-long option is refused too.
	long := `{"option":"` + string(bytes.Repeat([]byte("a"), maxOptionLength+1)) + `"}`
	if rec, _ := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/steps/folder/confirm", long); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an over-long option, got %d", rec.Code)
	}
}

func TestStepEndpoints_UnknownStepAndRequiredSkipAreStableErrors(t *testing.T) {
	handler, _, _ := newTestHandler(t)

	rec, body := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/steps/made-up/confirm", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an undeclared step, got %d", rec.Code)
	}
	if code, _ := body["code"].(string); code != "unknown_step" {
		t.Fatalf("expected unknown_step, got %+v", body)
	}

	rec, body = serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/steps/folder/skip", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when skipping a required step, got %d", rec.Code)
	}
	if code, _ := body["code"].(string); code != "invalid_action" {
		t.Fatalf("expected invalid_action, got %+v", body)
	}

	if rec, _ := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/steps/summary/skip", ""); rec.Code != http.StatusOK {
		t.Fatalf("skipping an optional step should succeed, got %d", rec.Code)
	}
}

func TestUnsupportedSnapshotIsAStableConflict(t *testing.T) {
	handler, adapter, store := newTestHandler(t)
	// A workspace.json recorded by a newer build.
	ws := store.workspaces["ws-mine"]
	provenance := ws.GetTemplateProvenance()
	provenance.SetupWizard.Version = workspace.SetupWizardSchemaVersion + 1
	ws.SetTemplateProvenance(provenance)

	rec, body := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/setup-wizard/steps/folder/confirm", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code, _ := body["code"].(string); code != "unsupported_setup_wizard" {
		t.Fatalf("expected unsupported_setup_wizard, got %+v", body)
	}
	if adapter.confirms != 0 {
		t.Fatal("no action may run against a snapshot this build cannot read")
	}
}

func TestRoutes_EnforceTheirMethods(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	for _, tc := range []struct{ method, target string }{
		{http.MethodPost, "/api/workspaces/ws-mine/setup-wizard"},
		{http.MethodGet, "/api/workspaces/ws-mine/setup-wizard/open"},
		{http.MethodGet, "/api/workspaces/ws-mine/setup-wizard/dismiss"},
		{http.MethodGet, "/api/workspaces/ws-mine/setup-wizard/complete"},
		{http.MethodDelete, "/api/workspaces/ws-mine/setup-wizard/steps/folder/confirm"},
	} {
		if rec, _ := serve(t, handler, tc.method, tc.target, ""); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", tc.method, tc.target, rec.Code)
		}
	}
}

func TestHandler_WithoutAServiceReports503(t *testing.T) {
	handler := NewHandler(nil, &fakeStore{workspaces: map[string]*workspace.Workspace{}}, fixedUser(userprofile.LocalUserID))
	rec, _ := serve(t, handler, http.MethodGet, "/api/workspaces/ws-mine/setup-wizard", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}
