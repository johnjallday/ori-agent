package calendarhttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// readyCalendarWorkspace builds a workspace with a fully set-up (mapped,
// enabled, read-allowlisted) calendar binding, matching what applySave
// produces. Tests mutate the returned workspace/binding to exercise a single
// broken precondition at a time.
func readyCalendarWorkspace(id, ownerUserID string) (*agentworkspace.Workspace, agentworkspace.MCPBinding) {
	ws := newCalendarOpsWorkspace(id)
	ws.OwnerUserID = ownerUserID
	mapping := googleShapedMappingForTest()
	binding := agentworkspace.MCPBinding{
		ID:                 "cal-binding",
		ServerName:         "google-calendar",
		Enabled:            true,
		CapabilityMappings: []agentworkspace.CapabilityMapping{mapping},
		AllowedTools:       calendar.ReadOnlyAllowedTools(mapping),
		Config:             calendar.WriteBindingSettings(nil, calendar.BindingSettings{SelectedCalendarIDs: []string{"primary"}, Validated: true}),
	}
	if err := ws.UpsertMCPBinding(binding); err != nil {
		panic(err)
	}
	saved, _ := findCalendarBinding(ws)
	return ws, *saved
}

func newGatewayTestHandler(ws *agentworkspace.Workspace, status connectorStatus, userID string) *Handler {
	store := newFakeFolderStore()
	store.workspaces[ws.ID] = ws
	h := NewHandler(store, &fakeWorkspaceLister{}, nil, nil, userprofile.LocalUserProvider{})
	h.WithConnectorStatusFn(func(string) connectorStatus { return status })
	if userID != "" {
		h.provider = fakeUserProvider{id: userID}
	}
	return h
}

type fakeUserProvider struct{ id string }

func (f fakeUserProvider) CurrentUserID(context.Context) (string, error) { return f.id, nil }

var readyStatus = connectorStatus{Present: true, Connected: true, Status: mcp.StatusRunning}

func TestResolveGateway_HappyPath(t *testing.T) {
	ws, binding := readyCalendarWorkspace("ws-cal", "local")
	h := newGatewayTestHandler(ws, readyStatus, "local")

	gw, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr != nil {
		t.Fatalf("resolveGateway error: %v", gerr)
	}
	if gw.Binding.ID != binding.ID {
		t.Errorf("resolved binding ID = %q, want %q", gw.Binding.ID, binding.ID)
	}
	if gw.UserID != "local" {
		t.Errorf("resolved UserID = %q, want local", gw.UserID)
	}
}

func TestResolveGateway_MissingWorkspaceID(t *testing.T) {
	h := newGatewayTestHandler(&agentworkspace.Workspace{}, readyStatus, "local")
	_, gerr := h.resolveGateway(context.Background(), "")
	if gerr == nil || gerr.status != 400 {
		t.Fatalf("expected 400 for missing workspace_id, got %+v", gerr)
	}
}

func TestResolveGateway_UnknownWorkspaceIsNotFound(t *testing.T) {
	h := newGatewayTestHandler(&agentworkspace.Workspace{}, readyStatus, "local")
	_, gerr := h.resolveGateway(context.Background(), "does-not-exist")
	if gerr == nil || gerr.status != 404 {
		t.Fatalf("expected 404 for unknown workspace, got %+v", gerr)
	}
}

func TestResolveGateway_DeniesNonOwner(t *testing.T) {
	ws, _ := readyCalendarWorkspace("ws-cal", "alice")
	h := newGatewayTestHandler(ws, readyStatus, "mallory")

	_, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr == nil || gerr.status != 403 {
		t.Fatalf("expected 403 for a non-owner, got %+v", gerr)
	}
}

func TestResolveGateway_CrossWorkspaceDenied(t *testing.T) {
	// A user owns ws-a but requests gateway access scoped to ws-b, which they
	// do not own -- the gateway must resolve strictly per-workspace, not
	// "any workspace this user owns any calendar binding on".
	wsA, _ := readyCalendarWorkspace("ws-a", "alice")
	wsB, _ := readyCalendarWorkspace("ws-b", "bob")
	store := newFakeFolderStore()
	store.workspaces["ws-a"] = wsA
	store.workspaces["ws-b"] = wsB
	h := NewHandler(store, &fakeWorkspaceLister{}, nil, nil, fakeUserProvider{id: "alice"})
	h.WithConnectorStatusFn(func(string) connectorStatus { return readyStatus })

	if _, gerr := h.resolveGateway(context.Background(), "ws-a"); gerr != nil {
		t.Fatalf("alice should access her own workspace: %+v", gerr)
	}
	if _, gerr := h.resolveGateway(context.Background(), "ws-b"); gerr == nil || gerr.status != 403 {
		t.Fatalf("alice must be denied bob's workspace, got %+v", gerr)
	}
}

