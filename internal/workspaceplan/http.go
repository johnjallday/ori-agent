package workspaceplan

import (
	"context"
	"errors"
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
	// resolveGuidance and resolveAvailability supply the workspace context the
	// planner needs. They are optional: without them the API still works, with
	// no workspace-specific guidance and no assignment checking.
	resolveGuidance     GuidanceResolver
	resolveAvailability AvailabilityResolver
	// materializer spends approvals. It is optional so the API still serves
	// reads and drafting in a build with no task store wired.
	materializer *Materializer
	// executor supervises approved work through the existing Run machinery.
	executor *Executor
	// slots answers who is executing and who is waiting.
	slots *SlotCoordinator
	// auto drives automatic Plans after a successful materialization.
	auto *AutoRunner
	// reconciler previews and confirms what a revision does to prior work.
	reconciler *Reconciler
	// resolvePolicy supplies the enforced policy snapshotted onto a version.
	resolvePolicy PolicyResolver
}

// GuidanceResolver returns the model-guidance half of a workspace's planning
// settings (FR-125).
type GuidanceResolver func(ctx context.Context, workspaceID string) GuidanceInput

// AvailabilityResolver returns the agents and capabilities a workspace actually
// has, so the planner is never asked to guess (FR-46).
type AvailabilityResolver func(ctx context.Context, workspaceID string) ValidationContext

// PolicyResolver returns the ENFORCED half of a workspace's planning policy as
// it stands right now.
//
// It is deliberately separate from GuidanceResolver. Guidance reaches the model
// and nothing else; a policy snapshot reaches the compiled lifecycle checks and
// never the model. Two resolvers rather than one shared blob is what makes it
// impossible for a guidance field to arrive somewhere enforcement is read
// (FR-124, FR-129).
type PolicyResolver func(ctx context.Context, workspaceID string) PolicySnapshot

// NewHandler returns the Plan API handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// SetGuidanceResolver attaches the workspace planning-guidance lookup.
func (h *Handler) SetGuidanceResolver(resolve GuidanceResolver) { h.resolveGuidance = resolve }

// SetAvailabilityResolver attaches the workspace agent/capability lookup.
func (h *Handler) SetAvailabilityResolver(resolve AvailabilityResolver) {
	h.resolveAvailability = resolve
}

// SetMaterializer attaches the service that spends approvals.
func (h *Handler) SetMaterializer(materializer *Materializer) { h.materializer = materializer }

// SetExecutor attaches the service that supervises approved work.
func (h *Handler) SetExecutor(executor *Executor) { h.executor = executor }

// SetSlots attaches the workspace execution-slot coordinator.
func (h *Handler) SetSlots(slots *SlotCoordinator) { h.slots = slots }

// SetAutoRunner attaches automatic execution. Without it, an automatic Plan
// still materializes its Tasks; they simply wait to be started by hand, which
// is a visible degradation rather than a silent one.
func (h *Handler) SetAutoRunner(auto *AutoRunner) { h.auto = auto }

// SetReconciler attaches revision reconciliation.
func (h *Handler) SetReconciler(reconciler *Reconciler) { h.reconciler = reconciler }

// SetPolicyResolver attaches the enforced-policy lookup snapshotted onto each
// review version.
func (h *Handler) SetPolicyResolver(resolve PolicyResolver) { h.resolvePolicy = resolve }

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

