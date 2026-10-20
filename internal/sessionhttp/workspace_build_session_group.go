package sessionhttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// workspaceBuildGroupPrerequisite is the group the drafted blueprint must be
// created inside, as grouprequirements.Evaluate sees it right now: still to
// be prepared (home_creation_review_required, with the proposed name) or
// ready (ready_grouped, with the existing Home). It is the same projection the
// form's template-agent plan carries, read through the form's own template
// resolver, so the pane and the form never disagree about the group.
//
// nil when the draft needs no group, when the build is no longer open, and
// when a ready setup plan sets the group up itself.
func (h *Handler) workspaceBuildGroupPrerequisite(ctx context.Context, userID string, session *personalassistant.WorkspaceBuildSession, plan *personalassistant.BuildFolderPlan) *templateGroupRequirementPlan {
	if h == nil || h.workspaceBuild == nil || session == nil || session.NeedsHome == nil ||
		session.Status != personalassistant.WorkspaceBuildOpen || (plan != nil && plan.Ready) {
		return nil
	}
	catalog, err := h.workspaceBuild.Catalog(ctx, userID)
	if err != nil {
		return nil
	}
	drafted, ok := buildValidation{catalog: catalog}.currentTemplate(session.Draft)
	if !ok || drafted.GroupRequirement == nil {
		return nil
	}
	template, err := h.resolveProjectTemplate(drafted.ID, "")
	if err != nil || template.GroupRequirement == nil {
		return nil
	}
	ownerUserID, ownerErr := "", error(grouprequirements.ErrUnavailable)
	if h.currentUserID != nil {
		ownerUserID, ownerErr = h.currentUserID(ctx)
		ownerUserID = strings.TrimSpace(ownerUserID)
		if ownerErr == nil && ownerUserID == "" {
			ownerErr = grouprequirements.ErrUnavailable
		}
	}
	return h.buildTemplateGroupRequirementPlan(template, grouprequirements.CompositionGrouped, ownerUserID, ownerErr, nil)
}
