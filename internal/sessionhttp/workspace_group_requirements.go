package sessionhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

type createWorkspaceGroupPlan struct {
	claim          grouprequirements.Claim
	sourceTemplate projecttemplates.Template
}

func groupRequirementCreateDigest(request createWorkspaceRequest) (string, error) {
	request.GroupRequirementReview = false
	request.GroupReviewToken = ""
	request.IdempotencyKey = ""
	return grouprequirements.DigestInput(request)
}

func (h *Handler) prepareCreateWorkspaceGroupRequirement(
	ctx context.Context,
	w http.ResponseWriter,
	request createWorkspaceRequest,
	template projecttemplates.Template,
) (projecttemplates.Template, *createWorkspaceGroupPlan, bool) {
	if template.GroupRequirement == nil {
		if request.GroupRequirementReview {
			_ = orihttp.RespondSuccess(w, map[string]any{"group_requirement_review": grouprequirements.Review{
				Evaluation: grouprequirements.Evaluation{
					State: grouprequirements.StateLegacy, Summary: "This template uses its existing placement behavior.",
				},
			}})
			return template, nil, true
		}
		return template, nil, false
	}
	if h == nil || h.groupRequirements == nil || h.currentUserID == nil {
		respondGroupRequirementUnavailable(w)
		return projecttemplates.Template{}, nil, true
	}
	ownerUserID, err := h.currentUserID(ctx)
	if err != nil || strings.TrimSpace(ownerUserID) == "" {
		respondGroupRequirementUnavailable(w)
		return projecttemplates.Template{}, nil, true
	}
	inputDigest, err := groupRequirementCreateDigest(request)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "workspace review input is invalid")
		return projecttemplates.Template{}, nil, true
	}
	input := grouprequirements.Input{
		OwnerUserID: strings.TrimSpace(ownerUserID), OperationKind: grouprequirements.OperationCreateWorkspace,
		Template: template, Composition: request.GroupComposition, CreateHome: request.CreateRequiredHome,
		RequestedParentID: request.ParentID, InputDigest: inputDigest,
	}
	if request.GroupRequirementReview {
		review, reviewErr := h.groupRequirements.Review(ctx, input)
		if reviewErr != nil {
			respondGroupRequirementError(w, reviewErr)
			return projecttemplates.Template{}, nil, true
		}
		_ = orihttp.RespondSuccess(w, map[string]any{"group_requirement_review": review})
		return template, nil, true
	}
	claim, claimErr := h.groupRequirements.Claim(ctx, input, request.GroupReviewToken, request.IdempotencyKey)
	if claimErr != nil {
		respondGroupRequirementError(w, claimErr)
		return projecttemplates.Template{}, nil, true
	}
	return claim.EffectiveTemplate, &createWorkspaceGroupPlan{claim: claim, sourceTemplate: template}, false
}

func (h *Handler) respondCreateWorkspaceGroupReplay(ctx context.Context, w http.ResponseWriter, plan *createWorkspaceGroupPlan) bool {
	if plan == nil || h.workspaceTaskStore == nil || h.store == nil {
		return false
	}
	operation := plan.claim.Operation
	project, err := h.workspaceTaskStore.Get(operation.ChildWorkspaceID)
	if err != nil || project == nil {
		return false
	}
	canonical := project
	if reader, ok := h.workspaceTaskStore.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	}); ok {
		canonical, err = reader.GetFolderWorkspace(operation.ChildWorkspaceID)
		if err != nil || canonical == nil {
			respondGroupOperationIncomplete(w, "The operation-owned workspace could not be read for retry.")
			return true
		}
	}
	provenance := canonical.GetTemplateProvenance()
	observed := provenance != nil && provenance.GroupRequirement != nil &&
		provenance.GroupRequirement.StructurallyValid() &&
		provenance.GroupRequirement.OperationDigest == operation.OperationDigest
	if observed && plan.claim.Snapshot.SelectedComposition == grouprequirements.CompositionGrouped {
		link := project.GetAssistantProjectLink()
		observed = canonical.ParentID == operation.HomeWorkspaceID && link != nil &&
			link.ID == operation.ProjectLinkID && link.StationWorkspaceID == operation.HomeWorkspaceID
		if observed {
			home, homeErr := h.workspaceTaskStore.Get(operation.HomeWorkspaceID)
			observed = homeErr == nil && home != nil
			if observed {
				state := home.GetAssistantProgramState()
				observed = state != nil && containsWorkspaceID(state.LinkedProjectIDs, project.ID)
			}
		}
	}
	if !observed {
		if project.GetAssistantProjectLink() != nil {
			respondGroupOperationIncomplete(w, "The existing operation needs guided reconciliation before it can continue.")
			return true
		}
		// The deterministic child is operation-owned and has no live membership;
		// remove only that incomplete shell so this retry can recreate it.
		if err := h.rollbackIncompleteGroupWorkspace(ctx, operation.ChildWorkspaceID, operation.OperationDigest, string(operation.Status), seedAgentsResult{}); err != nil {
			respondGroupOperationIncomplete(w, "The operation-owned incomplete workspace could not be rolled back safely.")
			return true
		}
		return false
	}
	if err := h.groupRequirements.Mark(ctx, operation, grouprequirements.OperationSucceeded); err != nil {
		respondGroupOperationIncomplete(w, "The completed workspace receipt could not be finalized.")
		return true
	}
	sessionWorkspace, err := h.store.GetWorkspace(ctx, operation.ChildWorkspaceID)
	if err != nil || sessionWorkspace == nil {
		respondGroupOperationIncomplete(w, "The completed workspace could not be loaded.")
		return true
	}
	response := map[string]any{"success": true, "folder": sessionWorkspace, "idempotent_replay": true}
	if operation.HomeWorkspaceID != "" {
		response["assistant_station_id"] = operation.HomeWorkspaceID
	}
	_ = orihttp.RespondCreated(w, response)
	return true
}

