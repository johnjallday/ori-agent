package workspacesurfacehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

type testWorkspaceStore struct {
	workspaces map[string]*workspace.Workspace
}

func (s testWorkspaceStore) Get(id string) (*workspace.Workspace, error) {
	return s.workspaces[id], nil
}

type mutableUser struct {
	mu  sync.Mutex
	id  string
	err error
}

func (u *mutableUser) CurrentUserID(context.Context) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.id, u.err
}

func (u *mutableUser) set(id string, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.id, u.err = id, err
}

type attachmentSet struct {
	values map[string]bool
}

func (a attachmentSet) Attached(_ context.Context, workspaceID string, surface workspacesurface.RegisteredSurface) bool {
	return a.values[workspaceID+"\x00"+surface.Key]
}

type recordingRuntime struct {
	mu          sync.Mutex
	statusCalls int
	invocations []workspacesurface.Invocation
}

func (r *recordingRuntime) Status(context.Context, workspacesurface.WorkspaceContext) (workspacesurface.StationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusCalls++
	return workspacesurface.StationStatus{
		State: workspacesurface.StationReady, Value: "Available",
		Description: "The demo service is ready.", CheckedAt: "2026-08-24T12:00:00Z",
	}, nil
}

func (r *recordingRuntime) Invoke(ctx context.Context, invocation workspacesurface.Invocation) (workspacesurface.Result, error) {
	r.mu.Lock()
	invocation.Input = append(json.RawMessage(nil), invocation.Input...)
	r.invocations = append(r.invocations, invocation)
	r.mu.Unlock()
	switch invocation.Operation {
	case "slow.read":
		<-ctx.Done()
		return workspacesurface.Result{}, ctx.Err()
	case "crash.read":
		return workspacesurface.Result{}, errors.New("service crashed at /Users/example/plugin on localhost:2307")
	default:
		return workspacesurface.Result{Output: json.RawMessage(`{"message":"Hello, Ori."}`)}, nil
	}
}

func (r *recordingRuntime) invokeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.invocations)
}

func (r *recordingRuntime) lastInvocation() workspacesurface.Invocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invocations[len(r.invocations)-1]
}

type prototypeHTTPFixture struct {
	handler     *Handler
	mux         *http.ServeMux
	registry    *workspacesurface.Registry
	runtime     *recordingRuntime
	user        *mutableUser
	surface     workspacesurface.RegisteredSurface
	workspaceID string
	root        string
}

