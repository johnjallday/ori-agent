package runtimecapabilityhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeStore struct {
	workspaces map[string]*workspace.Workspace
}

func (s *fakeStore) Get(id string) (*workspace.Workspace, error) {
	ws, ok := s.workspaces[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return ws, nil
}
func (s *fakeStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) { return s.Get(id) }
func (s *fakeStore) Update(id string, mutate func(*workspace.Workspace) error) error {
	ws, err := s.Get(id)
	if err != nil {
		return err
	}
	return mutate(ws)
}

type fixedUser string

func (u fixedUser) CurrentUserID(context.Context) (string, error) { return string(u), nil }

type actionAdapter struct {
	configured bool
	actions    int
	verifies   int
}

func (a *actionAdapter) ID() string { return "fixture_adapter" }
func (a *actionAdapter) EvaluateDurable(context.Context, runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	if a.configured {
		return runtimecapability.DurableResult{State: runtimecapability.DurableConfigured, Summary: "Configured."}, nil
	}
	return runtimecapability.DurableResult{
		State: runtimecapability.DurableInProgress, ReasonCode: "fixture_missing", Summary: "Configure the fixture.",
		Action: &runtimecapability.Action{Token: "configure_fixture", Code: "configure_fixture", Label: "Configure fixture"},
	}, nil
}
func (a *actionAdapter) CheckLive(context.Context, runtimecapability.EvaluationRequest) (runtimecapability.LiveResult, error) {
	return runtimecapability.LiveResult{State: runtimecapability.LiveAvailable, Summary: "Available."}, nil
}
func (a *actionAdapter) ConfirmAction(_ context.Context, request runtimecapability.ConfirmedActionRequest) error {
	if request.ActionToken != "configure_fixture" {
		return errors.New("wrong token")
	}
	a.actions++
	a.configured = true
	return nil
}
func (a *actionAdapter) Verify(context.Context, runtimecapability.VerificationRequest) (runtimecapability.VerificationResult, error) {
	a.verifies++
	a.configured = true
	return runtimecapability.VerificationResult{Succeeded: true}, nil
}

func runtimeHTTPWorkspace(id, owner string) *workspace.Workspace {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
	ws.ID = id
	ws.OwnerUserID = owner
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "fixture",
		RuntimeRequirements: &workspace.RuntimeRequirementsContract{
			SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
			OperatingModes: []workspace.RuntimeOperatingMode{
				{ID: "limited", Label: "Limited", Description: "Use files."},
				{ID: "assisted", Label: "Assisted", Description: "Use runtime.", Requires: []string{"fixture"}},
			},
			Requirements: []workspace.RuntimeRequirement{{Key: "fixture", Label: "Fixture", Description: "Configure it.", Adapter: "fixture_adapter"}},
		},
	})
	return ws
}

func newHTTPTestHandler(t *testing.T) (*Handler, *fakeStore, *actionAdapter) {
	t.Helper()
	store := &fakeStore{workspaces: map[string]*workspace.Workspace{
		"ws-mine":   runtimeHTTPWorkspace("ws-mine", userprofile.LocalUserID),
		"ws-theirs": runtimeHTTPWorkspace("ws-theirs", "someone-else"),
	}}
	adapter := &actionAdapter{}
	registry := runtimecapability.NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	service := runtimecapability.NewService(store, registry)
	return NewHandler(service, store, fixedUser(userprofile.LocalUserID)), store, adapter
}

func serve(t *testing.T, h *Handler, method, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	var decoded map[string]any
	if strings.Contains(recorder.Header().Get("Content-Type"), "application/json") && recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode response %d: %v: %s", recorder.Code, err, recorder.Body.String())
		}
	}
	return recorder, decoded
}

func runtimePayload(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	value, ok := body["runtime"].(map[string]any)
	if !ok {
		t.Fatalf("missing runtime payload: %#v", body)
	}
	return value
}

