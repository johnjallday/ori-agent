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
	APIStateNeedsHire APIState = "needs_hire"
	APIStateHiring    APIState = "hiring"
	// APIStateNeedsHQ is a healthy-but-incomplete relationship: a real assistant
	// was hired and no Personal HQ has been built yet. The named identity is
	// fully readable; HQ-backed sources report not_configured/hq_not_built.
	APIStateNeedsHQ APIState = "needs_hq"
	// APIStateProvisioningHQ is a confirmed HQ setup that is partially applied
	// and resumable through the same HQ request ID.
	APIStateProvisioningHQ APIState = "provisioning_hq"
	APIStateActive         APIState = "active"
	APIStatePaused         APIState = "paused"
	APIStateRepairNeeded   APIState = "repair_needed"
)

// Stable next_action values. Clients switch on these rather than on copy.
const (
	NextActionHire          = "hire"
	NextActionResumeHire    = "resume_hire"
	NextActionBuildHQ       = "build_hq"
	NextActionResumeHQ      = "resume_hq_setup"
	NextActionRepair        = "repair"
	NextActionRetryRename   = "retry_rename"
	NextActionResume        = "resume"
	NextActionAsk           = "ask"
	ReasonHQNotBuilt        = "hq_not_built"
	ReasonHQSetupIncomplete = "hq_setup_incomplete"
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
	profiles   ProfileReader
}

// NewService constructs the read service. Optional sources are reported as
// dependency_error rather than silently appearing healthy.
func NewService(store Store, hq PersonalHQReader, briefs BriefConfigReader, models ModelAvailabilityReader) *Service {
	return &Service{store: store, personalHQ: hq, briefs: briefs, models: models}
}

// WithProfileReader attaches the narrow global-profile provenance seam used to
// validate a hired assistant that has no Personal HQ yet. Before HQ exists
// there is no workspace or entry instance to validate against, so the profile
// marker is the only thing that proves the relationship still owns a real agent.
func (s *Service) WithProfileReader(profiles ProfileReader) *Service {
	if s != nil {
		s.profiles = profiles
	}
	return s
}

// Get projects the current relationship without mutating any dependency.
func (s *Service) Get(ctx context.Context, userID string) (*Projection, error) {
	projection := &Projection{State: APIStateNeedsHire, NextAction: NextActionHire}
	if s != nil {
		projection.Availability.Model = s.modelAvailability()
	}
	if s == nil || s.store == nil {
		return nil, errors.New("personal assistant: relationship store is unavailable")
	}
	state, err := s.store.GetState(ctx, strings.TrimSpace(userID))
	if errors.Is(err, ErrNotFound) {
		projection.State = APIStateNeedsHire
		projection.NextAction = NextActionHire
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
		projection.NextAction = NextActionHire
		projection.Availability.PersonalHQ = notConfiguredSource("not_hired")
		projection.Availability.AgentInstance = notConfiguredSource("not_hired")
		projection.Availability.DailyBrief = notConfiguredSource("not_hired")
		return projection, nil
	case StatusHiring:
		projection.State = APIStateHiring
		projection.NextAction = NextActionResumeHire
		projection.HQWorkspaceID = state.HQWorkspaceID
		projection.HQAgentInstanceID = state.HQEntryAgentInstanceID
		projection.GlobalAgentProfile = state.GlobalAgentProfileName
		projection.Availability.PersonalHQ = notConfiguredSource("hire_incomplete")
		projection.Availability.AgentInstance = notConfiguredSource("hire_incomplete")
		projection.Availability.DailyBrief = notConfiguredSource("hire_incomplete")
		return projection, nil
	case StatusAwaitingHQ, StatusProvisioningHQ:
		// A genuinely hired assistant with no HQ. The identity is real and is
		// reported in full; the HQ-backed sources are honestly not_configured
		// rather than unavailable (which would read as breakage) or healthy-empty
		// (which would be a fabrication).
		return s.projectPreHQ(state, projection)
	case StatusRepairNeeded:
		projection.State = APIStateRepairNeeded
		projection.NextAction = NextActionRepair
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
		projection.NextAction = NextActionRepair
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
		projection.NextAction = NextActionRetryRename
	} else if state.Status == StatusPaused {
		projection.State = APIStatePaused
		projection.NextAction = NextActionResume
	} else {
		projection.State = APIStateActive
		projection.NextAction = NextActionAsk
	}
	s.loadBrief(ctx, userID, state.HQWorkspaceID, projection)
	return projection, nil
}

// projectPreHQ reports a hired assistant that has no Personal HQ yet.
//
// Invariant: this path must never advertise an HQ workspace ID, an entry
// instance ID, or a Daily Brief configuration, because none of them can exist
// before the user confirms Build My HQ. It must also never degrade into
// repair_needed for the ordinary case — only a genuinely missing or foreign
// owned profile does that.
func (s *Service) projectPreHQ(state *State, projection *Projection) (*Projection, error) {
	preHQRepair := func(source func(string) SourceAvailability, reason string) (*Projection, error) {
		projection.State = APIStateRepairNeeded
		projection.NextAction = NextActionRepair
		projection.HQWorkspaceID = ""
		projection.HQAgentInstanceID = ""
		projection.GlobalAgentProfile = ""
		projection.Mandate = ""
		projection.FocusAreas = nil
		projection.Availability.PersonalHQ = source(reason)
		projection.Availability.AgentInstance = source(reason)
		projection.Availability.DailyBrief = source(reason)
		return projection, nil
	}

	if err := state.ValidateStateInvariants(); err != nil {
		// A malformed persisted combination is a bounded repair, not something to
		// smooth over by guessing which half is right.
		return preHQRepair(unavailableSource, "invalid_persisted_state")
	}
	if s.profiles == nil {
		// Without the provenance seam the owned profile cannot be validated, and
		// an unvalidated identity is exactly what this state must never invent.
		return preHQRepair(dependencyErrorSource, "profile_reader_unavailable")
	}
	provenance, ok := s.profiles.PersonalAssistantProfileProvenance(state.GlobalAgentProfileName)
	if !ok {
		return preHQRepair(unavailableSource, "assistant_profile_missing")
	}
	if !provenance.OwnedBy(state.AssistantID) {
		// A same-named profile owned by someone else — or by nobody — is never a
		// fallback. Resolving by name is the failure mode this seam exists to stop.
		return preHQRepair(unavailableSource, "assistant_profile_conflict")
	}

	reason := ReasonHQNotBuilt
	projection.State = APIStateNeedsHQ
	projection.NextAction = NextActionBuildHQ
	if state.Status == StatusProvisioningHQ {
		reason = ReasonHQSetupIncomplete
		projection.State = APIStateProvisioningHQ
		projection.NextAction = NextActionResumeHQ
	}

	// The hired identity is validated, so it is returned in full. The profile's
	// current name comes from the store rather than the PAF row so a rename
	// outside PAF is reported truthfully instead of as a stale duplicate.
	projection.GlobalAgentProfile = provenance.Name
	projection.Mandate = state.Mandate
	projection.FocusAreas = append([]FocusArea(nil), state.FocusAreas...)
	projection.Availability.PersonalHQ = notConfiguredSource(reason)
	projection.Availability.AgentInstance = notConfiguredSource(reason)
	projection.Availability.DailyBrief = notConfiguredSource(reason)
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
