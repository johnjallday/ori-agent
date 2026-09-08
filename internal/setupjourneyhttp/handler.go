// Package setupjourneyhttp exposes the current user's bounded specialist setup
// journey. Request bodies cannot select relationship, owner, declaration,
// adapter, plugin source, Home, workspace, or runtime scope.
package setupjourneyhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

const maxRequestBodyBytes = 40 << 10

// Service is the current-user journey boundary used by the HTTP layer.
type Service interface {
	Read(ctx context.Context, userID, runID string) (*setupjourney.JourneyProjection, error)
	Open(ctx context.Context, userID, runID string, request setupjourney.PresentationMutation) (*setupjourney.JourneyProjection, error)
	Dismiss(ctx context.Context, userID, runID string, request setupjourney.PresentationMutation) (*setupjourney.JourneyProjection, error)
	CreateOrResumeChild(ctx context.Context, userID string, request setupjourney.PresentationMutation) (*setupjourney.JourneyProjection, error)
	Mutate(ctx context.Context, userID, runID string, actionID setupjourney.ActionID, request setupjourney.ActionMutation) (*setupjourney.ActionResult, error)
}

type Handler struct {
	service  Service
	provider userprofile.UserProvider
}

func NewHandler(service Service, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{service: service, provider: provider}
}

type journeyResponse struct {
	Journey *setupjourney.JourneyProjection `json:"setup_journey"`
}

type errorResponse struct {
	Error   *setupjourney.Failure           `json:"error"`
	Current *setupjourney.JourneyProjection `json:"current,omitempty"`
}

type mutationRequest struct {
	IfRevision     int64  `json:"if_revision"`
	IdempotencyKey string `json:"idempotency_key"`
}

type actionRequest struct {
	IfRevision     int64           `json:"if_revision"`
	IdempotencyKey string          `json:"idempotency_key"`
	ReviewToken    string          `json:"review_token,omitempty"`
	Input          json.RawMessage `json:"input,omitempty"`
}

// GetRoot handles GET /api/personal-assistant/setup-journey.
func (h *Handler) GetRoot(w http.ResponseWriter, r *http.Request) {
	h.read(w, r, "")
}

