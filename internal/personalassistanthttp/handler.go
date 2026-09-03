// Package personalassistanthttp exposes the bounded user-scoped personal
// assistant read and mutation API.
package personalassistanthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

const maxHireBodyBytes = 64 << 10

// StateReader is the read-only service contract used by the HTTP layer.
type StateReader interface {
	Get(ctx context.Context, userID string) (*personalassistant.Projection, error)
}

// HQSetupService is the sole consequential Personal HQ boundary used by this
// package. It is separate from HireService because the two consequences now
// happen at different times and under different confirmations.
type HQSetupService interface {
	Setup(ctx context.Context, userID string, request personalassistant.HQSetupRequest) (*personalassistant.HQSetupResult, error)
}

// HireService is the sole consequential hire boundary used by this package.
type HireService interface {
	Hire(ctx context.Context, userID string, request personalassistant.HireRequest) (*personalassistant.HireResult, error)
}

// RecoveryService reconnects a server-validated orphan identity. The request
// carries no profile, assistant, workspace, or instance ID.
type RecoveryService interface {
	Repair(ctx context.Context, userID string, ifVersion int64) (*personalassistant.State, error)
}

// AssignmentPreviewService persists deterministic first-assignment previews.
type AssignmentPreviewService interface {
	Current(ctx context.Context, userID string) (*personalassistant.AssignmentCurrentResult, error)
	Preview(ctx context.Context, userID string, ifVersion int64, input personalassistant.AssignmentInput) (*personalassistant.AssignmentPreviewResult, error)
	Apply(ctx context.Context, userID string, request personalassistant.AssignmentApplyRequest) (*personalassistant.AssignmentApplyResult, error)
}

// TodayReader builds the bounded server-owned Home projection.
type TodayReader interface {
	Get(ctx context.Context, userID string) (*personalassistant.TodayProjection, error)
}

// ContinuityService owns working-agreement and pause/resume writes.
type ContinuityService interface {
	UpdateWorkingAgreement(ctx context.Context, userID string, request personalassistant.WorkingAgreementUpdate) (*personalassistant.Projection, error)
	Pause(ctx context.Context, userID string, ifVersion int64) (*personalassistant.Projection, error)
	Resume(ctx context.Context, userID string, ifVersion int64) (*personalassistant.Projection, error)
}

type RenameService interface {
	Rename(ctx context.Context, userID, newName string, ifVersion int64) (*personalassistant.Projection, error)
}

type CapabilityReader interface {
	Get(ctx context.Context, userID string) (*personalassistant.CapabilityProjection, error)
}

// SpecialistOfferService records the post-hire domain offer's answer.
type SpecialistOfferService interface {
	Answer(ctx context.Context, userID string, request personalassistant.SpecialistOfferRequest) (*personalassistant.State, error)
}

// Handler serves /api/personal-assistant.
type Handler struct {
	service                    StateReader
	hirer                      HireService
	recovery                   RecoveryService
	hqSetup                    HQSetupService
	assignments                AssignmentPreviewService
	today                      TodayReader
	continuity                 ContinuityService
	renamer                    RenameService
	capabilities               CapabilityReader
	specialistOffers           SpecialistOfferService
	provider                   userprofile.UserProvider
	onFirstAssignmentCompleted func()
}

// NewHandler constructs a personal-assistant HTTP handler.
func NewHandler(service StateReader, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, provider: provider}
}

// SetHireService adds the mutation boundary after its setup dependencies exist.
func (h *Handler) SetHireService(hirer HireService) {
	if h != nil {
		h.hirer = hirer
	}
}

// SetRecoveryService adds the explicit orphan-reconnection boundary.
func (h *Handler) SetRecoveryService(recovery RecoveryService) {
	if h != nil {
		h.recovery = recovery
	}
}

// SetHQSetupService adds the post-hire Personal HQ mutation boundary.
func (h *Handler) SetHQSetupService(hqSetup HQSetupService) {
	if h != nil {
		h.hqSetup = hqSetup
	}
}

