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

// Handler serves /api/personal-assistant.
type Handler struct {
	service  StateReader
	hirer    HireService
	provider userprofile.UserProvider
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
	if err := decodeHireRequest(w, r, &body); err != nil {
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

func decodeHireRequest(w http.ResponseWriter, r *http.Request, target *hireRequest) error {
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
