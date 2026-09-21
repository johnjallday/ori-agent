package blueprintintake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
)

type ModelProvider struct {
	Name  string
	Local bool
}

type ProviderResolver func(context.Context, string) (ModelProvider, error)

type ConsentRecord struct {
	Provider   string    `json:"provider"`
	Local      bool      `json:"local,omitempty"`
	AcceptedBy string    `json:"accepted_by"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type ConsentStatus struct {
	Statement  string     `json:"statement"`
	Provider   string     `json:"provider"`
	Local      bool       `json:"local"`
	Accepted   bool       `json:"accepted"`
	AcceptedBy string     `json:"accepted_by,omitempty"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
}

func ConsentStatement(provider ModelProvider) string {
	name := strings.TrimSpace(provider.Name)
	if provider.Local {
		return fmt.Sprintf("Ori will read the contents of these files and pages. The text stays on this computer and is handled by %s. Nothing is created until you review it.", name)
	}
	return fmt.Sprintf("Ori will read the contents of these files and pages. The text is sent to %s, this workspace's model provider. Nothing is created until you review it.", name)
}

func (s *SourceService) ConsentStatus(ctx context.Context, workspaceID, intakeKey string) (ConsentStatus, error) {
	requirement, err := s.Requirement(workspaceID, intakeKey)
	if err != nil {
		return ConsentStatus{}, err
	}
	provider, err := s.resolveProvider(ctx, workspaceID)
	if err != nil {
		return ConsentStatus{}, err
	}
	state, _, _, err := s.loadState(workspaceID)
	if err != nil {
		return ConsentStatus{}, err
	}
	status := ConsentStatus{Statement: ConsentStatement(provider), Provider: provider.Name, Local: provider.Local}
	record, exists := state.Consents[requirement.Key]
	if exists && strings.EqualFold(strings.TrimSpace(record.Provider), provider.Name) && record.Local == provider.Local && !record.AcceptedAt.IsZero() {
		acceptedAt := record.AcceptedAt
		status.Accepted = true
		status.AcceptedBy = record.AcceptedBy
		status.AcceptedAt = &acceptedAt
	}
	return status, nil
}

func (s *SourceService) AcceptConsent(ctx context.Context, workspaceID, intakeKey, actor string) (ConsentStatus, error) {
	requirement, err := s.Requirement(workspaceID, intakeKey)
	if err != nil {
		return ConsentStatus{}, err
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return ConsentStatus{}, errors.New("consent actor is required")
	}
	provider, err := s.resolveProvider(ctx, workspaceID)
	if err != nil {
		return ConsentStatus{}, err
	}
	lock := s.lockFor(workspaceID, requirement.Key)
	lock.Lock()
	defer lock.Unlock()
	state, stateDir, _, err := s.loadState(workspaceID)
	if err != nil {
		return ConsentStatus{}, err
	}
	if state.Consents == nil {
		state.Consents = make(map[string]ConsentRecord)
	}
	acceptedAt := s.now().UTC()
	state.Consents[requirement.Key] = ConsentRecord{Provider: provider.Name, Local: provider.Local, AcceptedBy: actor, AcceptedAt: acceptedAt}
	if err := saveSourceState(stateDir, state); err != nil {
		return ConsentStatus{}, err
	}
	return ConsentStatus{
		Statement: ConsentStatement(provider), Provider: provider.Name, Local: provider.Local,
		Accepted: true, AcceptedBy: actor, AcceptedAt: &acceptedAt,
	}, nil
}

func (s *SourceService) resolveProvider(ctx context.Context, workspaceID string) (ModelProvider, error) {
	if s == nil || s.providerResolver == nil {
		return ModelProvider{}, errors.New("workspace model provider is unavailable")
	}
	provider, err := s.providerResolver(ctx, workspaceID)
	if err != nil {
		return ModelProvider{}, err
	}
	provider.Name = strings.TrimSpace(provider.Name)
	if provider.Name == "" {
		return ModelProvider{}, errors.New("workspace model provider is not configured")
	}
	provider.Local = provider.Local || llm.IsLocalProviderName(provider.Name)
	return provider, nil
}

// EvaluateSetup makes consent and at least one captured source authoritative
// prerequisites for completing an intake wizard step.
func (s *SourceService) EvaluateSetup(ctx context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if req.Intake == nil {
		return setupwizard.StepReadiness{}, errors.New("intake requirement is missing from the workspace snapshot")
	}
	sources, err := s.ListSources(req.WorkspaceID, req.Intake.Key)
	if err != nil {
		return setupwizard.StepReadiness{}, err
	}
	if len(sources) == 0 {
		return setupwizard.StepReadiness{Summary: "Add at least one source before continuing.", ErrorCategory: setupwizard.ErrorCategoryNotConfigured}, nil
	}
	consent, err := s.ConsentStatus(ctx, req.WorkspaceID, req.Intake.Key)
	if err != nil {
		return setupwizard.StepReadiness{Blocked: true, Summary: "Ori could not determine this workspace's model provider.", ErrorCategory: setupwizard.ErrorCategoryUnavailable}, nil
	}
	if !consent.Accepted {
		return setupwizard.StepReadiness{Summary: "Review and accept the content-reading statement before continuing.", ErrorCategory: setupwizard.ErrorCategoryPermissionRequired}, nil
	}
	return setupwizard.StepReadiness{Ready: true, Summary: "Sources added and content reading accepted."}, nil
}

func (s *SourceService) ConfirmSetup(ctx context.Context, req setupwizard.StepRequest, _ setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	readiness, err := s.EvaluateSetup(ctx, req)
	if err != nil {
		return readiness, err
	}
	if !readiness.Ready {
		return readiness, fmt.Errorf("%w: %s", setupwizard.ErrStepRejected, readiness.Summary)
	}
	return readiness, nil
}