// SetAssignmentService adds the first-value mutation boundary.
func (h *Handler) SetAssignmentService(assignments AssignmentPreviewService) {
	if h != nil {
		h.assignments = assignments
	}
}

// SetOnFirstAssignmentCompleted connects a successful, durable apply to the
// optional progression presentation without making progression part of the
// assignment transaction.
func (h *Handler) SetOnFirstAssignmentCompleted(fn func()) {
	if h != nil {
		h.onFirstAssignmentCompleted = fn
	}
}

// SetTodayService adds the read-only Home projection after canonical stores are wired.
func (h *Handler) SetTodayService(today TodayReader) {
	if h != nil {
		h.today = today
	}
}

func (h *Handler) SetContinuityService(service ContinuityService) {
	if h != nil {
		h.continuity = service
	}
}

func (h *Handler) SetRenameService(service RenameService) {
	if h != nil {
		h.renamer = service
	}
}

func (h *Handler) SetCapabilityService(service CapabilityReader) {
	if h != nil {
		h.capabilities = service
	}
}

func (h *Handler) SetSpecialistOfferService(service SpecialistOfferService) {
	if h != nil {
		h.specialistOffers = service
	}
}

// GetState handles GET /api/personal-assistant.
func (h *Handler) GetState(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.service == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant service is unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, err := h.service.Get(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "personal assistant state is temporarily unavailable")
		return
	}
	personalassistant.RecordStateViewed(projection)
	orihttp.Success(w, map[string]any{"personal_assistant": projection})
}

// GetToday handles GET /api/personal-assistant/today.
func (h *Handler) GetToday(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.today == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant Today is unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, err := h.today.Get(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "personal assistant Today is temporarily unavailable")
		return
	}
	orihttp.Success(w, map[string]any{"today": projection})
}

type hireRequest struct {
	RequestID   string                 `json:"request_id"`
	IfVersion   int64                  `json:"if_version"`
	DisplayName string                 `json:"display_name"`
	Appearance  *types.AgentAppearance `json:"appearance"`
	Mandate     string                 `json:"mandate"`
	FocusAreas  []string               `json:"focus_areas"`

	// Deprecated: a fresh hire collects no Daily Brief rhythm — the Map's HQ
	// build form owns it, where it can be written against a real workspace ID.
	// These fields are still decoded so a stale client's request is accepted and
	// ignored rather than rejected outright; the coordinator drops them.
	Timezone      string   `json:"timezone,omitempty"`
	ScheduleDays  []string `json:"schedule_days,omitempty"`
	ScheduleTime  string   `json:"schedule_time,omitempty"`
	NotifyOnReady bool     `json:"notify_on_ready,omitempty"`
}

// hireResponse reports the durable identity a hire produced. HQ and Daily Brief
// fields are omitted when empty: a fresh hire has neither, and claiming an empty
// HQ would be exactly the fabrication the needs_hq state exists to avoid.
type hireResponse struct {
	Status                 personalassistant.RelationshipStatus    `json:"status"`
	State                  personalassistant.APIState              `json:"state"`
	NextAction             string                                  `json:"next_action"`
	AssistantID            string                                  `json:"assistant_id"`
	DisplayName            string                                  `json:"display_name"`
	Appearance             *types.AgentAppearance                  `json:"appearance,omitempty"`
	HQWorkspaceID          string                                  `json:"hq_workspace_id,omitempty"`
	HQEntryAgentInstanceID string                                  `json:"hq_entry_agent_instance_id,omitempty"`
	GlobalAgentProfileName string                                  `json:"global_agent_profile_name"`
	StateVersion           int64                                   `json:"state_version"`
	FirstAssignmentStatus  personalassistant.FirstAssignmentStatus `json:"first_assignment_status"`
	SpecialistSlug         string                                  `json:"specialist_slug,omitempty"`
	HiredAt                *time.Time                              `json:"hired_at,omitempty"`
	DailyBrief             *dailybrief.Config                      `json:"daily_brief,omitempty"`
	Resumed                bool                                    `json:"resumed"`
}