// PlanDraft dispatches /api/workspaces/{workspaceID}/plans/{planID}/draft.
//
// Registered without a method for the same reason as the other Plan routes:
// the app's /api/workspaces/ catch-all matches every verb, so a method-scoped
// pattern would let a wrong verb answer 200 with unrelated JSON.
func (h *Handler) PlanDraft(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPatch, http.MethodPut:
		h.UpdateDraft(w, r)
	case http.MethodPost:
		h.GenerateDraft(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// UpdateDraft handles PATCH /api/workspaces/{workspaceID}/plans/{planID}/draft.
//
// It carries an optimistic revision token. A stale write is refused with a
// conflict payload that includes the current revision and content, so the
// editor can offer recover-or-discard rather than losing the user's work
// (FR-30).
//
// Saving a draft has no side effects: no Task, artifact, approval, or execution
// follows from it (FR-29).
func (h *Handler) UpdateDraft(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethods(w, r, http.MethodPatch, http.MethodPut) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	var req struct {
		Title     string      `json:"title,omitempty"`
		Objective string      `json:"objective"`
		Content   PlanContent `json:"content"`
		Intent    string      `json:"intent,omitempty"`
		Actor     string      `json:"actor,omitempty"`
		// Revision is the draft revision the editor loaded.
		Revision int64 `json:"revision"`
		// Autosave records a recovery point before writing (FR-30).
		Autosave bool `json:"autosave,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	plan, err := h.service.Edit(r.Context(), workspaceID, planID, EditInput{
		Title:            req.Title,
		Objective:        req.Objective,
		Content:          req.Content,
		Intent:           RevisionIntent(strings.TrimSpace(req.Intent)),
		Actor:            req.Actor,
		ExpectedRevision: req.Revision,
		Snapshot:         req.Autosave,
	})
	if err != nil {
		h.writeDraftError(w, r, workspaceID, planID, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// writeDraftError adds the losing editor's recovery context to a stale-write
// conflict. Telling someone their save failed without showing them what won,
// or what they were about to write, is how edits get lost (FR-151).
func (h *Handler) writeDraftError(w http.ResponseWriter, r *http.Request, workspaceID, planID string, err error) {
	if !errors.Is(err, ErrStaleDraft) {
		writeError(w, err)
		return
	}

	details := map[string]any{}
	if current, getErr := h.service.Get(r.Context(), workspaceID, planID); getErr == nil {
		details["current_revision"] = current.DraftRevision
		details["current"] = newPlanResponse(current)
	}
	if snapshots, snapErr := h.service.Snapshots(r.Context(), workspaceID, planID); snapErr == nil && len(snapshots) > 0 {
		details["recoverable_snapshots"] = snapshots
	}

	_ = orihttp.RespondAPIError(w, http.StatusConflict,
		orihttp.NewAPIError(string(CodeStaleDraft), err.Error()).WithDetails(details))
}

// GenerateDraft handles POST /api/workspaces/{workspaceID}/plans/{planID}/draft.
//
// The planner either drafts or asks questions; either way nothing is approved
// and no work is created (FR-20, FR-22, FR-23).
func (h *Handler) GenerateDraft(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	var req struct {
		Actor              string   `json:"actor,omitempty"`
		AllowClarification bool     `json:"allow_clarification,omitempty"`
		Sections           []string `json:"sections,omitempty"`
		Revision           int64    `json:"revision"`
	}
	if r.ContentLength > 0 && !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	plan, err := h.service.Draft(r.Context(), workspaceID, planID, DraftingOptions{
		Actor:              req.Actor,
		AllowClarification: req.AllowClarification,
		Sections:           req.Sections,
		ExpectedRevision:   req.Revision,
		Guidance:           h.guidance(r.Context(), workspaceID),
		Validation:         h.availability(r.Context(), workspaceID),
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// writeGenerationError reports a failed generation with the issues that made it
// fail, so the editor can show what is wrong rather than only that something is
// (FR-45). Model unavailability is reported distinctly, because it disables
// only the generate controls (FR-58).
func writeGenerationError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrModelUnavailable) {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError(string(CodeModelUnavailable), err.Error()))
		return
	}

	var genErr *GenerationError
	if errors.As(err, &genErr) && len(genErr.Issues) > 0 {
		_ = orihttp.RespondAPIError(w, http.StatusUnprocessableEntity,
			orihttp.NewAPIError(string(CodeValidationFailed), genErr.Error()).
				WithDetails(map[string]any{
					"issues":   genErr.Issues,
					"attempts": genErr.Attempts,
				}))
		return
	}
	writeError(w, err)
}

// PlanVersions dispatches /api/workspaces/{workspaceID}/plans/{planID}/versions.
//
// GET lists retained versions; POST snapshots the current draft as a new
// immutable one (FR-31, FR-35).
func (h *Handler) PlanVersions(w http.ResponseWriter, r *http.Request) {
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	switch r.Method {
	case http.MethodGet:
		versions, err := h.service.Versions(r.Context(), workspaceID, planID)
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, map[string]any{
			"plan_id":  planID,
			"versions": versions,
			// Stating the cap alongside the data keeps the UI from guessing
			// when to warn about it (FR-31).
			"max_versions": MaxReviewVersions,
		})

	case http.MethodPost:
		var req struct {
			Actor  string `json:"actor,omitempty"`
			Intent string `json:"intent,omitempty"`
		}
		if r.ContentLength > 0 && !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
		version, err := h.service.RequestReview(r.Context(), workspaceID, planID, ReviewInput{
			Actor:      req.Actor,
			Intent:     RevisionIntent(strings.TrimSpace(req.Intent)),
			Validation: h.availability(r.Context(), workspaceID),
			// The enforced policy is snapshotted onto the version here, at the
			// moment it becomes immutable. Later settings changes do not reach
			// back into it: a plan approved under one policy keeps behaving
			// that way until somebody revises it (FR-143, FR-144).
			Policy: h.policy(r.Context(), workspaceID),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Created(w, version)

	default:
		orihttp.MethodNotAllowed(w)
	}
}

// PlanVersion handles GET .../versions/{version} — the read-only review
// contract for one exact version (FR-62, FR-152).
func (h *Handler) PlanVersion(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}
	number, ok := requireVersionNumber(w, r, "version")
	if !ok {
		return
	}

	contract, err := h.service.BuildReviewContract(
		r.Context(), workspaceID, planID, number, h.availability(r.Context(), workspaceID))
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, contract)
}

// PlanCompare handles GET .../compare?from=N&to=M (FR-35, FR-36).
func (h *Handler) PlanCompare(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	from, ok := requireVersionQuery(w, r, "from")
	if !ok {
		return
	}
	to, ok := requireVersionQuery(w, r, "to")
	if !ok {
		return
	}

	diff, err := h.service.Compare(r.Context(), workspaceID, planID, from, to)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, diff)
}

// PlanDecision handles POST .../decision — request changes or reject, both of
// which retain the reviewed version (FR-37, FR-66, FR-67).
func (h *Handler) PlanDecision(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	var req struct {
		Decision string `json:"decision"`
		Version  int    `json:"version,omitempty"`
		Reason   string `json:"reason,omitempty"`
		Actor    string `json:"actor,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	input := DecisionInput{Actor: req.Actor, Version: req.Version, Reason: req.Reason}
	var (
		plan *Plan
		err  error
	)
	switch strings.TrimSpace(strings.ToLower(req.Decision)) {
	case "request_changes":
		plan, err = h.service.RequestChanges(r.Context(), workspaceID, planID, input)
	case "reject":
		plan, err = h.service.Reject(r.Context(), workspaceID, planID, input)
	default:
		writeError(w, fmt.Errorf(
			"%w: decision must be request_changes or reject", ErrValidation))
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// PlanApprovals dispatches .../approvals.
//
// GET returns the approval history; POST records a user's approval of one exact
// version. Approval is a user action and this is the only route to it (FR-59,
// FR-70, FR-79).
func (h *Handler) PlanApprovals(w http.ResponseWriter, r *http.Request) {
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	switch r.Method {
	case http.MethodGet:
		approvals, err := h.service.Approvals(r.Context(), workspaceID, planID)
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, map[string]any{"plan_id": planID, "approvals": approvals})

	case http.MethodPost:
		var req struct {
			Version     int    `json:"version"`
			ContentHash string `json:"content_hash"`
			Effect      string `json:"effect"`
			UserID      string `json:"user_id,omitempty"`
			UserName    string `json:"user_name,omitempty"`
			// IdempotencyKey lets a retried request return the original
			// approval rather than a second one (FR-73).
			IdempotencyKey string `json:"idempotency_key"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		approval, err := h.service.Approve(r.Context(), workspaceID, planID, ApprovalRequest{
			Version:        req.Version,
			ContentHash:    req.ContentHash,
			Effect:         ApprovalEffect(strings.TrimSpace(req.Effect)),
			UserID:         req.UserID,
			UserName:       req.UserName,
			IdempotencyKey: req.IdempotencyKey,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Created(w, approval)

	default:
		orihttp.MethodNotAllowed(w)
	}
}

// PlanMaterialize handles POST .../materialize — spend an approval and create
// the work it authorized (FR-81).
//
// It is separate from approval on purpose. Approval is the user's decision and
// is recorded durably; materialization is the effect, and separating them is
// what lets a retry replay the original result instead of doing the work twice
// (FR-72, FR-73, FR-99).
func (h *Handler) PlanMaterialize(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}
	if h.materializer == nil {
		writeError(w, fmt.Errorf("%w: materialization is not configured", ErrValidation))
		return
	}

	var req struct {
		ApprovalID string `json:"approval_id"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ApprovalID) == "" {
		writeError(w, fmt.Errorf("%w: approval_id is required", ErrValidation))
		return
	}

	result, err := h.materializer.Materialize(r.Context(), workspaceID, planID, MaterializeInput{
		ApprovalID: req.ApprovalID,
		Validation: h.availability(r.Context(), workspaceID),
	})
	if err != nil {
		writeError(w, err)
		return
	}

	// Automatic dispatch begins here and nowhere else. StartExecution mirrors
	// the effect of the approval that was just spent, so only an Approve and
	// Start for this exact version can reach it — and a replayed
	// materialization does not start a second run, because Launch refuses a
	// Plan already running (FR-103).
	if result.StartExecution && h.auto != nil {
		launch, err := h.auto.Launch(r.Context(), workspaceID, planID, result.Actor)
		if err != nil {
			writeError(w, err)
			return
		}
		result.Launched = launch.Launched
		result.LaunchReason = launch.Reason
	}

	orihttp.Success(w, result)
}

// PlanReviseApproved handles POST .../revise-approved — start a new working
// draft from an approved Plan, classified as additive, corrective, or
// superseding (FR-38, FR-39).
func (h *Handler) PlanReviseApproved(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	var req struct {
		Intent string `json:"intent"`
		Actor  string `json:"actor,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	plan, err := h.service.EditApproved(r.Context(), workspaceID, planID,
		RevisionIntent(strings.TrimSpace(req.Intent)), req.Actor)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

func requireVersionNumber(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := strings.TrimSpace(r.PathValue(name))
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 {
		orihttp.BadRequest(w, name+" must be a positive version number")
		return 0, false
	}
	return number, true
}

func requireVersionQuery(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 {
		orihttp.BadRequest(w, name+" must be a positive version number")
		return 0, false
	}
	return number, true
}

// PlanRevision dispatches /api/workspaces/{workspaceID}/plans/{planID}/revision.
//
// GET discloses what a revision would replace without changing anything; POST
// performs it. Splitting them is the point: the user sees the collateral before
// it happens, not after (FR-56).
func (h *Handler) PlanRevision(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.DiscloseRevision(w, r)
	case http.MethodPost:
		h.RevisePlan(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// DiscloseRevision handles GET .../revision?section=…
func (h *Handler) DiscloseRevision(w http.ResponseWriter, r *http.Request) {
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	disclosure, err := h.service.DiscloseRevisionFor(
		r.Context(), workspaceID, planID, r.URL.Query()["section"])
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{
		"plan_id":            planID,
		"disclosure":         disclosure,
		"needs_confirmation": disclosure.NeedsConfirmation(),
		"revisable_sections": AllSections(),
	})
}

// RevisePlan handles POST .../revision.
func (h *Handler) RevisePlan(w http.ResponseWriter, r *http.Request) {
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	var req struct {
		Sections  []string `json:"sections,omitempty"`
		Confirmed bool     `json:"confirmed,omitempty"`
		Actor     string   `json:"actor,omitempty"`
		Revision  int64    `json:"revision"`
	}
	if r.ContentLength > 0 && !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	plan, err := h.service.Revise(r.Context(), workspaceID, planID, ReviseInput{
		Sections:         req.Sections,
		Confirmed:        req.Confirmed,
		Actor:            req.Actor,
		ExpectedRevision: req.Revision,
		Guidance:         h.guidance(r.Context(), workspaceID),
		Validation:       h.availability(r.Context(), workspaceID),
	})
	if err != nil {
		// A revision awaiting confirmation is not a failure; it is the
		// disclosure the user has to see first (FR-56).
		var required *RevisionRequiredError
		if errors.As(err, &required) {
			_ = orihttp.RespondAPIError(w, http.StatusConflict,
				orihttp.NewAPIError(string(CodeRevisionNeedsConfirmation), required.Error()).
					WithDetails(map[string]any{"disclosure": required.Disclosure}))
			return
		}
		writeGenerationError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// AnswerClarification handles
// POST /api/workspaces/{workspaceID}/plans/{planID}/clarifications/{clarificationID}.
func (h *Handler) AnswerClarification(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}
	clarificationID := strings.TrimSpace(r.PathValue("clarificationID"))
	if clarificationID == "" {
		orihttp.BadRequest(w, "clarificationID is required")
		return
	}

	var req struct {
		Answer     string `json:"answer,omitempty"`
		Skip       bool   `json:"skip,omitempty"`
		SkipReason string `json:"skip_reason,omitempty"`
		Actor      string `json:"actor,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	plan, err := h.service.Answer(r.Context(), workspaceID, planID, clarificationID, AnswerInput{
		Answer:     req.Answer,
		Skip:       req.Skip,
		SkipReason: req.SkipReason,
		Actor:      req.Actor,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// DraftSnapshots handles
// GET /api/workspaces/{workspaceID}/plans/{planID}/snapshots and
// POST .../snapshots/{snapshotID}/recover.
func (h *Handler) DraftSnapshots(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}

	snapshots, err := h.service.Snapshots(r.Context(), workspaceID, planID)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{
		"plan_id":   planID,
		"snapshots": snapshots,
		// Stating the policy alongside the data keeps the UI from inventing
		// its own retention story (FR-30).
		"retained":    MaxDraftSnapshots,
		"retain_days": int(DraftSnapshotTTL.Hours() / 24),
	})
}

// RecoverDraftSnapshot handles
// POST /api/workspaces/{workspaceID}/plans/{planID}/snapshots/{snapshotID}/recover.
func (h *Handler) RecoverDraftSnapshot(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}
	snapshotID := strings.TrimSpace(r.PathValue("snapshotID"))
	if snapshotID == "" {
		orihttp.BadRequest(w, "snapshotID is required")
		return
	}

	var req struct {
		Actor string `json:"actor,omitempty"`
	}
	if r.ContentLength > 0 && !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	plan, err := h.service.RecoverSnapshot(r.Context(), workspaceID, planID, snapshotID, req.Actor)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, newPlanResponse(plan))
}

// guidance and availability resolve the workspace context the planner needs.
// They are wired by the server; without them generation still runs, but with
// no workspace-specific guidance and no assignment checking — which is why
// validation treats a nil availability list as "not checked" rather than as
// "nothing is available".
func (h *Handler) guidance(ctx context.Context, workspaceID string) GuidanceInput {
	if h.resolveGuidance == nil {
		return GuidanceInput{}
	}
	return h.resolveGuidance(ctx, workspaceID)
}

// policy returns the enforced policy to snapshot. An empty snapshot is the
// honest answer for a build with no resolver: it records that nothing was
// enforced, rather than inventing controls that never ran.
func (h *Handler) policy(ctx context.Context, workspaceID string) PolicySnapshot {
	if h.resolvePolicy == nil {
		return PolicySnapshot{}
	}
	return h.resolvePolicy(ctx, workspaceID)
}

func (h *Handler) availability(ctx context.Context, workspaceID string) ValidationContext {
	if h.resolveAvailability == nil {
		return ValidationContext{}
	}
	return h.resolveAvailability(ctx, workspaceID)
}

// --- Execution (FR-100 through FR-121) -------------------------------------

// PlanExecution dispatches .../execution — start, pause, resume, retry, skip,
// cancel, complete, and fail.
//
// One route with a named action keeps the supervision verbs together, and keeps
// every one of them going through the same ownership and method checks.
func (h *Handler) PlanExecution(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}
	if h.executor == nil {
		writeError(w, fmt.Errorf("%w: execution is not configured", ErrValidation))
		return
	}

	var req struct {
		Action string `json:"action"`
		Actor  string `json:"actor,omitempty"`
		TaskID string `json:"task_id,omitempty"`
		Reason string `json:"reason,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	ctx := r.Context()
	switch strings.TrimSpace(strings.ToLower(req.Action)) {
	case "start":
		result, err := h.executor.Start(ctx, workspaceID, planID, StartInput{
			Actor: req.Actor, TaskID: req.TaskID,
		})
		h.writeExecutionResult(w, result, err)

	case "retry":
		if strings.TrimSpace(req.TaskID) == "" {
			writeError(w, fmt.Errorf("%w: retry requires a task_id", ErrValidation))
			return
		}
		result, err := h.executor.Retry(ctx, workspaceID, planID, req.TaskID, req.Actor)
		h.writeExecutionResult(w, result, err)

	case "pause":
		result, err := h.executor.Pause(ctx, workspaceID, planID, PauseInput{
			Actor: req.Actor, Reason: req.Reason,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, result)

	case "resume":
		plan, err := h.executor.Resume(ctx, workspaceID, planID, req.Actor)
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, newPlanResponse(plan))

	case "skip":
		if strings.TrimSpace(req.TaskID) == "" {
			writeError(w, fmt.Errorf("%w: skip requires a task_id", ErrValidation))
			return
		}
		plan, err := h.executor.Skip(ctx, workspaceID, planID, req.TaskID, SkipInput{
			Actor: req.Actor, Reason: req.Reason,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, newPlanResponse(plan))

	case "cancel":
		plan, err := h.executor.Cancel(ctx, workspaceID, planID, req.Reason, req.Actor)
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, newPlanResponse(plan))

	case "complete":
		report, err := h.executor.Complete(ctx, workspaceID, planID, req.Actor)
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, report)

	case "fail":
		plan, err := h.executor.Fail(ctx, workspaceID, planID, req.Reason, req.Actor)
		if err != nil {
			writeError(w, err)
			return
		}
		orihttp.Success(w, newPlanResponse(plan))

	default:
		writeError(w, fmt.Errorf(
			"%w: unsupported action %q; expected start, retry, pause, resume, skip, cancel, complete, or fail",
			ErrValidation, req.Action))
	}
}

func (h *Handler) writeExecutionResult(w http.ResponseWriter, result *StartResult, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, result)
}

// WorkspaceExecutionSlot handles GET
// /api/workspaces/{workspaceID}/plan-execution-slot — who is executing and who
// is waiting (FR-107).
//
// It is a workspace-level read rather than a plan-level one because the answer
// is about the workspace: one plan runs, the rest queue.
func (h *Handler) WorkspaceExecutionSlot(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return
	}
	if h.slots == nil {
		// No arbitration configured means no plan is ever queued, which is a
		// truthful answer rather than an error.
		orihttp.Success(w, map[string]any{"studio_id": workspaceID, "queue": []any{}})
		return
	}

	holder, err := h.slots.Holder(r.Context(), workspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	queue, err := h.slots.store.Queue(r.Context(), workspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	if queue == nil {
		queue = []QueueEntry{}
	}

	orihttp.Success(w, map[string]any{
		"studio_id":      workspaceID,
		"executing_plan": holder,
		"queue":          queue,
		"queue_length":   len(queue),
		"max_concurrent": 1,
		"slot_available": holder == "",
	})
}

// PlanCancelPreview handles GET .../cancel-preview — what cancelling would
// affect, before it happens (FR-111, FR-154).
func (h *Handler) PlanCancelPreview(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}
	if h.executor == nil {
		writeError(w, fmt.Errorf("%w: execution is not configured", ErrValidation))
		return
	}

	preview, err := h.executor.PreviewCancel(r.Context(), workspaceID, planID)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, preview)
}

// PlanReconcile dispatches .../reconcile.
//
// GET previews what a revision would do to the work its earlier approval
// created; POST records the user's confirmation of one exact preview. They are
// the same resource because the preview is what the confirmation is OF: a
// confirmation that named nothing, or named a preview the server never
// produced, would authorize whatever the state happened to be (FR-77, FR-154).
func (h *Handler) PlanReconcile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.PlanReconcilePreview(w, r)
	case http.MethodPost:
		h.PlanReconcileConfirm(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// PlanReconcilePreview handles GET .../reconcile.
func (h *Handler) PlanReconcilePreview(w http.ResponseWriter, r *http.Request) {
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}
	if h.reconciler == nil {
		writeError(w, fmt.Errorf("%w: reconciliation is not configured", ErrValidation))
		return
	}

	preview, err := h.reconciler.Preview(r.Context(), workspaceID, planID)
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, preview)
}

// PlanReconcileConfirm handles POST .../reconcile.
func (h *Handler) PlanReconcileConfirm(w http.ResponseWriter, r *http.Request) {
	workspaceID, planID := requireWorkspaceAndPlanID(w, r)
	if workspaceID == "" || planID == "" {
		return
	}
	if h.reconciler == nil {
		writeError(w, fmt.Errorf("%w: reconciliation is not configured", ErrValidation))
		return
	}

	var req struct {
		Token string `json:"token"`
		Actor string `json:"actor,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	confirmation, err := h.reconciler.Confirm(r.Context(), workspaceID, planID, ConfirmInput{
		Token: req.Token, Actor: req.Actor,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	orihttp.Success(w, confirmation)
}

// PlanForTask handles GET /api/workspaces/{workspaceID}/plan-for-task/{taskID}.
//
// This is the reverse half of the Plan-to-Task link: Task detail asks "which
// plan produced me?" and gets a compact answer that deep-links to the canonical
// Plan route rather than duplicating its editor (FR-10, FR-148).
func (h *Handler) PlanForTask(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return
	}
	taskID := strings.TrimSpace(r.PathValue("taskID"))
	if taskID == "" {
		orihttp.BadRequest(w, "taskID is required")
		return
	}

	link, err := h.service.Store().PlanForTask(r.Context(), workspaceID, taskID)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRelatedPlan(w, r, workspaceID, link.PlanID, map[string]any{
		"task_id":     link.TaskID,
		"group_id":    link.GroupID,
		"item_id":     link.ItemID,
		"role":        link.Role,
		"approval_id": link.ApprovalID,
	}, link.Version)
}

// PlanForRun handles GET /api/workspaces/{workspaceID}/plan-for-run/{runID}.
func (h *Handler) PlanForRun(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return
	}
	runID := strings.TrimSpace(r.PathValue("runID"))
	if runID == "" {
		orihttp.BadRequest(w, "runID is required")
		return
	}

	link, err := h.service.Store().PlanForRun(r.Context(), workspaceID, runID)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRelatedPlan(w, r, workspaceID, link.PlanID, map[string]any{
		"run_id":   link.RunID,
		"task_id":  link.TaskID,
		"group_id": link.GroupID,
		"item_id":  link.ItemID,
	}, link.Version)
}

// writeRelatedPlan renders the compact related-Plan summary Task and Run detail
// show. It is deliberately small: a link and enough context to know what this
// work belongs to, never a second copy of the Plan (FR-148, FR-149).
func (h *Handler) writeRelatedPlan(w http.ResponseWriter, r *http.Request, workspaceID, planID string, provenance map[string]any, version int) {
	plan, err := h.service.Get(r.Context(), workspaceID, planID)
	if err != nil {
		writeError(w, err)
		return
	}

	summary := map[string]any{
		"plan_id":      plan.ID,
		"studio_id":    plan.WorkspaceID,
		"title":        plan.Title,
		"objective":    plan.Objective,
		"status":       plan.Status,
		"status_label": plan.Status.Label(),
		"plan_version": version,
		"provenance":   provenance,
		// The canonical route is returned rather than built by the client, so
		// every entry point lands on the same surface (FR-145, FR-149).
		"url": fmt.Sprintf("/workspaces/%s/plans/%s", plan.WorkspaceID, plan.ID),
	}
	if plan.Progress != nil {
		summary["progress"] = plan.Progress
	}
	orihttp.Success(w, summary)
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
	case CodeNotFound, CodeWorkspaceNotFound, CodeVersionNotFound, CodeApprovalNotFound,
		CodeReconcileNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeStaleDraft, CodeStaleVersion, CodeApprovalConsumed,
		CodeMaterializationConflict, CodeExecutionConflict, CodeNotDeletable,
		CodeReconcileConsumed, CodeStalePreview:
		return http.StatusConflict
	case CodeInvalidTransition, CodeApprovalMismatch, CodeArchived:
		return http.StatusConflict
	case CodeApprovalAuthority:
		return http.StatusForbidden
	case CodeValidationFailed, CodeLimitExceeded, CodeUnavailableCapability, CodeUnsafePath:
		return http.StatusUnprocessableEntity
	case CodeModelUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
