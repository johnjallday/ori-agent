package templateonboarding

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is the persisted lifecycle state for a template-onboarding session.
type Status string

const (
	StatusPendingEntryAgent Status = "pending_entry_agent"
	StatusCollecting        Status = "collecting"
	StatusReadyToComplete   Status = "ready_to_complete"
	StatusRunning           Status = "running"
	StatusBlocked           Status = "blocked"
	StatusFailed            Status = "failed"
	StatusSucceeded         Status = "succeeded"
	StatusCancelled         Status = "cancelled"
)

var (
	ErrInvalidSession      = errors.New("invalid template onboarding session")
	ErrInvalidStatus       = errors.New("invalid template onboarding status")
	ErrInvalidTransition   = errors.New("invalid template onboarding status transition")
	ErrTerminalSession     = errors.New("template onboarding session is terminal")
	ErrCompletionNotReady  = errors.New("template onboarding session is not ready to complete")
	ErrSessionRunning      = errors.New("template onboarding session is running")
	ErrSessionMissingInput = errors.New("template onboarding session missing required input")
)

var nowFunc = time.Now

// ActionResult stores the durable output of a completion action.
type ActionResult struct {
	Result      string         `json:"result,omitempty"`
	RunID       string         `json:"run_id,omitempty"`
	TaskID      string         `json:"task_id,omitempty"`
	ProjectPath string         `json:"project_path,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
}

// Session is the machine-readable source of truth for one workspace's
// template-onboarding run. Spec is a snapshot captured at session creation, so
// later edits to template.json cannot alter an in-flight onboarding.
type Session struct {
	WorkspaceID  string         `json:"workspace_id"`
	TemplateID   string         `json:"template_id,omitempty"`
	TemplatePath string         `json:"template_path,omitempty"`
	ProjectName  string         `json:"project_name,omitempty"`
	ProjectPath  string         `json:"project_path,omitempty"`
	Spec         OnboardingSpec `json:"spec"`
	Values       map[string]any `json:"values,omitempty"`
	Status       Status         `json:"status"`
	ActionResult *ActionResult  `json:"action_result,omitempty"`
	ActionError  string         `json:"action_error,omitempty"`
	Blockers     []string       `json:"blockers,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// NewSession creates a persisted-session value and snapshots the resolved spec.
func NewSession(workspaceID string, spec *OnboardingSpec, status Status) (*Session, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: workspace_id is required", ErrInvalidSession)
	}
	snapshot, err := cloneSpec(spec)
	if err != nil {
		return nil, err
	}
	if status == "" {
		status = StatusCollecting
	}
	if status != StatusPendingEntryAgent && status != StatusCollecting {
		return nil, fmt.Errorf("%w: new sessions must start in %q or %q (got %q)", ErrInvalidStatus, StatusPendingEntryAgent, StatusCollecting, status)
	}
	now := nowFunc()
	return &Session{
		WorkspaceID: workspaceID,
		Spec:        snapshot,
		Values:      make(map[string]any),
		Status:      status,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// IsKnownStatus reports whether status is one of the persisted lifecycle states.
func IsKnownStatus(status Status) bool {
	switch status {
	case StatusPendingEntryAgent, StatusCollecting, StatusReadyToComplete, StatusRunning, StatusBlocked, StatusFailed, StatusSucceeded, StatusCancelled:
		return true
	default:
		return false
	}
}

// IsTerminalStatus reports whether status forbids all future transitions.
func IsTerminalStatus(status Status) bool {
	return status == StatusSucceeded || status == StatusCancelled
}

// IsTerminal reports whether the session forbids all future transitions.
func (s *Session) IsTerminal() bool {
	if s == nil {
		return false
	}
	return IsTerminalStatus(s.Status)
}

// AttachEntryAgent moves a pending session into collection. Calls after the
// session has already advanced are idempotent.
func (s *Session) AttachEntryAgent() (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	if s.Status != StatusPendingEntryAgent {
		if IsTerminalStatus(s.Status) {
			return false, fmt.Errorf("%w: %s", ErrTerminalSession, s.Status)
		}
		return false, nil
	}
	return s.Transition(StatusCollecting)
}

// MarkReadyToComplete marks a session ready for action execution.
func (s *Session) MarkReadyToComplete() (bool, error) {
	return s.Transition(StatusReadyToComplete)
}

// StartCompletion transitions ready/failed sessions into running. Duplicate
// calls while already running are a no-op so double-clicks cannot dispatch twice.
func (s *Session) StartCompletion() (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	switch s.Status {
	case StatusRunning:
		return false, nil
	case StatusReadyToComplete, StatusFailed:
		s.Status = StatusRunning
		s.ActionError = ""
		s.ActionResult = nil
		s.Blockers = nil
		s.touch()
		return true, nil
	case StatusSucceeded, StatusCancelled:
		return false, fmt.Errorf("%w: %s", ErrTerminalSession, s.Status)
	default:
		return false, fmt.Errorf("%w: cannot complete from %q", ErrCompletionNotReady, s.Status)
	}
}

// Retry is an explicit alias for StartCompletion from failed sessions.
func (s *Session) Retry() (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	if s.Status != StatusFailed && s.Status != StatusRunning {
		return false, fmt.Errorf("%w: retry requires %q (got %q)", ErrCompletionNotReady, StatusFailed, s.Status)
	}
	return s.StartCompletion()
}

// MarkSucceeded persists a successful completion result. Repeated calls after
// success are idempotent, but a cancelled session cannot be revived.
func (s *Session) MarkSucceeded(result *ActionResult) (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	if s.Status == StatusSucceeded {
		return false, nil
	}
	if s.Status == StatusCancelled {
		return false, fmt.Errorf("%w: %s", ErrTerminalSession, s.Status)
	}
	if s.Status != StatusRunning {
		return false, fmt.Errorf("%w: success requires %q (got %q)", ErrInvalidTransition, StatusRunning, s.Status)
	}
	s.Status = StatusSucceeded
	if result != nil {
		cloned, err := cloneActionResult(result)
		if err != nil {
			return false, err
		}
		s.ActionResult = cloned
	} else {
		s.ActionResult = nil
	}
	s.ActionError = ""
	s.Blockers = nil
	s.touch()
	return true, nil
}

// MarkFailed persists a retryable completion failure.
func (s *Session) MarkFailed(message string) (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	if IsTerminalStatus(s.Status) {
		return false, fmt.Errorf("%w: %s", ErrTerminalSession, s.Status)
	}
	if s.Status != StatusRunning {
		return false, fmt.Errorf("%w: failure requires %q (got %q)", ErrInvalidTransition, StatusRunning, s.Status)
	}
	s.Status = StatusFailed
	s.ActionError = strings.TrimSpace(message)
	s.ActionResult = nil
	s.Blockers = nil
	s.touch()
	return true, nil
}

// Block records a blocker that must be resolved before completion can continue.
func (s *Session) Block(blockers ...string) (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	if IsTerminalStatus(s.Status) {
		return false, fmt.Errorf("%w: %s", ErrTerminalSession, s.Status)
	}
	if s.Status == StatusBlocked && equalStrings(s.Blockers, cleanStrings(blockers)) {
		return false, nil
	}
	cleaned := cleanStrings(blockers)
	if len(cleaned) == 0 {
		cleaned = []string{"blocked"}
	}
	s.Status = StatusBlocked
	s.Blockers = cleaned
	s.ActionError = strings.Join(cleaned, "; ")
	s.touch()
	return true, nil
}

// Cancel moves a non-terminal, non-running session to cancelled. Running actions
// must finish or fail before cancellation can be persisted.
func (s *Session) Cancel() (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	switch s.Status {
	case StatusCancelled:
		return false, nil
	case StatusSucceeded:
		return false, fmt.Errorf("%w: %s", ErrTerminalSession, s.Status)
	case StatusRunning:
		return false, ErrSessionRunning
	default:
		return s.Transition(StatusCancelled)
	}
}

// MergeValues accepts already-validated values into the collected value set.
func (s *Session) MergeValues(values map[string]any) (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	if IsTerminalStatus(s.Status) {
		return false, fmt.Errorf("%w: %s", ErrTerminalSession, s.Status)
	}
	if s.Status == StatusRunning {
		return false, ErrSessionRunning
	}
	if len(values) == 0 {
		return false, nil
	}
	if s.Values == nil {
		s.Values = make(map[string]any, len(values))
	}
	changed := false
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			return false, fmt.Errorf("%w: value key is required", ErrInvalidSession)
		}
		if old, ok := s.Values[key]; ok && valuesEqual(old, value) {
			continue
		}
		s.Values[key] = cloneJSONValue(value)
		changed = true
	}
	if changed {
		s.touch()
	}
	return changed, nil
}

