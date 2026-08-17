package runtimecapability

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestRuntimeCapabilityDemoJourney is the Group 2 self-demo: one compiled fake
// adapter drives the same service, persistence, and composite task gate the
// server wires, without any mock-backed UI claim.
func TestRuntimeCapabilityDemoJourney(t *testing.T) {
	adapter := &recordingAdapter{
		id:      "runtime_adapter",
		durable: DurableResult{State: DurableInProgress, ReasonCode: "setup_required", Summary: "Configure runtime."},
		live:    LiveResult{State: LiveOffline, ReasonCode: "app_offline", Summary: "The application is offline.", Action: &Action{Token: "check_runtime", Code: "check_runtime", Label: "Check runtime"}},
	}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	contract := contractWithRequirements("runtime")
	contract.Requirements[0].Adapter = adapter.ID()
	contract.OperatingModes = append(contract.OperatingModes, workspace.RuntimeOperatingMode{ID: "limited", Label: "Limited", Description: "Use files."})
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	service := NewService(store, registry)
	ctx := context.Background()

	limited, err := service.SelectMode(ctx, store.ws.ID, "limited")
	if err != nil || limited.DurableState != DurableConfigured || len(limited.Requirements) != 0 {
		t.Fatalf("limited mode: status=%+v err=%v", limited, err)
	}
	t.Logf("limited: durable=%s live=%s requirements=%d", limited.DurableState, limited.LiveState, len(limited.Requirements))

	assisted, err := service.SelectMode(ctx, store.ws.ID, "assisted")
	if err != nil || assisted.DurableState != DurableInProgress || assisted.FirstBlocker == nil {
		t.Fatalf("assisted setup: status=%+v err=%v", assisted, err)
	}
	t.Logf("assisted: durable=%s blocker=%s", assisted.DurableState, assisted.FirstBlocker.ReasonCode)

	adapter.durable = DurableResult{State: DurableConfigured, Summary: "Configured."}
	configured, err := service.Status(ctx, store.ws.ID)
	if err != nil || configured.DurableState != DurableConfigured {
		t.Fatalf("configured: status=%+v err=%v", configured, err)
	}
	t.Logf("configured: durable=%s live=%s", configured.DurableState, configured.LiveState)

	adapter.durable = DurableResult{State: DurableNeedsAttention, ReasonCode: "durable_regression", Summary: "Repair runtime.", Action: &Action{Token: "repair_runtime", Code: "repair_runtime", Label: "Repair runtime"}}
	regressed, err := service.Status(ctx, store.ws.ID)
	if err != nil || regressed.DurableState != DurableNeedsAttention || regressed.FirstBlocker == nil {
		t.Fatalf("regressed: status=%+v err=%v", regressed, err)
	}
	t.Logf("regressed: durable=%s blocker=%s", regressed.DurableState, regressed.FirstBlocker.ReasonCode)

	adapter.durable = DurableResult{State: DurableConfigured, Summary: "Configured."}
	offline, err := service.Recheck(ctx, store.ws.ID)
	if err != nil || offline.DurableState != DurableConfigured || offline.LiveState != LiveOffline {
		t.Fatalf("offline: status=%+v err=%v", offline, err)
	}
	t.Logf("offline: durable=%s live=%s", offline.DurableState, offline.LiveState)

	gate := workspace.NewCompositeTaskCapabilityGate(service)
	blocked := gate.CheckTaskCapabilities(store.ws.ID, []string{"runtime"})
	if blocked == nil || blocked.ReasonCode != "app_offline" || blocked.Repair == nil || blocked.Repair.Code != "check_runtime" {
		t.Fatalf("task blocker = %+v", blocked)
	}
	t.Logf("task: blocked=%s repair=%s", blocked.ReasonCode, blocked.Repair.Code)

	adapter.live = LiveResult{State: LiveAvailable, Summary: "Available."}
	if blocked := gate.CheckTaskCapabilities(store.ws.ID, []string{"runtime"}); blocked != nil {
		t.Fatalf("repaired task remained blocked: %+v", blocked)
	}
	t.Log("task: allowed after fresh available preflight")
}
