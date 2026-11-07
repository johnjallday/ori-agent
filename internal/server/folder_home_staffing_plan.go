package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// withFolderHomeStaffingReview is the pending-plan producer. Accepted runs are
// never upgraded. The existing review actions/role controls freeze choices;
// an undecided team is not executable.
func (h *folderSetupHost) withFolderHomeStaffingReview(ctx context.Context, req personalassistant.FolderSetupRequest, plan personalassistant.FolderSetupPlan) (personalassistant.FolderSetupPlan, error) {
	if req.Offer.Setup != nil {
		return plan, nil
	}
	review := req.Offer.HomeStaffingReview
	hasTeam := review != nil && review.Team != nil
	if !plan.Intent.Portfolio {
		if hasTeam {
			return personalassistant.FolderSetupPlan{}, personalassistant.ErrFolderPlanChanged
		}
		return plan, nil
	}
	if !plan.Intent.StaffsHome && !hasTeam {
		return plan, nil
	}
	base := homeStaffingBasePlan(plan)
	// A newly occupied role cannot make the original choice disappear into a
	// no-staffing/legacy plan. Keep it visible and blocked until explicit clear.
	base.Intent.StaffsHome = true
	base = base.Stamped()
	req.Plan = base
	target, err := h.folderHomeReviewTarget(ctx, req)
	if err != nil {
		return personalassistant.FolderSetupPlan{}, err
	}
	view := &personalassistant.FolderHomeStaffingView{ContextDigest: personalassistant.FolderHomeStaffingContext(req, base), State: "needs_choice", ChoiceProblems: map[string]string{}}
	var home *workspace.Workspace
	if target.HomeID != "" {
		home, err = h.builder.workspaceStore.Get(target.HomeID)
		if err != nil || home == nil {
			return personalassistant.FolderSetupPlan{}, personalassistant.ErrFolderPlanChanged
		}
	}
	held, sound := folderHeldHomeRoles(home)
	known := map[string]bool{}
	for _, role := range target.Declaration.Roles {
		if role.Scope != workspace.AssistantRoleScopeHome {
			continue
		}
		known[role.ID] = true
		view.Roles = append(view.Roles, personalassistant.FolderHomeRoleView{RoleID: role.ID, Label: role.Label, Description: role.Description,
			Required: role.Required, Primary: role.Primary, HeldName: held[role.ID], Skills: append([]string(nil), role.Skills...)})
	}
	if review != nil {
		view.Revision, view.Choices = review.Revision, review.Team.Choices()
		if review.ContextDigest == view.ContextDigest {
			view.Advice = review.Advice.Public()
		}
		for _, choice := range view.Choices {
			if !known[choice.RoleID] {
				view.Roles = append(view.Roles, personalassistant.FolderHomeRoleView{RoleID: choice.RoleID, Label: choice.RoleID})
				view.ChoiceProblems[choice.RoleID] = "This role changed. Clear the original choice before choosing again."
			}
		}
	}
	switch {
	case !sound:
		view.State, view.Problem = "stale", "An existing Home role needs attention. Open the Home to review it before continuing."
	case len(view.Roles) == 0:
		view.State, view.Problem = "unavailable", "The Home declaration has no roles available for this review."
	case view.ContextDigest == "":
		// A build's first preview has no stored offer yet. An explicit action
		// establishes its existing canonical review owner, never a GET.
		view.State = "needs_review"
	case review != nil && review.Team != nil && review.ContextDigest != view.ContextDigest:
		view.State, view.Problem = "stale", "The setup changed. Clear the pending choices and review this Home team again."
	default:
		chosen := map[string]bool{}
		for _, choice := range view.Choices {
			chosen[choice.RoleID] = true
			if held[choice.RoleID] != "" {
				view.ChoiceProblems[choice.RoleID] = "This role is already held. Clear the pending choice; the existing holder is not replaced."
			}
		}
		if hasTeam {
			for _, role := range review.Team.Roles {
				one := &rosterstaffing.TeamIntent{Version: review.Team.Version, Roles: []rosterstaffing.RoleIntent{role}}
				if err := h.builder.sessionHandler.CheckReviewedHomeSources(ctx, one, target.Declaration, target.Personal); err != nil {
					view.ChoiceProblems[role.RoleID] = "The selected profile, model or capability changed. Clear this original choice and review it again."
				}
			}
		}
		complete := len(view.ChoiceProblems) == 0
		for _, role := range view.Roles {
			if role.Required && role.HeldName == "" && !chosen[role.RoleID] {
				complete = false
			}
		}
		if len(view.ChoiceProblems) > 0 {
			view.State, view.Problem = "stale", "Review the marked Home roles before confirming."
		}
		if complete && len(view.Choices) == 0 {
			// No required role is missing. Optional empty roles are not consent
			// for automatic hires, and the runner skips staffing altogether.
			base.Intent.StaffsHome = false
			view.State = "ready"
			for i := range base.Lines {
				if base.Lines[i].Kind == personalassistant.FolderPlanAgents {
					base.Lines[i].Name, base.Lines[i].Detail = "No Home agents selected", "Existing holders stay in place. No additional Home agents will be added."
					break
				}
			}
		} else if complete {
			base.Intent.HomeStaffing = review.Team.Clone()
			base.Intent.RequiresHomeStaffing = true
			base = base.Stamped()
			req.Plan = base
			if err := h.checkFolderHomeTeam(ctx, req, home); err == nil {
				view.State = "ready"
			} else {
				view.State, view.Problem = "stale", "A selected profile, model or capability changed. Clear the affected choice and review it again."
			}
		}
	}
	if view.State != "ready" {
		base.Intent.HomeStaffing = nil
	}
	base.Intent.RequiresHomeStaffing, base.HomeStaffing = base.Intent.StaffsHome, view
	return base.Stamped(), nil
}

// Occupancy is existing canonical metadata, not a name lookup or permission to
// replace/repair a role. Unknown or inconsistent holders stop the new plan.
func folderHeldHomeRoles(home *workspace.Workspace) (map[string]string, bool) {
	held := map[string]string{}
	if home == nil {
		return held, true
	}
	state := home.GetAssistantProgramState()
	if state == nil {
		return held, false
	}
	instances := home.GetAgentInstances()
	for _, binding := range state.HomeBindings.Bindings {
		if binding.RoleID == "" || binding.AgentName == "" || held[binding.RoleID] != "" {
			return held, false
		}
		matches := 0
		for _, instance := range instances {
			if instance.RoleID == binding.RoleID {
				if instance.ID != binding.AgentInstanceID || instance.Name != binding.AgentName {
					return held, false
				}
				matches++
			}
		}
		if matches != 1 {
			return held, false
		}
		held[binding.RoleID] = binding.AgentName
	}
	for _, instance := range instances {
		if instance.RoleID != "" && held[instance.RoleID] == "" {
			return held, false
		}
	}
	return held, true
}
