package blueprintintake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
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
