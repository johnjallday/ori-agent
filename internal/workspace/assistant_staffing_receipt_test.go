package workspace

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestHomeStaffingReceiptSurvivesCloneAndSerializationWithoutSourceAuthority(t *testing.T) {
	state := &AssistantProgramState{SchemaVersion: AssistantProgramStateSchemaVersion, StaffingReceipts: []AssistantStaffingReceipt{{
		RoleID: "manager", ChoiceDigest: strings.Repeat("a", 64), EffectDigest: strings.Repeat("b", 64),
		AgentName: "Aster", AgentInstanceID: "instance", Action: "assign",
	}}}
	copy := CloneAssistantProgramState(state)
	copy.StaffingReceipts[0].AgentName = "Changed"
	if state.StaffingReceipts[0].AgentName != "Aster" {
		t.Fatal("canonical receipt aliases its clone")
	}
	data, err := json.Marshal(state)
	if err != nil || strings.Contains(string(data), "source_witness") {
		t.Fatal("receipt serialization exposed source authority", err)
	}
	var restored AssistantProgramState
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	got, found, err := HomeStaffingReceipt(&restored, "manager")
	if err != nil || !found || !reflect.DeepEqual(got, state.StaffingReceipts[0]) {
		t.Fatal("stored receipt changed", err)
	}
	restored.HomeBindings = AssistantRoleBindingSet{}
	if _, found, err := HomeStaffingReceipt(&restored, "manager"); err != nil || !found {
		t.Fatal("clear erased completed-step evidence", err)
	}
}

func TestHomeStaffingReceiptRefusesAmbiguousOrMalformedEvidence(t *testing.T) {
	valid := AssistantStaffingReceipt{RoleID: "manager", ChoiceDigest: strings.Repeat("a", 64), EffectDigest: strings.Repeat("b", 64), AgentName: "Aster", AgentInstanceID: "instance", Action: "assign"}
	for _, records := range [][]AssistantStaffingReceipt{
		{valid, valid},
		{{RoleID: "manager", ChoiceDigest: "invalid", AgentName: "Aster", AgentInstanceID: "instance", Action: "assign"}},
		{{RoleID: "manager", ChoiceDigest: valid.ChoiceDigest, EffectDigest: valid.EffectDigest, AgentName: "Aster", AgentInstanceID: "instance", Action: "unknown"}},
		make([]AssistantStaffingReceipt, 65),
	} {
		if _, _, err := HomeStaffingReceipt(&AssistantProgramState{StaffingReceipts: records}, "manager"); err == nil {
			t.Fatal("ambiguous receipt accepted")
		}
	}
	if _, found, err := HomeStaffingReceipt(&AssistantProgramState{}, "manager"); err != nil || found {
		t.Fatal("legacy absence became a receipt", err)
	}
	if _, _, err := HomeStaffingReceipt(nil, "manager"); err == nil {
		t.Fatal("unknown Home treated as known empty slot")
	}
}
