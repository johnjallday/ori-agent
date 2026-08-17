package runtimecapability

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestRuntimeTaskEvaluatorClaimsOnlyPersistedRuntimeKeys(t *testing.T) {
	plain := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plain"})
	plain.ID = "ws-plain"
	service := NewService(&runtimeStore{ws: plain}, NewRegistry())
	for _, key := range []string{"planning", "filesystem", "reaper_live_control"} {
		claimed, blocked := service.EvaluateTaskCapability(plain.ID, key)
		if claimed || blocked != nil {
			t.Fatalf("plain workspace key %q was claimed: claimed=%v blocked=%+v", key, claimed, blocked)
		}
	}

	contract := contractWithRequirements("runtime")
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	service = NewService(store, NewRegistry())
	if claimed, _ := service.EvaluateTaskCapability(store.ws.ID, "planning"); claimed {
		t.Fatal("ordinary planning key was claimed")
	}
	claimed, blocked := service.EvaluateTaskCapability(store.ws.ID, "runtime")
	if !claimed || blocked == nil || blocked.ReasonCode != ReasonAdapterUnavailable {
		t.Fatalf("declared runtime key = claimed %v blocked %+v", claimed, blocked)
	}
}

func TestRuntimeTaskEvaluatorIsModeAwareAndRunsFreshLiveCheck(t *testing.T) {
	adapter := &recordingAdapter{
		id:      "runtime_adapter",
		durable: DurableResult{State: DurableConfigured, Summary: "Configured."},
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

	claimed, blocked := service.EvaluateTaskCapability(store.ws.ID, "runtime")
	if !claimed || blocked == nil || blocked.ReasonCode != ReasonModeSelectionRequired {
		t.Fatalf("unselected mode preflight = %v %+v", claimed, blocked)
	}
	store.ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "limited"})
	_, blocked = service.EvaluateTaskCapability(store.ws.ID, "runtime")
	if blocked == nil || blocked.ReasonCode != ReasonModeNotEnabled || blocked.Repair == nil || blocked.Repair.Code != "review_runtime_setup" {
		t.Fatalf("limited mode preflight = %+v", blocked)
	}

	store.ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: "assisted"})
	_, blocked = service.EvaluateTaskCapability(store.ws.ID, "runtime")
	if blocked == nil || blocked.ReasonCode != "app_offline" || blocked.Repair == nil || blocked.Repair.Code != "check_runtime" {
		t.Fatalf("offline preflight = %+v", blocked)
	}
	if len(adapter.liveChecks) != 1 {
		t.Fatalf("preflight did not run a fresh live check: %d", len(adapter.liveChecks))
	}

	adapter.live = LiveResult{State: LiveAvailable, Summary: "Available now."}
	_, blocked = service.EvaluateTaskCapability(store.ws.ID, "runtime")
	if blocked != nil {
		t.Fatalf("available runtime remained blocked: %+v", blocked)
	}
	if len(adapter.liveChecks) != 2 {
		t.Fatalf("second preflight reused stale availability: %d", len(adapter.liveChecks))
	}
}

func TestRuntimeTaskEvaluatorUsesModeRequirementOrderForFirstBlocker(t *testing.T) {
	first := &recordingAdapter{id: "first_adapter", durable: DurableResult{State: DurableInProgress, ReasonCode: "first_missing", Summary: "Configure first."}}
	second := &recordingAdapter{id: "second_adapter", durable: DurableResult{State: DurableInProgress, ReasonCode: "second_missing", Summary: "Configure second."}}
	registry := NewRegistry()
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	store := &runtimeStore{ws: runtimeWorkspace(contractWithRequirements("first", "second"))}
	service := NewService(store, registry)
	claimed, blocked := service.EvaluateTaskCapability(store.ws.ID, "second")
	if !claimed || blocked == nil || blocked.ReasonCode != "first_missing" {
		t.Fatalf("mode first blocker = claimed %v blocked %+v", claimed, blocked)
	}
}

func TestRuntimeTaskEvaluatorClaimsMalformedDeclaredSnapshotAndFailsClosed(t *testing.T) {
	contract := contractWithRequirements("runtime")
	contract.OperatingModes[0].Requires = []string{"missing"}
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	service := NewService(store, NewRegistry())
	claimed, blocked := service.EvaluateTaskCapability(store.ws.ID, "runtime")
	if !claimed || blocked == nil || blocked.ReasonCode != ReasonUnsupportedSnapshot {
		t.Fatalf("malformed declared runtime fell through: claimed=%v blocked=%+v", claimed, blocked)
	}
}
