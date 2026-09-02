package personalassistant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/types"
)

// APIState is the bounded read projection exposed to clients.
type APIState string

const (
	APIStateNeedsHire    APIState = "needs_hire"
	APIStateHiring       APIState = "hiring"
	APIStateActive       APIState = "active"
	APIStatePaused       APIState = "paused"
	APIStateRepairNeeded APIState = "repair_needed"
)

// AvailabilityStatus explains one canonical dependency without fabricating an
// empty healthy result.
type AvailabilityStatus string

const (
	AvailabilityAvailable       AvailabilityStatus = "available"
	AvailabilityNotConfigured   AvailabilityStatus = "not_configured"
	AvailabilityUnavailable     AvailabilityStatus = "unavailable"
	AvailabilityDependencyError AvailabilityStatus = "dependency_error"
)

// SourceAvailability is a bounded source-specific capability flag.
type SourceAvailability struct {
	Available bool               `json:"available"`
	Status    AvailabilityStatus `json:"status"`
	Reason    string             `json:"reason,omitempty"`
}

// Availability reports the health of each independent canonical source.
type Availability struct {
	PersonalHQ    SourceAvailability `json:"personal_hq"`
	AgentInstance SourceAvailability `json:"agent_instance"`
	DailyBrief    SourceAvailability `json:"daily_brief"`
	Model         SourceAvailability `json:"model"`
}

// BriefConfigProjection is the bounded Daily Brief configuration included in
// an active relationship read. It contains no generated brief content.
type BriefConfigProjection struct {
	Timezone                string           `json:"timezone"`
	ScheduleDays            []string         `json:"schedule_days"`
	ScheduleTime            string           `json:"schedule_time"`
	ScheduleEnabled         bool             `json:"schedule_enabled"`
	Scope                   dailybrief.Scope `json:"scope"`
	SelectedWorkspaceIDs    []string         `json:"selected_workspace_ids"`
	IncludeFutureWorkspaces bool             `json:"include_future_workspaces"`
	NotifyOnReady           bool             `json:"notify_on_ready"`
	ConfigRevision          int              `json:"config_revision"`
	NextGenerationAt        *time.Time       `json:"next_generation_at,omitempty"`
}

// RenameProjection reports a durable in-progress rename without implying that
// a second assistant was created.
type RenameProjection struct {
	FromName string     `json:"from_name"`
	ToName   string     `json:"to_name"`
	Step     RenameStep `json:"step"`
}

// Projection is the discriminated read model for GET /api/personal-assistant.
type Projection struct {
	State              APIState               `json:"state"`
	StateVersion       int64                  `json:"state_version,omitempty"`
	HireRequestID      string                 `json:"hire_request_id,omitempty"`
	AssistantID        string                 `json:"assistant_id,omitempty"`
	DisplayName        string                 `json:"display_name,omitempty"`
	Appearance         *types.AgentAppearance `json:"appearance,omitempty"`
	HQWorkspaceID      string                 `json:"hq_workspace_id,omitempty"`
	HQAgentInstanceID  string                 `json:"hq_agent_instance_id,omitempty"`
	GlobalAgentProfile string                 `json:"global_agent_profile_name,omitempty"`
	Mandate            string                 `json:"mandate,omitempty"`
	FocusAreas         []FocusArea            `json:"focus_areas,omitempty"`
	SpecialistSlug     string                 `json:"specialist_slug,omitempty"`
	FirstAssignment    FirstAssignmentStatus  `json:"first_assignment_status,omitempty"`
	RepairStep         RepairStep             `json:"repair_step,omitempty"`
	NextAction         string                 `json:"next_action"`
	Availability       Availability           `json:"availability"`
	DailyBrief         *BriefConfigProjection `json:"daily_brief,omitempty"`
	Rename             *RenameProjection      `json:"rename,omitempty"`
}

// PersonalHQReader is implemented by personalhq.Service.
type PersonalHQReader interface {
	Status(ctx context.Context, userID string) (*personalhq.Status, error)
}

// BriefConfigReader is implemented by dailybrief.SQLiteStore.
type BriefConfigReader interface {
	GetConfig(ctx context.Context, workspaceID string) (*dailybrief.Config, error)
}

// ModelAvailabilityReader resolves current model capability without exposing
// provider credentials or binding the domain to the LLM factory.
type ModelAvailabilityReader interface {
	PersonalAssistantModelAvailability() SourceAvailability
}

// Service combines durable PAF state with canonical read-only dependencies.
type Service struct {
	store      Store
	personalHQ PersonalHQReader
	briefs     BriefConfigReader
	models     ModelAvailabilityReader
}

