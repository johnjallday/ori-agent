package templateonboardinghttp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/templateonboarding"
)

const workspaceEntryAgentNameKey = "entry_agent_name"

type SessionStore interface {
	Load(ctx context.Context, workspaceID string) (*templateonboarding.Session, error)
	Save(ctx context.Context, session *templateonboarding.Session) error
}

type EntryAgentResolver interface {
	EntryAgentName(ctx context.Context, workspaceID string) (string, error)
}

type EntryAgentResolverFunc func(ctx context.Context, workspaceID string) (string, error)

func (f EntryAgentResolverFunc) EntryAgentName(ctx context.Context, workspaceID string) (string, error) {
	if f == nil {
		return "", nil
	}
	return f(ctx, workspaceID)
}

type WorkspaceStoreEntryAgentResolver struct {
	store session.HybridStore
}

func NewWorkspaceStoreEntryAgentResolver(store session.HybridStore) *WorkspaceStoreEntryAgentResolver {
	return &WorkspaceStoreEntryAgentResolver{store: store}
}

func (r *WorkspaceStoreEntryAgentResolver) EntryAgentName(ctx context.Context, workspaceID string) (string, error) {
	if r == nil || r.store == nil {
		return "", nil
	}
	ws, err := r.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, session.ErrWorkspaceNotFound) {
			return "", nil
		}
		return "", err
	}
	return EntryAgentNameFromWorkspace(ws), nil
}

type CompletionRunner interface {
	Complete(ctx context.Context, session *templateonboarding.Session, entryAgentName string) (*templateonboarding.ActionResult, error)
}

type Handler struct {
	store                 SessionStore
	entryAgentResolver    EntryAgentResolver
	completionRunner      CompletionRunner
	modelProviderResolver ModelProviderResolver
	systemModelResolver   SystemModelResolver
}

func NewHandler(store SessionStore, entryAgentResolver EntryAgentResolver) *Handler {
	return &Handler{
		store:              store,
		entryAgentResolver: entryAgentResolver,
	}
}

func (h *Handler) SetCompletionRunner(runner CompletionRunner) {
	if h != nil {
		h.completionRunner = runner
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/template-onboarding", h.GetStatus)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/template-onboarding/values", h.UpdateValues)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/template-onboarding/complete", h.Complete)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/template-onboarding/retry", h.Retry)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/template-onboarding/cancel", h.Cancel)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/template-onboarding/extract", h.Extract)
}

type StatusResponse struct {
	WorkspaceID           string                              `json:"workspace_id"`
	Status                templateonboarding.Status           `json:"status"`
	Fields                []templateonboarding.Field          `json:"fields"`
	Values                map[string]any                      `json:"values"`
	MissingRequiredFields []string                            `json:"missing_required_fields"`
	DependencyBlockers    []string                            `json:"dependency_blockers"`
	EntryAgentName        string                              `json:"entry_agent_name,omitempty"`
	ActionResult          *templateonboarding.ActionResult    `json:"action_result,omitempty"`
	ActionError           string                              `json:"action_error,omitempty"`
	Blockers              []string                            `json:"blockers,omitempty"`
	Completion            templateonboarding.CompletionAction `json:"completion"`
}

