package workspacesurfacehttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	case "setting.validate":
		return workspacesurface.Result{Output: json.RawMessage(`{"accepted":true}`)}, nil
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
	handler      *Handler
	mux          *http.ServeMux
	registry     *workspacesurface.Registry
	runtime      *recordingRuntime
	user         *mutableUser
	surface      workspacesurface.RegisteredSurface
	registration workspacesurface.Registration
	workspaceID  string
	root         string
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
				OperationIDs: []string{"status.read", "greeting.create", "slow.read", "crash.read", "setting.validate"}, StatusOperation: "status.read",
				StateEnabled: true, ConfirmationEnabled: true, CloseEnabled: true,
				AskOriCapabilities: []string{"demo_runtime"}, SetupProviderID: "demo-runtime",
			}},
		}},
		Bindings: []workspacesurface.Binding{{
			CapabilityID: "demo-tools", SurfaceID: "main", AssetRoot: root, AssetVersion: "fixture-v1", EntryAsset: "ui/index.html", Runtime: runtime,
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
				"setting.validate": {
					ID:             "setting.validate",
					InputSchema:    json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}},"required":["enabled"],"additionalProperties":false}`),
					OutputSchema:   json.RawMessage(`{"type":"object","properties":{"accepted":{"type":"boolean"}},"required":["accepted"],"additionalProperties":false}`),
					MaxOutputBytes: 4096, Timeout: workspacesurface.TimeoutFast, Policy: workspacesurface.PolicyConfirmationRequired,
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
	handler.SetStateStore(workspacesurface.NewStateStore(t.TempDir()))
	mux := http.NewServeMux()
	handler.Register(mux)
	return &prototypeHTTPFixture{
		handler: handler, mux: mux, registry: registry, runtime: runtime,
		user: user, surface: surface, registration: registration, workspaceID: workspaceID, root: root,
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
	for _, forbidden := range []string{fixture.root, "ui/index.html", "/canonical/", "status.read", "greeting.create", "slow.read", "crash.read", "setting.validate"} {
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
	if asset.Code != http.StatusOK || asset.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("script asset = %d %s headers=%v", asset.Code, asset.Body.String(), asset.Header())
	}
	staleVersionURL := strings.Replace(opened.FrameURL, "/fixture-v1/", "/stale-version/", 1)
	if stale := fixture.serve(http.MethodGet, staleVersionURL, ""); stale.Code != http.StatusGone {
		t.Fatalf("stale asset version status = %d, body=%s", stale.Code, stale.Body.String())
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

func TestHostIntentsUseOnlySurfaceRegisteredRequirements(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	opened := fixture.openSession(t)
	askRecorder := fixture.serve(http.MethodPost, "/api/workspace-surfaces/intents",
		`{"session":"`+opened.Session+`","type":"ask_ori","context":"Explain this status"}`)
	var ask intentResponse
	if err := json.Unmarshal(askRecorder.Body.Bytes(), &ask); err != nil || askRecorder.Code != http.StatusOK || ask.Intent != "ask_ori" ||
		len(ask.RequiredCapabilities) != 1 || ask.RequiredCapabilities[0] != "demo_runtime" || ask.PluginContext != "Explain this status" {
		t.Fatalf("Ask Ori intent = %d %+v, %v", askRecorder.Code, ask, err)
	}
	setup := fixture.serve(http.MethodPost, "/api/workspace-surfaces/intents",
		`{"session":"`+opened.Session+`","type":"open_setup"}`)
	var setupIntent intentResponse
	if err := json.Unmarshal(setup.Body.Bytes(), &setupIntent); err != nil || setupIntent.ProviderID != "demo-runtime" {
		t.Fatalf("Setup intent = %d %+v, %v", setup.Code, setupIntent, err)
	}
	injected := fixture.serve(http.MethodPost, "/api/workspace-surfaces/intents",
		`{"session":"`+opened.Session+`","type":"ask_ori","context":"x","required_capabilities":["admin"]}`)
	if injected.Code != http.StatusBadRequest {
		t.Fatalf("injected intent authority status = %d, body=%s", injected.Code, injected.Body.String())
	}
}

func TestNamespacedStatePreservesMissingNullAndRevisionConflicts(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	opened := fixture.openSession(t)
	state := func(body string) *httptest.ResponseRecorder {
		return fixture.serve(http.MethodPost, "/api/workspace-surfaces/state", body)
	}
	missing := state(`{"session":"` + opened.Session + `","action":"get","key":"display"}`)
	var value workspacesurface.StateValue
	if err := json.Unmarshal(missing.Body.Bytes(), &value); err != nil || missing.Code != http.StatusOK || value.Found || value.Revision != "0" {
		t.Fatalf("missing state = %d %+v, %v", missing.Code, value, err)
	}
	saved := state(`{"session":"` + opened.Session + `","action":"set","key":"display","schema_version":1,"expected_revision":"0","value":null}`)
	if err := json.Unmarshal(saved.Body.Bytes(), &value); err != nil || saved.Code != http.StatusOK || !value.Found || string(value.Value) != "null" || value.Revision != "1" {
		t.Fatalf("saved state = %d %+v, %v", saved.Code, value, err)
	}
	conflict := state(`{"session":"` + opened.Session + `","action":"set","key":"display","schema_version":1,"expected_revision":"0","value":{}}`)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"state_conflict"`) {
		t.Fatalf("state conflict = %d %s", conflict.Code, conflict.Body.String())
	}
	got := state(`{"session":"` + opened.Session + `","action":"get","key":"display"}`)
	if err := json.Unmarshal(got.Body.Bytes(), &value); err != nil || !value.Found || string(value.Value) != "null" {
		t.Fatalf("explicit null read = %d %+v, %v", got.Code, value, err)
	}
}