type hireErrorResponse struct {
	Error         string                       `json:"error"`
	Code          string                       `json:"code"`
	Retryable     bool                         `json:"retryable"`
	RepairStep    personalassistant.RepairStep `json:"repair_step,omitempty"`
	DurableResult *hireResponse                `json:"durable_result,omitempty"`
}

// Hire handles POST /api/personal-assistant/hire.
func (h *Handler) Hire(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.hirer == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant hire service is unavailable")
		return
	}
	var body hireRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		writeHireError(w, http.StatusBadRequest, "invalid_hire_request", "The hire request is invalid.", false, nil)
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	result, err := h.hirer.Hire(r.Context(), userID, personalassistant.HireRequest{
		RequestID: body.RequestID, IfVersion: body.IfVersion,
		DisplayName: body.DisplayName, Appearance: body.Appearance,
		Mandate: body.Mandate, FocusAreas: body.FocusAreas,
		Timezone: body.Timezone, ScheduleDays: body.ScheduleDays,
		ScheduleTime: body.ScheduleTime, NotifyOnReady: body.NotifyOnReady,
	})
	if err != nil {
		switch {
		case errors.Is(err, personalassistant.ErrValidation):
			writeHireError(w, http.StatusBadRequest, "invalid_hire_request", "Check the assistant name, working agreement, and appearance.", false, nil)
		case errors.Is(err, personalassistant.ErrConflict), errors.Is(err, personalhq.ErrAssistantNameConflict):
			writeHireError(w, http.StatusConflict, "hire_conflict", "This hire conflicts with the current assistant relationship. Refresh and try again.", false, nil)
		default:
			var partial *personalassistant.PartialHireError
			if errors.As(err, &partial) {
				writeHireError(w, http.StatusServiceUnavailable, "hire_partial", "Part of the hire is already saved. Retry to finish setup.", true, partial)
				return
			}
			writeHireError(w, http.StatusServiceUnavailable, "hire_unavailable", "Personal assistant hiring is temporarily unavailable. Retry this same request.", true, nil)
		}
		return
	}
	response := responseFromResult(result)
	if result.Resumed {
		orihttp.Success(w, map[string]any{"personal_assistant": response})
		return
	}
	orihttp.Created(w, map[string]any{"personal_assistant": response})
}

type recoveryRequest struct {
	IfVersion *int64 `json:"if_version"`
}

type recoveryErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// Repair reconnects one independently validated orphan profile/HQ identity.
// Identity fields are intentionally absent from recoveryRequest: the server
// re-runs the complete inspection immediately before inserting the missing row.
func (h *Handler) Repair(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.recovery == nil || h.service == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant recovery is unavailable")
		return
	}
	var body recoveryRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil || body.IfVersion == nil {
		writeRecoveryError(w, http.StatusBadRequest, "invalid_recovery_request",
			"The recovery request is invalid.")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	if _, err := h.recovery.Repair(r.Context(), userID, *body.IfVersion); err != nil {
		switch {
		case errors.Is(err, personalassistant.ErrValidation):
			writeRecoveryError(w, http.StatusBadRequest, "invalid_recovery_request",
				"The recovery request is invalid.")
		case errors.Is(err, personalassistant.ErrNotFound):
			writeRecoveryError(w, http.StatusConflict, "recovery_not_available",
				"No existing assistant relationship can be safely recovered.")
		case errors.Is(err, personalassistant.ErrConflict):
			writeRecoveryError(w, http.StatusConflict, "recovery_conflict",
				"The assistant relationship changed. Refresh before trying again.")
		case errors.Is(err, personalassistant.ErrRepairNeeded):
			writeRecoveryError(w, http.StatusConflict, "recovery_blocked",
				"Existing assistant records do not agree, so nothing was reconnected.")
		default:
			orihttp.ServiceUnavailable(w, "personal assistant recovery is temporarily unavailable")
		}
		return
	}
	projection, err := h.service.Get(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "personal assistant recovery was saved; reload to verify it")
		return
	}
	orihttp.Success(w, map[string]any{"personal_assistant": projection})
}