type ValidationIssue struct {
	FieldID string `json:"field_id,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type valuesRequest struct {
	Values map[string]any `json:"values"`
}

func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	session, entryAgentName, ok := h.loadFreshSession(w, r)
	if !ok {
		return
	}
	orihttp.WriteJSON(w, h.statusResponse(session, entryAgentName))
}

func (h *Handler) UpdateValues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	session, entryAgentName, ok := h.loadFreshSession(w, r)
	if !ok {
		return
	}

	var req valuesRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.Values == nil {
		_ = orihttp.RespondBadRequest(w, "values is required")
		return
	}

	if ok := h.mergeValidatedValues(w, r, session, entryAgentName, req.Values); !ok {
		return
	}
	orihttp.WriteJSON(w, h.statusResponse(session, entryAgentName))
}

func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	session, entryAgentName, ok := h.loadFreshSession(w, r)
	if !ok {
		return
	}
	if !h.requireEntryAgent(w, entryAgentName) || !h.requireAllRequiredValues(w, session) {
		return
	}
	if ok := h.ensureReadyToComplete(w, r, session, entryAgentName); !ok {
		return
	}
	if h.completionRunner == nil {
		_ = orihttp.RespondNotImplemented(w, "template onboarding completion executor is not available")
		return
	}

	if _, err := h.completionRunner.Complete(r.Context(), session, entryAgentName); err != nil {
		respondExecutionError(w, err)
		return
	}
	orihttp.WriteJSON(w, h.statusResponse(session, entryAgentName))
}

func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	session, entryAgentName, ok := h.loadFreshSession(w, r)
	if !ok {
		return
	}
	if !h.requireEntryAgent(w, entryAgentName) || !h.requireAllRequiredValues(w, session) {
		return
	}
	if session.Status != templateonboarding.StatusFailed {
		_ = orihttp.RespondConflict(w, "retry requires a failed template onboarding session")
		return
	}
	if h.completionRunner == nil {
		_ = orihttp.RespondNotImplemented(w, "template onboarding completion executor is not available")
		return
	}

	if _, err := h.completionRunner.Complete(r.Context(), session, entryAgentName); err != nil {
		respondExecutionError(w, err)
		return
	}
	orihttp.WriteJSON(w, h.statusResponse(session, entryAgentName))
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	session, entryAgentName, ok := h.loadFreshSession(w, r)
	if !ok {
		return
	}
	changed, err := session.Cancel()
	if err != nil {
		respondStateError(w, err)
		return
	}
	if changed {
		if err := h.store.Save(r.Context(), session); err != nil {
			_ = orihttp.RespondInternalError(w, "failed to save template onboarding session")
			return
		}
	}
	orihttp.WriteJSON(w, h.statusResponse(session, entryAgentName))
}

func (h *Handler) loadFreshSession(w http.ResponseWriter, r *http.Request) (*templateonboarding.Session, string, bool) {
	workspaceID := workspaceIDFromRequest(r)
	if workspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace_id is required")
		return nil, "", false
	}
	if h == nil || h.store == nil {
		_ = orihttp.RespondServiceUnavailable(w, "template onboarding store is unavailable")
		return nil, "", false
	}
	session, err := h.store.Load(r.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, templateonboarding.ErrSessionNotFound) {
			_ = orihttp.RespondNotFound(w, "template onboarding session not found")
			return nil, "", false
		}
		_ = orihttp.RespondInternalError(w, "failed to load template onboarding session")
		return nil, "", false
	}

	entryAgentName, err := h.entryAgentName(r.Context(), workspaceID)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "failed to resolve workspace entry agent")
		return nil, "", false
	}
	if changed, err := attachEntryAgentIfPresent(session, entryAgentName); err != nil {
		respondStateError(w, err)
		return nil, "", false
	} else if changed {
		if err := h.store.Save(r.Context(), session); err != nil {
			_ = orihttp.RespondInternalError(w, "failed to save template onboarding session")
			return nil, "", false
		}
	}
	return session, entryAgentName, true
}

func (h *Handler) mergeValidatedValues(w http.ResponseWriter, r *http.Request, session *templateonboarding.Session, entryAgentName string, values map[string]any) bool {
	validated, issues := validateValues(session.Spec.Fields, values)
	if len(issues) > 0 {
		_ = orihttp.RespondValidationError(w, "invalid template onboarding values", issues)
		return false
	}

	changed, err := session.MergeValues(validated)
	if err != nil {
		respondStateError(w, err)
		return false
	}

	if !hasMissingRequiredFields(session) && entryAgentName != "" {
		if readyChanged, err := markReadyAfterValueChange(session); err != nil {
			respondStateError(w, err)
			return false
		} else {
			changed = changed || readyChanged
		}
	}

	if changed {
		if err := h.store.Save(r.Context(), session); err != nil {
			_ = orihttp.RespondInternalError(w, "failed to save template onboarding session")
			return false
		}
	}
	return true
}

func (h *Handler) ensureReadyToComplete(w http.ResponseWriter, r *http.Request, session *templateonboarding.Session, entryAgentName string) bool {
	if session.Status == templateonboarding.StatusReadyToComplete {
		return true
	}
	if session.Status == templateonboarding.StatusCollecting || session.Status == templateonboarding.StatusBlocked {
		changed, err := markReadyAfterValueChange(session)
		if err != nil {
			respondStateError(w, err)
			return false
		}
		if changed {
			if err := h.store.Save(r.Context(), session); err != nil {
				_ = orihttp.RespondInternalError(w, "failed to save template onboarding session")
				return false
			}
		}
		return session.Status == templateonboarding.StatusReadyToComplete && strings.TrimSpace(entryAgentName) != ""
	}
	_ = orihttp.RespondConflict(w, fmt.Sprintf("template onboarding cannot complete from %q", session.Status))
	return false
}

func (h *Handler) requireEntryAgent(w http.ResponseWriter, entryAgentName string) bool {
	if strings.TrimSpace(entryAgentName) == "" {
		_ = orihttp.RespondConflict(w, "workspace entry agent is required before template onboarding can continue")
		return false
	}
	return true
}

func (h *Handler) requireAllRequiredValues(w http.ResponseWriter, session *templateonboarding.Session) bool {
	missing := missingRequiredFieldIDs(session)
	if len(missing) > 0 {
		_ = orihttp.RespondConflict(w, "required template onboarding fields are missing")
		return false
	}
	return true
}

func (h *Handler) entryAgentName(ctx context.Context, workspaceID string) (string, error) {
	if h == nil || h.entryAgentResolver == nil {
		return "", nil
	}
	return h.entryAgentResolver.EntryAgentName(ctx, workspaceID)
}

func (h *Handler) statusResponse(session *templateonboarding.Session, entryAgentName string) StatusResponse {
	values := make(map[string]any, len(session.Values))
	for key, value := range session.Values {
		values[key] = value
	}
	blockers := append([]string(nil), session.Blockers...)
	fields := append([]templateonboarding.Field(nil), session.Spec.Fields...)
	return StatusResponse{
		WorkspaceID:           session.WorkspaceID,
		Status:                session.Status,
		Fields:                fields,
		Values:                values,
		MissingRequiredFields: missingRequiredFieldIDs(session),
		DependencyBlockers:    dependencyBlockers(session),
		EntryAgentName:        strings.TrimSpace(entryAgentName),
		ActionResult:          session.ActionResult,
		ActionError:           session.ActionError,
		Blockers:              blockers,
		Completion:            session.Spec.Completion,
	}
}

func workspaceIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id := strings.TrimSpace(r.PathValue("workspaceID")); id != "" {
		return id
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	if trimmed == r.URL.Path {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[1] != "template-onboarding" {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func attachEntryAgentIfPresent(session *templateonboarding.Session, entryAgentName string) (bool, error) {
	if session == nil || strings.TrimSpace(entryAgentName) == "" || session.Status != templateonboarding.StatusPendingEntryAgent {
		return false, nil
	}
	return session.AttachEntryAgent()
}

func markReadyAfterValueChange(session *templateonboarding.Session) (bool, error) {
	if session == nil {
		return false, templateonboarding.ErrInvalidSession
	}
	if hasMissingRequiredFields(session) {
		return false, nil
	}
	switch session.Status {
	case templateonboarding.StatusCollecting, templateonboarding.StatusBlocked:
		return session.MarkReadyToComplete()
	case templateonboarding.StatusReadyToComplete:
		return false, nil
	default:
		return false, nil
	}
}

func hasMissingRequiredFields(session *templateonboarding.Session) bool {
	return len(missingRequiredFieldIDs(session)) > 0
}

func missingRequiredFieldIDs(session *templateonboarding.Session) []string {
	if session == nil {
		return nil
	}
	missing := make([]string, 0)
	for _, field := range session.Spec.Fields {
		if !field.Required {
			continue
		}
		value, ok := session.Values[field.ID]
		if !ok || isEmptyFieldValue(value, field.Type) {
			missing = append(missing, field.ID)
		}
	}
	return missing
}

func dependencyBlockers(session *templateonboarding.Session) []string {
	if session == nil {
		return nil
	}
	if len(session.Blockers) == 0 {
		return []string{}
	}
	return append([]string(nil), session.Blockers...)
}

func validateValues(fields []templateonboarding.Field, values map[string]any) (map[string]any, []ValidationIssue) {
	fieldByID := make(map[string]templateonboarding.Field, len(fields))
	for _, field := range fields {
		fieldByID[field.ID] = field
	}

	validated := make(map[string]any, len(values))
	var issues []ValidationIssue
	for key, value := range values {
		fieldID := strings.TrimSpace(key)
		field, ok := fieldByID[fieldID]
		if !ok {
			issues = append(issues, ValidationIssue{
				FieldID: fieldID,
				Code:    "unknown_field",
				Message: "unknown onboarding field",
			})
			continue
		}

		coerced, fieldIssues := coerceAndValidateFieldValue(field, value)
		if len(fieldIssues) > 0 {
			issues = append(issues, fieldIssues...)
			continue
		}
		validated[field.ID] = coerced
	}
	return validated, issues
}

func coerceAndValidateFieldValue(field templateonboarding.Field, value any) (any, []ValidationIssue) {
	var issues []ValidationIssue
	if value == nil {
		if field.Required {
			return nil, []ValidationIssue{{FieldID: field.ID, Code: "required", Message: "field is required"}}
		}
		return nil, nil
	}

	var coerced any
	switch field.Type {
	case templateonboarding.FieldString:
		text, ok := value.(string)
		if !ok {
			return nil, []ValidationIssue{typeIssue(field, "string")}
		}
		coerced = text
	case templateonboarding.FieldNumber:
		number, ok := coerceNumber(value)
		if !ok {
			return nil, []ValidationIssue{typeIssue(field, "number")}
		}
		coerced = number
	case templateonboarding.FieldEnum:
		text, ok := value.(string)
		if !ok {
			return nil, []ValidationIssue{typeIssue(field, "enum option")}
		}
		option, ok := canonicalEnumOption(field.Options, text)
		if !ok {
			return nil, []ValidationIssue{{
				FieldID: field.ID,
				Code:    "invalid_enum",
				Message: "field must match one of the configured options",
			}}
		}
		coerced = option
	case templateonboarding.FieldBoolean:
		boolean, ok := coerceBool(value)
		if !ok {
			return nil, []ValidationIssue{typeIssue(field, "boolean")}
		}
		coerced = boolean
	default:
		return nil, []ValidationIssue{{FieldID: field.ID, Code: "invalid_type", Message: "field has an unsupported type"}}
	}

	if field.Required && isEmptyFieldValue(coerced, field.Type) {
		issues = append(issues, ValidationIssue{FieldID: field.ID, Code: "required", Message: "field is required"})
	}
	issues = append(issues, validateFieldConstraints(field, coerced)...)
	if len(issues) > 0 {
		return nil, issues
	}
	return coerced, nil
}

func validateFieldConstraints(field templateonboarding.Field, value any) []ValidationIssue {
	if field.Validation == nil || value == nil {
		return nil
	}
	var issues []ValidationIssue
	switch field.Type {
	case templateonboarding.FieldString, templateonboarding.FieldEnum:
		text, _ := value.(string)
		length := float64(len(text))
		if field.Validation.Min != nil && length < *field.Validation.Min {
			issues = append(issues, ValidationIssue{FieldID: field.ID, Code: "min", Message: "field is shorter than the configured minimum"})
		}
		if field.Validation.Max != nil && length > *field.Validation.Max {
			issues = append(issues, ValidationIssue{FieldID: field.ID, Code: "max", Message: "field is longer than the configured maximum"})
		}
		if pattern := strings.TrimSpace(field.Validation.Pattern); pattern != "" {
			re, err := regexp.Compile(pattern)
			if err == nil && !re.MatchString(text) {
				issues = append(issues, ValidationIssue{FieldID: field.ID, Code: "pattern", Message: "field does not match the configured pattern"})
			}
		}
	case templateonboarding.FieldNumber:
		number, _ := value.(float64)
		if field.Validation.Min != nil && number < *field.Validation.Min {
			issues = append(issues, ValidationIssue{FieldID: field.ID, Code: "min", Message: "field is below the configured minimum"})
		}
		if field.Validation.Max != nil && number > *field.Validation.Max {
			issues = append(issues, ValidationIssue{FieldID: field.ID, Code: "max", Message: "field is above the configured maximum"})
		}
	}
	return issues
}

func typeIssue(field templateonboarding.Field, expected string) ValidationIssue {
	return ValidationIssue{
		FieldID: field.ID,
		Code:    "invalid_type",
		Message: "field must be a " + expected,
	}
}

func isEmptyFieldValue(value any, fieldType templateonboarding.FieldType) bool {
	if value == nil {
		return true
	}
	switch fieldType {
	case templateonboarding.FieldString, templateonboarding.FieldEnum:
		text, ok := value.(string)
		return !ok || strings.TrimSpace(text) == ""
	default:
		return false
	}
}

func coerceNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return finiteNumber(v)
	case float32:
		return finiteNumber(float64(v))
	case int:
		return finiteNumber(float64(v))
	case int8:
		return finiteNumber(float64(v))
	case int16:
		return finiteNumber(float64(v))
	case int32:
		return finiteNumber(float64(v))
	case int64:
		return finiteNumber(float64(v))
	case uint:
		return finiteNumber(float64(v))
	case uint8:
		return finiteNumber(float64(v))
	case uint16:
		return finiteNumber(float64(v))
	case uint32:
		return finiteNumber(float64(v))
	case uint64:
		return finiteNumber(float64(v))
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return finiteNumber(parsed)
	default:
		return 0, false
	}
}

func finiteNumber(v float64) (float64, bool) {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}

func coerceBool(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "y", "1":
			return true, true
		case "false", "no", "n", "0":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func canonicalEnumOption(options []string, value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), trimmed) {
			return strings.TrimSpace(option), true
		}
	}
	return "", false
}

func EntryAgentNameFromWorkspace(workspace *session.Workspace) string {
	if workspace == nil {
		return ""
	}
	if workspace.SharedData != nil {
		if raw, ok := workspace.SharedData[workspaceEntryAgentNameKey]; ok {
			if name := strings.TrimSpace(fmt.Sprint(raw)); name != "" && workspaceHasAgentName(workspace, name) {
				return name
			}
		}
	}
	for _, inst := range workspace.AgentInstances {
		if inst.EntryPoint && strings.TrimSpace(inst.Name) != "" {
			return strings.TrimSpace(inst.Name)
		}
	}
	for _, inst := range workspace.AgentInstances {
		if name := strings.TrimSpace(inst.Name); name != "" {
			return name
		}
	}
	return ""
}

func workspaceHasAgentName(workspace *session.Workspace, agentName string) bool {
	target := strings.TrimSpace(agentName)
	if workspace == nil || target == "" {
		return false
	}
	for _, inst := range workspace.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(inst.Name), target) {
			return true
		}
	}
	return false
}

func respondStateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, templateonboarding.ErrTerminalSession),
		errors.Is(err, templateonboarding.ErrCompletionNotReady),
		errors.Is(err, templateonboarding.ErrSessionRunning),
		errors.Is(err, templateonboarding.ErrSessionMissingInput),
		errors.Is(err, templateonboarding.ErrInvalidTransition):
		_ = orihttp.RespondConflict(w, err.Error())
	default:
		_ = orihttp.RespondInternalError(w, "template onboarding state transition failed")
	}
}

func respondExecutionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, templateonboarding.ErrTerminalSession),
		errors.Is(err, templateonboarding.ErrCompletionNotReady),
		errors.Is(err, templateonboarding.ErrSessionRunning),
		errors.Is(err, templateonboarding.ErrSessionMissingInput),
		errors.Is(err, templateonboarding.ErrInvalidTransition):
		_ = orihttp.RespondConflict(w, err.Error())
	default:
		_ = orihttp.RespondInternalError(w, "failed to complete template onboarding")
	}
}
