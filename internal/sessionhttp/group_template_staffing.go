package sessionhttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

type groupHomeRoleChoice = rosterstaffing.RoleChoice

func (h *Handler) reviewGroupHomeTeam(ctx context.Context, input grouprequirements.Input, request groupTemplateHomeRequest) (*rosterstaffing.TeamIntent, error) {
	if len(request.RoleStaffing) == 0 {
		if request.StaffingAdviceToken != "" {
			return nil, grouprequirements.ErrReviewRequired
		}
		return nil, nil
	}
	current := h.groupRequirements.Evaluate(input)
	if current.State != grouprequirements.StateReadyGrouped || current.HomeDeclaration == nil {
		return nil, grouprequirements.ErrReviewStale
	}
	hasRecommendation := false
	for _, choice := range request.RoleStaffing {
		hasRecommendation = hasRecommendation || choice.Recommendation != ""
	}
	if (request.StaffingAdviceToken != "") != hasRecommendation {
		return nil, grouprequirements.ErrReviewRequired
	}
	var advice *rosterstaffing.Advice
	if request.StaffingAdviceToken != "" {
		base := request
		base.RoleStaffing, base.StaffingAdviceToken = nil, ""
		lookup := input
		var err error
		lookup.InputDigest, err = groupTemplateHomeDigest(base)
		if err != nil {
			return nil, grouprequirements.ErrReviewRequired
		}
		advice, err = h.groupRequirements.ReviewedHomeAdvice(ctx, lookup, request.StaffingAdviceToken)
		if err != nil {
			return nil, err
		}
	}
	return h.ReviewHomeStaffingChoices(ctx, groupHomeStaffingTarget(input, current), request.RoleStaffing, advice)
}

func publicGroupHomeTeam(team *rosterstaffing.TeamIntent) []groupHomeRoleChoice {
	return team.Choices()
}