func writeRecoveryError(w http.ResponseWriter, status int, code, message string) {
	_ = orihttp.RespondJSON(w, status, recoveryErrorResponse{Error: message, Code: code})
}

// hqSetupRequest is the bounded Build My HQ submission.
//
// Note what it cannot say: there is no assistant, profile, workspace, or
// instance field. Those come from the server's own relationship record, so a
// client cannot aim this operation at records it does not own.
type hqSetupRequest struct {
	RequestID string `json:"request_id"`
	IfVersion int64  `json:"if_version"`

	Name                    string   `json:"name"`
	Timezone                string   `json:"timezone"`
	ScheduleDays            []string `json:"schedule_days"`
	ScheduleTime            string   `json:"schedule_time"`
	Scope                   string   `json:"scope"`
	SelectedWorkspaceIDs    []string `json:"selected_workspace_ids"`
	IncludeFutureWorkspaces bool     `json:"include_future_workspaces"`
	NotifyOnReady           bool     `json:"notify_on_ready"`
}

type hqSetupErrorResponse struct {
	Error         string                       `json:"error"`
	Code          string                       `json:"code"`
	Retryable     bool                         `json:"retryable"`
	RepairStep    personalassistant.RepairStep `json:"repair_step,omitempty"`
	DurableResult *hireResponse                `json:"durable_result,omitempty"`
}

// SetupHQ handles POST /api/personal-assistant/hq: the one confirmed
// consequence of the guided Map walkthrough.
func (h *Handler) SetupHQ(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.hqSetup == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant hq setup is unavailable")
		return
	}
	var body hqSetupRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		writeHQSetupError(w, http.StatusBadRequest, "invalid_hq_setup_request",
			"The Personal HQ request is invalid.", false, nil)
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}

	result, err := h.hqSetup.Setup(r.Context(), userID, personalassistant.HQSetupRequest{
		RequestID: body.RequestID, IfVersion: body.IfVersion,
		HQName: body.Name, Timezone: body.Timezone,
		ScheduleDays: body.ScheduleDays, ScheduleTime: body.ScheduleTime,
		Scope: body.Scope, SelectedIDs: body.SelectedWorkspaceIDs,
		IncludeFuture: body.IncludeFutureWorkspaces, NotifyOnReady: body.NotifyOnReady,
	})
	if err != nil {
		switch {
		case errors.Is(err, personalassistant.ErrValidation):
			writeHQSetupError(w, http.StatusBadRequest, "invalid_hq_setup_request",
				"Check the Personal HQ name and the daily rhythm.", false, nil)
		case errors.Is(err, personalassistant.ErrConflict), errors.Is(err, personalhq.ErrAssistantNameConflict):
			writeHQSetupError(w, http.StatusConflict, "hq_setup_conflict",
				"This request conflicts with your current Personal HQ setup. Refresh and try again.", false, nil)
		case errors.Is(err, personalassistant.ErrRepairNeeded):
			writeHQSetupError(w, http.StatusConflict, "hq_setup_repair_needed",
				"Your Personal HQ setup needs repair before it can continue.", false, nil)
		default:
			var partial *personalassistant.PartialHQSetupError
			if errors.As(err, &partial) {
				// Durable partial: some canonical records exist. Retrying the SAME
				// request finishes it, so this must never read as "start over".
				writeHQSetupError(w, http.StatusServiceUnavailable, "hq_setup_partial",
					"Part of your Personal HQ is already saved. Retry to finish setup.", true, partial)
				return
			}
			writeHQSetupError(w, http.StatusServiceUnavailable, "hq_setup_unavailable",
				"Building Personal HQ is temporarily unavailable. Retry this same request.", true, nil)
		}
		return
	}

	response := responseFromResult(&personalassistant.HireResult{
		State: result.State, BriefConfig: result.BriefConfig, Resumed: result.Resumed,
	})
	if result.Resumed {
		orihttp.Success(w, map[string]any{"personal_assistant": response})
		return
	}
	orihttp.Created(w, map[string]any{"personal_assistant": response})
}