func TestHostConfirmationBindsPayloadAndIsSingleUse(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	opened := fixture.openSession(t)
	invoke := func(payload string) *httptest.ResponseRecorder {
		return fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", payload)
	}
	request := `{"session":"` + opened.Session + `","operation_id":"setting.validate","input":{"enabled":true}}`
	pending := invoke(request)
	if pending.Code != http.StatusConflict || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("pending confirmation = %d %s; calls=%d", pending.Code, pending.Body.String(), fixture.runtime.invokeCount())
	}
	var pendingError errorResponse
	if err := json.Unmarshal(pending.Body.Bytes(), &pendingError); err != nil || pendingError.Code != "confirmation_required" || pendingError.ConfirmationID == "" {
		t.Fatalf("pending response = %+v, %v", pendingError, err)
	}
	approved := fixture.serve(http.MethodPost, "/api/workspace-surfaces/confirmations",
		`{"session":"`+opened.Session+`","confirmation_id":"`+pendingError.ConfirmationID+`"}`)
	var approval confirmationResponse
	if err := json.Unmarshal(approved.Body.Bytes(), &approval); err != nil || approved.Code != http.StatusOK || approval.ConfirmationToken == "" {
		t.Fatalf("approval = %d %+v, %v", approved.Code, approval, err)
	}
	confirmedRequest := `{"session":"` + opened.Session + `","operation_id":"setting.validate","input":{"enabled":true},"confirmation_token":"` + approval.ConfirmationToken + `"}`
	confirmed := invoke(confirmedRequest)
	if confirmed.Code != http.StatusOK || fixture.runtime.invokeCount() != 1 || !strings.Contains(confirmed.Body.String(), `"accepted":true`) {
		t.Fatalf("confirmed = %d %s; calls=%d", confirmed.Code, confirmed.Body.String(), fixture.runtime.invokeCount())
	}
	replay := invoke(confirmedRequest)
	if replay.Code != http.StatusConflict || fixture.runtime.invokeCount() != 1 {
		t.Fatalf("replay = %d %s; calls=%d", replay.Code, replay.Body.String(), fixture.runtime.invokeCount())
	}
}

func TestConfirmationRejectsChangedPayloadAndCancellationBeforeService(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	opened := fixture.openSession(t)
	initial := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+opened.Session+`","operation_id":"setting.validate","input":{"enabled":true}}`)
	var pending errorResponse
	if err := json.Unmarshal(initial.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	approved := fixture.serve(http.MethodPost, "/api/workspace-surfaces/confirmations",
		`{"session":"`+opened.Session+`","confirmation_id":"`+pending.ConfirmationID+`"}`)
	var approval confirmationResponse
	if err := json.Unmarshal(approved.Body.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	changed := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+opened.Session+`","operation_id":"setting.validate","input":{"enabled":false},"confirmation_token":"`+approval.ConfirmationToken+`"}`)
	if changed.Code != http.StatusConflict || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("changed payload = %d %s; calls=%d", changed.Code, changed.Body.String(), fixture.runtime.invokeCount())
	}

	initial = fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+opened.Session+`","operation_id":"setting.validate","input":{"enabled":true}}`)
	if err := json.Unmarshal(initial.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	canceled := fixture.serve(http.MethodDelete, "/api/workspace-surfaces/confirmations",
		`{"session":"`+opened.Session+`","confirmation_id":"`+pending.ConfirmationID+`"}`)
	if canceled.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", canceled.Code, canceled.Body.String())
	}
	approveCanceled := fixture.serve(http.MethodPost, "/api/workspace-surfaces/confirmations",
		`{"session":"`+opened.Session+`","confirmation_id":"`+pending.ConfirmationID+`"}`)
	if approveCanceled.Code != http.StatusConflict || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("approve canceled = %d %s; calls=%d", approveCanceled.Code, approveCanceled.Body.String(), fixture.runtime.invokeCount())
	}
}

