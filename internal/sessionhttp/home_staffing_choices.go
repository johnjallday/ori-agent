package sessionhttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agentappearance"
	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ReviewHomeStaffingChoices freezes only explicitly submitted choices using
// the shared canonical readers. The existing review owner must bind target and
// original advice to its context and CAS the result after these unlocked reads.
// To edit one role, submit that role only and retain the other original intents:
// a neighbouring edit must not renew an already reviewed source witness.
func (h *Handler) ReviewHomeStaffingChoices(ctx context.Context, target HomeStaffingTarget, choices []rosterstaffing.RoleChoice, advice *rosterstaffing.Advice) (*rosterstaffing.TeamIntent, error) {
	if h == nil || len(choices) == 0 || len(choices) > rosterstaffing.MaxRoles || h.assistantWorkspaceRoleStaffer == nil || h.agentStore == nil {
		return nil, grouprequirements.ErrUnavailable
	}
	state, occupied, err := h.homeStaffingOccupancy(target)
	if err != nil {
		return nil, err
	}
	byRole := map[string]rosterstaffing.RoleChoice{}
	for _, choice := range choices {
		if _, duplicate := byRole[choice.RoleID]; duplicate || occupied[choice.RoleID] {
			return nil, grouprequirements.ErrReviewStale
		}
		if choice.Recommendation != "" && (advice == nil || advice.Version != 2 || advice.Validate(0) != nil || advice.TemplateID != target.TemplateID || advice.TemplateRevision != target.TemplateRevision) {
			return nil, grouprequirements.ErrReviewStale
		}
		byRole[choice.RoleID] = choice
	}
	profiles := store.ReadStaffingSnapshot(ctx, h.agentStore)
	team := &rosterstaffing.TeamIntent{Version: 1}
	for _, declaration := range target.Declaration.Roles {
		choice, selected := byRole[declaration.ID]
		if !selected {
			continue
		}
		if declaration.Scope != workspace.AssistantRoleScopeHome {
			return nil, grouprequirements.ErrReviewRequired
		}
		role := rosterstaffing.RoleIntent{RoleID: choice.RoleID, Scope: "home", Mode: choice.Mode, Name: choice.Name}
		if state != nil {
			previous, exists, err := workspace.HomeStaffingReceipt(state, role.RoleID)
			if err != nil {
				return nil, grouprequirements.ErrReviewStale
			}
			if exists {
				role.PreviousChoiceDigest = previous.ChoiceDigest
			}
		}
		switch choice.Mode {
		case "assign":
			if choice.Provider != "" || choice.Model != "" || choice.ReasoningEffort != "" || choice.Appearance != nil {
				return nil, grouprequirements.ErrReviewRequired
			}
			if err := h.reviewHomeAssignment(ctx, profiles, target.Personal, declaration, choice, advice, &role); err != nil {
				return nil, err
			}
		case "create":
			if choice.Recommendation != "" || ((choice.Provider == "") != (choice.Model == "")) {
				return nil, grouprequirements.ErrReviewRequired
			}
			for _, name := range h.agentStore.ListAgents() {
				if strings.EqualFold(name, choice.Name) {
					return nil, grouprequirements.ErrReviewStale
				}
			}
			model, provider, reasoning, _ := h.templateAgentModelDefaults(projecttemplates.AgentSpec{Provider: choice.Provider, Model: choice.Model, ReasoningEffort: choice.ReasoningEffort})
			appearance, err := agentappearance.ValidateStaged(choice.Appearance)
			if err != nil {
				return nil, grouprequirements.ErrReviewRequired
			}
			role.Provider, role.Model, role.ReasoningEffort, role.Appearance = provider, model, reasoning, appearance
			if model != "" && (h.workspaceBuild == nil || h.workspaceBuild.ProfileModelConfigured == nil || !h.workspaceBuild.ProfileModelConfigured(provider, model)) {
				return nil, grouprequirements.ErrReviewRequired
			}
		default:
			return nil, grouprequirements.ErrReviewRequired
		}
		team.Roles = append(team.Roles, role)
	}
	if len(team.Roles) != len(byRole) || team.Validate() != nil {
		return nil, grouprequirements.ErrReviewRequired
	}
	return team, nil
}

func (h *Handler) reviewHomeAssignment(ctx context.Context, profiles store.StaffingSnapshot, personal bool, declaration workspace.AssistantProgramRoleSpec, choice rosterstaffing.RoleChoice, advice *rosterstaffing.Advice, role *rosterstaffing.RoleIntent) error {
	var profile *store.StaffingProfile
	for i := range profiles.Profiles {
		if profiles.Profiles[i].Name == choice.Name {
			profile = &profiles.Profiles[i]
			break
		}
	}
	if profile == nil || profile.Paused || h.workspaceBuild == nil || h.workspaceBuild.ProfileModelConfigured == nil || !h.workspaceBuild.ProfileModelConfigured(profile.Provider, profile.Model) {
		return grouprequirements.ErrReviewStale
	}
	role.SourceWitness = profile.Witness
	var recommended *rosterstaffing.AdviceChoice
	if choice.Recommendation != "" {
		if advice == nil || (advice.State != rosterstaffing.Recommended && advice.State != rosterstaffing.NeedsChoice) {
			return grouprequirements.ErrReviewStale
		}
		for _, row := range advice.Roles {
			if row.RoleID != choice.RoleID || row.Scope != "home" || (row.State != rosterstaffing.Recommended && row.State != rosterstaffing.NeedsChoice) {
				continue
			}
			for _, candidate := range row.Choices {
				if candidate.Handle == choice.Recommendation && candidate.Name == choice.Name {
					copy := candidate
					recommended = &copy
				}
			}
		}
		if recommended == nil || recommended.SourceWitness != profile.Witness {
			return grouprequirements.ErrReviewStale
		}
		role.SourceWitness, role.CapabilityWitness = recommended.SourceWitness, recommended.CapabilityWitness
	}
	if len(declaration.Skills) > 0 || role.CapabilityWitness != "" {
		if h.staffingSkillReader == nil {
			return grouprequirements.ErrUnavailable
		}
		membership := h.staffingSkillReader(ctx, store.StaffingSelection{Name: role.Name, Witness: role.SourceWitness}, declaration.Skills, personal)
		if membership.SourceWitness != role.SourceWitness || membership.Witness == "" || (recommended != nil && membership.Witness != recommended.CapabilityWitness) {
			return grouprequirements.ErrReviewStale
		}
		enabled := map[string]bool{}
		for _, name := range membership.Enabled {
			enabled[name] = true
		}
		for _, name := range declaration.Skills {
			if !enabled[name] {
				return grouprequirements.ErrReviewStale
			}
		}
		role.CapabilityWitness = membership.Witness
	}
	return nil
}
