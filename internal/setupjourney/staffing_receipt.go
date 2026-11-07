package setupjourney

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func staffingChoiceDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func staffingEffectDigest(role staffingRoleInput) string {
	data, err := json.Marshal(struct {
		Role                 staffingRoleInput
		SourceWitness        string
		CapabilityWitness    string
		PreviousChoiceDigest string
	}{role, role.SourceWitness, role.CapabilityWitness, role.PreviousChoiceDigest})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func staffingChoiceAction(role staffingRoleInput) string {
	if role.binds() {
		return "assign"
	}
	return "create"
}

// A completed reviewed step is an observation, not another assignment. Its
// receipt survives clear: absence after completion is a conflict, never a
// license for a retry to undo the user's later action.
func reviewedHomeStaffingReplay(home *workspace.Workspace, role staffingRoleInput) (bool, error) {
	if role.ChoiceDigest == "" {
		if role.PreviousChoiceDigest != "" {
			return false, ErrInvalid
		}
		return false, nil
	}
	if home == nil || !staffingChoiceDigest(role.ChoiceDigest) ||
		(role.PreviousChoiceDigest != "" && !staffingChoiceDigest(role.PreviousChoiceDigest)) ||
		(role.binds() && role.SourceWitness == "") || (!role.binds() && (role.Provider == "" || role.Model == "")) {
		return false, ErrInvalid
	}
	state := home.GetAssistantProgramState()
	receipt, found, err := workspace.HomeStaffingReceipt(state, role.RoleID)
	if err != nil {
		return false, ErrConflict
	}
	if found && receipt.ChoiceDigest == role.ChoiceDigest {
		if receipt.AgentName != role.Name || receipt.Action != staffingChoiceAction(role) || receipt.EffectDigest != staffingEffectDigest(role) {
			return false, ErrConflict
		}
		bindings, instances := 0, 0
		for _, binding := range state.HomeBindings.Bindings {
			if binding.RoleID == role.RoleID {
				if binding.AgentName != receipt.AgentName || binding.AgentInstanceID != receipt.AgentInstanceID {
					return false, ErrConflict
				}
				bindings++
			}
		}
		source := workspace.RoleSourceCreated
		if role.binds() {
			source = workspace.RoleSourceAssigned
		}
		for _, instance := range home.GetAgentInstances() {
			if instance.RoleID == role.RoleID {
				if instance.ID != receipt.AgentInstanceID || instance.Name != receipt.AgentName || instance.RoleSource != source {
					return false, ErrConflict
				}
				instances++
			}
		}
		if bindings != 1 || instances != 1 {
			return false, ErrConflict
		}
		return true, nil
	}
	if (found && receipt.ChoiceDigest != role.PreviousChoiceDigest) || (!found && role.PreviousChoiceDigest != "") {
		return false, ErrConflict
	}
	return false, nil
}

// Record inside the very same canonical workspace update as the binding. One
// record per role bounds history while preserving a predecessor check for a
// fresh review after clear. Legacy immediate role edits do not acquire receipts.
func recordHomeStaffingChoice(state *workspace.AssistantProgramState, role staffingRoleInput, binding workspace.AssistantRoleBinding) error {
	if role.ChoiceDigest == "" {
		if role.PreviousChoiceDigest != "" {
			return ErrInvalid
		}
		return nil
	}
	prior, found, err := workspace.HomeStaffingReceipt(state, role.RoleID)
	if err != nil || !staffingChoiceDigest(role.ChoiceDigest) ||
		(found && prior.ChoiceDigest != role.PreviousChoiceDigest) || (!found && role.PreviousChoiceDigest != "") {
		return ErrConflict
	}
	receipt := workspace.AssistantStaffingReceipt{RoleID: role.RoleID, ChoiceDigest: role.ChoiceDigest, EffectDigest: staffingEffectDigest(role),
		AgentInstanceID: binding.AgentInstanceID, AgentName: binding.AgentName, Action: staffingChoiceAction(role)}
	if found {
		for i := range state.StaffingReceipts {
			if state.StaffingReceipts[i].RoleID == role.RoleID {
				state.StaffingReceipts[i] = receipt
				return nil
			}
		}
	}
	if len(state.StaffingReceipts) >= 64 {
		return ErrConflict
	}
	state.StaffingReceipts = append(state.StaffingReceipts, receipt)
	return nil
}
