package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

const MaxActionInputBytes = 32 << 10

// ActionID is one closed host-owned journey action. It never contains a URL,
// method, adapter key, command, or payload schema.
type ActionID string

// ActionEffect tells the generic shell which host interaction to request.
type ActionEffect string

const (
	ActionEffectReview     ActionEffect = "review"
	ActionEffectCommit     ActionEffect = "commit"
	ActionEffectNavigation ActionEffect = "navigation"
)

// ActionDefinition is safe display metadata compiled into Ori.
type ActionDefinition struct {
	ID             ActionID     `json:"id"`
	Label          string       `json:"label"`
	Effect         ActionEffect `json:"effect"`
	RequiresReview bool         `json:"requires_review,omitempty"`
}

// ActionMutation carries only generic concurrency/consent tokens plus bounded
// action-specific input. The selected action's compiled adapter must strictly
// decode Input; it is never persisted as generic JSON.
type ActionMutation struct {
	IfRevision     int64
	IdempotencyKey string
	ReviewToken    string
	Input          json.RawMessage
}

// ActionResult is the bounded generic result shape. Adapter-specific review
// details are added as typed fields by the adapter task; arbitrary maps are not
// accepted here.
type ActionResult struct {
	Journey *JourneyProjection `json:"setup_journey"`
	Review  *ReviewProjection  `json:"review,omitempty"`
}

// ReviewProjection is an expiring consent boundary. The commit action and
// typed disclosure are server-selected; clients cannot supply owner identity.
type ReviewProjection struct {
	Token             string                        `json:"token"`
	CommitAction      ActionID                      `json:"commit_action"`
	ExpiresAt         time.Time                     `json:"expires_at"`
	Integration       *IntegrationProjection        `json:"integration,omitempty"`
	ProjectConnection *projectconnection.Projection `json:"project_connection,omitempty"`
	WorkspaceSetup    *WorkspaceSetupProjection     `json:"workspace_setup,omitempty"`
	Staffing          *StaffingProjection           `json:"staffing,omitempty"`
}

// ActionReviewMaterial is produced only by one compiled action adapter. Digests
// bind the durable token without storing the response disclosure itself.
type ActionReviewMaterial struct {
	CommitAction        ActionID
	InputDigest         string
	OwnerRevisionDigest string
	DisclosureDigest    string
	Integration         *IntegrationProjection
	ProjectConnection   *projectconnection.Projection
	WorkspaceSetup      *WorkspaceSetupProjection
	Staffing            *StaffingProjection
}

// JourneyActionAdapter is the closed review/commit contract for one setup step
// kind. PrepareCommit is read-only; Commit is called only after the durable
// operation claim and review consumption succeed.
type JourneyActionAdapter interface {
	InputDigest(ActionID, json.RawMessage) (string, error)
	Review(context.Context, ReadScope, ActionID, json.RawMessage) (ActionReviewMaterial, error)
	PrepareCommit(context.Context, ReadScope, ActionID, json.RawMessage) (ActionReviewMaterial, error)
	Commit(context.Context, ReadScope, ActionID, json.RawMessage, ActionReviewMaterial) (CanonicalResult, error)
	ConsequenceObserved(ActionID, CanonicalStepRead) bool
}

const (
	ActionReviewInstall     ActionID = "review_install"
	ActionInstall           ActionID = "install"
	ActionReviewEnable      ActionID = "review_enable"
	ActionEnable            ActionID = "enable"
	ActionReviewUpdate      ActionID = "review_update"
	ActionUpdate            ActionID = "update"
	ActionManageIntegration ActionID = "manage_integration"

	ActionReviewExistingProject  ActionID = "review_existing_project"
	ActionConnectExistingProject ActionID = "connect_existing_project"
	ActionReviewNewProject       ActionID = "review_new_project"
	ActionCreateNewProject       ActionID = "create_new_project"
	ActionOpenProject            ActionID = "open_project"

	ActionOpenWorkspaceSetup    ActionID = "open_workspace_setup"
	ActionRefreshWorkspaceSetup ActionID = "refresh_workspace_setup"
	ActionReviewFileOnlyMode    ActionID = "review_file_only_mode"
	ActionSelectFileOnlyMode    ActionID = "select_file_only_mode"

	ActionReviewHomeStaffing         ActionID = "review_home_staffing"
	ActionAddHomeStaffing            ActionID = "add_home_staffing"
	ActionReviewProjectStaffing      ActionID = "review_project_staffing"
	ActionAddProjectStaffing         ActionID = "add_project_staffing"
	ActionReviewOptionalHomeStaffing ActionID = "review_optional_home_staffing"
	ActionAddOptionalHomeStaffing    ActionID = "add_optional_home_staffing"
	ActionOpenHomeStaffing           ActionID = "open_home_staffing"
	ActionOpenProjectStaffing        ActionID = "open_project_staffing"

	ActionOpenHome               ActionID = "open_home"
	ActionConnectAnotherProject  ActionID = "connect_another_project"
	ActionOpenLiveSetup          ActionID = "open_live_setup"
	ActionOpenSampleLibrarySetup ActionID = "open_sample_library_setup"
	ActionReviewSetup            ActionID = "review_setup"
)

