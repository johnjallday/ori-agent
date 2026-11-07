package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

const maxGroupTemplateHomeRequestBytes = 32 << 10

// groupTemplateHomeRequest is the complete accepted body for Group Template
// Home review and commit. Ownership, plugin, program, template, parent and
// project fields are absent. Exact Home-role choices are checked against the
// server's declaration; private source/capability evidence is never accepted.
type groupTemplateHomeRequest struct {
	GroupTemplateID     string                `json:"group_template_id"`
	Revision            string                `json:"revision"`
	Name                string                `json:"name"`
	GroupReviewToken    string                `json:"group_review_token,omitempty"`
	IdempotencyKey      string                `json:"idempotency_key,omitempty"`
	RecommendStaffing   bool                  `json:"recommend_staffing,omitempty"`
	RoleStaffing        []groupHomeRoleChoice `json:"role_staffing,omitempty"`
	StaffingAdviceToken string                `json:"staffing_advice_token,omitempty"`
}

type groupTemplateHomeReview struct {
	GroupTemplateID string                 `json:"group_template_id"`
	Revision        string                 `json:"revision"`
	Name            string                 `json:"name"`
	Reuse           bool                   `json:"reuse"`
	HomeWorkspaceID string                 `json:"home_workspace_id,omitempty"`
	HomeName        string                 `json:"home_name"`
	Summary         string                 `json:"summary"`
	ReviewToken     string                 `json:"review_token"`
	ReviewDigest    string                 `json:"review_digest"`
	ExpiresAt       time.Time              `json:"expires_at"`
	StaffingAdvice  *rosterstaffing.Advice `json:"staffing_advice,omitempty"`
	RoleStaffing    []groupHomeRoleChoice  `json:"role_staffing,omitempty"`
}

func decodeGroupTemplateHomeRequest(w http.ResponseWriter, r *http.Request, commit bool) (groupTemplateHomeRequest, bool) {
	var request groupTemplateHomeRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGroupTemplateHomeRequestBytes))
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&request); err == nil && decoder.Decode(&struct{}{}) != io.EOF {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "group template request contains unsupported or malformed fields")
		return groupTemplateHomeRequest{}, false
	}
	request.GroupTemplateID = strings.TrimSpace(request.GroupTemplateID)
	request.Revision = strings.TrimSpace(request.Revision)
	request.Name = strings.TrimSpace(request.Name)
	request.GroupReviewToken = strings.TrimSpace(request.GroupReviewToken)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.StaffingAdviceToken = strings.TrimSpace(request.StaffingAdviceToken)
	if (request.RoleStaffing != nil && len(request.RoleStaffing) == 0) || len(request.RoleStaffing) > rosterstaffing.MaxRoles ||
		(request.RecommendStaffing && (request.RoleStaffing != nil || request.StaffingAdviceToken != "")) {
		_ = orihttp.RespondBadRequest(w, "choose a recommendation action or exact Home roles; omit role_staffing for a Home-only review")
		return groupTemplateHomeRequest{}, false
	}
	if request.GroupTemplateID == "" || request.Revision == "" {
		_ = orihttp.RespondBadRequest(w, "group_template_id and revision are required")
		return groupTemplateHomeRequest{}, false
	}
	if err := projecttemplates.ValidateHomeDisplayName(request.Name); err != nil {
		_ = orihttp.RespondBadRequest(w, "group name must be 1-120 bytes of display text without paths or control characters")
		return groupTemplateHomeRequest{}, false
	}
	hasCommitFields := request.GroupReviewToken != "" || request.IdempotencyKey != ""
	if !commit && hasCommitFields {
		_ = orihttp.RespondBadRequest(w, "group template review does not accept commit fields")
		return groupTemplateHomeRequest{}, false
	}
	if commit && request.RecommendStaffing {
		_ = orihttp.RespondBadRequest(w, "recommendation is a read-only review action, never a commit action")
		return groupTemplateHomeRequest{}, false
	}
	if commit && (request.GroupReviewToken == "" || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 200) {
		_ = orihttp.RespondBadRequest(w, "group template commit requires its review token and idempotency key")
		return groupTemplateHomeRequest{}, false
	}
	return request, true
}

func groupTemplateHomeDigest(request groupTemplateHomeRequest) (string, error) {
	// Domain separation keeps this consent distinct from the direct required-
	// group recovery receipt, whose digest covers only a project template ref.
	value := map[string]any{
		"domain": "group_template_home:v1", "group_template_id": request.GroupTemplateID,
		"revision": request.Revision, "name": request.Name,
	}
	if len(request.RoleStaffing) > 0 || request.StaffingAdviceToken != "" {
		value["domain"], value["role_staffing"], value["staffing_advice_token"] = "group_template_home:v2", request.RoleStaffing, request.StaffingAdviceToken
	}
	return grouprequirements.DigestInput(value)
}

func respondGroupTemplateConflict(w http.ResponseWriter, reason, summary string, availability *groupTemplateAvailability) {
	payload := map[string]any{"state": "review_required", "reason": reason, "summary": summary}
	if availability != nil {
		payload["availability"] = availability
	}
	_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": summary, "group_template": payload})
}

