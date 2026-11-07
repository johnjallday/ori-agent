package workspace

import "encoding/hex"

// AssistantStaffingReceipt is the canonical role owner's last reviewed effect
// for one Home role. It is saved atomically with the binding and survives clear,
// so replay cannot quietly rebind a role the user subsequently cleared. These
// opaque digests are inert outcome evidence, not source witnesses or permission
// to fill a role. No profile configuration, prompt or grant lives here.
type AssistantStaffingReceipt struct {
	RoleID          string `json:"role_id"`
	ChoiceDigest    string `json:"choice_digest"`
	EffectDigest    string `json:"effect_digest"`
	AgentInstanceID string `json:"agent_instance_id"`
	AgentName       string `json:"agent_name"`
	Action          string `json:"action"` // assign or create
}

// HomeStaffingReceipt returns the single canonical receipt for a role. A
// duplicate is ambiguous rather than an invitation to select the last one.
func HomeStaffingReceipt(state *AssistantProgramState, roleID string) (AssistantStaffingReceipt, bool, error) {
	if state == nil || roleID == "" || len(state.StaffingReceipts) > 64 {
		return AssistantStaffingReceipt{}, false, ErrAssistantBindingInvalid
	}
	var result AssistantStaffingReceipt
	found := false
	for _, receipt := range state.StaffingReceipts {
		if receipt.RoleID != roleID {
			continue
		}
		if found || len(receipt.ChoiceDigest) != 64 || receipt.AgentInstanceID == "" || receipt.AgentName == "" ||
			(receipt.Action != "assign" && receipt.Action != "create") {
			return AssistantStaffingReceipt{}, false, ErrAssistantBindingInvalid
		}
		for _, digest := range []string{receipt.ChoiceDigest, receipt.EffectDigest} {
			if _, err := hex.DecodeString(digest); err != nil || len(digest) != 64 {
				return AssistantStaffingReceipt{}, false, ErrAssistantBindingInvalid
			}
		}
		result, found = receipt, true
	}
	return result, found, nil
}
