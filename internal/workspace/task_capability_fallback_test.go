package workspace

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRuntimeFileFallbackRequiresDeclarationApprovalAndIsConsumable(t *testing.T) {
	task := &Task{
		RequiredCapabilities: []string{"reaper_live_control"},
		FileFallbackFor:      []string{"reaper_live_control"},
	}
	if task.ApprovedRuntimeFileFallback() != "" {
		t.Fatal("declaration alone must not approve fallback")
	}
	if !task.ApproveRuntimeFileFallback("reaper_live_control", "block-1", time.Now()) {
		t.Fatal("explicit declared fallback should be recordable")
	}
	if got := task.ApprovedRuntimeFileFallback(); got != "reaper_live_control" {
		t.Fatalf("approval = %q", got)
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Task
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if got := roundTrip.ApprovedRuntimeFileFallback(); got != "reaper_live_control" {
		t.Fatalf("round-trip approval = %q", got)
	}
	roundTrip.ConsumeRuntimeFileFallbackApproval()
	if got := roundTrip.ApprovedRuntimeFileFallback(); got != "" {
		t.Fatalf("consumed approval = %q", got)
	}
}

func TestRuntimeFileFallbackCannotApproveUndeclaredCapability(t *testing.T) {
	task := &Task{RequiredCapabilities: []string{"reaper_live_control"}}
	if task.ApproveRuntimeFileFallback("reaper_live_control", "block-1", time.Now()) {
		t.Fatal("undeclared fallback should not be approved")
	}
}

func TestAddFileFallbackChoicePreservesExactRepairAndRemovesRetry(t *testing.T) {
	blocked := &TaskBlockedError{
		CapabilityKey:    "reaper_live_control",
		ReasonCode:       "reaper_offline",
		Repair:           &TaskRepairAction{Code: "open_check_reaper", Label: "Open or check REAPER"},
		SuggestedActions: []string{"Open or check REAPER"},
	}
	got := AddFileFallbackChoice(blocked)
	if got == blocked || got.Repair == nil || got.Repair.Code != "open_check_reaper" || got.WorkflowStep == nil || len(got.WorkflowStep.Choices) != 1 || got.WorkflowStep.Choices[0].ID != "use_file_fallback" {
		t.Fatalf("fallback block = %+v", got)
	}
	for _, action := range got.SuggestedActions {
		if action == "retry" {
			t.Fatal("fallback workflow must not expose a useless retry")
		}
	}
}
