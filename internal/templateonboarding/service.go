package templateonboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	sessionmodel "github.com/johnjallday/ori-agent/internal/session"
)

const entryAgentNameKey = "entry_agent_name"

// Summary is the API-facing shape returned when a workspace has a
// template-onboarding session.
type Summary struct {
	WorkspaceID     string         `json:"workspace_id"`
	Status          Status         `json:"status"`
	Fields          []Field        `json:"fields,omitempty"`
	Values          map[string]any `json:"values"`
	EntryAgentName  string         `json:"entry_agent_name,omitempty"`
	ActionResult    *ActionResult  `json:"action_result,omitempty"`
	ActionError     string         `json:"action_error,omitempty"`
	Blockers        []string       `json:"blockers,omitempty"`
	ValidationHints []string       `json:"validation_hints,omitempty"`
}

// Service resolves template onboarding specs and creates/resumes sessions.
type Service struct {
	store *Store
}

// StartOptions carries the template metadata needed when completion later
// instantiates the skeleton after intake has finished.
type StartOptions struct {
	TemplateID   string
	TemplatePath string
	ProjectName  string
}

// NewService creates a template-onboarding service.
func NewService(store *Store) *Service {
	return &Service{store: store}
}

// ResolveAndStart parses and validates the template's onboarding block. It
// returns handled=false for templates with no valid onboarding, so callers can
// keep the existing immediate-instantiation path. For valid onboarding specs it
// persists a new session and returns handled=true; callers should then skip
// immediate project instantiation.
func (s *Service) ResolveAndStart(ctx context.Context, ws *sessionmodel.Workspace, tpl projecttemplates.Template, opts ...StartOptions) (*Summary, bool, error) {
	if s == nil || s.store == nil {
		return nil, false, fmt.Errorf("%w: template onboarding store is required", ErrInvalidSession)
	}
	if ws == nil {
		return nil, false, fmt.Errorf("%w: workspace is required", ErrInvalidSession)
	}
	if !tpl.HasOnboarding() {
		return nil, false, nil
	}

	spec, err := ParseSpec(tpl.Onboarding)
	if err != nil || spec == nil {
		return nil, false, nil
	}
	if res := Validate(spec); !res.OK() {
		return nil, false, nil
	}

	status := StatusPendingEntryAgent
	entryAgentName := selectedEntryAgentName(ws)
	if entryAgentName != "" {
		status = StatusCollecting
	}
	session, err := NewSession(ws.ID, spec, status)
	if err != nil {
		return nil, true, err
	}
	if len(opts) > 0 {
		session.TemplateID = strings.TrimSpace(opts[0].TemplateID)
		session.TemplatePath = strings.TrimSpace(opts[0].TemplatePath)
		session.ProjectName = strings.TrimSpace(opts[0].ProjectName)
	}
	if err := s.store.Save(ctx, session); err != nil {
		return NewSummary(session, entryAgentName), true, err
	}
	return NewSummary(session, entryAgentName), true, nil
}

// ResumeForEntryAgent transitions a pending session to collecting once the
// workspace has a selected entry agent. Missing sessions are a no-op.
func (s *Service) ResumeForEntryAgent(ctx context.Context, workspaceID, entryAgentName string) (*Summary, bool, error) {
	if s == nil || s.store == nil {
		return nil, false, nil
	}
	entryAgentName = strings.TrimSpace(entryAgentName)
	if entryAgentName == "" {
		return nil, false, nil
	}
	session, err := s.store.Load(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	changed, err := session.AttachEntryAgent()
	if err != nil {
		return NewSummary(session, entryAgentName), false, err
	}
	if changed {
		if err := s.store.Save(ctx, session); err != nil {
			return NewSummary(session, entryAgentName), false, err
		}
	}
	return NewSummary(session, entryAgentName), changed, nil
}

// NewSummary converts a session into the response shape.
func NewSummary(session *Session, entryAgentName string) *Summary {
	if session == nil {
		return nil
	}
	values := make(map[string]any, len(session.Values))
	for key, value := range session.Values {
		values[key] = cloneJSONValue(value)
	}
	fields := append([]Field(nil), session.Spec.Fields...)
	return &Summary{
		WorkspaceID:    session.WorkspaceID,
		Status:         session.Status,
		Fields:         fields,
		Values:         values,
		EntryAgentName: strings.TrimSpace(entryAgentName),
		ActionResult:   session.ActionResult,
		ActionError:    session.ActionError,
		Blockers:       append([]string(nil), session.Blockers...),
	}
}

func selectedEntryAgentName(ws *sessionmodel.Workspace) string {
	if ws == nil {
		return ""
	}
	if ws.SharedData != nil {
		if raw, ok := ws.SharedData[entryAgentNameKey]; ok {
			if name := strings.TrimSpace(fmt.Sprint(raw)); name != "" && sessionWorkspaceHasAgentName(ws, name) {
				return name
			}
		}
	}
	for _, inst := range ws.AgentInstances {
		if inst.EntryPoint && strings.TrimSpace(inst.Name) != "" {
			return strings.TrimSpace(inst.Name)
		}
	}
	for _, inst := range ws.AgentInstances {
		if name := strings.TrimSpace(inst.Name); name != "" {
			return name
		}
	}
	for _, name := range ws.Agents {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func sessionWorkspaceHasAgentName(ws *sessionmodel.Workspace, agentName string) bool {
	target := strings.TrimSpace(agentName)
	if ws == nil || target == "" {
		return false
	}
	for _, inst := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(inst.Name), target) {
			return true
		}
	}
	for _, name := range ws.Agents {
		if strings.EqualFold(strings.TrimSpace(name), target) {
			return true
		}
	}
	return false
}