func newPrototypeHTTPFixture(t *testing.T) *prototypeHTTPFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ui"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ui", "index.html"), []byte("<!doctype html><title>Surface Demo</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ui", "app.js"), []byte("globalThis.demoLoaded = true;"), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime := &recordingRuntime{}
	registry := workspacesurface.NewRegistry()
	owner := workspacesurface.Owner{
		Kind: workspacesurface.OwnerPlugin, ID: "workspace-surface-demo", Version: "0.1.0",
		Generation: 7, ProtocolMin: 1, ProtocolMax: 1,
	}
	registration := workspacesurface.Registration{
		Owner: owner,
		Capabilities: []workspacesurface.Capability{{
			ID: "demo-tools", Version: 1,
			Display: workspacesurface.Display{Name: "Surface Demo", Description: "A harmless demo."},
			Surfaces: []workspacesurface.Surface{{
				ID: "main", Label: "Surface Demo", Description: "Open the harmless demo surface.",
				Icon: workspacesurface.Icon{Kind: "host", Value: "puzzle"}, Placement: "map_modal",
				Modal:        workspacesurface.Modal{Width: 720, Height: 560},
				Polling:      workspacesurface.Polling{MapSeconds: 5, OpenSeconds: 1},
				OperationIDs: []string{"status.read", "greeting.create", "slow.read", "crash.read"}, StatusOperation: "status.read",
			}},
		}},
		Bindings: []workspacesurface.Binding{{
			CapabilityID: "demo-tools", SurfaceID: "main", AssetRoot: root, EntryAsset: "ui/index.html", Runtime: runtime,
			Operations: map[string]workspacesurface.Operation{
				"status.read": {
					ID:             "status.read",
					InputSchema:    json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
					OutputSchema:   json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
					MaxOutputBytes: 4096, Timeout: workspacesurface.TimeoutFast, Policy: workspacesurface.PolicyReadOnly,
				},
				"greeting.create": {
					ID:             "greeting.create",
					InputSchema:    json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","maxLength":80}},"required":["name"],"additionalProperties":false}`),
					OutputSchema:   json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","maxLength":160}},"required":["message"],"additionalProperties":false}`),
					MaxOutputBytes: 4096, Timeout: workspacesurface.TimeoutFast, Policy: workspacesurface.PolicyReadOnly,
				},
				"slow.read": {
					ID:             "slow.read",
					InputSchema:    json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
					OutputSchema:   json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
					MaxOutputBytes: 4096, Timeout: workspacesurface.TimeoutFast, Policy: workspacesurface.PolicyReadOnly,
				},
				"crash.read": {
					ID:             "crash.read",
					InputSchema:    json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
					OutputSchema:   json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
					MaxOutputBytes: 4096, Timeout: workspacesurface.TimeoutFast, Policy: workspacesurface.PolicyReadOnly,
				},
			},
		}},
	}
	if err := registry.RegisterTrusted(registration); err != nil {
		t.Fatal(err)
	}
	surface, ok := registry.Surface(workspacesurface.SurfaceKey(owner, "demo-tools", "main"))
	if !ok {
		t.Fatal("registered surface missing")
	}

	const workspaceID = "workspace-owned"
	user := &mutableUser{id: "owner-a"}
	attachments := attachmentSet{values: map[string]bool{workspaceID + "\x00" + surface.Key: true}}
	store := testWorkspaceStore{workspaces: map[string]*workspace.Workspace{
		workspaceID:            {ID: workspaceID, OwnerUserID: "owner-a"},
		"workspace-foreign":    {ID: "workspace-foreign", OwnerUserID: "owner-b"},
		"workspace-unattached": {ID: "workspace-unattached", OwnerUserID: "owner-a"},
	}}
	contexts := ContextResolverFunc(func(_ context.Context, gotWorkspaceID string, _ workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error) {
		if gotWorkspaceID != workspaceID {
			return workspacesurface.WorkspaceContext{}, errors.New("no context")
		}
		return workspacesurface.WorkspaceContext{
			WorkspaceID:   "browser-must-not-select-this",
			WorkspaceRoot: "/canonical/workspaces/owned", ProjectEntry: "/canonical/workspaces/owned/song.txt",
			PluginDataRoot: "/canonical/plugin-data/demo",
		}, nil
	})
	handler := NewHandler(registry, store, user, attachments, contexts)
	mux := http.NewServeMux()
	handler.Register(mux)
	return &prototypeHTTPFixture{
		handler: handler, mux: mux, registry: registry, runtime: runtime,
		user: user, surface: surface, workspaceID: workspaceID, root: root,
	}
}

func (f *prototypeHTTPFixture) serve(method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	f.mux.ServeHTTP(recorder, request)
	return recorder
}

