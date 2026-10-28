package sessionhttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type workspaceGroupCreationReference struct {
	ReviewToken    string `json:"group_review_token"`
	IdempotencyKey string `json:"idempotency_key"`
	InputDigest    string `json:"input_digest"`
}

// ObserveWorkspaceGroupCreation reads the existing requirement-owned creation
// journal. Unlike the POST replay helpers it cannot roll back/recreate a shell,
// finalize a receipt, connect membership or continue a partly completed action.
func (h *Handler) ObserveWorkspaceGroupCreation(w http.ResponseWriter, r *http.Request) {
	var reference workspaceGroupCreationReference
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&reference)
		if err == nil && decoder.Decode(&struct{}{}) != io.EOF {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Use only the original group review token, operation key and input digest.")
		return
	}
	if h == nil || h.groupRequirements == nil || h.currentUserID == nil || h.workspaceTaskStore == nil || h.store == nil {
		respondGroupRequirementUnavailable(w)
		return
	}
	owner, err := h.currentUserID(r.Context())
	if err != nil || strings.TrimSpace(owner) == "" {
		respondGroupRequirementUnavailable(w)
		return
	}
	operation, receipt, err := h.groupRequirements.ObserveWorkspaceCreation(r.Context(), owner, reference.ReviewToken, reference.IdempotencyKey, reference.InputDigest)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, grouprequirements.ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, grouprequirements.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		respondGroupCreationObservation(w, status, "outcome_unknown", "The original workspace creation could not be observed. Do not submit another creation; reading status has resumed nothing.")
		return
	}
	if operation.Status != grouprequirements.OperationSucceeded {
		respondGroupCreationObservation(w, http.StatusAccepted, string(operation.Status), "The original workspace operation is incomplete. Reading status has not rolled back, recreated, staffed or resumed it.")
		return
	}
	project, err := h.workspaceTaskStore.Get(operation.ChildWorkspaceID)
	if err != nil || project == nil {
		respondGroupCreationResourceUnavailable(w)
		return
	}
	canonical := project
	if reader, ok := h.workspaceTaskStore.(interface {
		GetFolderWorkspace(string) (*workspace.Workspace, error)
	}); ok {
		canonical, err = reader.GetFolderWorkspace(operation.ChildWorkspaceID)
	}
	if err != nil || canonical == nil || canonical.ID != operation.ChildWorkspaceID || canonical.OwnerUserID != operation.OwnerUserID ||
		canonical.Kind == "group" || canonical.Status == workspace.StatusTrashed || canonical.Status == workspace.StatusMissing {
		respondGroupCreationResourceUnavailable(w)
		return
	}
	provenance := canonical.GetTemplateProvenance()
	var snapshot *workspace.GroupRequirementSnapshot
	if provenance != nil {
		snapshot = provenance.GroupRequirement
	}
	if snapshot == nil || !snapshot.StructurallyValid() || snapshot.OperationDigest != operation.OperationDigest ||
		snapshot.ReviewDigest != receipt.ReviewDigest || snapshot.DefinitionDigest != receipt.DefinitionDigest || snapshot.TemplateID != receipt.TemplateID ||
		snapshot.TemplateRevision != receipt.TemplateRevision || snapshot.VariantID != receipt.VariantID || snapshot.VariantRevision != receipt.VariantRevision ||
		snapshot.HomeWorkspaceID != operation.HomeWorkspaceID || snapshot.ProjectLinkID != operation.ProjectLinkID {
		respondGroupCreationResourceUnavailable(w)
		return
	}
	link := canonical.GetAssistantProjectLink()
	if snapshot.SelectedComposition == grouprequirements.CompositionGrouped {
		if receipt.ProgramKey == nil || snapshot.ProgramKey == nil || snapshot.ProgramKey.Normalize() != receipt.ProgramKey.Normalize() ||
			canonical.ParentID != operation.HomeWorkspaceID || link == nil || link.ID != operation.ProjectLinkID ||
			link.StationWorkspaceID != operation.HomeWorkspaceID || link.Key.Normalize() != receipt.ProgramKey.Normalize() ||
			!reflect.DeepEqual(link.HomeProvider, snapshot.HomeProvider) || !reflect.DeepEqual(link.ProjectProvider, snapshot.ProjectProvider) {
			respondGroupCreationResourceUnavailable(w)
			return
		}
		home, homeErr := h.workspaceTaskStore.Get(operation.HomeWorkspaceID)
		state := home.GetAssistantProgramState()
		if homeErr != nil || state == nil || !containsWorkspaceID(state.LinkedProjectIDs, canonical.ID) {
			respondGroupCreationResourceUnavailable(w)
			return
		}
		resolved, status := h.resolveGroupTemplateHome(*receipt.ProgramKey, state.Declaration)
		if status != groupTemplateHomeExists || resolved == nil || resolved.ID != operation.HomeWorkspaceID {
			respondGroupCreationResourceUnavailable(w)
			return
		}
	} else if snapshot.SelectedComposition != grouprequirements.CompositionStandalone || canonical.ParentID != strings.TrimSpace(receipt.RequestedParentID) || link != nil || operation.HomeWorkspaceID != "" || operation.ProjectLinkID != "" {
		respondGroupCreationResourceUnavailable(w)
		return
	}
	// Home-free composition may still have an explicitly reviewed ordinary
	// organizational parent. It is not inferred Home membership or link access.
	if canonical.ParentID != "" {
		if err := h.requireGroupParent(r.Context(), canonical.ParentID); err != nil {
			respondGroupCreationResourceUnavailable(w)
			return
		}
	}
	current, err := h.store.GetWorkspace(r.Context(), canonical.ID)
	if err != nil || current == nil {
		respondGroupCreationResourceUnavailable(w)
		return
	}
	current = h.hydrateWorkspaceMetadataFromFileStore(current)
	_ = orihttp.RespondSuccess(w, map[string]any{
		"group_requirement_operation": map[string]any{
			"state": "complete", "workspace_id": canonical.ID,
			"home_workspace_id": operation.HomeWorkspaceID, "home_created": operation.HomeCreated,
			"project_link_id": operation.ProjectLinkID, "operation_digest": operation.OperationDigest,
			"layout_subject_new": true, "requires_composite_layout": operation.HomeCreated,
			"summary": "The original workspace creation is complete. Reading status has resumed no setup, staffing, task, application opening or positioning.",
		},
		"folder": h.buildWorkspaceDetailResponse(current),
	})
}

func respondGroupCreationObservation(w http.ResponseWriter, status int, state, summary string) {
	_ = orihttp.RespondJSON(w, status, map[string]any{"group_requirement_operation": map[string]any{"state": state, "summary": summary}})
}

func respondGroupCreationResourceUnavailable(w http.ResponseWriter) {
	respondGroupCreationObservation(w, http.StatusConflict, "resource_unavailable", "The original workspace or its exact Home/link identity is no longer verifiable. Nothing was recreated, reparented or positioned.")
}
