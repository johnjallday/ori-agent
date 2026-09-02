package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
)

// BriefConfigManager is the canonical Daily Brief configuration boundary used
// by working-agreement updates. PAF never stores a duplicate schedule.
type BriefConfigManager interface {
	GetConfig(ctx context.Context, workspaceID string) (*dailybrief.Config, error)
	UpdateConfig(ctx context.Context, cfg dailybrief.Config) (*dailybrief.Config, error)
}

// WorkingAgreementUpdate is a partial, compare-and-swap update. Pointer fields
// distinguish an omitted field from an intentional false/empty value.
type WorkingAgreementUpdate struct {
	IfVersion               int64
	IfConfigRevision        int
	Mandate                 *string
	FocusAreas              *[]string
	Timezone                *string
	ScheduleDays            *[]string
	ScheduleTime            *string
	ScheduleEnabled         *bool
	Scope                   *dailybrief.Scope
	SelectedWorkspaceIDs    *[]string
	IncludeFutureWorkspaces *bool
	NotifyOnReady           *bool
}

// ContinuityService owns working-agreement and proactive-routine lifecycle
// changes. It mutates only PAF relationship fields and canonical Daily Brief
// config; it never touches records, memory, grants, tools, or connections.
type ContinuityService struct {
	store  Store
	hq     PersonalHQReader
	briefs BriefConfigManager
	read   *Service
}

func NewContinuityService(store Store, hq PersonalHQReader, briefs BriefConfigManager, read *Service) *ContinuityService {
	return &ContinuityService{store: store, hq: hq, briefs: briefs, read: read}
}

func (s *ContinuityService) UpdateWorkingAgreement(ctx context.Context, userID string, request WorkingAgreementUpdate) (*Projection, error) {
	state, err := s.mutableState(ctx, userID, request.IfVersion)
	if err != nil {
		return nil, err
	}
	if s.briefs == nil {
		return nil, errors.New("personal assistant: daily brief configuration is unavailable")
	}
	currentConfig, err := s.briefs.GetConfig(ctx, state.HQWorkspaceID)
	if err != nil {
		return nil, err
	}
	if currentConfig == nil || strings.TrimSpace(currentConfig.UserID) != state.UserID ||
		strings.TrimSpace(currentConfig.WorkspaceID) != state.HQWorkspaceID {
		return nil, ErrRepairNeeded
	}
	if request.IfConfigRevision > 0 && request.IfConfigRevision != currentConfig.ConfigRevision {
		return nil, fmt.Errorf("%w: expected daily brief config revision %d", ErrConflict, request.IfConfigRevision)
	}

	original := state.Clone()
	if request.Mandate != nil {
		state.Mandate, err = validateMandate(*request.Mandate)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidation, err)
		}
	}
	if request.FocusAreas != nil {
		state.FocusAreas, err = NormalizeFocusAreas(*request.FocusAreas)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidation, err)
		}
	}

	config := *currentConfig
	config.ScheduleDays = append([]string(nil), currentConfig.ScheduleDays...)
	config.SelectedWorkspaceIDs = append([]string(nil), currentConfig.SelectedWorkspaceIDs...)
	if request.Timezone != nil {
		config.Timezone = strings.TrimSpace(*request.Timezone)
	}
	if request.ScheduleDays != nil {
		config.ScheduleDays = append([]string(nil), (*request.ScheduleDays)...)
	}
	if request.ScheduleTime != nil {
		config.ScheduleTime = strings.TrimSpace(*request.ScheduleTime)
	}
	if request.ScheduleEnabled != nil {
		config.ScheduleEnabled = *request.ScheduleEnabled
	}
	if request.Scope != nil {
		config.Scope = *request.Scope
	}
	if request.SelectedWorkspaceIDs != nil {
		config.SelectedWorkspaceIDs = append([]string(nil), (*request.SelectedWorkspaceIDs)...)
	}
	if request.IncludeFutureWorkspaces != nil {
		config.IncludeFutureWorkspaces = *request.IncludeFutureWorkspaces
	}
	if request.NotifyOnReady != nil {
		config.NotifyOnReady = *request.NotifyOnReady
	}
	if _, err := dailybrief.NormalizeConfig(config); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	updated, err := s.store.UpdateState(ctx, state, request.IfVersion)
	if err != nil {
		return nil, err
	}
	if _, err := s.briefs.UpdateConfig(ctx, config); err != nil {
		// The relationship row is rolled back only while it is provably still the
		// version written above. A concurrent change is never overwritten.
		_, rollbackErr := s.store.UpdateState(ctx, original, updated.StateVersion)
		if rollbackErr != nil {
			return nil, fmt.Errorf("%w: relationship saved but routine update failed; refresh before retrying", ErrRepairNeeded)
		}
		return nil, err
	}
	return s.project(ctx, state.UserID)
}

func (s *ContinuityService) Pause(ctx context.Context, userID string, ifVersion int64) (*Projection, error) {
	return s.setPaused(ctx, userID, ifVersion, true)
}

func (s *ContinuityService) Resume(ctx context.Context, userID string, ifVersion int64) (*Projection, error) {
	return s.setPaused(ctx, userID, ifVersion, false)
}

func (s *ContinuityService) setPaused(ctx context.Context, userID string, ifVersion int64, paused bool) (*Projection, error) {
	state, err := s.mutableState(ctx, userID, ifVersion)
	if err != nil {
		return nil, err
	}
	want := StatusActive
	if paused {
		want = StatusPaused
	}
	if state.Status != want {
		state.Status = want
		updated, updateErr := s.store.UpdateState(ctx, state, ifVersion)
		if updateErr != nil {
			return nil, updateErr
		}
		event := EventResumed
		if paused {
			event = EventPaused
		}
		recordEvent(event, EventData{
			AssistantID: updated.AssistantID, WorkspaceID: updated.HQWorkspaceID, State: string(updated.Status),
		})
	}
	return s.project(ctx, state.UserID)
}

func (s *ContinuityService) mutableState(ctx context.Context, userID string, ifVersion int64) (*State, error) {
	if s == nil || s.store == nil || ifVersion < 1 {
		return nil, fmt.Errorf("%w: a positive current state version is required", ErrConflict)
	}
	state, err := s.store.GetState(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	if state.StateVersion != ifVersion {
		return nil, fmt.Errorf("%w: expected state version %d", ErrConflict, ifVersion)
	}
	if state.Status == StatusAwaitingHQ || state.Status == StatusProvisioningHQ {
		return nil, ErrNeedsHQ
	}
	if state.Status != StatusActive && state.Status != StatusPaused {
		return nil, ErrRepairNeeded
	}
	if state.RenameStep != RenameNone {
		return nil, ErrRepairNeeded
	}
	if s.hq == nil {
		return nil, ErrRepairNeeded
	}
	status, err := s.hq.Status(ctx, state.UserID)
	if err != nil || status == nil || !status.Valid || status.Workspace == nil || status.WorkspaceID != state.HQWorkspaceID {
		return nil, ErrRepairNeeded
	}
	for _, instance := range status.Workspace.AgentInstances {
		if instance.ID == state.HQEntryAgentInstanceID && instance.EntryPoint && strings.EqualFold(instance.Name, state.GlobalAgentProfileName) {
			return state, nil
		}
	}
	return nil, ErrRepairNeeded
}

func (s *ContinuityService) project(ctx context.Context, userID string) (*Projection, error) {
	if s.read == nil {
		return nil, errors.New("personal assistant: read projection is unavailable")
	}
	return s.read.Get(ctx, userID)
}