func writeHQSetupError(
	w http.ResponseWriter, status int, code, message string,
	retryable bool, partial *personalassistant.PartialHQSetupError,
) {
	response := hqSetupErrorResponse{Error: message, Code: code, Retryable: retryable}
	if partial != nil {
		response.RepairStep = partial.Step
		response.DurableResult = responseFromResult(
			&personalassistant.HireResult{State: partial.State, Resumed: true})
	}
	_ = orihttp.RespondJSON(w, status, response)
}

type assignmentPreviewRequest struct {
	IfVersion int64                                  `json:"if_version"`
	Rows      []personalassistant.AssignmentInputRow `json:"rows"`
}

type assignmentConflictResponse struct {
	Error        string                               `json:"error"`
	Code         string                               `json:"code"`
	StateVersion int64                                `json:"state_version,omitempty"`
	Preview      *personalassistant.AssignmentPreview `json:"current_preview,omitempty"`
}

// GetFirstAssignment returns restart-safe current preview/apply state.
func (h *Handler) GetFirstAssignment(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.assignments == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant assignment service is unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	result, err := h.assignments.Current(r.Context(), userID)
	if errors.Is(err, personalassistant.ErrNotFound) {
		_ = orihttp.RespondJSON(w, http.StatusConflict, assignmentConflictResponse{
			Error: "An active assistant relationship is required.", Code: "assignment_conflict",
		})
		return
	}
	if err != nil {
		orihttp.ServiceUnavailable(w, "first-assignment state is temporarily unavailable")
		return
	}
	orihttp.Success(w, map[string]any{"first_assignment": result})
}

// PreviewFirstAssignment handles
// POST /api/personal-assistant/first-assignment/preview.
func (h *Handler) PreviewFirstAssignment(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.assignments == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant assignment service is unavailable")
		return
	}
	var body assignmentPreviewRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		_ = orihttp.RespondJSON(w, http.StatusBadRequest, assignmentConflictResponse{
			Error: "The first-assignment preview request is invalid.", Code: "invalid_assignment",
		})
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	result, err := h.assignments.Preview(r.Context(), userID, body.IfVersion, personalassistant.AssignmentInput{Rows: body.Rows})
	if err != nil {
		if errors.Is(err, personalassistant.ErrValidation) {
			_ = orihttp.RespondJSON(w, http.StatusBadRequest, assignmentConflictResponse{
				Error: "Check each assignment row and try again.", Code: "invalid_assignment",
			})
			return
		}
		var conflict *personalassistant.AssignmentPreviewConflictError
		if errors.As(err, &conflict) || errors.Is(err, personalassistant.ErrConflict) || errors.Is(err, personalassistant.ErrNotFound) {
			response := assignmentConflictResponse{
				Error: "The assistant relationship or preview changed. Refresh before continuing.",
				Code:  "assignment_conflict",
			}
			if conflict != nil {
				response.StateVersion = conflict.StateVersion
				response.Preview = conflict.Preview
			}
			_ = orihttp.RespondJSON(w, http.StatusConflict, response)
			return
		}
		orihttp.ServiceUnavailable(w, "first-assignment preview is temporarily unavailable")
		return
	}
	orihttp.Success(w, map[string]any{"first_assignment": result})
}

type assignmentPartialResponse struct {
	Error           string                                   `json:"error"`
	Code            string                                   `json:"code"`
	FirstAssignment *personalassistant.AssignmentApplyResult `json:"first_assignment,omitempty"`
}

