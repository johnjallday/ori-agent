package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
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
	Token             string                             `json:"token"`
	CommitAction      ActionID                           `json:"commit_action"`
	ExpiresAt         time.Time                          `json:"expires_at"`
	Integration       *IntegrationProjection             `json:"integration,omitempty"`
	ProjectConnection *projectconnection.Projection      `json:"project_connection,omitempty"`
	WorkspaceSetup    *WorkspaceSetupProjection          `json:"workspace_setup,omitempty"`
	Staffing          *StaffingProjection                `json:"staffing,omitempty"`
	Group             *projectconnection.HomePreparation `json:"group,omitempty"`
	AccountLink       *AccountLinkProjection             `json:"account_link,omitempty"`
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
	Group               *projectconnection.HomePreparation
	AccountLink         *AccountLinkProjection
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

	ActionReviewCreateGroup      ActionID = "review_create_group"
	ActionCreateGroup            ActionID = "create_group"
	ActionAcknowledgePreparation ActionID = "acknowledge_preparation"
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

	ActionReviewTeam          ActionID = "review_team"
	ActionOpenWorkspace       ActionID = "open_workspace"
	ActionOpenAccountSettings ActionID = "open_account_settings"
	ActionRecheckConnection   ActionID = "recheck_connection"
	ActionReviewMailboxLink   ActionID = "review_mailbox_link"
	ActionLinkMailbox         ActionID = "link_mailbox"
	ActionStartInboxTriage    ActionID = "start_inbox_triage"
	ActionOpenModelSettings   ActionID = "open_model_settings"

	// Install-quest summary offers. Continuing opens the plugin's own quest,
	// named by the summary step's host-resolved handoff.
	ActionContinueIntegrationSetup ActionID = "continue_integration_setup"
	ActionOpenPlugins              ActionID = "open_plugins"
)

var actionDefinitionsByKind = map[specialist.SetupStepKind][]ActionDefinition{
	specialist.SetupStepIntegrationInstall: {
		{ID: ActionReviewInstall, Label: "Review integration", Effect: ActionEffectReview},
		{ID: ActionInstall, Label: "Install integration", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionReviewEnable, Label: "Review enabling", Effect: ActionEffectReview},
		{ID: ActionEnable, Label: "Enable integration", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionReviewUpdate, Label: "Review verified replacement", Effect: ActionEffectReview},
		{ID: ActionUpdate, Label: "Update integration", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionManageIntegration, Label: "Manage integration", Effect: ActionEffectNavigation},
	},
	specialist.SetupStepProjectConnect: {
		{ID: ActionReviewCreateGroup, Label: "Review Group", Effect: ActionEffectReview},
		{ID: ActionCreateGroup, Label: "Create Group", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionAcknowledgePreparation, Label: "Continue to workspace creation", Effect: ActionEffectCommit},
		{ID: ActionReviewExistingProject, Label: "Import Existing Project", Effect: ActionEffectReview},
		{ID: ActionConnectExistingProject, Label: "Connect existing project", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionReviewNewProject, Label: "Create New Project", Effect: ActionEffectReview},
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
		// Account-link shape summary offers. The summary reader selects by shape,
		// so a specialist summary never publishes these.
		{ID: ActionOpenWorkspace, Label: "Open Email Ops", Effect: ActionEffectNavigation},
		{ID: ActionStartInboxTriage, Label: "Start inbox triage", Effect: ActionEffectNavigation},
		{ID: ActionOpenModelSettings, Label: "Set up a model", Effect: ActionEffectNavigation},
		// Integration-install shape summary offers.
		{ID: ActionContinueIntegrationSetup, Label: "Continue setup", Effect: ActionEffectNavigation},
		{ID: ActionOpenPlugins, Label: "Open Plugins", Effect: ActionEffectNavigation},
	},
	specialist.SetupStepWorkspaceCreate: {
		{ID: ActionReviewTeam, Label: "Review your team", Effect: ActionEffectNavigation},
		{ID: ActionOpenWorkspace, Label: "Open workspace", Effect: ActionEffectNavigation},
	},
	specialist.SetupStepAccountConnect: {
		{ID: ActionOpenAccountSettings, Label: "Open Google Account", Effect: ActionEffectNavigation},
		{ID: ActionRecheckConnection, Label: "Check again", Effect: ActionEffectNavigation},
	},
	specialist.SetupStepAccountLink: {
		{ID: ActionReviewMailboxLink, Label: "Review mailbox link", Effect: ActionEffectReview},
		{ID: ActionLinkMailbox, Label: "Link mailbox", Effect: ActionEffectCommit, RequiresReview: true},
		{ID: ActionOpenAccountSettings, Label: "Open Google Account", Effect: ActionEffectNavigation},
	},
}