// Transition performs low-level status movement and enforces the state machine.
func (s *Session) Transition(to Status) (bool, error) {
	if s == nil {
		return false, ErrInvalidSession
	}
	if !IsKnownStatus(to) {
		return false, fmt.Errorf("%w: %q", ErrInvalidStatus, to)
	}
	if s.Status == to {
		return false, nil
	}
	if IsTerminalStatus(s.Status) {
		return false, fmt.Errorf("%w: %s", ErrTerminalSession, s.Status)
	}
	if !canTransition(s.Status, to) {
		return false, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, s.Status, to)
	}
	s.Status = to
	if to != StatusBlocked {
		s.Blockers = nil
	}
	if to != StatusFailed && to != StatusBlocked {
		s.ActionError = ""
	}
	s.touch()
	return true, nil
}

// Clone returns a deep copy of the session.
func (s *Session) Clone() (*Session, error) {
	if s == nil {
		return nil, ErrInvalidSession
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("clone onboarding session: %w", err)
	}
	var cloned Session
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("clone onboarding session: %w", err)
	}
	cloned.normalize()
	return &cloned, nil
}

func canTransition(from, to Status) bool {
	switch from {
	case StatusPendingEntryAgent:
		return to == StatusCollecting || to == StatusCancelled
	case StatusCollecting:
		return to == StatusReadyToComplete || to == StatusBlocked || to == StatusCancelled
	case StatusReadyToComplete:
		return to == StatusRunning || to == StatusBlocked || to == StatusCancelled
	case StatusRunning:
		return to == StatusSucceeded || to == StatusFailed || to == StatusBlocked
	case StatusFailed:
		return to == StatusRunning || to == StatusCancelled
	case StatusBlocked:
		return to == StatusCollecting || to == StatusReadyToComplete || to == StatusCancelled
	default:
		return false
	}
}

func (s *Session) touch() {
	s.UpdatedAt = nowFunc()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
}

func (s *Session) normalize() {
	if s.Values == nil {
		s.Values = make(map[string]any)
	}
}

func cloneSpec(spec *OnboardingSpec) (OnboardingSpec, error) {
	if spec == nil {
		return OnboardingSpec{}, fmt.Errorf("%w: spec is required", ErrInvalidSession)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return OnboardingSpec{}, fmt.Errorf("snapshot onboarding spec: %w", err)
	}
	var cloned OnboardingSpec
	if err := json.Unmarshal(data, &cloned); err != nil {
		return OnboardingSpec{}, fmt.Errorf("snapshot onboarding spec: %w", err)
	}
	return cloned, nil
}

func cloneActionResult(result *ActionResult) (*ActionResult, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("clone action result: %w", err)
	}
	var cloned ActionResult
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("clone action result: %w", err)
	}
	return &cloned, nil
}

func cloneJSONValue(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
}

func valuesEqual(a, b any) bool {
	adata, aerr := json.Marshal(a)
	bdata, berr := json.Marshal(b)
	return aerr == nil && berr == nil && string(adata) == string(bdata)
}

func cleanStrings(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return cleaned
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