// prepareGroupTemplateHome re-derives the Group Template catalog for the
// current owner and binds the request to one exact, still-trusted selection.
// It performs no mutation.
func (h *Handler) prepareGroupTemplateHome(ctx context.Context, w http.ResponseWriter, request groupTemplateHomeRequest) (grouprequirements.Input, bool) {
	if h == nil || h.groupRequirements == nil || h.currentUserID == nil || h.workspaceTaskStore == nil {
		respondGroupRequirementUnavailable(w)
		return grouprequirements.Input{}, false
	}
	ownerUserID, err := h.currentUserID(ctx)
	if err != nil || strings.TrimSpace(ownerUserID) == "" {
		respondGroupRequirementUnavailable(w)
		return grouprequirements.Input{}, false
	}
	listing := h.groupTemplateListing(ctx)
	if listing.CatalogUnavailable {
		respondGroupRequirementUnavailable(w)
		return grouprequirements.Input{}, false
	}
	var selected *groupTemplateView
	for index := range listing.Templates {
		if listing.Templates[index].ID == request.GroupTemplateID {
			selected = &listing.Templates[index]
			break
		}
	}
	if selected == nil {
		respondGroupTemplateConflict(w, "group_template_unavailable", "This group template is no longer available. Choose a template again.", nil)
		return grouprequirements.Input{}, false
	}
	if selected.Kind != projecttemplates.GroupTemplateKindManaged {
		_ = orihttp.RespondBadRequest(w, "General groups are created with their reviewed Group Manager roster")
		return grouprequirements.Input{}, false
	}
	if selected.Revision != request.Revision {
		respondGroupTemplateConflict(w, "review_stale", "This group template changed. Review it again.", selected.Availability)
		return grouprequirements.Input{}, false
	}
	if selected.Availability == nil || (selected.Availability.State != groupTemplateAvailabilityCreatable && selected.Availability.State != groupTemplateAvailabilityReusable) {
		respondGroupTemplateConflict(w, "group_template_unavailable", "This group template cannot prepare its group right now.", selected.Availability)
		return grouprequirements.Input{}, false
	}

	// Mutation uses the same trusted resolver as every other creation path.
	// The projected representative must still be exactly that template.
	representative := selected.Representative
	template, err := h.resolveProjectTemplate(representative.ID, "")
	if err != nil || template.Revision != representative.Revision || template.VariantRevision != representative.VariantRevision ||
		projecttemplates.GroupTemplateHomeDigest(template.AssistantProgram) != selected.HomeDigest {
		respondGroupTemplateConflict(w, "review_stale", "This group template's source changed. Review it again.", selected.Availability)
		return grouprequirements.Input{}, false
	}
	if readiness := h.revalidateBlueprintReadiness(template); blueprintCreationBlocked(template, readiness) {
		respondGroupTemplateConflict(w, "group_template_unavailable", "This group template's source is not ready.", selected.Availability)
		return grouprequirements.Input{}, false
	}
	digest, err := groupTemplateHomeDigest(request)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "group template request is invalid")
		return grouprequirements.Input{}, false
	}
	return grouprequirements.Input{
		OwnerUserID: strings.TrimSpace(ownerUserID), OperationKind: grouprequirements.OperationPrepareHome,
		Template: template, Composition: grouprequirements.CompositionGrouped, CreateHome: true, InputDigest: digest,
		HomeName: request.Name, GroupTemplateID: request.GroupTemplateID, GroupTemplateRevision: request.Revision,
	}, true
}

// ReviewGroupTemplateHome handles POST /api/workspaces/group-templates/review.
// It returns an inert receipt for the selected canonical group and, when
// explicitly supplied, its exact Home-role choices. Advice alone selects none.
func (h *Handler) ReviewGroupTemplateHome(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeGroupTemplateHomeRequest(w, r, false)
	if !ok {
		return
	}
	input, ok := h.prepareGroupTemplateHome(r.Context(), w, request)
	if !ok {
		return
	}
	if request.RecommendStaffing {
		input.HomeAdvice = h.groupHomeStaffingAdvice(r.Context(), input)
	} else {
		team, err := h.reviewGroupHomeTeam(r.Context(), input, request)
		if err != nil {
			respondGroupRequirementError(w, err)
			return
		}
		input.HomeStaffing = team
	}
	review, err := h.groupRequirements.Review(r.Context(), input)
	if err != nil {
		respondGroupRequirementError(w, err)
		return
	}
	if review.State != grouprequirements.StateReadyGrouped || strings.TrimSpace(review.Token) == "" {
		respondGroupTemplateConflict(w, "group_template_unavailable", review.Summary, nil)
		return
	}
	result := groupTemplateHomeReview{
		GroupTemplateID: request.GroupTemplateID, Revision: request.Revision, Name: request.Name,
		Reuse: review.HomeWorkspaceID != "", HomeWorkspaceID: review.HomeWorkspaceID, HomeName: review.HomeName,
		ReviewToken: review.Token, ReviewDigest: review.ReviewDigest, ExpiresAt: review.ExpiresAt,
		StaffingAdvice: input.HomeAdvice.Public(), RoleStaffing: publicGroupHomeTeam(input.HomeStaffing),
		Summary: "Only this group will be created. No project, team, schedule, tool access, or runtime setup is created.",
	}
	if result.Reuse {
		result.Summary = "This existing group will be reused unchanged, and nothing else is created."
		if request.Name != review.HomeName {
			result.Summary = "This existing group will be reused unchanged. The name you entered is not applied, and nothing else is created."
		}
	}
	if input.HomeStaffing != nil {
		result.Summary = "Prepare this group and apply only the exact Home team shown. Saved profiles keep their own configuration and receive no new skills or access. No project or first work is started."
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"group_template_review": result})
}

