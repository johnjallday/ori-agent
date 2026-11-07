package sessionhttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CheckReviewedHomeSources is an in-process, read-only preflight of pending
// choices, shared by Group Template and folder review owners. Callers establish
// the exact destination/declaration first and omit only canonically completed
// choices. This never refreshes witnesses or grants execution authority; the
// canonical role writer still fences each selected source/capability at bind.
func (h *Handler) CheckReviewedHomeSources(ctx context.Context, team *rosterstaffing.TeamIntent, declaration *workspace.AssistantProgramDeclaration, personal bool) error {
	if h == nil || h.agentStore == nil || team == nil || team.Validate() != nil || declaration == nil || len(declaration.Stages) == 0 {
		return grouprequirements.ErrReviewStale
	}
	profiles := store.ReadStaffingSnapshot(ctx, h.agentStore)
	roles := map[string]workspace.AssistantProgramRoleSpec{}
	for _, role := range declaration.Roles {
		if _, duplicate := roles[role.ID]; duplicate {
			return grouprequirements.ErrReviewStale
		}
		roles[role.ID] = role
	}
	configured := func(provider, model string) bool {
		return h.workspaceBuild != nil && h.workspaceBuild.ProfileModelConfigured != nil && h.workspaceBuild.ProfileModelConfigured(provider, model)
	}
	for _, role := range team.Roles {
		declared, exists := roles[role.RoleID]
		if !exists || role.Scope != "home" || declared.Scope != workspace.AssistantRoleScopeHome {
			return grouprequirements.ErrReviewStale
		}
		if role.Mode == "create" {
			if role.Provider == "" || role.Model == "" || !configured(role.Provider, role.Model) {
				return grouprequirements.ErrReviewRequired
			}
			for _, name := range h.agentStore.ListAgents() {
				if strings.EqualFold(name, role.Name) {
					return grouprequirements.ErrReviewStale
				}
			}
			continue
		}
		found := false
		for _, profile := range profiles.Profiles {
			if profile.Name == role.Name && profile.Witness == role.SourceWitness && !profile.Paused && configured(profile.Provider, profile.Model) {
				found = true
				break
			}
		}
		if !found {
			return grouprequirements.ErrReviewStale
		}
		if role.CapabilityWitness != "" || len(declared.Skills) > 0 {
			if h.staffingSkillReader == nil {
				return grouprequirements.ErrUnavailable
			}
			membership := h.staffingSkillReader(ctx, store.StaffingSelection{Name: role.Name, Witness: role.SourceWitness}, declared.Skills, personal)
			if membership.Witness != role.CapabilityWitness || membership.SourceWitness != role.SourceWitness {
				return grouprequirements.ErrReviewStale
			}
			enabled := map[string]bool{}
			for _, name := range membership.Enabled {
				enabled[name] = true
			}
			for _, name := range declared.Skills {
				if !enabled[name] {
					return grouprequirements.ErrReviewStale
				}
			}
		}
	}
	return nil
}
