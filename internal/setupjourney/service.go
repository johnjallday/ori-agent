package setupjourney

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/specialistevents"
)

const (
	maxReconcileCASAttempts     = 4
	MaxOverviewChildProjections = 64
)

// RelationshipReader is the narrow current-user authority used by the journey.
type RelationshipReader interface {
	GetState(ctx context.Context, userID string) (*personalassistant.State, error)
}

// JourneyProjection is the only setup state intended for HTTP/UI consumers.
// It contains no relationship identity, path, manifest, prompt, credential,
// role binding, catalog content, or arbitrary owner error.
type JourneyProjection struct {
	RunID                   string                `json:"run_id"`
	RunKind                 RunKind               `json:"run_kind"`
	RootRunID               string                `json:"root_run_id,omitempty"`
	Journey                 DeclarationProjection `json:"journey"`
	StateRevision           int64                 `json:"state_revision"`
	Lifecycle               LifecycleState        `json:"lifecycle_state"`
	CurrentStepID           string                `json:"current_step_id,omitempty"`
	Dismissed               bool                  `json:"dismissed"`
	Busy                    bool                  `json:"busy,omitempty"`
	ReconciliationRequired  bool                  `json:"reconciliation_required,omitempty"`
	DeclarationIncompatible bool                  `json:"declaration_incompatible,omitempty"`
	// Precondition is set while a project_setup quest's integration is not
	// ready. Every step is then blocked and the install quest is the repair.
	Precondition     *PreconditionProjection `json:"precondition,omitempty"`
	Receipts         ResourceProjection      `json:"receipts"`
	Steps            []StepProjection        `json:"steps"`
	FirstOpenedAt    *time.Time              `json:"first_opened_at,omitempty"`
	LastDismissedAt  *time.Time              `json:"last_dismissed_at,omitempty"`
	FirstCompletedAt *time.Time              `json:"first_completed_at,omitempty"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

// PreconditionProjection explains why a project_setup quest cannot proceed:
// the closed reason and guidance of its integration read, the read's receipt,
// and the host install quest that repairs it. It selects no route or action.
type PreconditionProjection struct {
	ReasonCode     ReasonCode             `json:"reason_code"`
	Guidance       string                 `json:"guidance"`
	Integration    *IntegrationProjection `json:"integration,omitempty"`
	InstallQuestID string                 `json:"install_quest_id"`
}

func clonePreconditionProjection(source *PreconditionProjection) *PreconditionProjection {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Integration = cloneIntegrationProjection(source.Integration)
	return &clone
}

// OverviewProjection is the bounded canonical root/child read used by Home
// reporting. ChildCount remains exact even when the response list is capped.
// Every included run is reconciled through the same owner reads as Read; this
// is not a second setup-status implementation.
type OverviewProjection struct {
	Root       *JourneyProjection   `json:"root"`
	Children   []*JourneyProjection `json:"children"`
	ChildCount int                  `json:"child_count"`
	Truncated  bool                 `json:"truncated,omitempty"`
}

// DeclarationProjection is inert normalized display data from the built-in
// selected declaration. Integration/blueprint/program constraints remain on the
// server and are not client-selectable fields.
type DeclarationProjection struct {
	Source          QuestSource                     `json:"source,omitempty"`
	PluginID        string                          `json:"plugin_id,omitempty"`
	TemplateID      string                          `json:"template_id,omitempty"`
	AttachmentID    string                          `json:"attachment_id,omitempty"`
	ID              string                          `json:"id"`
	SchemaVersion   int                             `json:"schema_version"`
	Version         int                             `json:"version"`
	Title           string                          `json:"title"`
	Description     string                          `json:"description"`
	WorkspaceLaunch *specialist.WorkspaceLaunchCopy `json:"workspace_launch,omitempty"`
}

// ResourceProjection contains bounded historical/resume receipt IDs only.
type ResourceProjection struct {
	IntegrationPluginID string `json:"integration_plugin_id,omitempty"`
	IntegrationVersion  string `json:"integration_version,omitempty"`
	HomeWorkspaceID     string `json:"home_workspace_id,omitempty"`
	ProjectWorkspaceID  string `json:"project_workspace_id,omitempty"`
	SelectedModeID      string `json:"selected_mode_id,omitempty"`
}

// StepProjection combines inert declaration copy with the current canonical
// read and only the closed host actions currently available.
type StepProjection struct {
	ID              string                             `json:"id"`
	Kind            specialist.SetupStepKind           `json:"kind"`
	Title           string                             `json:"title"`
	Description     string                             `json:"description"`
	Status          StepStatus                         `json:"status"`
	ReasonCode      ReasonCode                         `json:"reason_code,omitempty"`
	Guidance        string                             `json:"guidance,omitempty"`
	Actions         []ActionDefinition                 `json:"actions,omitempty"`
	Integration     *IntegrationProjection             `json:"integration,omitempty"`
	WorkspaceSetup  *WorkspaceSetupProjection          `json:"workspace_setup,omitempty"`
	Staffing        *StaffingProjection                `json:"staffing,omitempty"`
	Preparation     *projectconnection.HomePreparation `json:"preparation,omitempty"`
	HomeProvider    *HomeProviderProjection            `json:"home_provider,omitempty"`
	WorkspaceCreate *WorkspaceCreateProjection         `json:"workspace_create,omitempty"`
	AccountConnect  *AccountConnectProjection          `json:"account_connect,omitempty"`
	AccountLink     *AccountLinkProjection             `json:"account_link,omitempty"`
	Handoff         *QuestHandoffProjection            `json:"handoff,omitempty"`
}

// Failure is a safe public service error. Error returns only compiled guidance;
// arbitrary downstream/store text is never retained or exposed.
type Failure struct {
	ReasonCode    ReasonCode `json:"reason_code"`
	Guidance      string     `json:"guidance"`
	StateRevision int64      `json:"state_revision,omitempty"`
}

func (f *Failure) Error() string {
	if f == nil {
		return "setup journey unavailable"
	}
	return f.Guidance
}

// FailureFor constructs one safe public failure from the closed guidance table.
func FailureFor(code ReasonCode, revision int64) *Failure {
	return failure(code, revision)
}

func failure(code ReasonCode, revision int64) *Failure {
	guidance, ok := safeGuidance[code]
	if !ok {
		code = ReasonOperationFailed
		guidance = safeGuidance[code]
	}
	return &Failure{ReasonCode: code, Guidance: guidance, StateRevision: revision}
}

var safeGuidance = map[ReasonCode]string{
	ReasonDeclarationInvalid:          "This setup definition changed and needs a supported upgrade before setup can continue.",
	ReasonJourneyUnavailable:          "This specialist setup is not available in this version of Ori.",
	ReasonRelationshipNotAccepted:     "Accept an available specialist offer before starting this setup.",
	ReasonRunNotFound:                 "This setup run is no longer available. Reopen setup from your assistant.",
	ReasonRevisionConflict:            "Setup changed in another window. Review the refreshed state before continuing.",
	ReasonIdempotencyConflict:         "This request key was already used for a different setup action.",
	ReasonStepNotCurrent:              "Finish or repair the current setup step before starting that action.",
	ReasonActionUnavailable:           "That action is not available for the current setup state.",
	ReasonInputInvalid:                "Review the setup fields and try again.",
	ReasonReviewRequired:              "Review this change before confirming it.",
	ReasonReviewStale:                 "The reviewed details changed. Review the current details again.",
	ReasonOwnerUnavailable:            "Ori could not verify this step right now. Nothing was changed; try again.",
	ReasonOperationFailed:             "Ori could not finish this setup action. The setup remains safe to resume.",
	ReasonIntegrationNotInstalled:     "Review and install the required integration to continue.",
	ReasonIntegrationDisabled:         "Review and enable the required integration to continue.",
	ReasonIntegrationUpdateRequired:   "Review the current integration update before continuing.",
	ReasonIntegrationLocalUnverified:  "Local development copy installed; not release-verified.",
	ReasonIntegrationIdentityMismatch: "Ori could not verify this installation against the reviewed source, format, and version. Open Manage integration to inspect it.",
	ReasonIntegrationUnsupported:      "The reviewed integration is not supported by this Ori installation.",
	ReasonIntegrationReleaseNotReady:  "This Ori version has not enabled a reviewed release of the integration. Update Ori, then check again. Reinstalling the plugin alone will not resolve this.",
	ReasonBlueprintUnavailable:        "The required project blueprint is not currently available.",
	ReasonAssistantProgramMismatch:    "The available assistant program does not match this setup.",
	ReasonProjectSelectionRequired:    "Import an existing project or create a new project to continue.",
	ReasonProjectScopeInvalid:         "The selected project scope could not be verified safely.",
	ReasonProjectAlreadyConnected:     "That project is already connected to another setup.",
	ReasonProjectUnavailable:          "Ori could not verify the connected project.",
	ReasonRuntimeSetupRequired:        "Choose and finish a supported workspace setup mode to continue.",
	ReasonRuntimeNeedsAttention:       "The workspace setup needs attention before it is ready.",
	ReasonHomeUnavailable:             "Ori could not verify the linked Home workspace.",
	ReasonHomeProviderMissing:         "Install the plugin that provides this Home, then check again.",
	ReasonStaffingRequired:            "Review the required Home and project staffing to continue.",
	ReasonStaffingNeedsAttention:      "One or more required roles need attention.",

	ReasonWorkspaceRequired:              "Create the Email Ops workspace to continue.",
	ReasonAccountConnectionNotConfigured: "Google sign-in isn't configured on this Ori server yet. Ask whoever runs it to set up the Google connection, then check again.",
	ReasonAccountReconnectRequired:       "Reconnect Gmail in Settings, then choose Check again.",
	ReasonAccountVaultRepairRequired:     "Repair the vault that holds your email credentials in Settings, then choose Check again.",
	ReasonMailboxLinkRequired:            "Review and confirm linking the connected account to this workspace.",
	ReasonMailboxAccountUnavailable:      "The linked email account is no longer available. Reconnect it in Settings, then check again.",
}

// DeclarationMigration is one compiled exact old-to-new step identity mapping.
// The store resets mapped structural state to unfinished and canonical readers
// decide every completion again after the migration.
type DeclarationMigration struct {
	StepIDMap map[string]string
}

type declarationMigrationKey struct {
	JourneyID              string
	FromSchemaVersion      int
	FromDeclarationVersion int
	ToSchemaVersion        int
	ToDeclarationVersion   int
}

// Migrations are intentionally compiled into Ori. V1 ships with none; adding a
// declaration revision requires an explicit entry and focused migration tests.
var builtInDeclarationMigrations = map[declarationMigrationKey]DeclarationMigration{}

type entryResolver func(slug string) (specialist.Entry, bool)

// Service reconciles bounded journey rows against current relationship and
// canonical owner reads.
type Service struct {
	store              Store
	relationships      RelationshipReader
	readers            *ReaderRegistry
	actionAdapters     map[specialist.SetupStepKind]JourneyActionAdapter
	resolveEntry       entryResolver
	quests             QuestCatalog
	quest              *QuestKey
	userTemplateLocker userTemplateQuestMutationLocker
	migrations         map[declarationMigrationKey]DeclarationMigration
	now                func() time.Time
	// onFirstReady is shared by pointer, so every scoped copy of this service
	// (ForHostQuest and friends copy the struct) sees a hook installed on the
	// original at startup.
	onFirstReady *firstReadyHook
}

// firstReadyHook holds the in-process callback for a journey's first ready.
type firstReadyHook struct {
	mu sync.RWMutex
	fn func(userID string, key QuestKey)
}

// SetOnFirstReady installs the callback fired exactly once per journey root:
// when its FirstCompletedAt goes from unset to set (the first LifecycleReady).
// It never fires on a repair, a regression, or a second ready, and it runs
// after the store write has committed. Journey lifecycle events are otherwise
// log-only; this is the one in-process observer, for the starter missions'
// "Connect one source". Intended for startup wiring.
func (s *Service) SetOnFirstReady(fn func(userID string, key QuestKey)) {
	if s == nil || s.onFirstReady == nil {
		return
	}
	s.onFirstReady.mu.Lock()
	s.onFirstReady.fn = fn
	s.onFirstReady.mu.Unlock()
}

// notifyFirstReady fires the first-ready hook for a committed root transition.
func (s *Service) notifyFirstReady(before, after *Run) {
	if s == nil || s.onFirstReady == nil || before == nil || after == nil {
		return
	}
	if after.Kind != RunKindRoot || before.FirstCompletedAt != nil || after.FirstCompletedAt == nil {
		return
	}
	s.onFirstReady.mu.RLock()
	fn := s.onFirstReady.fn
	s.onFirstReady.mu.RUnlock()
	if fn != nil {
		fn(after.OwnerUserID, s.questKeyForRun(after))
	}
}

// finalizeOperation finalizes an operation receipt together with its run and
// fires the first-ready hook when that write is the one that first made the
// root ready. The stored run is read first so the transition is judged against
// what was persisted, not against the derived candidate (which already carries
// the new completion time). A failed pre-read only suppresses the hook; the
// finalize itself is unchanged.
func (s *Service) finalizeOperation(
	ctx context.Context,
	candidate *Run,
	idempotencyKey string,
	completion OperationCompletion,
) (*OperationReceipt, *Run, bool, error) {
	var before *Run
	if candidate != nil {
		before, _ = s.store.GetRun(ctx, candidate.ID)
	}
	receipt, updated, replayed, err := s.store.FinalizeOperation(ctx, candidate, idempotencyKey, completion)
	if err == nil && !replayed {
		s.notifyFirstReady(before, updated)
	}
	return receipt, updated, replayed, err
}

// questKeyForRun names the quest a root belongs to. A scoped service already
// knows its key; otherwise the root's identity slug says which catalog owns it.
func (s *Service) questKeyForRun(run *Run) QuestKey {
	if s.quest != nil {
		return *s.quest
	}
	switch run.SpecialistSlug {
	case hostQuestSlug:
		return QuestKey{Source: QuestSourceHost, ID: run.JourneyID}
	case userTemplateQuestSlug:
		return QuestKey{Source: QuestSourceUserTemplate, ID: run.JourneyID}
	case pluginQuestSlug:
		// The owner plugin is not on the root row; a consumer that needs it
		// scopes the service first.
		return QuestKey{Source: QuestSourcePlugin, ID: run.JourneyID}
	default:
		return QuestKey{ID: run.JourneyID}
	}
}

func NewService(store Store, relationships RelationshipReader, readers *ReaderRegistry) (*Service, error) {
	return newService(store, relationships, readers, specialist.Get, builtInDeclarationMigrations)
}

func newService(
	store Store,
	relationships RelationshipReader,
	readers *ReaderRegistry,
	resolve entryResolver,
	migrations map[declarationMigrationKey]DeclarationMigration,
) (*Service, error) {
	if store == nil || relationships == nil || readers == nil || resolve == nil {
		return nil, errors.New("setup journey service is not configured")
	}
	migrationCopy := make(map[declarationMigrationKey]DeclarationMigration, len(migrations))
	for key, migration := range migrations {
		mapping := make(map[string]string, len(migration.StepIDMap))
		for from, to := range migration.StepIDMap {
			mapping[from] = to
		}
		migrationCopy[key] = DeclarationMigration{StepIDMap: mapping}
	}
	return &Service{
		store: store, relationships: relationships, readers: readers,
		actionAdapters: make(map[specialist.SetupStepKind]JourneyActionAdapter),
		resolveEntry:   resolve, migrations: migrationCopy, now: time.Now,
		onFirstReady: &firstReadyHook{},
	}, nil
}

// SetActionAdapter installs the single compiled mutation owner for a supported
// step kind. It is intended for startup wiring before the service is served.
func (s *Service) SetActionAdapter(kind specialist.SetupStepKind, adapter JourneyActionAdapter) error {
	if s == nil || adapter == nil {
		return errors.New("setup journey action adapter is not configured")
	}
	if _, supported := actionDefinitionsByKind[kind]; !supported {
		return errors.New("setup journey action adapter kind is unsupported")
	}
	if _, exists := s.actionAdapters[kind]; exists {
		return errors.New("setup journey action adapter kind is already registered")
	}
	s.actionAdapters[kind] = adapter
	return nil
}

// Read resolves the selected plugin quest or accepted assistant alias, creates the inert
// root row when first needed, authorizes an optional child ID through that
// exact root, and reconciles every declared step.
func (s *Service) Read(ctx context.Context, userID, runID string) (*JourneyProjection, error) {
	if s != nil && s.quest != nil && s.quest.Source == QuestSourceUserTemplate {
		if s.userTemplateLocker == nil {
			return nil, failure(ReasonJourneyUnavailable, 0)
		}
		var projection *JourneyProjection
		var readErr error
		if err := s.userTemplateLocker.WithUserSetupQuestMutationLock(ctx, func() error {
			projection, readErr = s.read(ctx, userID, runID)
			return readErr
		}); err != nil {
			if readErr != nil {
				return nil, readErr
			}
			return nil, failure(ReasonJourneyUnavailable, 0)
		}
		return projection, nil
	}
	return s.read(ctx, userID, runID)
}

func (s *Service) read(ctx context.Context, userID, runID string) (*JourneyProjection, error) {
	if s == nil || s.store == nil || s.relationships == nil || s.readers == nil || s.resolveEntry == nil || s.now == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, failure(ReasonRelationshipNotAccepted, 0)
	}
	relationship, declaration, err := s.currentDeclaration(ctx, userID)
	if err != nil {
		return nil, err
	}
	stepIDs := make([]string, len(declaration.Steps))
	for index, step := range declaration.Steps {
		stepIDs[index] = step.ID
	}
	rootSpec := RootSpec{
		OwnerUserID: userID, RelationshipID: relationship.AssistantID,
		SpecialistSlug: relationship.SpecialistSlug, JourneyID: declaration.ID,
		DeclarationSchemaVersion: declaration.SchemaVersion,
		DeclarationVersion:       declaration.Version, StepIDs: stepIDs,
	}
	var root *Run
	var storeErr error
	if relationship.Binding != nil {
		root, _, storeErr = s.store.CreateOrGetUserTemplateRoot(ctx, rootSpec, *relationship.Binding)
	} else {
		root, _, storeErr = s.store.CreateOrGetRoot(ctx, rootSpec)
	}
	if storeErr != nil {
		if errors.Is(storeErr, ErrUserTemplateBindingMismatch) {
			return nil, failure(ReasonDeclarationInvalid, 0)
		}
		return nil, safeStoreFailure(storeErr, 0)
	}

	run := root
	runID = strings.TrimSpace(runID)
	if runID != "" && runID != root.ID {
		run, storeErr = s.store.GetRun(ctx, runID)
		if storeErr != nil || run.Kind != RunKindChild || run.RootRunID != root.ID {
			return nil, failure(ReasonRunNotFound, root.StateRevision)
		}
	}
	projection, err := s.reconcile(ctx, declaration, root, run)
	if err == nil && projection != nil && relationship.QuestKey.Source == QuestSourceUserTemplate {
		projection.Journey.Source = QuestSourceUserTemplate
		projection.Journey.TemplateID = relationship.QuestKey.TemplateID
		projection.Journey.AttachmentID = relationship.QuestKey.AttachmentID
	}
	if err == nil && projection != nil && relationship.QuestKey.Source == QuestSourceHost {
		projection.Journey.Source = QuestSourceHost
		projection.Journey.TemplateID = declaration.ExpectedBlueprintID
	}
	return projection, err
}

// Overview reconciles the accepted relationship's root and a bounded list of
// child runs for deterministic Home reporting. It may create only the same
// inert root row as Read. It never opens a journey, creates a child run, or
// invokes a consequence adapter.
func (s *Service) Overview(ctx context.Context, userID string) (*OverviewProjection, error) {
	root, err := s.Read(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	children, err := s.store.ListChildRuns(ctx, root.RunID)
	if err != nil {
		return nil, safeStoreFailure(err, root.StateRevision)
	}
	result := &OverviewProjection{
		Root: root, ChildCount: len(children),
		Children:  make([]*JourneyProjection, 0, min(len(children), MaxOverviewChildProjections)),
		Truncated: len(children) > MaxOverviewChildProjections,
	}
	for index, child := range children {
		if index >= MaxOverviewChildProjections {
			break
		}
		projection, readErr := s.Read(ctx, userID, child.ID)
		if readErr != nil {
			return nil, readErr
		}
		result.Children = append(result.Children, projection)
	}
	return result, nil
}

func (s *Service) currentDeclaration(ctx context.Context, userID string) (*declarationIdentity, *specialist.SetupJourney, error) {
	if s.quest != nil {
		return s.questDeclaration(ctx, userID, *s.quest)
	}
	state, err := s.relationships.GetState(ctx, userID)
	if err != nil || state == nil || state.UserID != userID ||
		state.SpecialistOfferState != personalassistant.SpecialistOfferAccepted ||
		strings.TrimSpace(state.SpecialistSlug) == "" {
		return nil, nil, failure(ReasonRelationshipNotAccepted, 0)
	}
	if state.Status != personalassistant.StatusActive && state.Status != personalassistant.StatusPaused {
		return nil, nil, failure(ReasonRelationshipNotAccepted, 0)
	}
	entry, ok := s.resolveEntry(state.SpecialistSlug)
	if !ok || entry.Slug != state.SpecialistSlug || strings.TrimSpace(entry.IntegrationKey) == "" || s.quests == nil {
		return nil, nil, failure(ReasonJourneyUnavailable, 0)
	}
	integration, reviewed := reviewedintegration.Get(entry.IntegrationKey)
	if !reviewed {
		return nil, nil, failure(ReasonJourneyUnavailable, 0)
	}
	// FR 20: the plugin's own quest once the catalog lists one for this
	// integration (it lists plugin quests only for an installed plugin);
	// otherwise the generated install quest, which always resolves.
	if handoff := IntegrationHandoff(ctx, s.quests, integration.Key); handoff != nil {
		return s.questDeclaration(ctx, userID, QuestKey{Source: QuestSourcePlugin, PluginID: handoff.PluginID, ID: handoff.ID})
	}
	return s.questDeclaration(ctx, userID, QuestKey{Source: QuestSourceHost, ID: integration.InstallQuestID()})
}

// listQuestSource lists only the catalogs serving one source, so resolving the
// alias never reads the user-template library.
func listQuestSource(ctx context.Context, catalog QuestCatalog, source QuestSource) ([]QuestSummary, error) {
	if catalog == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	if lister, ok := catalog.(questSourceLister); ok {
		return lister.listSource(ctx, source)
	}
	quests, err := catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]QuestSummary, 0, len(quests))
	for _, quest := range quests {
		if normalizeQuestKey(quest.QuestKey).Source == source {
			result = append(result, quest)
		}
	}
	return result, nil
}

// pinAliasQuest resolves the assistant alias once for an operation that reads
// the journey several times. Without it, installing the integration inside an
// action would switch the alias from the install quest to the plugin quest
// between the claim and the final read.
func (s *Service) pinAliasQuest(ctx context.Context, userID string) (*Service, error) {
	if s == nil || s.quest != nil || s.quests == nil || s.relationships == nil || s.resolveEntry == nil {
		return s, nil
	}
	identity, _, err := s.currentDeclaration(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	if identity.QuestKey.ID == "" {
		return s, nil
	}
	pinned := *s
	key := identity.QuestKey
	pinned.quest = &key
	return &pinned, nil
}

func (s *Service) reconcile(ctx context.Context, declaration *specialist.SetupJourney, root, initial *Run) (*JourneyProjection, error) {
	run := initial.Clone()
	for attempt := 0; attempt < maxReconcileCASAttempts; attempt++ {
		if run.Kind == RunKindChild {
			migratedRoot, rootCompatible, rootErr := s.migrateDeclaration(ctx, declaration, root)
			if rootErr != nil {
				return nil, safeStoreFailure(rootErr, root.StateRevision)
			}
			if !rootCompatible {
				return incompatibleProjection(declaration, run), nil
			}
			root = migratedRoot
		}
		migrated, compatible, err := s.migrateDeclaration(ctx, declaration, run)
		if err != nil {
			if failureValue, ok := err.(*Failure); ok {
				return nil, failureValue
			}
			return nil, safeStoreFailure(err, run.StateRevision)
		}
		if !compatible {
			return incompatibleProjection(declaration, run), nil
		}
		run = migrated

		// Child reads always use the latest shared root receipts. They never copy
		// those receipts into the child row.
		if run.Kind == RunKindChild {
			latestRoot, getErr := s.store.GetRun(ctx, run.RootRunID)
			if getErr != nil || latestRoot.Kind != RunKindRoot || latestRoot.ID != root.ID {
				return nil, failure(ReasonRunNotFound, run.StateRevision)
			}
			root = latestRoot
		} else {
			root = run
		}

		busy, busyErr := s.store.GetBusyOperationReceipt(ctx, run.Kind, run.ID)
		if busyErr != nil && !errors.Is(busyErr, ErrNotFound) {
			return nil, safeStoreFailure(busyErr, run.StateRevision)
		}
		candidate, reads, precondition := s.deriveCanonical(ctx, declaration, root, run, busy)
		if busy != nil {
			// An interrupted response must not strand an observed consequence.
			// Only finish the durable receipt from the owner's observed result;
			// never re-execute a mutation from a read or settle an active claim.
			if busy.Status == OperationReconcileRequired {
				kind, definition, known := actionKindAndDefinition(ActionID(busy.ActionID))
				adapter := s.actionAdapters[kind]
				// Group/project connection, File-only, and the mailbox link have
				// unambiguous observed consequences: each is one idempotent write whose
				// presence the owner read shows directly. Do not generalize this to
				// staffing or plugin actions.
				recoverable := kind == specialist.SetupStepProjectConnect ||
					ActionID(busy.ActionID) == ActionSelectFileOnlyMode || ActionID(busy.ActionID) == ActionLinkMailbox
				if recoverable && known && definition.Effect == ActionEffectCommit && adapter != nil {
					settled, settledReads, settledPrecondition := s.deriveCanonical(ctx, declaration, root, run, nil)
					for index, step := range declaration.Steps {
						if step.ID != busy.StepID || step.Kind != kind || !validCanonicalRead(kind, settledReads[index]) ||
							!adapter.ConsequenceObserved(ActionID(busy.ActionID), settledReads[index]) {
							continue
						}
						if claimErr := s.claimUserTemplateResultRoots(ctx, root.OwnerUserID, settledReads[index].Result); claimErr != nil {
							return nil, failure(ReasonProjectAlreadyConnected, run.StateRevision)
						}
						_, updated, replayed, finalizeErr := s.store.FinalizeOperation(ctx, settled, busy.IdempotencyKey, OperationCompletion{
							Status: OperationSucceeded, ResultCode: ResultAlreadyCurrent, Result: settledReads[index].Result,
						})
						if finalizeErr != nil {
							return nil, safeStoreFailure(finalizeErr, run.StateRevision)
						}
						projection := projectionFromRun(declaration, updated, settledReads, nil, settledPrecondition)
						if !replayed {
							emitActionOutcome(projection, step.ID, ActionID(busy.ActionID), specialistevents.OutcomeSucceeded, "")
							emitLifecycleTransition(declaration, run, updated)
							s.notifyFirstReady(run, updated)
						}
						return projection, nil
					}
				}
			}
			return projectionFromRun(declaration, candidate, reads, busy, precondition), nil
		}
		if !materialRunChange(run, candidate) {
			return projectionFromRun(declaration, run, reads, nil, precondition), nil
		}
		updated, updateErr := s.store.CompareAndSwapRun(ctx, candidate, run.StateRevision)
		if updateErr == nil {
			emitLifecycleTransition(declaration, run, updated)
			s.notifyFirstReady(run, updated)
			return projectionFromRun(declaration, updated, reads, nil, precondition), nil
		}
		if errors.Is(updateErr, ErrConflict) {
			run, updateErr = s.store.GetRun(ctx, run.ID)
			if updateErr != nil {
				return nil, safeStoreFailure(updateErr, run.StateRevision)
			}
			continue
		}
		if errors.Is(updateErr, ErrOperationBusy) {
			run, updateErr = s.store.GetRun(ctx, run.ID)
			if updateErr != nil {
				return nil, safeStoreFailure(updateErr, candidate.StateRevision)
			}
			continue
		}
		return nil, safeStoreFailure(updateErr, run.StateRevision)
	}
	return nil, failure(ReasonRevisionConflict, run.StateRevision)
}

func (s *Service) deriveCanonical(
	ctx context.Context,
	declaration *specialist.SetupJourney,
	root *Run,
	current *Run,
	busy *OperationReceipt,
) (*Run, []CanonicalStepRead, *PreconditionProjection) {
	candidate := current.Clone()
	candidate.NeedsNormalization = false
	candidate.DeclarationSchemaVersion = declaration.SchemaVersion
	candidate.DeclarationVersion = declaration.Version
	candidate.StepStates = make([]StepState, len(declaration.Steps))
	reads := make([]CanonicalStepRead, len(declaration.Steps))

	scope := scopeForRun(declaration, root, candidate)
	// FR 18: a project_setup quest has no install step, so the integration read
	// runs first as a precondition. It fills the same receipts the install step
	// filled, and while it is not met no step reader runs.
	var integration *CanonicalStepRead
	if declaration.Shape() == specialist.SetupJourneyShapeProjectSetup {
		read := s.readers.read(ctx, specialist.SetupStepIntegrationInstall, scope)
		if !read.Complete {
			return blockedByPrecondition(declaration, candidate, reads, read)
		}
		applyCanonicalRead(candidate, specialist.SetupStepIntegrationInstall, read.Result)
		integration = &read
	}
	withIntegration := func(scope ReadScope) ReadScope {
		// A child run never stores integration receipts; its readers use the
		// ones this read just verified, even before the root row is updated.
		if integration != nil && integration.Result.IntegrationPluginID != "" {
			scope.IntegrationPluginID = integration.Result.IntegrationPluginID
			scope.IntegrationVersion = integration.Result.IntegrationVersion
		}
		return scope
	}
	scope = withIntegration(scopeForRun(declaration, root, candidate))
	for index, step := range declaration.Steps {
		read := s.readers.read(ctx, step.Kind, scope)
		reads[index] = read
		status := StepPending
		if read.Complete {
			status = StepComplete
		} else if read.BlockedReason != "" {
			status = StepBlocked
		}
		candidate.StepStates[index] = StepState{StepID: step.ID, Status: status, ReasonCode: read.BlockedReason}
		applyCanonicalRead(candidate, step.Kind, read.Result)
		scope = withIntegration(scopeForRun(declaration, root, candidate))
	}

	// Summary completion is owned by this read-only reconciler, not by an
	// adapter assertion. All consequence steps retain independent canonical
	// results even when an earlier shared prerequisite regresses.
	allConsequencesComplete := true
	for index := 0; index < len(candidate.StepStates)-1; index++ {
		if candidate.StepStates[index].Status != StepComplete {
			allConsequencesComplete = false
		}
	}
	last := len(candidate.StepStates) - 1
	if allConsequencesComplete {
		candidate.StepStates[last] = StepState{StepID: candidate.StepStates[last].StepID, Status: StepComplete}
	} else {
		candidate.StepStates[last] = StepState{StepID: candidate.StepStates[last].StepID, Status: StepPending}
	}

	firstUnresolved := -1
	for index, state := range candidate.StepStates {
		if state.Status != StepComplete && state.Status != StepOptionalSkipped {
			firstUnresolved = index
			break
		}
	}
	for index := range candidate.StepStates {
		if index == firstUnresolved || candidate.StepStates[index].Status == StepComplete {
			continue
		}
		candidate.StepStates[index].Status = StepPending
		candidate.StepStates[index].ReasonCode = ""
	}

	canonicalConsequence := candidate.IntegrationPluginID != "" || candidate.HomeWorkspaceID != "" ||
		candidate.ProjectWorkspaceID != "" || candidate.SelectedModeID != ""
	for index := 0; index < len(candidate.StepStates)-1; index++ {
		canonicalConsequence = canonicalConsequence || candidate.StepStates[index].Status == StepComplete
	}
	if firstUnresolved == -1 {
		candidate.Lifecycle = LifecycleReady
		candidate.CurrentStepID = ""
		if candidate.FirstCompletedAt == nil {
			now := s.now().UTC()
			candidate.FirstCompletedAt = &now
		}
	} else {
		candidate.CurrentStepID = candidate.StepStates[firstUnresolved].StepID
		if candidate.StepStates[firstUnresolved].Status == StepPending {
			if len(reads[firstUnresolved].AvailableActions) > 0 {
				candidate.StepStates[firstUnresolved].Status = StepActive
			} else {
				candidate.StepStates[firstUnresolved].Status = StepBlocked
				candidate.StepStates[firstUnresolved].ReasonCode = ReasonActionUnavailable
			}
		}
		priorReady := candidate.FirstCompletedAt != nil
		requiresRepair := priorReady || candidate.StepStates[firstUnresolved].Status == StepBlocked ||
			(busy != nil && busy.Status == OperationReconcileRequired)
		switch {
		case requiresRepair:
			candidate.Lifecycle = LifecycleNeedsAttention
		case candidate.FirstOpenedAt == nil && !canonicalConsequence:
			candidate.Lifecycle = LifecycleNotStarted
		default:
			candidate.Lifecycle = LifecycleInProgress
		}
	}
	return candidate, reads, nil
}

// blockedByPrecondition is the derivation for a project_setup run whose
// integration read is not complete: every step is blocked with the
// integration's reason, the run needs attention, and the projection names the
// install quest that repairs it. Nothing is read or changed beyond that.
func blockedByPrecondition(
	declaration *specialist.SetupJourney,
	candidate *Run,
	reads []CanonicalStepRead,
	integration CanonicalStepRead,
) (*Run, []CanonicalStepRead, *PreconditionProjection) {
	reason := integration.BlockedReason
	if reason == "" {
		reason = ReasonIntegrationNotInstalled
	}
	for index, step := range declaration.Steps {
		candidate.StepStates[index] = StepState{StepID: step.ID, Status: StepBlocked, ReasonCode: reason}
	}
	candidate.CurrentStepID = declaration.Steps[0].ID
	candidate.Lifecycle = LifecycleNeedsAttention
	return candidate, reads, &PreconditionProjection{
		ReasonCode: reason, Guidance: safeGuidance[reason],
		Integration:    cloneIntegrationProjection(integration.Integration),
		InstallQuestID: IntegrationInstallQuestID(declaration.IntegrationKey),
	}
}

func scopeForRun(declaration *specialist.SetupJourney, root, run *Run) ReadScope {
	scope := ReadScope{
		OwnerUserID: root.OwnerUserID, Shape: declaration.Shape(),
		RelationshipID: root.RelationshipID, WorkspaceLaunch: declaration.WorkspaceLaunch != nil,
		SpecialistSlug: root.SpecialistSlug, JourneyID: run.JourneyID,
		IntegrationKey:             declaration.IntegrationKey,
		ExpectedBlueprintID:        declaration.ExpectedBlueprintID,
		ExpectedAssistantProgramID: declaration.ExpectedAssistantProgramID,
		RunKind:                    run.Kind, RunID: run.ID, RootRunID: root.ID,
		IntegrationPluginID: root.IntegrationPluginID, IntegrationVersion: root.IntegrationVersion,
		HomeWorkspaceID: root.HomeWorkspaceID, ProjectWorkspaceID: run.ProjectWorkspaceID,
		SelectedModeID: run.SelectedModeID,
	}
	switch root.SpecialistSlug {
	case userTemplateQuestSlug:
		scope.QuestSource = QuestSourceUserTemplate
		scope.UserTemplateID = declaration.ExpectedBlueprintID
	case hostQuestSlug:
		scope.QuestSource = QuestSourceHost
	}
	if run.Kind == RunKindRoot {
		scope.ProjectWorkspaceID = root.ProjectWorkspaceID
		scope.SelectedModeID = root.SelectedModeID
		if scope.Shape == specialist.SetupJourneyShapeAccountLink {
			// The account-link shape's later steps all act on the workspace step 1
			// just resolved. During reconciliation run is the fresh candidate, so
			// read its receipt instead of the not-yet-persisted root row; otherwise
			// the link step would read an empty or stale workspace for one read.
			scope.ProjectWorkspaceID = run.ProjectWorkspaceID
		}
	}
	return scope
}

func applyCanonicalRead(run *Run, kind specialist.SetupStepKind, result CanonicalResult) {
	switch kind {
	case specialist.SetupStepIntegrationInstall:
		if run.Kind == RunKindRoot {
			if result.IntegrationPluginID != "" {
				run.IntegrationPluginID = result.IntegrationPluginID
			}
			if result.IntegrationVersion != "" {
				run.IntegrationVersion = result.IntegrationVersion
			}
		}
	case specialist.SetupStepProjectConnect:
		if run.Kind == RunKindRoot && result.HomeWorkspaceID != "" {
			run.HomeWorkspaceID = result.HomeWorkspaceID
		}
		if result.ProjectWorkspaceID != "" {
			run.ProjectWorkspaceID = result.ProjectWorkspaceID
		}
	case specialist.SetupStepWorkspaceSetup:
		if result.SelectedModeID != "" {
			run.SelectedModeID = result.SelectedModeID
		}
	case specialist.SetupStepWorkspaceCreate:
		// The account-link shape's created workspace reuses the existing target
		// workspace receipt column; later steps read it back through the scope.
		if result.ProjectWorkspaceID != "" {
			run.ProjectWorkspaceID = result.ProjectWorkspaceID
		}
	}
}

func (s *Service) migrateDeclaration(ctx context.Context, declaration *specialist.SetupJourney, run *Run) (*Run, bool, error) {
	if run.DeclarationSchemaVersion == declaration.SchemaVersion && run.DeclarationVersion == declaration.Version {
		return run, true, nil
	}
	key := declarationMigrationKey{
		JourneyID:              declaration.ID,
		FromSchemaVersion:      run.DeclarationSchemaVersion,
		FromDeclarationVersion: run.DeclarationVersion,
		ToSchemaVersion:        declaration.SchemaVersion,
		ToDeclarationVersion:   declaration.Version,
	}
	migration, ok := s.migrations[key]
	if !ok || !validDeclarationMigration(migration, run, declaration) {
		return run, false, nil
	}
	targetStepIDs := make([]string, len(declaration.Steps))
	for index, step := range declaration.Steps {
		targetStepIDs[index] = step.ID
	}
	digest := declarationMigrationDigest(key, migration)
	migrated, _, err := s.store.ApplyDeclarationMigration(
		ctx, run.Kind, run.ID, run.StateRevision, declaration.SchemaVersion,
		declaration.Version, targetStepIDs, digest,
	)
	if err != nil {
		return nil, false, err
	}
	return migrated, true, nil
}

func validDeclarationMigration(migration DeclarationMigration, run *Run, declaration *specialist.SetupJourney) bool {
	if len(migration.StepIDMap) != len(declaration.Steps) || len(run.StepStates) != len(declaration.Steps) {
		return false
	}
	targets := make(map[string]struct{}, len(declaration.Steps))
	for _, step := range declaration.Steps {
		targets[step.ID] = struct{}{}
	}
	mapped := make(map[string]struct{}, len(migration.StepIDMap))
	for _, previous := range run.StepStates {
		target, ok := migration.StepIDMap[previous.StepID]
		if !ok {
			return false
		}
		if _, ok := targets[target]; !ok {
			return false
		}
		if _, duplicate := mapped[target]; duplicate {
			return false
		}
		mapped[target] = struct{}{}
	}
	return len(mapped) == len(targets)
}

func declarationMigrationDigest(key declarationMigrationKey, migration DeclarationMigration) string {
	fromIDs := make([]string, 0, len(migration.StepIDMap))
	for from := range migration.StepIDMap {
		fromIDs = append(fromIDs, from)
	}
	sort.Strings(fromIDs)
	var builder strings.Builder
	builder.WriteString(key.JourneyID)
	builder.WriteByte('|')
	builder.WriteString(strconv.Itoa(key.FromSchemaVersion))
	builder.WriteByte('|')
	builder.WriteString(strconv.Itoa(key.FromDeclarationVersion))
	builder.WriteByte('|')
	builder.WriteString(strconv.Itoa(key.ToSchemaVersion))
	builder.WriteByte('|')
	builder.WriteString(strconv.Itoa(key.ToDeclarationVersion))
	for _, from := range fromIDs {
		builder.WriteByte('|')
		builder.WriteString(from)
		builder.WriteByte('>')
		builder.WriteString(migration.StepIDMap[from])
	}
	return Digest([]byte(builder.String()))
}

func incompatibleProjection(declaration *specialist.SetupJourney, run *Run) *JourneyProjection {
	steps := make([]StepProjection, len(declaration.Steps))
	for index, step := range declaration.Steps {
		status := StepPending
		reason := ReasonCode("")
		guidance := ""
		if index == 0 {
			status = StepBlocked
			reason = ReasonDeclarationInvalid
			guidance = safeGuidance[reason]
		}
		steps[index] = StepProjection{
			ID: step.ID, Kind: step.Kind, Title: step.Title,
			Description: step.Description, Status: status,
			ReasonCode: reason, Guidance: guidance,
		}
	}
	projection := baseProjection(declaration, run)
	projection.Lifecycle = LifecycleNeedsAttention
	projection.CurrentStepID = declaration.Steps[0].ID
	projection.DeclarationIncompatible = true
	projection.Steps = steps
	return projection
}

func projectionFromRun(
	declaration *specialist.SetupJourney,
	run *Run,
	reads []CanonicalStepRead,
	busy *OperationReceipt,
	precondition *PreconditionProjection,
) *JourneyProjection {
	projection := baseProjection(declaration, run)
	projection.Precondition = clonePreconditionProjection(precondition)
	projection.Steps = make([]StepProjection, len(declaration.Steps))
	for index, step := range declaration.Steps {
		state := run.StepStates[index]
		stepProjection := StepProjection{
			ID: step.ID, Kind: step.Kind, Title: step.Title, Description: step.Description,
			Status: state.Status, ReasonCode: state.ReasonCode,
			Integration:     cloneIntegrationProjection(reads[index].Integration),
			WorkspaceSetup:  cloneWorkspaceSetupProjection(reads[index].WorkspaceSetup),
			Staffing:        cloneStaffingProjection(reads[index].Staffing),
			Preparation:     cloneHomePreparation(reads[index].Preparation),
			HomeProvider:    cloneHomeProviderProjection(reads[index].HomeProvider),
			WorkspaceCreate: cloneWorkspaceCreateProjection(reads[index].WorkspaceCreate),
			AccountConnect:  cloneAccountConnectProjection(reads[index].AccountConnect),
			AccountLink:     cloneAccountLinkProjection(reads[index].AccountLink),
		}
		// The handoff is published only with the continue action it belongs to.
		if state.Status == StepComplete || state.StepID == run.CurrentStepID {
			stepProjection.Handoff = cloneQuestHandoffProjection(reads[index].Handoff)
		}
		if state.ReasonCode != "" {
			stepProjection.Guidance = safeGuidance[state.ReasonCode]
		}
		// Only complete steps, the current unresolved step, and a ready summary
		// publish actions. Later pending steps cannot be invoked out of order.
		if state.Status == StepComplete || state.StepID == run.CurrentStepID {
			stepProjection.Actions = actionProjections(step.Kind, reads[index].AvailableActions)
		}
		projection.Steps[index] = stepProjection
	}
	if busy != nil {
		projection.Busy = true
		projection.ReconciliationRequired = busy.Status == OperationReconcileRequired
	}
	return projection
}

func baseProjection(declaration *specialist.SetupJourney, run *Run) *JourneyProjection {
	projection := &JourneyProjection{
		RunID: run.ID, RunKind: run.Kind, RootRunID: run.RootRunID,
		Journey: DeclarationProjection{
			PluginID: declaration.OwnerPluginID,
			ID:       declaration.ID, SchemaVersion: declaration.SchemaVersion,
			Version: declaration.Version, Title: declaration.Title, Description: declaration.Description,
			WorkspaceLaunch: cloneLaunchCopy(declaration.WorkspaceLaunch),
		},
		StateRevision: run.StateRevision, Lifecycle: run.Lifecycle,
		CurrentStepID: run.CurrentStepID, Dismissed: run.Dismissed,
		Receipts: ResourceProjection{
			IntegrationPluginID: run.IntegrationPluginID, IntegrationVersion: run.IntegrationVersion,
			HomeWorkspaceID: run.HomeWorkspaceID, ProjectWorkspaceID: run.ProjectWorkspaceID,
			SelectedModeID: run.SelectedModeID,
		},
		FirstOpenedAt: cloneTime(run.FirstOpenedAt), LastDismissedAt: cloneTime(run.LastDismissedAt),
		FirstCompletedAt: cloneTime(run.FirstCompletedAt), UpdatedAt: run.UpdatedAt.UTC(),
	}
	switch run.SpecialistSlug {
	case userTemplateQuestSlug:
		projection.Journey.Source = QuestSourceUserTemplate
		projection.Journey.TemplateID = declaration.ExpectedBlueprintID
	case hostQuestSlug:
		projection.Journey.Source = QuestSourceHost
		projection.Journey.TemplateID = declaration.ExpectedBlueprintID
	}
	return projection
}

func materialRunChange(before, after *Run) bool {
	left := before.Clone()
	right := after.Clone()
	left.StateRevision, right.StateRevision = 0, 0
	left.UpdatedAt, right.UpdatedAt = time.Time{}, time.Time{}
	left.NeedsNormalization, right.NeedsNormalization = false, false
	return !reflect.DeepEqual(left, right)
}

func safeStoreFailure(err error, revision int64) *Failure {
	switch {
	case errors.Is(err, ErrNotFound):
		return failure(ReasonRunNotFound, revision)
	case errors.Is(err, ErrConflict):
		return failure(ReasonRevisionConflict, revision)
	case errors.Is(err, ErrIdempotencyConflict):
		return failure(ReasonIdempotencyConflict, revision)
	case errors.Is(err, ErrInvalid):
		return failure(ReasonInputInvalid, revision)
	case errors.Is(err, ErrMalformed):
		return failure(ReasonRunNotFound, revision)
	default:
		return failure(ReasonOperationFailed, revision)
	}
}

func (p JourneyProjection) String() string {
	// Keep accidental formatting/logging bounded and free of resource IDs.
	return fmt.Sprintf("setup journey %s (%s, revision %d)", p.Journey.ID, p.Lifecycle, p.StateRevision)
}