// CommitGroupTemplateHome handles POST /api/workspaces/group-templates/commit.
// Its journal applies the reviewed Home-only consequence; an optional frozen
// team is delegated to canonical per-role writers and their outcome receipts.
func (h *Handler) CommitGroupTemplateHome(w http.ResponseWriter, r *http.Request) {
	// Only this pre-Claim phase proves a definite refusal applied nothing.
	// A Claim error can follow Home creation/journal writes, so 400/409 alone
	// must never authorize discarding an uncertain operation or fresh consent.
	w.Header().Set("X-Ori-Group-Commit-Phase", "not_accepted")
	request, ok := decodeGroupTemplateHomeRequest(w, r, true)
	if !ok {
		return
	}
	input, ok := h.prepareGroupTemplateHome(r.Context(), w, request)
	if !ok {
		return
	}
	reviewed, err := h.groupRequirements.ReviewedHomeStaffing(r.Context(), input, request.GroupReviewToken)
	team := reviewed.Team
	if err != nil || (team == nil) != (len(request.RoleStaffing) == 0) {
		if err == nil {
			err = grouprequirements.ErrReviewStale
		}
		respondGroupRequirementError(w, err)
		return
	}
	input.HomeStaffing = team
	if err := h.checkNewGroupHomeTeam(r.Context(), input, reviewed); err != nil {
		respondGroupRequirementError(w, err)
		return
	}
	w.Header().Set("X-Ori-Group-Commit-Phase", "outcome_unknown")
	claim, err := h.groupRequirements.Claim(r.Context(), input, request.GroupReviewToken, request.IdempotencyKey)
	if err != nil {
		respondGroupRequirementError(w, err)
		return
	}
	w.Header().Set("X-Ori-Group-Commit-Phase", "accepted")
	homeID := strings.TrimSpace(claim.Operation.HomeWorkspaceID)
	home, err := h.workspaceTaskStore.Get(homeID)
	state := home.GetAssistantProgramState()
	if homeID == "" || err != nil || home == nil || home.Kind != "group" || state == nil || claim.Snapshot != nil ||
		claim.Operation.ChildWorkspaceID != "" || claim.Operation.ProjectLinkID != "" {
		_ = h.groupRequirements.Mark(r.Context(), claim.Operation, grouprequirements.OperationReconcileRequired)
		respondGroupOperationIncomplete(w, "The confirmed group could not be verified. Recheck before retrying.")
		return
	}
	if err := h.groupRequirements.Mark(r.Context(), claim.Operation, grouprequirements.OperationSucceeded); err != nil {
		respondGroupRequirementUnavailable(w)
		return
	}
	createdByThisOperation := state.GroupTemplate != nil && state.GroupTemplate.ReviewDigest == claim.Operation.ReviewDigest
	if createdByThisOperation {
		// Without this a coordinator later staffed on the Home is dropped from
		// the agent registry on restart whenever the workspace root is unconfirmed.
		h.allowlistLocallyCreatedWorkspace(home.ID)
	}
	summary := "The group is ready. No project workspace or team was created."
	if !createdByThisOperation {
		summary = "An existing group was reused unchanged. No project workspace or team was created."
	}
	var staffing *groupHomeStaffingOutcome
	if team != nil && (reviewed.HomeWorkspaceID != "" || !createdByThisOperation) && reviewed.HomeWorkspaceID != home.ID {
		staffing = groupHomeTeamNeedsReview(team)
	} else {
		staffing = h.applyGroupHomeTeam(r.Context(), home.ID, claim.Operation.ReviewDigest, team)
	}
	if staffing != nil {
		summary = "The reviewed Home team was applied. No project or first work was started."
		if staffing.State != "staffed" {
			summary = "The group is ready. Some reviewed Home roles need review; completed choices were kept."
		}
	}
	w.Header().Set("X-Ori-Group-Commit-Phase", "succeeded")
	result := map[string]any{
		"state": "home_ready", "summary": summary,
		"home_workspace_id": home.ID, "home_name": home.Name,
		"home_created": claim.HomeCreated, "created_by_this_operation": createdByThisOperation,
		"layout_subject_new": claim.Operation.HomeCreated && createdByThisOperation,
		"name_applied":       createdByThisOperation, "idempotent_replay": claim.Replayed,
	}
	if staffing != nil {
		result["home_staffing"] = staffing
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "group_template": result})
}