var actionDefinitionsByKind = map[specialist.SetupStepKind][]ActionDefinition{
	specialist.SetupStepIntegrationInstall: {
		{ID: ActionReviewInstall, Label: "Review integration", Effect: ActionEffectReview},
		{ID: ActionInstall, Label: "Install integration", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionReviewEnable, Label: "Review enabling", Effect: ActionEffectReview},
		{ID: ActionEnable, Label: "Enable integration", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionReviewUpdate, Label: "Review update", Effect: ActionEffectReview},
		{ID: ActionUpdate, Label: "Update integration", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionManageIntegration, Label: "Manage integration", Effect: ActionEffectNavigation},
	},
	specialist.SetupStepProjectConnect: {
		{ID: ActionReviewExistingProject, Label: "Review existing project", Effect: ActionEffectReview},
		{ID: ActionConnectExistingProject, Label: "Connect existing project", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionReviewNewProject, Label: "Review new project", Effect: ActionEffectReview},
		{ID: ActionCreateNewProject, Label: "Create new project", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionOpenProject, Label: "Open project", Effect: ActionEffectNavigation},
	},
	specialist.SetupStepWorkspaceSetup: {
		{ID: ActionReviewFileOnlyMode, Label: "Review File-only", Effect: ActionEffectReview},
		{ID: ActionSelectFileOnlyMode, Label: "Use File-only", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionOpenWorkspaceSetup, Label: "Open workspace setup", Effect: ActionEffectNavigation},
		{ID: ActionRefreshWorkspaceSetup, Label: "Refresh setup status", Effect: ActionEffectNavigation},
		{ID: ActionOpenProject, Label: "Open project", Effect: ActionEffectNavigation},
	},
	specialist.SetupStepAssistantProgramStaffing: {
		{ID: ActionReviewHomeStaffing, Label: "Review Home staffing", Effect: ActionEffectReview},
		{ID: ActionAddHomeStaffing, Label: "Add Home staffing", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionReviewProjectStaffing, Label: "Review project staffing", Effect: ActionEffectReview},
		{ID: ActionAddProjectStaffing, Label: "Add project staffing", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionReviewOptionalHomeStaffing, Label: "Review optional Home role", Effect: ActionEffectReview},
		{ID: ActionAddOptionalHomeStaffing, Label: "Add optional Home role", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionOpenHomeStaffing, Label: "Open Home staffing", Effect: ActionEffectNavigation},
		{ID: ActionOpenProjectStaffing, Label: "Open project staffing", Effect: ActionEffectNavigation},
	},
	specialist.SetupStepSummary: {
		{ID: ActionOpenProject, Label: "Open project", Effect: ActionEffectNavigation},
		{ID: ActionOpenHome, Label: "Open Home", Effect: ActionEffectNavigation},
		{ID: ActionConnectAnotherProject, Label: "Connect another project", Effect: ActionEffectCommit},
		{ID: ActionOpenLiveSetup, Label: "Set up live control", Effect: ActionEffectNavigation},
		{ID: ActionOpenSampleLibrarySetup, Label: "Set up sample library", Effect: ActionEffectNavigation},
		{ID: ActionReviewSetup, Label: "Review setup", Effect: ActionEffectNavigation},
	},
}

// ReadScope is the bounded server-derived identity passed to canonical readers.
// Shared root receipts are supplied separately for child runs; no path or
// declaration-selected adapter can enter this value.
type ReadScope struct {
	OwnerUserID                string
	RelationshipID             string
	SpecialistSlug             string
	JourneyID                  string
	IntegrationKey             string
	ExpectedBlueprintID        string
	ExpectedAssistantProgramID string
	RunKind                    RunKind
	RunID                      string
	RootRunID                  string
	IntegrationPluginID        string
	IntegrationVersion         string
	HomeWorkspaceID            string
	ProjectWorkspaceID         string
	SelectedModeID             string
}

// CanonicalStepRead is one read-only owner result. Complete is authoritative
// only for the current read; Result contains bounded resume receipts.
type CanonicalStepRead struct {
	Complete         bool
	BlockedReason    ReasonCode
	AvailableActions []ActionID
	Result           CanonicalResult
	Integration      *IntegrationProjection
	WorkspaceSetup   *WorkspaceSetupProjection
	Staffing         *StaffingProjection
}

// CanonicalReader asks one canonical owner for current state. Implementations
// must not mutate; review/commit adapters are composed separately.
type CanonicalReader interface {
	Read(ctx context.Context, scope ReadScope) (CanonicalStepRead, error)
}

type CanonicalReaderFunc func(context.Context, ReadScope) (CanonicalStepRead, error)

func (fn CanonicalReaderFunc) Read(ctx context.Context, scope ReadScope) (CanonicalStepRead, error) {
	return fn(ctx, scope)
}

// ReaderRegistry is the closed one-reader-per-v1-kind host registry.
type ReaderRegistry struct {
	readers map[specialist.SetupStepKind]CanonicalReader
}