func TestRuntimeGrantDenialStopsBeforeContextOrService(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	fixture.handler.SetOperationAuthorizer(OperationAuthorizerFunc(func(context.Context, string, string, workspacesurface.RegisteredSurface, workspacesurface.Operation) error {
		return errors.New("grant missing for /private/runtime")
	}))
	opened := fixture.openSession(t)
	response := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
		`{"session":"`+opened.Session+`","operation_id":"greeting.create","input":{"name":"Ori"}}`)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"runtime_grant_required"`) || fixture.runtime.invokeCount() != 0 {
		t.Fatalf("grant denial = %d %s; calls=%d", response.Code, response.Body.String(), fixture.runtime.invokeCount())
	}
	if strings.Contains(response.Body.String(), "/private/runtime") {
		t.Fatalf("grant denial leaked internal detail: %s", response.Body.String())
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

func TestCapabilityRemovalAndServiceRestartInvalidateBoundSessions(t *testing.T) {
	for _, test := range []struct {
		name       string
		invalidate func(*prototypeHTTPFixture) int
	}{
		{
			name: "capability removal",
			invalidate: func(fixture *prototypeHTTPFixture) int {
				return fixture.handler.InvalidateCapability(fixture.workspaceID, "workspace-surface-demo", "demo-tools")
			},
		},
		{
			name: "service restart",
			invalidate: func(fixture *prototypeHTTPFixture) int {
				return fixture.handler.InvalidateServiceRestart("workspace-surface-demo", 7)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPrototypeHTTPFixture(t)
			opened := fixture.openSession(t)
			if count := test.invalidate(fixture); count != 1 {
				t.Fatalf("invalidated sessions = %d", count)
			}
			if frame := fixture.serve(http.MethodGet, opened.FrameURL, ""); frame.Code != http.StatusNotFound {
				t.Fatalf("invalidated frame status = %d", frame.Code)
			}
			operation := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations",
				`{"session":"`+opened.Session+`","operation_id":"greeting.create","input":{"name":"Ori"}}`)
			if operation.Code != http.StatusNotFound || fixture.runtime.invokeCount() != 0 {
				t.Fatalf("invalidated operation = %d %s; calls=%d", operation.Code, operation.Body.String(), fixture.runtime.invokeCount())
			}
		})
	}
}

func TestConcurrentLifecycleChangesAreSafeDuringCatalogAssetStatusAndOperationRequests(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	opened := fixture.openSession(t)
	operationBody := `{"session":"` + opened.Session + `","operation_id":"greeting.create","input":{"name":"Ori"}}`
	openPath := "/api/workspaces/" + fixture.workspaceID + "/surfaces/" + url.PathEscape(fixture.surface.Key) + "/sessions"
	catalogPath := "/api/workspaces/" + fixture.workspaceID + "/surfaces"

	var wait sync.WaitGroup
	errorsSeen := make(chan string, 500)
	runRequests := func(name, method, path, body string, allowed ...int) {
		defer wait.Done()
		for range 75 {
			response := fixture.serve(method, path, body)
			accepted := false
			for _, status := range allowed {
				accepted = accepted || response.Code == status
			}
			if !accepted {
				errorsSeen <- name + ": unexpected HTTP status " + fmt.Sprint(response.Code)
			}
		}
	}
	wait.Add(5)
	go runRequests("catalog/status", http.MethodGet, catalogPath, "", http.StatusOK)
	go runRequests("asset", http.MethodGet, opened.FrameURL, "", http.StatusOK, http.StatusNotFound)
	go runRequests("operation", http.MethodPost, "/api/workspace-surfaces/operations", operationBody, http.StatusOK, http.StatusNotFound)
	go runRequests("session", http.MethodPost, openPath, "", http.StatusCreated, http.StatusNotFound)
	go func() {
		defer wait.Done()
		for range 75 {
			fixture.handler.InvalidateOwner(fixture.surface.Owner.ID, fixture.surface.Owner.Generation)
			if err := fixture.registry.UnregisterOwner(fixture.surface.Owner.Kind, fixture.surface.Owner.ID, fixture.surface.Owner.Generation); err != nil {
				errorsSeen <- "unregister: " + err.Error()
				return
			}
			if err := fixture.registry.RegisterTrusted(fixture.registration); err != nil {
				errorsSeen <- "register: " + err.Error()
				return
			}
		}
	}()
	wait.Wait()
	close(errorsSeen)
	for message := range errorsSeen {
		t.Error(message)
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