func TestRuntimeCapabilityHTTPStatusModeAndOwnership(t *testing.T) {
	handler, store, _ := newHTTPTestHandler(t)
	recorder, body := serve(t, handler, http.MethodGet, "/api/workspaces/ws-mine/runtime-capabilities", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	runtime := runtimePayload(t, body)
	if runtime["mode_selection_required"] != true || runtime["durable_state"] != runtimecapability.DurableNotStarted {
		t.Fatalf("unselected status = %#v", runtime)
	}

	recorder, _ = serve(t, handler, http.MethodGet, "/api/workspaces/ws-theirs/runtime-capabilities", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace status = %d", recorder.Code)
	}

	recorder, body = serve(t, handler, http.MethodPut, "/api/workspaces/ws-mine/runtime-capabilities/mode", `{"mode_id":"limited"}`)
	if recorder.Code != http.StatusOK || runtimePayload(t, body)["selected_mode_id"] != "limited" {
		t.Fatalf("select mode = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := store.workspaces["ws-mine"].GetRuntimeState(); got == nil || got.SelectedModeID != "limited" {
		t.Fatalf("mode selection was not persisted: %+v", got)
	}
	// The same closed transition is idempotent.
	recorder, _ = serve(t, handler, http.MethodPut, "/api/workspaces/ws-mine/runtime-capabilities/mode", `{"mode_id":"limited"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("repeat mode selection = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder, _ = serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/runtime-capabilities/mode", `{"mode_id":"assisted"}`)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method = %d, want 405", recorder.Code)
	}
}

func TestRuntimeCapabilityHTTPBodiesAreClosedAndBounded(t *testing.T) {
	handler, _, _ := newHTTPTestHandler(t)
	for name, body := range map[string]string{
		"adapter":  `{"mode_id":"assisted","adapter":"fixture_adapter"}`,
		"path":     `{"mode_id":"assisted","path":"/tmp/runner"}`,
		"endpoint": `{"mode_id":"assisted","port":8080}`,
		"probe":    `{"mode_id":"assisted","script":"check"}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder, _ := serve(t, handler, http.MethodPut, "/api/workspaces/ws-mine/runtime-capabilities/mode", body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("closed body accepted %s: %d %s", name, recorder.Code, recorder.Body.String())
			}
		})
	}

	recorder, _ := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/runtime-capabilities/recheck", `{"live":true}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("recheck accepted client live state: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder, _ = serve(t, handler, http.MethodPut, "/api/workspaces/ws-mine/runtime-capabilities/mode", `{"mode_id":"`+strings.Repeat("x", maxBodySize)+`"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized body = %d", recorder.Code)
	}
}

func TestRuntimeCapabilityHTTPConfirmedActionAndVerify(t *testing.T) {
	handler, store, adapter := newHTTPTestHandler(t)
	store.workspaces["ws-mine"].SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "assisted"})

	recorder, body := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/runtime-capabilities/requirements/fixture/actions/configure_fixture", "")
	if recorder.Code != http.StatusOK || adapter.actions != 1 || runtimePayload(t, body)["durable_state"] != runtimecapability.DurableConfigured {
		t.Fatalf("confirmed action = %d actions=%d body=%s", recorder.Code, adapter.actions, recorder.Body.String())
	}

	// A stale or invented token cannot run adapter behavior.
	recorder, _ = serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/runtime-capabilities/requirements/fixture/actions/configure_fixture", "")
	if recorder.Code != http.StatusConflict || adapter.actions != 1 {
		t.Fatalf("stale action = %d actions=%d", recorder.Code, adapter.actions)
	}

	adapter.configured = false
	recorder, _ = serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/runtime-capabilities/requirements/fixture/verify", "")
	if recorder.Code != http.StatusOK || adapter.verifies != 1 {
		t.Fatalf("verify = %d verifies=%d body=%s", recorder.Code, adapter.verifies, recorder.Body.String())
	}
	persisted := store.workspaces["ws-mine"].GetRuntimeState()
	if persisted == nil || len(persisted.RequirementStates) != 1 || persisted.RequirementStates[0].FirstVerifiedAt == nil {
		t.Fatalf("verification timestamp was not persisted: %+v", persisted)
	}
}

func TestRuntimeCapabilityHTTPGrantDelegationUnavailableIsHonest(t *testing.T) {
	handler, store, _ := newHTTPTestHandler(t)
	store.workspaces["ws-mine"].SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "assisted"})
	recorder, _ := serve(t, handler, http.MethodPost, "/api/workspaces/ws-mine/runtime-capabilities/requirements/fixture/grants", `{"agent_instance_id":"agent-1"}`)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "grant_unavailable") {
		t.Fatalf("unwired grant = %d: %s", recorder.Code, recorder.Body.String())
	}
}