// ApplyFirstAssignment handles
// POST /api/personal-assistant/first-assignment/apply.
func (h *Handler) ApplyFirstAssignment(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.assignments == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant assignment service is unavailable")
		return
	}
	var body personalassistant.AssignmentApplyRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		_ = orihttp.RespondJSON(w, http.StatusBadRequest, assignmentPartialResponse{
			Error: "The first-assignment apply request is invalid.", Code: "invalid_assignment_apply",
		})
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	result, err := h.assignments.Apply(r.Context(), userID, body)
	if err != nil {
		if errors.Is(err, personalassistant.ErrValidation) {
			_ = orihttp.RespondJSON(w, http.StatusBadRequest, assignmentPartialResponse{
				Error: "The first-assignment apply request is invalid.", Code: "invalid_assignment_apply",
			})
			return
		}
		var partial *personalassistant.PartialAssignmentError
		if errors.As(err, &partial) {
			_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, assignmentPartialResponse{
				Error: "Some first-assignment records were saved. Retry to continue safely.",
				Code:  "assignment_partial", FirstAssignment: partial.Result,
			})
			return
		}
		var conflict *personalassistant.AssignmentPreviewConflictError
		if errors.As(err, &conflict) || errors.Is(err, personalassistant.ErrConflict) || errors.Is(err, personalassistant.ErrNotFound) {
			response := assignmentConflictResponse{
				Error: "The assistant relationship or preview changed. Refresh before continuing.",
				Code:  "assignment_conflict",
			}
			if conflict != nil {
				response.StateVersion = conflict.StateVersion
				response.Preview = conflict.Preview
			}
			_ = orihttp.RespondJSON(w, http.StatusConflict, response)
			return
		}
		orihttp.ServiceUnavailable(w, "first-assignment apply is temporarily unavailable")
		return
	}
	if result != nil && result.Status == personalassistant.AssignmentCompleted && h.onFirstAssignmentCompleted != nil {
		h.onFirstAssignmentCompleted()
	}
	orihttp.Success(w, map[string]any{"first_assignment": result})
}

type workingAgreementRequest struct {
	IfVersion               int64             `json:"if_version"`
	IfConfigRevision        int               `json:"if_config_revision,omitempty"`
	Mandate                 *string           `json:"mandate,omitempty"`
	FocusAreas              *[]string         `json:"focus_areas,omitempty"`
	Timezone                *string           `json:"timezone,omitempty"`
	ScheduleDays            *[]string         `json:"schedule_days,omitempty"`
	ScheduleTime            *string           `json:"schedule_time,omitempty"`
	ScheduleEnabled         *bool             `json:"schedule_enabled,omitempty"`
	Scope                   *dailybrief.Scope `json:"scope,omitempty"`
	SelectedWorkspaceIDs    *[]string         `json:"selected_workspace_ids,omitempty"`
	IncludeFutureWorkspaces *bool             `json:"include_future_workspaces,omitempty"`
	NotifyOnReady           *bool             `json:"notify_on_ready,omitempty"`
}

type versionedRequest struct {
	IfVersion int64 `json:"if_version"`
}

type renameRequest struct {
	IfVersion int64  `json:"if_version"`
	Name      string `json:"name"`
}

type specialistOfferRequest struct {
	IfVersion int64  `json:"if_version"`
	Decision  string `json:"decision"`
	Slug      string `json:"slug"`
}

// AnswerSpecialistOffer handles POST /api/personal-assistant/specialist.
//
// This is the post-hire domain offer's only write. It records an accept or a
// decline on the existing relationship; it never creates a workspace, runs a
// setup wizard, or changes anything about the hire itself.
func (h *Handler) AnswerSpecialistOffer(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.specialistOffers == nil || h.service == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant specialist offer is unavailable")
		return
	}
	var body specialistOfferRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		orihttp.BadRequest(w, "The specialist answer is invalid.")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	if _, err := h.specialistOffers.Answer(r.Context(), userID, personalassistant.SpecialistOfferRequest{
		IfVersion: body.IfVersion, Decision: body.Decision, Slug: body.Slug,
	}); err != nil {
		switch {
		case errors.Is(err, personalassistant.ErrValidation):
			orihttp.BadRequest(w, "Check the specialist answer and try again.")
		case errors.Is(err, personalassistant.ErrConflict):
			orihttp.Conflict(w, "This answer conflicts with the current relationship. Refresh and try again.")
		case errors.Is(err, personalassistant.ErrNotFound):
			orihttp.BadRequest(w, "No assistant has been hired yet.")
		default:
			orihttp.ServiceUnavailable(w, "The specialist answer could not be saved. Try again.")
		}
		return
	}
	// Re-read rather than projecting the returned state, so the client gets the
	// same shape every other personal-assistant read returns.
	projection, err := h.service.Get(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "personal assistant state is temporarily unavailable")
		return
	}
	orihttp.Success(w, map[string]any{"personal_assistant": projection})
}

