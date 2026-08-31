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

// HireService is the sole consequential hire boundary used by this package.
type HireService interface {
	Hire(ctx context.Context, userID string, request personalassistant.HireRequest) (*personalassistant.HireResult, error)
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

// Handler serves /api/personal-assistant.
type Handler struct {
	service     StateReader
	hirer       HireService
	assignments AssignmentPreviewService
	today       TodayReader
	provider    userprofile.UserProvider
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

// SetAssignmentService adds the first-value mutation boundary.
func (h *Handler) SetAssignmentService(assignments AssignmentPreviewService) {
	if h != nil {
		h.assignments = assignments
	}
}

// SetTodayService adds the read-only Home projection after canonical stores are wired.
func (h *Handler) SetTodayService(today TodayReader) {
	if h != nil {
		h.today = today
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
	RequestID     string                 `json:"request_id"`
	IfVersion     int64                  `json:"if_version"`
	DisplayName   string                 `json:"display_name"`
	Appearance    *types.AgentAppearance `json:"appearance"`
	Mandate       string                 `json:"mandate"`
	FocusAreas    []string               `json:"focus_areas"`
	Timezone      string                 `json:"timezone"`
	ScheduleDays  []string               `json:"schedule_days"`
	ScheduleTime  string                 `json:"schedule_time"`
	NotifyOnReady bool                   `json:"notify_on_ready"`
}

type hireResponse struct {
	Status                 personalassistant.RelationshipStatus    `json:"status"`
	AssistantID            string                                  `json:"assistant_id"`
	DisplayName            string                                  `json:"display_name"`
	Appearance             *types.AgentAppearance                  `json:"appearance,omitempty"`
	HQWorkspaceID          string                                  `json:"hq_workspace_id"`
	HQEntryAgentInstanceID string                                  `json:"hq_entry_agent_instance_id"`
	GlobalAgentProfileName string                                  `json:"global_agent_profile_name"`
	StateVersion           int64                                   `json:"state_version"`
	FirstAssignmentStatus  personalassistant.FirstAssignmentStatus `json:"first_assignment_status"`
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
			writeHireError(w, http.StatusBadRequest, "invalid_hire_request", "Check the assistant name, working agreement, appearance, and Daily Brief rhythm.", false, nil)
		case errors.Is(err, personalassistant.ErrIneligible):
			writeHireError(w, http.StatusForbidden, "personal_assistant_ineligible", "Personal assistant hiring is not available for this install.", false, nil)
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
	orihttp.Success(w, map[string]any{"first_assignment": result})
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
		Status: state.Status, AssistantID: state.AssistantID,
		DisplayName: state.DisplayName, Appearance: state.Appearance.Clone(),
		HQWorkspaceID:          state.HQWorkspaceID,
		HQEntryAgentInstanceID: state.HQEntryAgentInstanceID,
		GlobalAgentProfileName: state.GlobalAgentProfileName,
		StateVersion:           state.StateVersion,
		FirstAssignmentStatus:  state.FirstAssignmentStatus,
		HiredAt:                state.HiredAt, DailyBrief: result.BriefConfig, Resumed: result.Resumed,
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
