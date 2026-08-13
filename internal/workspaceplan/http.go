package workspaceplan

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Handler serves the canonical workspace-scoped Plan API.
//
// Two contracts are held here rather than left to each caller:
//
//   - Workspace identity comes from the route and is passed to the service on
//     every call, so a Plan ID alone never reaches a record. A Plan from
//     another workspace reads as missing (FR-163, FR-167).
//   - Errors carry a stable machine-readable code alongside the message, so a
//     client can react to a stale draft or an invalid transition without
//     parsing prose (FR-166).
type Handler struct {
	service *Service
}

// NewHandler returns the Plan API handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// planResponse is the wire shape of a Plan. It is written explicitly rather
// than by serializing the domain type directly, so adding an internal field
// cannot silently widen the API.
type planResponse struct {
	*Plan
	// StatusLabel pairs the machine status with its human label, so the UI
	// never has to communicate state by color or raw enum alone (FR-162).
	StatusLabel string `json:"status_label"`
	// NextStatuses lists the transitions currently available, so a client can
	// disable actions rather than discovering the transition table by trial.
	NextStatuses []Status `json:"next_statuses"`
	// Archived is the Active/History split as a single flag (FR-146).
	Archived bool `json:"archived"`
}

func newPlanResponse(plan *Plan) planResponse {
	return planResponse{
		Plan:         plan,
		StatusLabel:  plan.Status.Label(),
		NextStatuses: NextStatuses(plan.Status),
		Archived:     plan.ArchivedAt != nil,
	}
}