func (h *Handler) respondWorkspaceProjectGroupReplay(ctx context.Context, w http.ResponseWriter, plan *createWorkspaceGroupPlan) bool {
	if plan == nil || plan.claim.Operation.Status != grouprequirements.OperationSucceeded || h.workspaceTaskStore == nil || h.store == nil {
		return false
	}
	operation := plan.claim.Operation
	project, err := h.workspaceTaskStore.Get(operation.ChildWorkspaceID)
	if err != nil || project == nil {
		respondGroupOperationIncomplete(w, "The completed project workspace could not be loaded for retry.")
		return true
	}
	canonical := project
	if reader, ok := h.workspaceTaskStore.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	}); ok {
		canonical, err = reader.GetFolderWorkspace(operation.ChildWorkspaceID)
	}
	observed := err == nil && canonical != nil
	if observed {
		provenance := canonical.GetTemplateProvenance()
		observed = provenance != nil && provenance.GroupRequirement != nil &&
			provenance.GroupRequirement.StructurallyValid() && provenance.GroupRequirement.OperationDigest == operation.OperationDigest
	}
	if observed && plan.claim.Snapshot.SelectedComposition == grouprequirements.CompositionGrouped {
		link := project.GetAssistantProjectLink()
		observed = canonical.ParentID == operation.HomeWorkspaceID && link != nil &&
			link.ID == operation.ProjectLinkID && link.StationWorkspaceID == operation.HomeWorkspaceID
		if observed {
			home, homeErr := h.workspaceTaskStore.Get(operation.HomeWorkspaceID)
			if homeErr != nil || home == nil {
				observed = false
			} else {
				state := home.GetAssistantProgramState()
				observed = state != nil && containsWorkspaceID(state.LinkedProjectIDs, project.ID)
			}
		}
	}
	if !observed {
		_ = h.groupRequirements.Mark(ctx, operation, grouprequirements.OperationReconcileRequired)
		respondGroupOperationIncomplete(w, "The completed project placement needs guided reconciliation before retry.")
		return true
	}
	sessionWorkspace, err := h.store.GetWorkspace(ctx, operation.ChildWorkspaceID)
	if err != nil || sessionWorkspace == nil {
		respondGroupOperationIncomplete(w, "The completed project workspace could not be loaded for retry.")
		return true
	}
	h.hydrateWorkspaceMetadataInto(sessionWorkspace)
	if strings.TrimSpace(sessionWorkspace.ProjectPath) == "" {
		respondGroupOperationIncomplete(w, "The completed project files could not be observed for retry.")
		return true
	}
	_ = orihttp.RespondCreated(w, map[string]any{
		"success": true, "project_path": sessionWorkspace.ProjectPath,
		"workspace": h.buildWorkspaceDetailResponse(sessionWorkspace), "group_requirement": plan.claim.Snapshot,
		"idempotent_replay": true,
	})
	return true
}