// ReadScope is the bounded server-derived identity passed to canonical readers.
// Shared root receipts are supplied separately for child runs; no path or
// declaration-selected adapter can enter this value.
type ReadScope struct {
	OwnerUserID string
	// Shape is the compiled step sequence of the declaration being read, so a
	// reader shared by several shapes (the summary) can offer shape-specific
	// actions without inspecting receipts.
	Shape                      specialist.SetupJourneyShape
	QuestSource                QuestSource
	UserTemplateID             string
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
	WorkspaceLaunch            bool
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
	Preparation      *projectconnection.HomePreparation
	WorkspaceCreate  *WorkspaceCreateProjection
	AccountConnect   *AccountConnectProjection
	AccountLink      *AccountLinkProjection
	// Handoff is the quest an install quest's summary continues into. Only a
	// summary read carries it, and only together with its continue action.
	Handoff *QuestHandoffProjection
}

// QuestHandoffProjection names one installed plugin quest by its host-resolved
// key and display title. It carries no route, URL or action handler; the
// browser builds the open request from the key through the host route helper.
type QuestHandoffProjection struct {
	Source   QuestSource `json:"source"`
	PluginID string      `json:"plugin_id"`
	ID       string      `json:"id"`
	Title    string      `json:"title"`
}

func cloneQuestHandoffProjection(source *QuestHandoffProjection) *QuestHandoffProjection {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}

func validQuestHandoffProjection(handoff *QuestHandoffProjection) bool {
	if handoff == nil {
		return true
	}
	key := QuestKey{Source: handoff.Source, PluginID: handoff.PluginID, ID: handoff.ID}
	return handoff.Source == QuestSourcePlugin && validQuestKey(key) && normalizeQuestKey(key) == key &&
		specialist.ValidateSetupJourneyText("handoff title", handoff.Title, specialist.MaxSetupJourneyTitleBytes) == nil
}