func (f *prototypeHTTPFixture) openSession(t *testing.T) openSessionResponse {
	t.Helper()
	path := "/api/workspaces/" + f.workspaceID + "/surfaces/" + url.PathEscape(f.surface.Key) + "/sessions"
	recorder := f.serve(http.MethodPost, path, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response openSessionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Session == "" || response.FrameURL == "" || response.ExpiresAt.IsZero() {
		t.Fatalf("open response = %+v", response)
	}
	return response
}

func TestCatalogReturnsOnlyOwnedAttachedSanitizedSurfaces(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	recorder := fixture.serve(http.MethodGet, "/api/workspaces/"+fixture.workspaceID+"/surfaces", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("catalog status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response catalogResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Surfaces) != 1 {
		t.Fatalf("catalog = %+v", response)
	}
	item := response.Surfaces[0]
	if item.Key != fixture.surface.Key || item.Status.State != workspacesurface.StationReady || !item.Available {
		t.Fatalf("catalog item = %+v", item)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{fixture.root, "ui/index.html", "/canonical/", "status.read", "greeting.create", "slow.read", "crash.read"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("catalog leaked %q: %s", forbidden, body)
		}
	}

	unattached := fixture.serve(http.MethodGet, "/api/workspaces/workspace-unattached/surfaces", "")
	if unattached.Code != http.StatusOK || !strings.Contains(unattached.Body.String(), `"surfaces":[]`) {
		t.Fatalf("unattached catalog = %d %s", unattached.Code, unattached.Body.String())
	}
	foreign := fixture.serve(http.MethodGet, "/api/workspaces/workspace-foreign/surfaces", "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign catalog status = %d", foreign.Code)
	}
}

func TestOpenSessionServesOnlyCanonicalBoundedAssets(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	opened := fixture.openSession(t)

	entry := fixture.serve(http.MethodGet, opened.FrameURL, "")
	if entry.Code != http.StatusOK || !strings.Contains(entry.Body.String(), "Surface Demo") {
		t.Fatalf("frame entry = %d %s", entry.Code, entry.Body.String())
	}
	if !strings.Contains(entry.Header().Get("Content-Security-Policy"), "connect-src 'none'") ||
		entry.Header().Get("Cache-Control") != "no-store" || entry.Header().Get("Access-Control-Allow-Origin") != "null" {
		t.Fatalf("frame security headers = %#v", entry.Header())
	}
	appURL := strings.TrimSuffix(opened.FrameURL, "index.html") + "app.js"
	asset := fixture.serve(http.MethodGet, appURL, "")
	if asset.Code != http.StatusOK || asset.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("script asset = %d %s headers=%v", asset.Code, asset.Body.String(), asset.Header())
	}
	missing := fixture.serve(http.MethodGet, strings.TrimSuffix(opened.FrameURL, "index.html")+"missing.js", "")
	if missing.Code != http.StatusNotFound || strings.Contains(missing.Body.String(), fixture.root) {
		t.Fatalf("missing asset = %d %s", missing.Code, missing.Body.String())
	}
}

func TestDeclaredOperationReceivesAuthoritativeWorkspaceContext(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	opened := fixture.openSession(t)
	body := `{"session":"` + opened.Session + `","operation_id":"greeting.create","input":{"name":"Ori"}}`
	recorder := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", body)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Hello, Ori.") {
		t.Fatalf("operation = %d %s", recorder.Code, recorder.Body.String())
	}
	if fixture.runtime.invokeCount() != 1 {
		t.Fatalf("runtime calls = %d", fixture.runtime.invokeCount())
	}
	invocation := fixture.runtime.lastInvocation()
	if invocation.Workspace.WorkspaceID != fixture.workspaceID || invocation.Workspace.WorkspaceRoot != "/canonical/workspaces/owned" || invocation.Workspace.ProjectEntry != "/canonical/workspaces/owned/song.txt" {
		t.Fatalf("authoritative context = %+v", invocation.Workspace)
	}
	if string(invocation.Input) != `{"name":"Ori"}` {
		t.Fatalf("service input = %s", invocation.Input)
	}
}

func TestRejectedIdentityCapabilitySessionAndOperationNeverReachRuntime(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)

	tests := []struct {
		name       string
		prepare    func() string
		method     string
		path       string
		wantStatus int
	}{
		{
			name: "unknown workspace", method: http.MethodPost,
			path:       "/api/workspaces/missing/surfaces/" + url.PathEscape(fixture.surface.Key) + "/sessions",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "foreign user", method: http.MethodPost,
			path:       "/api/workspaces/workspace-foreign/surfaces/" + url.PathEscape(fixture.surface.Key) + "/sessions",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "capability not attached", method: http.MethodPost,
			path:       "/api/workspaces/workspace-unattached/surfaces/" + url.PathEscape(fixture.surface.Key) + "/sessions",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "unknown session", method: http.MethodPost, path: "/api/workspace-surfaces/operations",
			prepare: func() string {
				return `{"session":"unknown","operation_id":"greeting.create","input":{"name":"Ori"}}`
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := ""
			if test.prepare != nil {
				body = test.prepare()
			}
			recorder := fixture.serve(test.method, test.path, body)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			if fixture.runtime.invokeCount() != 0 {
				t.Fatalf("rejected request reached runtime %d time(s)", fixture.runtime.invokeCount())
			}
		})
	}

	opened := fixture.openSession(t)
	unknownOperation := `{"session":"` + opened.Session + `","operation_id":"admin.shell","input":{}}`
	recorder := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", unknownOperation)
	if recorder.Code != http.StatusNotFound || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("unknown operation = %d %s; calls=%d", recorder.Code, recorder.Body.String(), fixture.runtime.invokeCount())
	}

	override := `{"session":"` + opened.Session + `","operation_id":"greeting.create","input":{"name":"Ori","workspace_id":"other"}}`
	recorder = fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", override)
	if recorder.Code != http.StatusBadRequest || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("workspace override = %d %s; calls=%d", recorder.Code, recorder.Body.String(), fixture.runtime.invokeCount())
	}

	fixture.user.set("owner-b", nil)
	validShape := `{"session":"` + opened.Session + `","operation_id":"greeting.create","input":{"name":"Ori"}}`
	recorder = fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", validShape)
	if recorder.Code != http.StatusNotFound || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("session user mismatch = %d %s; calls=%d", recorder.Code, recorder.Body.String(), fixture.runtime.invokeCount())
	}
}