// NewService constructs the read service. Optional sources are reported as
// dependency_error rather than silently appearing healthy.
func NewService(store Store, hq PersonalHQReader, briefs BriefConfigReader, models ModelAvailabilityReader) *Service {
	return &Service{store: store, personalHQ: hq, briefs: briefs, models: models}
}

// Get projects the current relationship without mutating any dependency.
func (s *Service) Get(ctx context.Context, userID string) (*Projection, error) {
	projection := &Projection{State: APIStateNeedsHire, NextAction: "hire"}
	if s != nil {
		projection.Availability.Model = s.modelAvailability()
	}
	if s == nil || s.store == nil {
		return nil, errors.New("personal assistant: relationship store is unavailable")
	}
	state, err := s.store.GetState(ctx, strings.TrimSpace(userID))
	if errors.Is(err, ErrNotFound) {
		projection.State = APIStateNeedsHire
		projection.NextAction = "hire"
		projection.Availability.PersonalHQ = notConfiguredSource("not_hired")
		projection.Availability.AgentInstance = notConfiguredSource("not_hired")
		projection.Availability.DailyBrief = notConfiguredSource("not_hired")
		return projection, nil
	}
	if err != nil {
		return nil, err
	}

	projection.StateVersion = state.StateVersion
	projection.HireRequestID = state.LastHireRequestID
	projection.AssistantID = state.AssistantID
	projection.DisplayName = state.DisplayName
	projection.Appearance = state.Appearance.Clone()
	projection.FirstAssignment = state.FirstAssignmentStatus
	projection.RepairStep = state.RepairStep
	if state.RenameStep != RenameNone {
		projection.Rename = &RenameProjection{FromName: state.RenameFromName, ToName: state.RenameToName, Step: state.RenameStep}
	}

	switch state.Status {
	case StatusNotHired:
		projection.State = APIStateNeedsHire
		projection.NextAction = "hire"
		projection.Availability.PersonalHQ = notConfiguredSource("not_hired")
		projection.Availability.AgentInstance = notConfiguredSource("not_hired")
		projection.Availability.DailyBrief = notConfiguredSource("not_hired")
		return projection, nil
	case StatusHiring:
		projection.State = APIStateHiring
		projection.NextAction = "resume_hire"
		projection.HQWorkspaceID = state.HQWorkspaceID
		projection.HQAgentInstanceID = state.HQEntryAgentInstanceID
		projection.GlobalAgentProfile = state.GlobalAgentProfileName
		projection.Availability.PersonalHQ = notConfiguredSource("hire_incomplete")
		projection.Availability.AgentInstance = notConfiguredSource("hire_incomplete")
		projection.Availability.DailyBrief = notConfiguredSource("hire_incomplete")
		return projection, nil
	case StatusRepairNeeded:
		projection.State = APIStateRepairNeeded
		projection.NextAction = "repair"
		projection.Availability.PersonalHQ = unavailableSource("persisted_repair_state")
		projection.Availability.AgentInstance = unavailableSource("persisted_repair_state")
		projection.Availability.DailyBrief = unavailableSource("persisted_repair_state")
		return projection, nil
	case StatusActive, StatusPaused:
		// Continue below: relationship data is returned only after validating
		// ownership and every stable HQ -> instance link.
	default:
		return nil, errors.New("personal assistant: unsupported persisted relationship status")
	}

	if !s.validateHQ(ctx, userID, state, projection) {
		projection.State = APIStateRepairNeeded
		projection.NextAction = "repair"
		// Do not expose mandate, memory-adjacent focus, or a possibly foreign HQ
		// ID when canonical ownership/linkage does not validate.
		projection.HQWorkspaceID = ""
		projection.HQAgentInstanceID = ""
		projection.GlobalAgentProfile = ""
		projection.Mandate = ""
		projection.FocusAreas = nil
		projection.SpecialistSlug = ""
		return projection, nil
	}

	projection.HQWorkspaceID = state.HQWorkspaceID
	projection.HQAgentInstanceID = state.HQEntryAgentInstanceID
	projection.GlobalAgentProfile = state.GlobalAgentProfileName
	projection.Mandate = state.Mandate
	projection.FocusAreas = append([]FocusArea(nil), state.FocusAreas...)
	projection.SpecialistSlug = state.SpecialistSlug
	if state.RenameStep != RenameNone {
		projection.State = APIStateRepairNeeded
		projection.NextAction = "retry_rename"
	} else if state.Status == StatusPaused {
		projection.State = APIStatePaused
		projection.NextAction = "resume"
	} else {
		projection.State = APIStateActive
		projection.NextAction = "ask"
	}
	s.loadBrief(ctx, userID, state.HQWorkspaceID, projection)
	return projection, nil
}