func TestResolveGateway_DisabledBindingIsConnectorMissing(t *testing.T) {
	ws, binding := readyCalendarWorkspace("ws-cal", "local")
	binding.Enabled = false
	_ = ws.UpsertMCPBinding(binding)
	h := newGatewayTestHandler(ws, readyStatus, "local")

	_, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr == nil || gerr.code != string(calendar.SetupConnectorMissing) {
		t.Fatalf("expected connector_missing for a disabled binding, got %+v", gerr)
	}
}

func TestResolveGateway_NoBindingIsConnectorMissing(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	h := newGatewayTestHandler(ws, readyStatus, "local")

	_, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr == nil || gerr.code != string(calendar.SetupConnectorMissing) {
		t.Fatalf("expected connector_missing with no binding at all, got %+v", gerr)
	}
}

func TestResolveGateway_MalformedMappingIsMappingRequired(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	// Missing list_events -- a required operation -- makes ValidateMapping fail.
	incomplete := agentworkspace.CapabilityMapping{
		Capability: calendar.CapabilityKey,
		Operations: map[string]agentworkspace.OperationMapping{
			calendar.OpListCalendars: {Tool: "cal_list", Fields: map[string]string{"id": "/id", "name": "/name"}},
		},
	}
	_ = ws.UpsertMCPBinding(agentworkspace.MCPBinding{
		ID: "cal-binding", ServerName: "google-calendar", Enabled: true,
		CapabilityMappings: []agentworkspace.CapabilityMapping{incomplete},
	})
	h := newGatewayTestHandler(ws, readyStatus, "local")

	_, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr == nil || gerr.code != string(calendar.SetupMappingRequired) {
		t.Fatalf("expected mapping_required for an invalid mapping, got %+v", gerr)
	}
}

func TestResolveGateway_AuthRequiredConnector(t *testing.T) {
	ws, _ := readyCalendarWorkspace("ws-cal", "local")
	h := newGatewayTestHandler(ws, connectorStatus{Present: true, AuthRequired: true}, "local")

	_, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr == nil || gerr.code != string(calendar.SetupAuthRequired) {
		t.Fatalf("expected auth_required, got %+v", gerr)
	}
}

func TestResolveGateway_StoppedConnectorIsAuthRequired(t *testing.T) {
	ws, _ := readyCalendarWorkspace("ws-cal", "local")
	h := newGatewayTestHandler(ws, connectorStatus{Present: true, Status: mcp.StatusStopped}, "local")

	_, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr == nil || gerr.code != string(calendar.SetupAuthRequired) {
		t.Fatalf("a present-but-not-connected server must read as auth_required, got %+v", gerr)
	}
}

func TestResolveGateway_DegradedConnector(t *testing.T) {
	ws, _ := readyCalendarWorkspace("ws-cal", "local")
	h := newGatewayTestHandler(ws, connectorStatus{Present: true, Degraded: true}, "local")

	_, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr == nil || gerr.code != string(calendar.SetupDegraded) || gerr.status != 503 {
		t.Fatalf("expected 503 degraded, got %+v", gerr)
	}
}

func TestResolveGateway_ConnectorRemovedFromRegistry(t *testing.T) {
	ws, _ := readyCalendarWorkspace("ws-cal", "local")
	h := newGatewayTestHandler(ws, connectorStatus{Present: false}, "local")

	_, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr == nil || gerr.code != string(calendar.SetupConnectorMissing) {
		t.Fatalf("expected connector_missing when the server was removed, got %+v", gerr)
	}
}

func TestResolveGateway_NeverExposesCredentials(t *testing.T) {
	// Structural guard: gatewayContext must not carry anything
	// credential-shaped. This is enforced by the type itself (Workspace,
	// Binding, Mapping only) -- this test documents that invariant so a
	// future field addition is a deliberate, reviewed decision.
	ws, _ := readyCalendarWorkspace("ws-cal", "local")
	h := newGatewayTestHandler(ws, readyStatus, "local")
	gw, gerr := h.resolveGateway(context.Background(), "ws-cal")
	if gerr != nil {
		t.Fatalf("resolveGateway error: %v", gerr)
	}
	if gw.Binding.Config != nil {
		if _, hasSecret := gw.Binding.Config["client_secret"]; hasSecret {
			t.Fatal("gatewayContext must never expose a client_secret")
		}
	}
}
