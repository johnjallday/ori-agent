package runtimecapability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type grantAdapter struct {
	*recordingAdapter
	validationErr error
	validated     []GrantValidationRequest
}

func (a *grantAdapter) ValidateGrant(_ context.Context, request GrantValidationRequest) error {
	a.validated = append(a.validated, request)
	return a.validationErr
}

func runtimeGrantService(t *testing.T) (*Service, *runtimeStore, *grantAdapter) {
	t.Helper()
	adapter := &grantAdapter{recordingAdapter: &recordingAdapter{id: "runtime_adapter", durable: DurableResult{State: DurableInProgress}}}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	contract := contractWithRequirements("runtime")
	contract.Requirements[0].Adapter = adapter.ID()
	contract.OperatingModes = append(contract.OperatingModes, workspace.RuntimeOperatingMode{ID: "limited", Label: "Limited", Description: "Use files."})
	ws := runtimeWorkspace(contract)
	ws.AgentInstances = []workspace.AgentInstance{{ID: "agent-1", Name: "Producer", NodeID: "producer-1"}}
	store := &runtimeStore{ws: ws}
	service := NewService(store, registry)
	service.now = func() time.Time { return time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC) }
	return service, store, adapter
}

func TestRuntimeGrantTransitionChecksModeRequirementAgentAndAdapter(t *testing.T) {
	service, store, adapter := runtimeGrantService(t)
	ctx := context.Background()

	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); !errors.Is(err, ErrModeRequired) {
		t.Fatalf("unselected mode grant error = %v", err)
	}
	store.ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "limited"})
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); !errors.Is(err, ErrGrantNotAllowed) {
		t.Fatalf("limited mode grant error = %v", err)
	}
	store.ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "assisted"})
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "missing-agent"); !errors.Is(err, ErrAgentNotSupported) {
		t.Fatalf("missing agent grant error = %v", err)
	}
	adapter.validationErr = errors.New("provider or root unavailable")
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); !errors.Is(err, ErrGrantNotAllowed) {
		t.Fatalf("adapter refusal error = %v", err)
	}
	if store.ws.GetRuntimeState().HasActiveRuntimeGrant("runtime", "agent-1") {
		t.Fatal("refused grant was persisted")
	}

	adapter.validationErr = nil
	store.ws.AllowNativeMCPCLI = false
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	state := store.ws.GetRuntimeState()
	if !state.HasActiveRuntimeGrant("runtime", "agent-1") || len(adapter.validated) == 0 {
		t.Fatalf("grant not recorded/validated: %+v", state)
	}
	if store.ws.AllowNativeMCPCLI {
		t.Fatal("runtime grant toggled the broad native-MCP workspace setting")
	}
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatalf("idempotent grant: %v", err)
	}
	if len(store.ws.GetRuntimeState().Grants) != 1 {
		t.Fatalf("idempotent grant duplicated records: %+v", store.ws.GetRuntimeState().Grants)
	}
}

func TestRuntimeGrantRevokeAndUseAuditFieldsAreSafeAndBounded(t *testing.T) {
	service, store, _ := runtimeGrantService(t)
	events := captureRuntimeEvents(t)
	ctx := context.Background()
	store.ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "assisted"})

	// One denied decision and one allowed decision are both auditable.
	_, _ = service.Grant(ctx, store.ws.ID, "runtime", "missing-agent")
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatal(err)
	}

	allowed := map[string]bool{
		eventFieldName: true, eventFieldWorkspace: true, eventFieldAgent: true,
		eventFieldCapability: true, eventFieldOutcome: true,
	}
	seen := map[string]bool{}
	for _, fields := range *events {
		name, _ := fields[eventFieldName].(string)
		seen[name] = true
		for key, value := range fields {
			if !allowed[key] {
				t.Errorf("audit event %q includes field %q", name, key)
			}
			rendered := fmt.Sprint(value)
			for _, forbidden := range []string{"/", "\\", ":8080", ".lua", ".rpp", "token", "command"} {
				if strings.Contains(strings.ToLower(rendered), strings.ToLower(forbidden)) {
					t.Errorf("audit field %q leaked %q: %q", key, forbidden, rendered)
				}
			}
		}
	}
	for _, name := range []string{EventGrantDecision, EventRevokeDecision} {
		if !seen[name] {
			t.Errorf("missing audit event %s in %v", name, seen)
		}
	}
}

func TestRuntimeRevokeTakesEffectAfterModeOrProviderRegression(t *testing.T) {
	service, store, _ := runtimeGrantService(t)
	ctx := context.Background()
	store.ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "assisted"})
	if _, err := service.Grant(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatal(err)
	}
	// Switching away must not prevent revocation or delete runner/workspace data.
	state := store.ws.GetRuntimeState()
	state.SelectedModeID = "limited"
	store.ws.SetRuntimeState(state)
	if _, err := service.Revoke(ctx, store.ws.ID, "runtime", "agent-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	state = store.ws.GetRuntimeState()
	if state.HasActiveRuntimeGrant("runtime", "agent-1") || len(state.Grants) != 1 || state.Grants[0].RevokedAt == nil {
		t.Fatalf("revocation did not take effect immediately: %+v", state.Grants)
	}
}