// GetRun handles GET /api/personal-assistant/setup-journey/runs/{runID}.
func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	h.read(w, r, r.PathValue("runID"))
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request, runID string) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if !noQuery(r) || (runID != "" && !boundedRunID(runID)) {
		h.writeFailure(w, r, "", runID, setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if h == nil || h.service == nil {
		h.writeFailure(w, r, userID, runID, setupjourney.FailureFor(setupjourney.ReasonJourneyUnavailable, 0))
		return
	}
	projection, err := h.service.Read(r.Context(), userID, runID)
	if err != nil {
		h.writeFailure(w, r, userID, runID, err)
		return
	}
	orihttp.Success(w, journeyResponse{Journey: projection})
}

// CheckPreparation reads machine prerequisites without project grants or live
// verification. Implementations that do not support it fail closed.
func (h *Handler) CheckPreparation(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	runID := r.PathValue("runID")
	if !noQuery(r) || !boundedRunID(runID) {
		h.writeFailure(w, r, "", runID, setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	service, ok := h.service.(interface {
		CheckPreparation(context.Context, string, string) (*setupjourney.PreparationCheck, error)
	})
	if !ok {
		h.writeFailure(w, r, userID, runID, setupjourney.FailureFor(setupjourney.ReasonOwnerUnavailable, 0))
		return
	}
	check, err := service.CheckPreparation(r.Context(), userID, runID)
	if err != nil {
		h.writeFailure(w, r, userID, runID, err)
		return
	}
	orihttp.Success(w, check)
}

// OpenRoot and OpenRun record presentation history only.
func (h *Handler) OpenRoot(w http.ResponseWriter, r *http.Request) {
	h.presentation(w, r, "", true)
}

func (h *Handler) OpenRun(w http.ResponseWriter, r *http.Request) {
	h.presentation(w, r, r.PathValue("runID"), true)
}

// DismissRoot and DismissRun never change canonical readiness.
func (h *Handler) DismissRoot(w http.ResponseWriter, r *http.Request) {
	h.presentation(w, r, "", false)
}

func (h *Handler) DismissRun(w http.ResponseWriter, r *http.Request) {
	h.presentation(w, r, r.PathValue("runID"), false)
}

func (h *Handler) presentation(w http.ResponseWriter, r *http.Request, runID string, open bool) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if !noQuery(r) || (runID != "" && !boundedRunID(runID)) {
		h.writeFailure(w, r, "", runID, setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	var body mutationRequest
	if err := decodeRequest(w, r, &body); err != nil || !validMutation(body.IfRevision, body.IdempotencyKey) {
		h.writeFailure(w, r, "", runID, setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if h == nil || h.service == nil {
		h.writeFailure(w, r, userID, runID, setupjourney.FailureFor(setupjourney.ReasonJourneyUnavailable, 0))
		return
	}
	request := setupjourney.PresentationMutation{IfRevision: body.IfRevision, IdempotencyKey: body.IdempotencyKey}
	var projection *setupjourney.JourneyProjection
	var err error
	if open {
		projection, err = h.service.Open(r.Context(), userID, runID, request)
	} else {
		projection, err = h.service.Dismiss(r.Context(), userID, runID, request)
	}
	if err != nil {
		h.writeFailure(w, r, userID, runID, err)
		return
	}
	orihttp.Success(w, journeyResponse{Journey: projection})
}

// CreateChild handles POST /api/personal-assistant/setup-journey/children.
func (h *Handler) CreateChild(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if !noQuery(r) {
		h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	var body mutationRequest
	if err := decodeRequest(w, r, &body); err != nil || !validMutation(body.IfRevision, body.IdempotencyKey) {
		h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if h == nil || h.service == nil {
		h.writeFailure(w, r, userID, "", setupjourney.FailureFor(setupjourney.ReasonJourneyUnavailable, 0))
		return
	}
	projection, err := h.service.CreateOrResumeChild(r.Context(), userID, setupjourney.PresentationMutation{
		IfRevision: body.IfRevision, IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		h.writeFailure(w, r, userID, "", err)
		return
	}
	orihttp.Success(w, journeyResponse{Journey: projection})
}

// Mutate executes exactly one current host-published action for one run.
func (h *Handler) Mutate(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	runID := r.PathValue("runID")
	actionID, knownAction := setupjourney.NormalizeActionID(r.PathValue("actionID"))
	if !noQuery(r) || !boundedRunID(runID) || !knownAction {
		h.writeFailure(w, r, "", runID, setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	var body actionRequest
	if err := decodeRequest(w, r, &body); err != nil || !validMutation(body.IfRevision, body.IdempotencyKey) ||
		!validOptionalToken(body.ReviewToken) || !validActionInput(body.Input) {
		h.writeFailure(w, r, "", runID, setupjourney.FailureFor(setupjourney.ReasonInputInvalid, 0))
		return
	}
	userID, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if h == nil || h.service == nil {
		h.writeFailure(w, r, userID, runID, setupjourney.FailureFor(setupjourney.ReasonJourneyUnavailable, 0))
		return
	}
	if len(body.Input) == 0 {
		body.Input = json.RawMessage(`{}`)
	}
	result, err := h.service.Mutate(r.Context(), userID, runID, actionID, setupjourney.ActionMutation{
		IfRevision: body.IfRevision, IdempotencyKey: body.IdempotencyKey,
		ReviewToken: body.ReviewToken, Input: append(json.RawMessage(nil), body.Input...),
	})
	if err != nil {
		h.writeFailure(w, r, userID, runID, err)
		return
	}
	orihttp.Success(w, result)
}

func (h *Handler) currentUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.provider == nil {
		h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonOwnerUnavailable, 0))
		return "", false
	}
	userID, err := h.provider.CurrentUserID(r.Context())
	if err != nil {
		h.writeFailure(w, r, "", "", setupjourney.FailureFor(setupjourney.ReasonOwnerUnavailable, 0))
		return "", false
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		userID = userprofile.LocalUserID
	}
	return userID, true
}

func (h *Handler) writeFailure(w http.ResponseWriter, r *http.Request, userID, runID string, err error) {
	var publicFailure *setupjourney.Failure
	if !errors.As(err, &publicFailure) {
		publicFailure = setupjourney.FailureFor(setupjourney.ReasonOperationFailed, 0)
	}
	status := statusForReason(publicFailure.ReasonCode)
	var current *setupjourney.JourneyProjection
	if status == http.StatusConflict && h != nil && h.service != nil && userID != "" {
		// A bounded fresh projection is the only conflict detail. If the read
		// fails, omit it rather than exposing or translating that error.
		current, _ = h.service.Read(r.Context(), userID, runID)
	}
	_ = orihttp.RespondJSON(w, status, errorResponse{Error: publicFailure, Current: current})
}

func statusForReason(reason setupjourney.ReasonCode) int {
	switch reason {
	case setupjourney.ReasonInputInvalid:
		return http.StatusBadRequest
	case setupjourney.ReasonRunNotFound, setupjourney.ReasonJourneyUnavailable:
		return http.StatusNotFound
	case setupjourney.ReasonDeclarationInvalid, setupjourney.ReasonRelationshipNotAccepted,
		setupjourney.ReasonRevisionConflict, setupjourney.ReasonIdempotencyConflict,
		setupjourney.ReasonStepNotCurrent, setupjourney.ReasonActionUnavailable,
		setupjourney.ReasonReviewRequired, setupjourney.ReasonReviewStale:
		return http.StatusConflict
	default:
		return http.StatusServiceUnavailable
	}
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain exactly one object")
	}
	return nil
}

func noQuery(r *http.Request) bool {
	return r != nil && r.URL != nil && r.URL.RawQuery == ""
}

func boundedRunID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 64 && !strings.ContainsAny(value, `/\\`)
}

func validMutation(revision int64, key string) bool {
	return revision > 0 && validToken(key, false)
}

func validOptionalToken(value string) bool {
	return validToken(value, true)
}

func validToken(value string, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	if strings.TrimSpace(value) != value || len(value) > setupjourney.MaxIdempotencyKeyBytes ||
		strings.ContainsAny(value, `/\\`) || sensitive.ContainsSecretLikeText(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.IsSpace(char) {
			return false
		}
	}
	return true
}

var forbiddenControlFields = map[string]struct{}{
	"user_id": {}, "owner_user_id": {}, "relationship_id": {},
	"station_id": {}, "home_workspace_id": {}, "workspace_id": {},
	"parent_id": {}, "plugin_source": {}, "source_url": {}, "source": {},
	"adapter": {}, "scope": {}, "declaration": {}, "journey_id": {},
}

func validActionInput(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	if len(raw) > setupjourney.MaxActionInputBytes || !json.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	object, ok := value.(map[string]any)
	return ok && boundedInputValue(object, 0)
}

func boundedInputValue(value any, depth int) bool {
	if depth > 8 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > 64 {
			return false
		}
		for key, child := range typed {
			if len(key) == 0 || len(key) > 64 {
				return false
			}
			if _, forbidden := forbiddenControlFields[strings.ToLower(strings.TrimSpace(key))]; forbidden {
				return false
			}
			if !boundedInputValue(child, depth+1) {
				return false
			}
		}
	case []any:
		if len(typed) > 64 {
			return false
		}
		for _, child := range typed {
			if !boundedInputValue(child, depth+1) {
				return false
			}
		}
	case string:
		return len(typed) <= 4096
	}
	return true
}