func (s *Service) validateHQ(ctx context.Context, userID string, state *State, projection *Projection) bool {
	if s.personalHQ == nil {
		projection.Availability.PersonalHQ = dependencyErrorSource("service_unavailable")
		projection.Availability.AgentInstance = dependencyErrorSource("hq_unavailable")
		projection.Availability.DailyBrief = dependencyErrorSource("hq_unavailable")
		return false
	}
	status, err := s.personalHQ.Status(ctx, userID)
	if err != nil {
		projection.Availability.PersonalHQ = dependencyErrorSource("read_failed")
		projection.Availability.AgentInstance = dependencyErrorSource("hq_unavailable")
		projection.Availability.DailyBrief = dependencyErrorSource("hq_unavailable")
		return false
	}
	if status == nil || !status.Valid || status.Workspace == nil ||
		strings.TrimSpace(status.WorkspaceID) != strings.TrimSpace(state.HQWorkspaceID) {
		projection.Availability.PersonalHQ = unavailableSource("link_mismatch")
		projection.Availability.AgentInstance = unavailableSource("hq_unavailable")
		projection.Availability.DailyBrief = unavailableSource("hq_unavailable")
		return false
	}
	projection.Availability.PersonalHQ = availableSource()
	for _, instance := range status.Workspace.AgentInstances {
		if strings.TrimSpace(instance.ID) != strings.TrimSpace(state.HQEntryAgentInstanceID) {
			continue
		}
		if !instance.EntryPoint || !strings.EqualFold(strings.TrimSpace(instance.Name), strings.TrimSpace(state.GlobalAgentProfileName)) {
			projection.Availability.AgentInstance = unavailableSource("entry_identity_mismatch")
			projection.Availability.DailyBrief = unavailableSource("assistant_unavailable")
			return false
		}
		projection.Availability.AgentInstance = availableSource()
		return true
	}
	projection.Availability.AgentInstance = unavailableSource("instance_missing")
	projection.Availability.DailyBrief = unavailableSource("assistant_unavailable")
	return false
}

func (s *Service) loadBrief(ctx context.Context, userID, workspaceID string, projection *Projection) {
	if s.briefs == nil {
		projection.Availability.DailyBrief = dependencyErrorSource("service_unavailable")
		return
	}
	cfg, err := s.briefs.GetConfig(ctx, workspaceID)
	if errors.Is(err, dailybrief.ErrConfigNotFound) {
		projection.Availability.DailyBrief = notConfiguredSource("missing_config")
		return
	}
	if err != nil {
		projection.Availability.DailyBrief = dependencyErrorSource("read_failed")
		return
	}
	if cfg == nil || strings.TrimSpace(cfg.WorkspaceID) != strings.TrimSpace(workspaceID) ||
		strings.TrimSpace(cfg.UserID) != strings.TrimSpace(userID) {
		projection.Availability.DailyBrief = unavailableSource("ownership_mismatch")
		return
	}
	projection.Availability.DailyBrief = availableSource()
	brief := &BriefConfigProjection{
		Timezone: cfg.Timezone, ScheduleDays: append([]string(nil), cfg.ScheduleDays...),
		ScheduleTime: cfg.ScheduleTime, ScheduleEnabled: cfg.ScheduleEnabled,
		Scope: cfg.Scope, SelectedWorkspaceIDs: append([]string(nil), cfg.SelectedWorkspaceIDs...),
		IncludeFutureWorkspaces: cfg.IncludeFutureWorkspaces,
		NotifyOnReady:           cfg.NotifyOnReady, ConfigRevision: cfg.ConfigRevision,
	}
	if next, ok, nextErr := dailybrief.NextOccurrence(*cfg, time.Now()); nextErr == nil && ok {
		brief.NextGenerationAt = &next
	}
	projection.DailyBrief = brief
}

func (s *Service) modelAvailability() SourceAvailability {
	if s.models == nil {
		return dependencyErrorSource("service_unavailable")
	}
	availability := s.models.PersonalAssistantModelAvailability()
	if availability.Status == "" {
		return dependencyErrorSource("invalid_response")
	}
	return availability
}

func availableSource() SourceAvailability {
	return SourceAvailability{Available: true, Status: AvailabilityAvailable}
}

func notConfiguredSource(reason string) SourceAvailability {
	return SourceAvailability{Status: AvailabilityNotConfigured, Reason: reason}
}

func unavailableSource(reason string) SourceAvailability {
	return SourceAvailability{Status: AvailabilityUnavailable, Reason: reason}
}

func dependencyErrorSource(reason string) SourceAvailability {
	return SourceAvailability{Status: AvailabilityDependencyError, Reason: reason}
}