// PlanCollection dispatches /api/workspaces/{workspaceID}/plans.
//
// The route is registered without a method on purpose. A method-scoped pattern
// would leave a wrong-method request to fall through to the app's
// /api/workspaces/ catch-all, which answers with an unrelated 200 payload —
// a client would read that as success. Owning every method here means a wrong
// verb gets an honest 405.
func (h *Handler) PlanCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.ListPlans(w, r)
	case http.MethodPost:
		h.CreatePlan(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// PlanItem dispatches /api/workspaces/{workspaceID}/plans/{planID}.
func (h *Handler) PlanItem(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.GetPlan(w, r)
	case http.MethodDelete:
		h.DeletePlan(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// CreatePlan handles POST /api/workspaces/{workspaceID}/plans.
func (h *Handler) CreatePlan(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return
	}

	var req createPlanRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// studio_id in the body is accepted but never trusted: the route is the
	// authority for which workspace owns the Plan (FR-163, FR-168).
	if req.StudioID != "" && req.StudioID != workspaceID {
		writeError(w, ErrPlanNotFound)
		return
	}

	plan, err := h.service.Create(r.Context(), workspaceID, CreateInput{
		Request:   req.Request,
		Title:     req.Title,
		Objective: req.Objective,
		Origin:    req.origin(),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Created(w, newPlanResponse(plan))
}

type createPlanRequest struct {
	// StudioID is the backend identifier for the workspace shown in the UI.
	StudioID  string `json:"studio_id,omitempty"`
	Request   string `json:"request"`
	Title     string `json:"title,omitempty"`
	Objective string `json:"objective,omitempty"`
	Actor     string `json:"actor,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	// Source names the surface that started the Plan. It is display
	// provenance: no value here grants authority (FR-60).
	Source string `json:"source,omitempty"`
}

func (r createPlanRequest) origin() Origin {
	kind := OriginUser
	switch strings.ToLower(strings.TrimSpace(r.Source)) {
	case "chat":
		kind = OriginChat
	case "orchestration":
		kind = OriginOrchestration
	case "api":
		kind = OriginAPI
	}
	return Origin{
		Kind:      kind,
		Actor:     r.Actor,
		SessionID: r.SessionID,
		MessageID: r.MessageID,
		AgentName: r.AgentName,
	}
}

// ListPlans handles GET /api/workspaces/{workspaceID}/plans.
func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return
	}

	filter := ListFilter{Scope: ListScope(strings.TrimSpace(r.URL.Query().Get("scope")))}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			orihttp.BadRequest(w, "limit must be a non-negative integer")
			return
		}
		filter.Limit = limit
	}
	for _, raw := range r.URL.Query()["status"] {
		status := Status(strings.TrimSpace(raw))
		if !status.Valid() {
			writeError(w, fmt.Errorf("%w: unsupported status %q", ErrValidation, raw))
			return
		}
		filter.Statuses = append(filter.Statuses, status)
	}

	plans, err := h.service.List(r.Context(), workspaceID, filter)
	if err != nil {
		writeError(w, err)
		return
	}

	responses := make([]planResponse, 0, len(plans))
	for _, plan := range plans {
		responses = append(responses, newPlanResponse(plan))
	}
	orihttp.Success(w, map[string]any{
		"studio_id": workspaceID,
		"scope":     filter.Normalized().Scope,
		"plans":     responses,
	})
}

// GetPlan handles GET /api/workspaces/{workspaceID}/plans/{planID}.
func (h *Handler) GetPlan(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	plan, err := h.service.Get(r.Context(), workspaceID, planID)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// GetPlanActivity handles GET /api/workspaces/{workspaceID}/plans/{planID}/activity.
func (h *Handler) GetPlanActivity(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			orihttp.BadRequest(w, "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}

	entries, err := h.service.Activity(r.Context(), workspaceID, planID, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"plan_id": planID, "activity": entries})
}

// ArchivePlan handles POST /api/workspaces/{workspaceID}/plans/{planID}/archive.
func (h *Handler) ArchivePlan(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	var req struct {
		Reason string `json:"reason,omitempty"`
		Actor  string `json:"actor,omitempty"`
	}
	if r.ContentLength > 0 && !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	plan, err := h.service.Archive(r.Context(), workspaceID, planID, req.Reason, req.Actor)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// ReopenPlan handles POST /api/workspaces/{workspaceID}/plans/{planID}/reopen.
func (h *Handler) ReopenPlan(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	var req struct {
		Actor string `json:"actor,omitempty"`
	}
	if r.ContentLength > 0 && !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	plan, err := h.service.Reopen(r.Context(), workspaceID, planID, req.Actor)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// DeletePlan handles DELETE /api/workspaces/{workspaceID}/plans/{planID}.
//
// Deletion is refused for anything that produced work; those Plans archive
// instead. The refusal is a 409 with a stable code so the client can offer
// Archive rather than reporting a failure (FR-17, FR-166).
func (h *Handler) DeletePlan(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodDelete) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	if err := h.service.Delete(r.Context(), workspaceID, planID); err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"deleted": true, "plan_id": planID})
}

func requireWorkspaceID(w http.ResponseWriter, r *http.Request) string {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspaceID is required")
	}
	return workspaceID
}

func requireWorkspaceAndPlanID(w http.ResponseWriter, r *http.Request) (string, string) {
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return "", ""
	}
	planID := strings.TrimSpace(r.PathValue("planID"))
	if planID == "" {
		orihttp.BadRequest(w, "planID is required")
		return "", ""
	}
	return workspaceID, planID
}

// writeError maps a domain error to its HTTP status and stable code (FR-166).
// The mapping is one function so a new endpoint cannot invent its own status
// for a condition clients already know how to handle.
func writeError(w http.ResponseWriter, err error) {
	code := CodeFor(err)
	status := statusForCode(code)
	message := err.Error()
	if status == http.StatusInternalServerError {
		// An unmapped error is a bug, not a contract. Clients get a stable
		// code and no internal detail.
		message = "Plan request failed"
	}
	_ = orihttp.RespondAPIError(w, status, orihttp.NewAPIError(string(code), message))
}

func statusForCode(code ErrorCode) int {
	switch code {
	case CodeNotFound, CodeWorkspaceNotFound, CodeVersionNotFound, CodeApprovalNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeStaleDraft, CodeStaleVersion, CodeApprovalConsumed,
		CodeMaterializationConflict, CodeExecutionConflict, CodeNotDeletable:
		return http.StatusConflict
	case CodeInvalidTransition, CodeApprovalMismatch, CodeArchived:
		return http.StatusConflict
	case CodeApprovalAuthority:
		return http.StatusForbidden
	case CodeValidationFailed, CodeLimitExceeded, CodeUnavailableCapability, CodeUnsafePath:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}