func TestExpiredMalformedAndOversizedRequestsFailClosed(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	current := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	fixture.handler.sessions.now = func() time.Time { return current }
	fixture.handler.sessions.ttl = time.Second
	opened := fixture.openSession(t)
	current = current.Add(2 * time.Second)

	expired := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+opened.Session+`","operation_id":"greeting.create","input":{"name":"Ori"}}`)
	if expired.Code != http.StatusNotFound || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("expired session = %d %s; calls=%d", expired.Code, expired.Body.String(), fixture.runtime.invokeCount())
	}
	malformed := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", `{"session":`)
	if malformed.Code != http.StatusBadRequest || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("malformed body = %d %s; calls=%d", malformed.Code, malformed.Body.String(), fixture.runtime.invokeCount())
	}
	oversized := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+strings.Repeat("x", maxBrokerBodyBytes)+`"}`)
	if oversized.Code != http.StatusBadRequest || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("oversized body = %d %s; calls=%d", oversized.Code, oversized.Body.String(), fixture.runtime.invokeCount())
	}
}

func TestServiceTimeoutAndCrashAreSanitized(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	fixture.handler.timeoutFor = func(workspacesurface.TimeoutClass) time.Duration { return 20 * time.Millisecond }
	opened := fixture.openSession(t)

	timedOut := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+opened.Session+`","operation_id":"slow.read","input":{}}`)
	if timedOut.Code != http.StatusGatewayTimeout || !strings.Contains(timedOut.Body.String(), `"service_timeout"`) {
		t.Fatalf("timeout = %d %s", timedOut.Code, timedOut.Body.String())
	}
	crashed := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+opened.Session+`","operation_id":"crash.read","input":{}}`)
	if crashed.Code != http.StatusBadGateway || !strings.Contains(crashed.Body.String(), `"service_unavailable"`) {
		t.Fatalf("crash = %d %s", crashed.Code, crashed.Body.String())
	}
	for _, secret := range []string{"/Users/example", "localhost", "2307", "crashed at"} {
		if strings.Contains(crashed.Body.String(), secret) {
			t.Fatalf("crash response leaked %q: %s", secret, crashed.Body.String())
		}
	}
	if fixture.runtime.invokeCount() != 2 {
		t.Fatalf("runtime calls = %d, want timeout and crash dispatch only", fixture.runtime.invokeCount())
	}
}

func TestCloseSessionInvalidatesFrameAndOperation(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	opened := fixture.openSession(t)
	closeBody := `{"session":"` + opened.Session + `"}`
	closed := fixture.serve(http.MethodDelete, "/api/workspace-surfaces/sessions", closeBody)
	if closed.Code != http.StatusNoContent {
		t.Fatalf("close = %d %s", closed.Code, closed.Body.String())
	}
	if frame := fixture.serve(http.MethodGet, opened.FrameURL, ""); frame.Code != http.StatusNotFound {
		t.Fatalf("closed frame status = %d", frame.Code)
	}
	operation := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+opened.Session+`","operation_id":"greeting.create","input":{"name":"Ori"}}`)
	if operation.Code != http.StatusNotFound || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("closed operation = %d %s; calls=%d", operation.Code, operation.Body.String(), fixture.runtime.invokeCount())
	}
}