func (h *Handler) UpdateWorkingAgreement(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPatch) {
		return
	}
	if h == nil || h.continuity == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant working agreement is unavailable")
		return
	}
	var body workingAgreementRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		writeContinuityError(w, http.StatusBadRequest, "invalid_working_agreement", "Check the working agreement fields and try again.", nil)
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, err := h.continuity.UpdateWorkingAgreement(r.Context(), userID, personalassistant.WorkingAgreementUpdate{
		IfVersion: body.IfVersion, IfConfigRevision: body.IfConfigRevision,
		Mandate: body.Mandate, FocusAreas: body.FocusAreas, Timezone: body.Timezone,
		ScheduleDays: body.ScheduleDays, ScheduleTime: body.ScheduleTime, ScheduleEnabled: body.ScheduleEnabled,
		Scope: body.Scope, SelectedWorkspaceIDs: body.SelectedWorkspaceIDs,
		IncludeFutureWorkspaces: body.IncludeFutureWorkspaces, NotifyOnReady: body.NotifyOnReady,
	})
	if err != nil {
		h.writeContinuityServiceError(w, r, userID, err)
		return
	}
	orihttp.Success(w, map[string]any{"personal_assistant": projection})
}

func (h *Handler) Pause(w http.ResponseWriter, r *http.Request) {
	h.setPaused(w, r, true)
}

func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	h.setPaused(w, r, false)
}

func (h *Handler) setPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.continuity == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant routine control is unavailable")
		return
	}
	var body versionedRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		writeContinuityError(w, http.StatusBadRequest, "invalid_state_change", "A current state version is required.", nil)
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	var projection *personalassistant.Projection
	var err error
	if paused {
		projection, err = h.continuity.Pause(r.Context(), userID, body.IfVersion)
	} else {
		projection, err = h.continuity.Resume(r.Context(), userID, body.IfVersion)
	}
	if err != nil {
		h.writeContinuityServiceError(w, r, userID, err)
		return
	}
	orihttp.Success(w, map[string]any{"personal_assistant": projection})
}

func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.renamer == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant rename is unavailable")
		return
	}
	var body renameRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		writeContinuityError(w, http.StatusBadRequest, "invalid_rename", "Check the assistant name and try again.", nil)
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, err := h.renamer.Rename(r.Context(), userID, body.Name, body.IfVersion)
	if err != nil {
		h.writeContinuityServiceError(w, r, userID, err)
		return
	}
	orihttp.Success(w, map[string]any{"personal_assistant": projection})
}

func (h *Handler) GetCapabilities(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.capabilities == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant capabilities are unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	projection, err := h.capabilities.Get(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "personal assistant capabilities are temporarily unavailable")
		return
	}
	orihttp.Success(w, map[string]any{"capabilities": projection})
}

type continuityErrorResponse struct {
	Error   string                        `json:"error"`
	Code    string                        `json:"code"`
	Current *personalassistant.Projection `json:"current,omitempty"`
}

func (h *Handler) writeContinuityServiceError(w http.ResponseWriter, r *http.Request, userID string, err error) {
	switch {
	case errors.Is(err, personalassistant.ErrValidation):
		writeContinuityError(w, http.StatusBadRequest, "invalid_working_agreement", "Check the values and try again.", nil)
	case errors.Is(err, personalassistant.ErrConflict):
		var current *personalassistant.Projection
		if h.service != nil {
			current, _ = h.service.Get(r.Context(), userID)
		}
		writeContinuityError(w, http.StatusConflict, "state_conflict", "The assistant changed. Review the current values before applying your edit again.", current)
	case errors.Is(err, personalassistant.ErrNeedsHQ):
		// An expected setup stage, not corruption: never reuse repair language
		// for a relationship that just has no Personal HQ yet.
		writeContinuityError(w, http.StatusConflict, "personal_hq_required", "Build Personal HQ before changing this setting.", nil)
	case errors.Is(err, personalassistant.ErrRepairNeeded), errors.Is(err, personalassistant.ErrNotFound):
		writeContinuityError(w, http.StatusConflict, "repair_required", "The assistant relationship needs repair before this change can continue.", nil)
	default:
		orihttp.ServiceUnavailable(w, "personal assistant continuity change is temporarily unavailable")
	}
}

