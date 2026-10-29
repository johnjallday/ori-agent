package sessionhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ObserveGroupTemplateHome handles POST /api/workspaces/group-templates/observe.
// The original commit envelope is a lookup, not another confirmation. This
// deliberately does not prepare/re-resolve a template or call Claim/Mark: a
// source becoming unavailable cannot make reading accepted history execute it.
func (h *Handler) ObserveGroupTemplateHome(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeGroupTemplateHomeRequest(w, r, true)
	if !ok {
		return
	}
	if h == nil || h.groupRequirements == nil || h.currentUserID == nil || h.workspaceTaskStore == nil {
		respondGroupRequirementUnavailable(w)
		return
	}
	owner, err := h.currentUserID(r.Context())
	if err != nil || strings.TrimSpace(owner) == "" {
		respondGroupRequirementUnavailable(w)
		return
	}
	digest, err := groupTemplateHomeDigest(request)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "The original group request is invalid.")
		return
	}
	operation, receipt, err := h.groupRequirements.ObserveHomeOperation(r.Context(), owner, request.GroupReviewToken, request.IdempotencyKey, digest)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, grouprequirements.ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, grouprequirements.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		_ = orihttp.RespondJSON(w, status, map[string]any{"group_template": map[string]any{
			"state": "outcome_unknown", "summary": "The original group operation could not be observed. Do not submit another creation; reading status has resumed nothing.",
		}})
		return
	}
	if operation.Status != grouprequirements.OperationSucceeded {
		_ = orihttp.RespondJSON(w, http.StatusAccepted, map[string]any{"group_template": map[string]any{
			"state": string(operation.Status), "summary": "The original group operation is not complete. Reading status has resumed nothing.",
		}})
		return
	}
	home, err := h.workspaceTaskStore.Get(operation.HomeWorkspaceID)
	state := home.GetAssistantProgramState()
	if err != nil || home == nil || home.Kind != "group" || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing ||
		state == nil || state.SchemaVersion != workspace.AssistantProgramStateSchemaVersion || receipt.ProgramKey == nil ||
		state.Key.Normalize() != receipt.ProgramKey.Normalize() {
		respondObservedGroupHomeUnavailable(w)
		return
	}
	// Do not adopt another, same-name or replacement Home. Even a duplicate
	// stable-key claim is attention-required rather than layout authority.
	canonical, status := h.resolveGroupTemplateHome(*receipt.ProgramKey, state.Declaration)
	if status != groupTemplateHomeExists || canonical == nil || canonical.ID != operation.HomeWorkspaceID {
		respondObservedGroupHomeUnavailable(w)
		return
	}
	created := operation.HomeCreated && state.GroupTemplate != nil && state.GroupTemplate.ReviewDigest == operation.ReviewDigest
	summary := "The original group operation is complete. No staffing, setup or positioning was resumed by reading it."
	_ = orihttp.RespondSuccess(w, map[string]any{"group_template": map[string]any{
		"state": "home_ready", "summary": summary,
		"home_workspace_id": home.ID, "home_name": home.Name,
		"created_by_this_operation": created, "layout_subject_new": created,
		"idempotent_replay": true,
	}})
}

func respondObservedGroupHomeUnavailable(w http.ResponseWriter) {
	_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"group_template": map[string]any{
		"state": "resource_unavailable", "summary": "The original group resource is no longer verifiable. Nothing was recreated or repositioned; resolve its existing identity before continuing.",
	}})
}