// IntegrationHandoff resolves the plugin quest an install quest continues into:
// the first quest, by ID, that the catalog lists for the reviewed integration's
// plugin. It returns nil when none is listed or the catalog cannot be read.
func IntegrationHandoff(ctx context.Context, catalog QuestCatalog, integrationKey string) *QuestHandoffProjection {
	entry, reviewed := reviewedintegration.Get(integrationKey)
	if catalog == nil || !reviewed {
		return nil
	}
	quests, err := catalog.List(ctx)
	if err != nil {
		return nil
	}
	var found *QuestSummary
	for index := range quests {
		quest := normalizeQuestKey(quests[index].QuestKey)
		if quest.Source != QuestSourcePlugin || quest.PluginID != entry.PluginID {
			continue
		}
		if found == nil || quest.ID < found.ID {
			found = &quests[index]
		}
	}
	if found == nil {
		return nil
	}
	handoff := &QuestHandoffProjection{
		Source: QuestSourcePlugin, PluginID: entry.PluginID,
		ID: normalizeQuestKey(found.QuestKey).ID, Title: strings.TrimSpace(found.Title),
	}
	if !validQuestHandoffProjection(handoff) {
		return nil
	}
	return handoff
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

// ReaderRegistry is the closed one-reader-per-compiled-kind host registry.
type ReaderRegistry struct {
	readers map[specialist.SetupStepKind]CanonicalReader
}

// NewReaderRegistry requires exactly one reader for every compiled step kind in
// every shape, so a host build cannot serve a declaration it cannot reconcile.
func NewReaderRegistry(readers map[specialist.SetupStepKind]CanonicalReader) (*ReaderRegistry, error) {
	if len(readers) != len(actionDefinitionsByKind) {
		return nil, errors.New("setup journey reader registry must cover every compiled step kind")
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
	state.Preparation = cloneHomePreparation(state.Preparation)
	state.WorkspaceCreate = cloneWorkspaceCreateProjection(state.WorkspaceCreate)
	state.AccountConnect = cloneAccountConnectProjection(state.AccountConnect)
	state.AccountLink = cloneAccountLinkProjection(state.AccountLink)
	state.Handoff = cloneQuestHandoffProjection(state.Handoff)
	return state
}

func validCanonicalRead(kind specialist.SetupStepKind, state CanonicalStepRead) bool {
	if !validHomePreparation(state.Preparation) || (state.Preparation != nil && kind != specialist.SetupStepProjectConnect) {
		return false
	}
	if state.Complete && state.BlockedReason != "" {
		return false
	}
	if (state.Integration != nil && kind != specialist.SetupStepIntegrationInstall) ||
		!validIntegrationProjection(state.Integration) ||
		(state.WorkspaceSetup != nil && kind != specialist.SetupStepWorkspaceSetup) ||
		!validWorkspaceSetupProjection(state.WorkspaceSetup) ||
		(state.Staffing != nil && kind != specialist.SetupStepAssistantProgramStaffing) ||
		!validStaffingProjection(state.Staffing) ||
		(state.WorkspaceCreate != nil && kind != specialist.SetupStepWorkspaceCreate) ||
		!validWorkspaceCreateProjection(state.WorkspaceCreate) ||
		(state.AccountConnect != nil && kind != specialist.SetupStepAccountConnect) ||
		!validAccountConnectProjection(state.AccountConnect) ||
		(state.AccountLink != nil && kind != specialist.SetupStepAccountLink) ||
		!validAccountLinkProjection(state.AccountLink) ||
		(state.Handoff != nil && kind != specialist.SetupStepSummary) ||
		!validQuestHandoffProjection(state.Handoff) {
		return false
	}
	// The continue action and its target travel together: an offer without a
	// target, or a target without the offer, is not a valid read.
	continues := false
	for _, actionID := range state.AvailableActions {
		continues = continues || actionID == ActionContinueIntegrationSetup
	}
	if continues != (state.Handoff != nil) {
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
	case specialist.SetupStepAssistantProgramStaffing, specialist.SetupStepSummary,
		specialist.SetupStepAccountConnect:
		return result.ChildRunID == "" && result.IntegrationPluginID == "" &&
			result.IntegrationVersion == "" && result.HomeWorkspaceID == "" &&
			result.ProjectWorkspaceID == "" && result.SelectedModeID == "" &&
			(kind != specialist.SetupStepAccountConnect || result.CanonicalReceiptID == "")
	case specialist.SetupStepWorkspaceCreate:
		// The created workspace is the one receipt this shape persists.
		return result.ChildRunID == "" && result.IntegrationPluginID == "" &&
			result.IntegrationVersion == "" && result.HomeWorkspaceID == "" &&
			result.SelectedModeID == "" && result.CanonicalReceiptID == ""
	case specialist.SetupStepAccountLink:
		// No account, credential, or binding identity is ever a journey receipt.
		return result.ChildRunID == "" && result.IntegrationPluginID == "" &&
			result.IntegrationVersion == "" && result.HomeWorkspaceID == "" &&
			result.ProjectWorkspaceID == "" && result.SelectedModeID == "" &&
			result.CanonicalReceiptID == ""
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
