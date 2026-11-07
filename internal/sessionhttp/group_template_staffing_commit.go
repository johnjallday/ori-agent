package sessionhttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type groupHomeStaffingOutcome struct {
	State string                 `json:"state"`
	Roles []groupHomeRoleOutcome `json:"roles"`
}

type groupHomeRoleOutcome struct {
	RoleID  string `json:"role_id"`
	Name    string `json:"name"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
}

// Refuse already-known stale pending sources before creating a new Home.
// Existing Homes may contain completed receipts from a lost response/restart;
// their canonical per-role owner distinguishes observation from pending work.
func (h *Handler) checkNewGroupHomeTeam(ctx context.Context, input grouprequirements.Input, reviewed grouprequirements.HomeStaffingReview) error {
	team := reviewed.Team
	if team == nil {
		return nil
	}
	current := h.groupRequirements.Evaluate(input)
	if reviewed.HomeWorkspaceID != "" {
		if current.HomeWorkspaceID != reviewed.HomeWorkspaceID {
			return grouprequirements.ErrReviewStale
		}
		return nil
	}
	if current.HomeWorkspaceID != "" {
		home, err := h.workspaceTaskStore.Get(current.HomeWorkspaceID)
		if err != nil || home == nil {
			return grouprequirements.ErrReviewStale
		}
		state := home.GetAssistantProgramState()
		if state == nil || state.GroupTemplate == nil || state.GroupTemplate.ReviewDigest != reviewed.ReviewDigest {
			return grouprequirements.ErrReviewStale
		}
		return nil
	}
	return h.CheckReviewedHomeSources(ctx, team, current.HomeDeclaration, current.HomeOwner != nil)
}

func groupHomeTeamNeedsReview(team *rosterstaffing.TeamIntent) *groupHomeStaffingOutcome {
	out := &groupHomeStaffingOutcome{State: "needs_review"}
	for _, role := range team.Roles {
		out.Roles = append(out.Roles, groupHomeRoleOutcome{RoleID: role.RoleID, Name: role.Name, Action: role.Mode, Outcome: "not_completed"})
	}
	return out
}

// The group journal owns only Home creation. Role effects stay in the existing
// canonical role writer, whose atomic outcome receipts own each exact replay.
func (h *Handler) applyGroupHomeTeam(ctx context.Context, homeID, reviewDigest string, team *rosterstaffing.TeamIntent) *groupHomeStaffingOutcome {
	if team == nil {
		return nil
	}
	out := &groupHomeStaffingOutcome{State: "staffed"}
	blocked := false
	for _, role := range team.Roles {
		item := groupHomeRoleOutcome{RoleID: role.RoleID, Name: role.Name, Action: role.Mode, Outcome: "not_completed"}
		digest := role.StepDigest(reviewDigest, homeID)
		if digest == "" || h.assistantWorkspaceRoleStaffer == nil {
			blocked = true
		}
		completed := false
		if !blocked {
			home, readErr := h.workspaceTaskStore.Get(homeID)
			if readErr != nil || home == nil {
				blocked = true
			} else {
				receipt, exists, receiptErr := workspace.HomeStaffingReceipt(home.GetAssistantProgramState(), role.RoleID)
				if receiptErr != nil {
					blocked = true
				}
				completed = exists && receipt.ChoiceDigest == digest
			}
		}
		if !blocked {
			err := h.assistantWorkspaceRoleStaffer(ctx, homeID, []RoleStaffingFill{{RoleID: role.RoleID, Mode: role.Mode, Name: role.Name,
				Provider: role.Provider, Model: role.Model, ReasoningEffort: role.ReasoningEffort, Appearance: role.Appearance.Clone(),
				SourceWitness: role.SourceWitness, CapabilityWitness: role.CapabilityWitness, ChoiceDigest: digest, PreviousChoiceDigest: role.PreviousChoiceDigest}})
			if err != nil {
				blocked = true
			} else if completed {
				item.Outcome = "already_present"
			} else if role.Mode == "assign" {
				item.Outcome = "assigned"
			} else {
				item.Outcome = "created"
			}
		}
		out.Roles = append(out.Roles, item)
	}
	if blocked {
		out.State = "needs_review"
	}
	return out
}