func respondGroupOperationIncomplete(w http.ResponseWriter, summary string) {
	_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error": summary,
		"group_requirement": map[string]any{
			"state": "operation_incomplete", "reason": "operation_incomplete", "summary": summary,
			"actions": []grouprequirements.Action{grouprequirements.ActionRetry},
		},
	})
}

func (h *Handler) finalizeCreateWorkspaceGroupRequirement(ctx context.Context, workspaceID string, plan *createWorkspaceGroupPlan, template projecttemplates.Template) error {
	if plan == nil {
		return nil
	}
	if h.workspaceTaskStore == nil || h.groupRequirements == nil {
		return grouprequirements.ErrUnavailable
	}
	provenance := newTemplateProvenance(template, plan.claim.Snapshot)
	if err := h.workspaceTaskStore.Update(workspaceID, func(current *agentworkspace.Workspace) error {
		current.SetTemplateProvenance(provenance)
		return nil
	}); err != nil {
		_ = h.groupRequirements.Mark(ctx, plan.claim.Operation, grouprequirements.OperationReconcileRequired)
		return fmt.Errorf("persist group requirement snapshot: %w", err)
	}
	if err := h.groupRequirements.Mark(ctx, plan.claim.Operation, grouprequirements.OperationChildReady); err != nil {
		return err
	}
	if plan.claim.Snapshot.SelectedComposition == grouprequirements.CompositionStandalone {
		return nil
	}
	station, _, err := agentworkspace.NewAssistantProgramStore(h.workspaceTaskStore).EnsureProjectStation(workspaceID)
	if err != nil || station == nil || station.ID != plan.claim.Operation.HomeWorkspaceID {
		_ = h.groupRequirements.Mark(ctx, plan.claim.Operation, grouprequirements.OperationReconcileRequired)
		if err != nil {
			return fmt.Errorf("establish required group link: %w", err)
		}
		return errors.New("required group link resolved to the wrong Home")
	}
	project, err := h.workspaceTaskStore.Get(workspaceID)
	if err != nil || project == nil {
		return grouprequirements.ErrUnavailable
	}
	link := project.GetAssistantProjectLink()
	if link == nil || link.ID != plan.claim.Operation.ProjectLinkID || link.StationWorkspaceID != station.ID || project.ParentID != station.ID {
		_ = h.groupRequirements.Mark(ctx, plan.claim.Operation, grouprequirements.OperationReconcileRequired)
		return errors.New("required group consequences were not observed")
	}
	state := station.GetAssistantProgramState()
	if state == nil || !containsWorkspaceID(state.LinkedProjectIDs, workspaceID) {
		_ = h.groupRequirements.Mark(ctx, plan.claim.Operation, grouprequirements.OperationReconcileRequired)
		return errors.New("required reciprocal group membership was not observed")
	}
	return h.groupRequirements.Mark(ctx, plan.claim.Operation, grouprequirements.OperationLinkReady)
}

