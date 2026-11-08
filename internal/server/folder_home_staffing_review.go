package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
)

var _ personalassistant.FolderHomeStaffingHost = (*folderSetupHost)(nil)

// homeStaffingBasePlan removes the legacy automatic Home hire/default-model
// promise before attaching explicit choices. Project sharing, library, profile
// and later Open effects remain with their existing owners and disclosures.
func homeStaffingBasePlan(plan personalassistant.FolderSetupPlan) personalassistant.FolderSetupPlan {
	plan.Lines = append([]personalassistant.FolderPlanLine(nil), plan.Lines...)
	plan.Visual = personalassistant.CloneFolderSetupVisual(plan.Visual)
	modelSentence := ""
	if plan.Visual != nil {
		if plan.Visual.Model != nil {
			modelSentence = homeAgentsModelSentence(plan.Visual.Model)
		}
		plan.Visual.Model = nil
	}
	first := true
	for i := range plan.Lines {
		if plan.Lines[i].Kind != personalassistant.FolderPlanAgents {
			continue
		}
		if modelSentence != "" {
			plan.Lines[i].Detail = strings.TrimSuffix(plan.Lines[i].Detail, modelSentence)
		}
		if first {
			plan.Lines[i].Name = "Staff the Home with your reviewed team"
			plan.Lines[i].Detail = "Choose the exact saved or new agent for each required Home role. Saved profiles stay unchanged."
			first = false
		}
	}
	plan.Intent.Model, plan.Intent.HomeStaffing = nil, nil
	plan.Intent.RequiresHomeStaffing, plan.HomeStaffing = false, nil
	return plan.Stamped()
}

func (h *folderSetupHost) pendingFolderHomeTarget(ctx context.Context, req personalassistant.FolderSetupRequest) (sessionhttp.HomeStaffingTarget, error) {
	var target sessionhttp.HomeStaffingTarget
	if h == nil || h.builder == nil || h.builder.sessionHandler == nil || req.Offer.Setup != nil || req.Plan.HomeStaffing == nil || req.Plan.HomeStaffing.ContextDigest == "" {
		return target, personalassistant.ErrFolderPlanChanged
	}
	base, err := h.plan(ctx, req)
	if err != nil {
		return target, err
	}
	if !base.Intent.Portfolio || !base.Intent.StaffsHome {
		return target, personalassistant.ErrFolderPlanChanged
	}
	base = homeStaffingBasePlan(base)
	contextDigest := personalassistant.FolderHomeStaffingContext(req, base)
	if contextDigest == "" || contextDigest != req.Plan.HomeStaffing.ContextDigest {
		return target, personalassistant.ErrFolderPlanChanged
	}
	req.Plan = base
	return h.folderHomeReviewTarget(ctx, req)
}

func (h *folderSetupHost) RecommendFolderHomeStaffing(ctx context.Context, req personalassistant.FolderSetupRequest) (*rosterstaffing.Advice, error) {
	target, err := h.pendingFolderHomeTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return h.builder.sessionHandler.RecommendHomeStaffing(ctx, target), nil
}

func (h *folderSetupHost) SelectFolderHomeStaffing(ctx context.Context, req personalassistant.FolderSetupRequest, choice rosterstaffing.RoleChoice, advice *rosterstaffing.Advice) (*rosterstaffing.TeamIntent, error) {
	target, err := h.pendingFolderHomeTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return h.builder.sessionHandler.ReviewHomeStaffingChoices(ctx, target, []rosterstaffing.RoleChoice{choice}, advice)
}