func NewReaderRegistry(readers map[specialist.SetupStepKind]CanonicalReader) (*ReaderRegistry, error) {
	if len(readers) != len(actionDefinitionsByKind) {
		return nil, errors.New("setup journey reader registry must cover every v1 step kind")
	}
	copyReaders := make(map[specialist.SetupStepKind]CanonicalReader, len(readers))
	for kind := range actionDefinitionsByKind {
		reader, ok := readers[kind]
		if !ok || reader == nil {
			return nil, fmt.Errorf("setup journey reader registry is missing kind %q", kind)
		}
		copyReaders[kind] = reader
	}
	for kind := range readers {
		if _, ok := actionDefinitionsByKind[kind]; !ok {
			return nil, fmt.Errorf("setup journey reader registry contains unsupported kind %q", kind)
		}
	}
	return &ReaderRegistry{readers: copyReaders}, nil
}

func (r *ReaderRegistry) read(ctx context.Context, kind specialist.SetupStepKind, scope ReadScope) CanonicalStepRead {
	if r == nil || r.readers[kind] == nil {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}
	}
	state, err := r.readers[kind].Read(ctx, scope)
	if err != nil || !validCanonicalRead(kind, state) {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}
	}
	state.AvailableActions = append([]ActionID(nil), state.AvailableActions...)
	state.Integration = cloneIntegrationProjection(state.Integration)
	state.WorkspaceSetup = cloneWorkspaceSetupProjection(state.WorkspaceSetup)
	state.Staffing = cloneStaffingProjection(state.Staffing)
	return state
}

func validCanonicalRead(kind specialist.SetupStepKind, state CanonicalStepRead) bool {
	if state.Complete && state.BlockedReason != "" {
		return false
	}
	if (state.Integration != nil && kind != specialist.SetupStepIntegrationInstall) ||
		!validIntegrationProjection(state.Integration) ||
		(state.WorkspaceSetup != nil && kind != specialist.SetupStepWorkspaceSetup) ||
		!validWorkspaceSetupProjection(state.WorkspaceSetup) ||
		(state.Staffing != nil && kind != specialist.SetupStepAssistantProgramStaffing) ||
		!validStaffingProjection(state.Staffing) {
		return false
	}
	if !validateReasonCode(state.BlockedReason, true) {
		return false
	}
	if _, _, err := normalizeCanonicalResult(state.Result); err != nil {
		return false
	}
	allowed := make(map[ActionID]struct{}, len(actionDefinitionsByKind[kind]))
	for _, definition := range actionDefinitionsByKind[kind] {
		allowed[definition.ID] = struct{}{}
	}
	seen := make(map[ActionID]struct{}, len(state.AvailableActions))
	for _, actionID := range state.AvailableActions {
		if _, ok := allowed[actionID]; !ok {
			return false
		}
		if _, duplicate := seen[actionID]; duplicate {
			return false
		}
		seen[actionID] = struct{}{}
	}
	return validResultForKind(kind, state.Result)
}

func validResultForKind(kind specialist.SetupStepKind, result CanonicalResult) bool {
	switch kind {
	case specialist.SetupStepIntegrationInstall:
		return result.ChildRunID == "" && result.HomeWorkspaceID == "" &&
			result.ProjectWorkspaceID == "" && result.SelectedModeID == ""
	case specialist.SetupStepProjectConnect:
		return result.ChildRunID == "" && result.IntegrationPluginID == "" &&
			result.IntegrationVersion == "" && result.SelectedModeID == ""
	case specialist.SetupStepWorkspaceSetup:
		return result.ChildRunID == "" && result.IntegrationPluginID == "" &&
			result.IntegrationVersion == "" && result.HomeWorkspaceID == "" &&
			result.ProjectWorkspaceID == ""
	case specialist.SetupStepAssistantProgramStaffing, specialist.SetupStepSummary:
		return result.ChildRunID == "" && result.IntegrationPluginID == "" &&
			result.IntegrationVersion == "" && result.HomeWorkspaceID == "" &&
			result.ProjectWorkspaceID == "" && result.SelectedModeID == ""
	default:
		return false
	}
}

// NormalizeActionID accepts only IDs compiled into the closed v1 registry.
func NormalizeActionID(raw string) (ActionID, bool) {
	candidate := ActionID(strings.ToLower(strings.TrimSpace(raw)))
	for _, definitions := range actionDefinitionsByKind {
		for _, definition := range definitions {
			if definition.ID == candidate {
				return candidate, true
			}
		}
	}
	return "", false
}

func actionProjections(kind specialist.SetupStepKind, available []ActionID) []ActionDefinition {
	if len(available) == 0 {
		return nil
	}
	wanted := make(map[ActionID]struct{}, len(available))
	for _, id := range available {
		wanted[id] = struct{}{}
	}
	definitions := actionDefinitionsByKind[kind]
	result := make([]ActionDefinition, 0, len(available))
	for _, definition := range definitions {
		if _, ok := wanted[definition.ID]; ok {
			result = append(result, definition)
		}
	}
	return result
}