func containsWorkspaceID(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

func (h *Handler) completeCreateWorkspaceGroupRequirement(ctx context.Context, plan *createWorkspaceGroupPlan) error {
	if plan == nil {
		return nil
	}
	return h.groupRequirements.Mark(ctx, plan.claim.Operation, grouprequirements.OperationSucceeded)
}

func (h *Handler) rollbackIncompleteGroupWorkspace(ctx context.Context, workspaceID, operationDigest, operationStatus string, seed seedAgentsResult) error {
	// This workspace ID is deterministic and owned by the reviewed operation.
	// The exact operation digest plus absence of a live link is required before
	// Required-contract lifecycle protection can be bypassed.
	var err error
	if h.workspaceTaskStore != nil {
		if deleter, ok := h.workspaceTaskStore.(interface {
			DeleteReviewedGroupRequirementOperation(string, string, string) error
		}); ok {
			err = deleter.DeleteReviewedGroupRequirementOperation(workspaceID, operationDigest, operationStatus)
		} else {
			err = h.workspaceTaskStore.Delete(workspaceID)
		}
	} else if h.store != nil {
		err = h.store.DeleteWorkspace(ctx, workspaceID)
	}
	if err == nil {
		h.rollbackSeededAgents(seed)
	}
	return err
}

func projectGroupRequirementDigest(workspaceID string, request createWorkspaceProjectRequest) (string, error) {
	request.GroupRequirementReview = false
	request.GroupReviewToken = ""
	request.IdempotencyKey = ""
	return grouprequirements.DigestInput(struct {
		WorkspaceID string                        `json:"workspace_id"`
		Request     createWorkspaceProjectRequest `json:"request"`
	}{WorkspaceID: strings.TrimSpace(workspaceID), Request: request})
}

func (h *Handler) prepareWorkspaceProjectGroupRequirement(
	ctx context.Context,
	w http.ResponseWriter,
	workspaceID string,
	request createWorkspaceProjectRequest,
	template projecttemplates.Template,
) (projecttemplates.Template, *createWorkspaceGroupPlan, bool) {
	if template.GroupRequirement == nil {
		if request.GroupRequirementReview {
			_ = orihttp.RespondSuccess(w, map[string]any{"group_requirement_review": grouprequirements.Review{
				Evaluation: grouprequirements.Evaluation{State: grouprequirements.StateLegacy, Summary: "This template uses its existing placement behavior."},
			}})
			return template, nil, true
		}
		return template, nil, false
	}
	if h == nil || h.groupRequirements == nil || h.currentUserID == nil {
		respondGroupRequirementUnavailable(w)
		return projecttemplates.Template{}, nil, true
	}
	ownerUserID, err := h.currentUserID(ctx)
	if err != nil || strings.TrimSpace(ownerUserID) == "" {
		respondGroupRequirementUnavailable(w)
		return projecttemplates.Template{}, nil, true
	}
	digest, err := projectGroupRequirementDigest(workspaceID, request)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "project review input is invalid")
		return projecttemplates.Template{}, nil, true
	}
	input := grouprequirements.Input{
		OwnerUserID: strings.TrimSpace(ownerUserID), OperationKind: grouprequirements.OperationCreateProject,
		Template: template, Composition: request.GroupComposition, CreateHome: request.CreateRequiredHome,
		TargetWorkspaceID: workspaceID, InputDigest: digest,
	}
	if request.GroupRequirementReview {
		review, reviewErr := h.groupRequirements.Review(ctx, input)
		if reviewErr != nil {
			respondGroupRequirementError(w, reviewErr)
			return projecttemplates.Template{}, nil, true
		}
		_ = orihttp.RespondSuccess(w, map[string]any{"group_requirement_review": review})
		return template, nil, true
	}
	claim, claimErr := h.groupRequirements.Claim(ctx, input, request.GroupReviewToken, request.IdempotencyKey)
	if claimErr != nil {
		respondGroupRequirementError(w, claimErr)
		return projecttemplates.Template{}, nil, true
	}
	return claim.EffectiveTemplate, &createWorkspaceGroupPlan{claim: claim, sourceTemplate: template}, false
}