func writeContinuityError(w http.ResponseWriter, status int, code, message string, current *personalassistant.Projection) {
	_ = orihttp.RespondJSON(w, status, continuityErrorResponse{Error: message, Code: code, Current: current})
}

func (h *Handler) currentUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := h.provider.CurrentUserID(r.Context())
	if err != nil {
		orihttp.InternalError(w, "Failed to resolve current user")
		return "", false
	}
	if strings.TrimSpace(userID) == "" {
		userID = userprofile.LocalUserID
	}
	return userID, true
}

func decodeBoundedRequest(w http.ResponseWriter, r *http.Request, target any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHireBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func responseFromResult(result *personalassistant.HireResult) *hireResponse {
	if result == nil || result.State == nil {
		return nil
	}
	state := result.State
	return &hireResponse{
		Status: state.Status, State: hireAPIState(state.Status),
		NextAction:  hireNextAction(state.Status),
		AssistantID: state.AssistantID,
		DisplayName: state.DisplayName, Appearance: state.Appearance.Clone(),
		HQWorkspaceID:          state.HQWorkspaceID,
		HQEntryAgentInstanceID: state.HQEntryAgentInstanceID,
		GlobalAgentProfileName: state.GlobalAgentProfileName,
		StateVersion:           state.StateVersion,
		FirstAssignmentStatus:  state.FirstAssignmentStatus,
		SpecialistSlug:         state.SpecialistSlug,
		HiredAt:                state.HiredAt, DailyBrief: result.BriefConfig, Resumed: result.Resumed,
	}
}

// hireAPIState maps the durable status to the same public vocabulary
// GET /api/personal-assistant uses, so a client never has to learn two names
// for one stage.
func hireAPIState(status personalassistant.RelationshipStatus) personalassistant.APIState {
	switch status {
	case personalassistant.StatusAwaitingHQ:
		return personalassistant.APIStateNeedsHQ
	case personalassistant.StatusProvisioningHQ:
		return personalassistant.APIStateProvisioningHQ
	case personalassistant.StatusActive:
		return personalassistant.APIStateActive
	case personalassistant.StatusPaused:
		return personalassistant.APIStatePaused
	case personalassistant.StatusRepairNeeded:
		return personalassistant.APIStateRepairNeeded
	case personalassistant.StatusNotHired:
		return personalassistant.APIStateNeedsHire
	default:
		return personalassistant.APIStateHiring
	}
}

func hireNextAction(status personalassistant.RelationshipStatus) string {
	switch status {
	case personalassistant.StatusAwaitingHQ:
		return personalassistant.NextActionBuildHQ
	case personalassistant.StatusProvisioningHQ:
		return personalassistant.NextActionResumeHQ
	case personalassistant.StatusActive:
		return personalassistant.NextActionAsk
	case personalassistant.StatusPaused:
		return personalassistant.NextActionResume
	case personalassistant.StatusRepairNeeded:
		return personalassistant.NextActionRepair
	case personalassistant.StatusNotHired:
		return personalassistant.NextActionHire
	default:
		return personalassistant.NextActionResumeHire
	}
}

func writeHireError(w http.ResponseWriter, status int, code, message string, retryable bool, partial *personalassistant.PartialHireError) {
	response := hireErrorResponse{Error: message, Code: code, Retryable: retryable}
	if partial != nil {
		response.RepairStep = partial.Step
		response.DurableResult = responseFromResult(&personalassistant.HireResult{State: partial.State, Resumed: true})
	}
	_ = orihttp.RespondJSON(w, status, response)
}
