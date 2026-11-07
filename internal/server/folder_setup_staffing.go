package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var _ foldersetup.ReviewedHomeStaffing = portfolioHomeStaffing{}

// checkFolderHomeTeam runs only after the existing destination owner has proved
// the original target (including its own newly-created Home). Completed roles
// are observations; pending roles keep their original witnesses. No inference,
// defaults, source renewal or skill grants occur at this guard.
func (h *folderSetupHost) checkFolderHomeTeam(ctx context.Context, req personalassistant.FolderSetupRequest, home *workspace.Workspace) error {
	team := req.Plan.Intent.HomeStaffing
	if team == nil {
		if req.Plan.Intent.RequiresHomeStaffing {
			return personalassistant.ErrFolderPlanChanged
		}
		return nil
	}
	if h == nil || h.builder == nil || h.builder.sessionHandler == nil || req.Plan.Destination == nil || req.Plan.Digest == "" || req.Plan.Stamped().Digest != req.Plan.Digest {
		return personalassistant.ErrFolderPlanChanged
	}
	declaration, personal, err := h.folderHomeStaffingDeclaration(ctx, req, home)
	if err != nil {
		return err
	}
	pending := &rosterstaffing.TeamIntent{Version: team.Version}
	for _, role := range team.Clone().Roles {
		if home == nil {
			if role.PreviousChoiceDigest != "" {
				return personalassistant.ErrFolderPlanChanged
			}
		} else {
			choice := role.StepDigest(req.Plan.Digest, home.ID)
			if folderHomeRolePresent(home, foldersetup.HomeRoleOutcome{RoleID: role.RoleID, Name: role.Name, Action: role.Mode, ChoiceDigest: choice}) {
				continue
			}
			state := home.GetAssistantProgramState()
			previous, exists, err := workspace.HomeStaffingReceipt(state, role.RoleID)
			if err != nil || (exists && (previous.ChoiceDigest == choice || previous.ChoiceDigest != role.PreviousChoiceDigest)) || (!exists && role.PreviousChoiceDigest != "") {
				return personalassistant.ErrFolderPlanChanged
			}
			for _, binding := range state.HomeBindings.Bindings {
				if binding.RoleID == role.RoleID {
					return personalassistant.ErrFolderPlanChanged
				}
			}
			for _, instance := range home.GetAgentInstances() {
				if instance.RoleID == role.RoleID {
					return personalassistant.ErrFolderPlanChanged
				}
			}
		}
		pending.Roles = append(pending.Roles, role)
	}
	if len(pending.Roles) == 0 {
		return nil
	}
	if err := h.builder.sessionHandler.CheckReviewedHomeSources(ctx, pending, declaration, personal); err != nil {
		return personalassistant.ErrFolderPlanChanged
	}
	return nil
}

// StaffReviewed delegates the original choice to the canonical role writer.
// Its atomic per-role receipt owns replay, including after a profile-store
// restart. Missing/default-model/free-name legacy methods are not consulted.
func (s portfolioHomeStaffing) StaffReviewed(ctx context.Context, homeID string, role rosterstaffing.RoleIntent, planDigest string) (string, error) {
	team := &rosterstaffing.TeamIntent{Version: 1, Roles: []rosterstaffing.RoleIntent{role}}
	choice := role.StepDigest(planDigest, homeID)
	if s.builder == nil || s.builder.workspaceStore == nil || s.builder.assistantStaffing == nil || role.Scope != "home" || team.Validate() != nil || choice == "" {
		return "", errSetupUnavailable
	}
	home, err := s.builder.workspaceStore.Get(homeID)
	if err != nil || home == nil {
		return "", errSetupUnavailable
	}
	previous, exists, err := workspace.HomeStaffingReceipt(home.GetAssistantProgramState(), role.RoleID)
	if err != nil {
		return "", err
	}
	completed := exists && previous.ChoiceDigest == choice
	mode := setupjourney.StaffingModeCreate
	if role.Mode == "assign" {
		mode = setupjourney.StaffingModeBind
	}
	fill := setupjourney.RoleFill{RoleID: role.RoleID, Mode: mode, Name: role.Name,
		Provider: role.Provider, Model: role.Model, ReasoningEffort: role.ReasoningEffort, Appearance: role.Appearance.Clone(),
		SourceWitness: role.SourceWitness, CapabilityWitness: role.CapabilityWitness,
		ChoiceDigest: choice, PreviousChoiceDigest: role.PreviousChoiceDigest}
	if err := s.builder.assistantStaffing.StaffRoleOnWorkspace(ctx, homeID, []setupjourney.RoleFill{fill}); err != nil {
		return "", err
	}
	outcome := "assigned"
	if role.Mode == "create" {
		outcome = "created"
	}
	if completed {
		outcome = "already_present"
	}
	home, err = s.builder.workspaceStore.Get(homeID)
	if err != nil || !folderHomeRolePresent(home, foldersetup.HomeRoleOutcome{RoleID: role.RoleID, Name: role.Name, Action: role.Mode, ChoiceDigest: choice}) {
		return "", errSetupUnavailable
	}
	return outcome, nil
}

// folderHomeRolePresent is observation only. A cleared/changed role cannot be
// reported as present merely because this pass previously completed it.
func folderHomeRolePresent(home *workspace.Workspace, outcome foldersetup.HomeRoleOutcome) bool {
	if home == nil || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return false
	}
	state := home.GetAssistantProgramState()
	receipt, exists, err := workspace.HomeStaffingReceipt(state, outcome.RoleID)
	if err != nil || !exists || receipt.ChoiceDigest != outcome.ChoiceDigest || receipt.AgentName != outcome.Name || receipt.Action != outcome.Action {
		return false
	}
	bindings := 0
	for _, binding := range state.HomeBindings.Bindings {
		if binding.RoleID == outcome.RoleID {
			if binding.AgentName != outcome.Name || binding.AgentInstanceID != receipt.AgentInstanceID {
				return false
			}
			bindings++
		}
	}
	instances := 0
	source := workspace.RoleSourceAssigned
	if outcome.Action == "create" {
		source = workspace.RoleSourceCreated
	}
	for _, instance := range home.GetAgentInstances() {
		if instance.ID == receipt.AgentInstanceID || instance.RoleID == outcome.RoleID {
			if instance.ID != receipt.AgentInstanceID || instance.Name != outcome.Name || instance.RoleID != outcome.RoleID || instance.RoleSource != source {
				return false
			}
			instances++
		}
	}
	return bindings == 1 && instances == 1
}