type groupRequirementHomeRequest struct {
	TemplateID       string `json:"template_id,omitempty"`
	TemplatePath     string `json:"template_path,omitempty"`
	GroupReviewToken string `json:"group_review_token,omitempty"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
}

func groupRequirementHomeDigest(request groupRequirementHomeRequest) (string, error) {
	return grouprequirements.DigestInput(struct {
		TemplateID   string `json:"template_id,omitempty"`
		TemplatePath string `json:"template_path,omitempty"`
	}{TemplateID: strings.TrimSpace(request.TemplateID), TemplatePath: strings.TrimSpace(request.TemplatePath)})
}

func (h *Handler) prepareGroupRequirementHomeAction(
	ctx context.Context,
	w http.ResponseWriter,
	request groupRequirementHomeRequest,
) (grouprequirements.Input, bool) {
	if (strings.TrimSpace(request.TemplateID) == "") == (strings.TrimSpace(request.TemplatePath) == "") {
		_ = orihttp.RespondBadRequest(w, "specify exactly one template_id or template_path")
		return grouprequirements.Input{}, false
	}
	if h == nil || h.groupRequirements == nil || h.currentUserID == nil {
		respondGroupRequirementUnavailable(w)
		return grouprequirements.Input{}, false
	}
	template, err := h.resolveProjectTemplate(request.TemplateID, request.TemplatePath)
	if err != nil {
		h.respondWorkspaceProjectError(w, err)
		return grouprequirements.Input{}, false
	}
	readiness := h.revalidateBlueprintReadiness(template)
	if blueprintCreationBlocked(template, readiness) {
		respondBlueprintReadinessConflict(w, template, readiness)
		return grouprequirements.Input{}, false
	}
	if template.GroupRequirement == nil || template.GroupRequirement.Policy == projecttemplates.GroupPolicyNone {
		_ = orihttp.RespondBadRequest(w, "this template does not declare a grouped Home")
		return grouprequirements.Input{}, false
	}
	ownerUserID, err := h.currentUserID(ctx)
	if err != nil || strings.TrimSpace(ownerUserID) == "" {
		respondGroupRequirementUnavailable(w)
		return grouprequirements.Input{}, false
	}
	digest, err := groupRequirementHomeDigest(request)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "group review input is invalid")
		return grouprequirements.Input{}, false
	}
	return grouprequirements.Input{
		OwnerUserID: strings.TrimSpace(ownerUserID), OperationKind: grouprequirements.OperationPrepareHome,
		Template: template, Composition: grouprequirements.CompositionGrouped, CreateHome: true, InputDigest: digest,
	}, true
}

// ReviewGroupRequirementHome returns an inert receipt for creating or reusing
// only the canonical Home. Project/workspace creation is deliberately a later
// request with its own review and confirmation.
func (h *Handler) ReviewGroupRequirementHome(w http.ResponseWriter, r *http.Request) {
	var request groupRequirementHomeRequest
	if !orihttp.ParseJSONBody(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.GroupReviewToken) != "" || strings.TrimSpace(request.IdempotencyKey) != "" {
		_ = orihttp.RespondBadRequest(w, "Home review does not accept commit fields")
		return
	}
	input, ok := h.prepareGroupRequirementHomeAction(r.Context(), w, request)
	if !ok {
		return
	}
	review, err := h.groupRequirements.Review(r.Context(), input)
	if err != nil {
		respondGroupRequirementError(w, err)
		return
	}
	if review.State != grouprequirements.StateReadyGrouped || strings.TrimSpace(review.Token) == "" {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": review.Summary, "group_requirement": review.Evaluation,
		})
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"group_requirement_review": review})
}

// CommitGroupRequirementHome applies one reviewed Home-only consequence. It
// never creates, reserves, moves, or links a project workspace.
func (h *Handler) CommitGroupRequirementHome(w http.ResponseWriter, r *http.Request) {
	var request groupRequirementHomeRequest
	if !orihttp.ParseJSONBody(w, r, &request) {
		return
	}
	input, ok := h.prepareGroupRequirementHomeAction(r.Context(), w, request)
	if !ok {
		return
	}
	claim, err := h.groupRequirements.Claim(r.Context(), input, request.GroupReviewToken, request.IdempotencyKey)
	if err != nil {
		respondGroupRequirementError(w, err)
		return
	}
	homeID := strings.TrimSpace(claim.Operation.HomeWorkspaceID)
	current := h.groupRequirements.Evaluate(input)
	if homeID == "" || current.State != grouprequirements.StateReadyGrouped || current.HomeWorkspaceID != homeID {
		respondGroupRequirementError(w, grouprequirements.ErrReviewStale)
		return
	}
	if err := h.groupRequirements.Mark(r.Context(), claim.Operation, grouprequirements.OperationSucceeded); err != nil {
		respondGroupRequirementUnavailable(w)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"success": true,
		"group_requirement": map[string]any{
			"state": "home_ready", "summary": "The canonical group is ready. No project workspace was created.",
			"home_workspace_id": homeID, "home_name": current.HomeName,
			"home_created": claim.HomeCreated, "idempotent_replay": claim.Replayed,
		},
	})
}

func respondGroupRequirementUnavailable(w http.ResponseWriter) {
	_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error": "Template group placement is unavailable.",
		"group_requirement": map[string]any{
			"state": grouprequirements.StateSourceUnavailable, "reason": "target_owner_unavailable",
			"summary": "The current owner or canonical storage could not be verified.",
			"actions": []grouprequirements.Action{grouprequirements.ActionRetry},
		},
	})
}

func respondGroupRequirementError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	reason := "review_stale"
	summary := "The placement review changed or expired. Review the destination again."
	actions := []grouprequirements.Action{grouprequirements.ActionRetry}
	switch {
	case errors.Is(err, grouprequirements.ErrReviewRequired):
		reason = "review_stale"
		summary = "Review the workspace destination before creating it."
	case errors.Is(err, grouprequirements.ErrOperationConflict):
		reason = "review_stale"
		summary = "This idempotency key belongs to a different reviewed request."
	case errors.Is(err, grouprequirements.ErrUnavailable):
		respondGroupRequirementUnavailable(w)
		return
	}
	_ = orihttp.RespondJSON(w, status, map[string]any{
		"error": summary,
		"group_requirement": map[string]any{
			"state": "review_required", "reason": reason, "summary": summary, "actions": actions,
		},
	})
}
