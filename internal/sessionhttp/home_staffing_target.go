package sessionhttp

import (
	"reflect"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// HomeStaffingTarget is trusted host metadata from the existing review owner,
// never browser/model input. That owner binds its identity/declaration to its
// original context and rechecks it before commit. This shared read-only seam
// does not produce a Home key, operation, approval or staffing effect.
type HomeStaffingTarget struct {
	OwnerUserID      string                                 `json:"-"`
	TemplateID       string                                 `json:"-"`
	TemplateRevision string                                 `json:"-"`
	HomeID           string                                 `json:"-"`
	Declaration      *workspace.AssistantProgramDeclaration `json:"-"`
	Personal         bool                                   `json:"-"`
}

func groupHomeStaffingTarget(input grouprequirements.Input, current grouprequirements.Evaluation) HomeStaffingTarget {
	return HomeStaffingTarget{OwnerUserID: input.OwnerUserID, TemplateID: input.GroupTemplateID, TemplateRevision: input.GroupTemplateRevision,
		HomeID: current.HomeWorkspaceID, Declaration: current.HomeDeclaration, Personal: current.HomeOwner != nil}
}

func (h *Handler) homeStaffingOccupancy(target HomeStaffingTarget) (*workspace.AssistantProgramState, map[string]bool, error) {
	if h == nil || target.OwnerUserID == "" || target.TemplateID == "" || target.TemplateRevision == "" || target.Declaration == nil {
		return nil, nil, grouprequirements.ErrReviewRequired
	}
	occupied := map[string]bool{}
	if target.HomeID == "" {
		return nil, occupied, nil
	}
	if h.workspaceTaskStore == nil {
		return nil, nil, grouprequirements.ErrUnavailable
	}
	home, err := h.workspaceTaskStore.Get(target.HomeID)
	if err != nil || home == nil || home.OwnerUserID != target.OwnerUserID || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return nil, nil, grouprequirements.ErrReviewStale
	}
	state := home.GetAssistantProgramState()
	if state == nil || !reflect.DeepEqual(state.Declaration, target.Declaration) {
		return nil, nil, grouprequirements.ErrReviewStale
	}
	for _, binding := range state.HomeBindings.Bindings {
		occupied[binding.RoleID] = true
	}
	for _, instance := range home.GetAgentInstances() {
		if instance.RoleID != "" {
			occupied[instance.RoleID] = true
		}
	}
	return state, occupied, nil
}
