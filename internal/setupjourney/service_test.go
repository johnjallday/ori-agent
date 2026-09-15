package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// syntheticJourneyAdapter reviews one commit action without executing it.
type syntheticJourneyAdapter struct {
	reviews int
	commit  ActionID
}

func (a *syntheticJourneyAdapter) InputDigest(_ ActionID, input json.RawMessage) (string, error) {
	return Digest(input), nil
}
func (a *syntheticJourneyAdapter) Review(_ context.Context, _ ReadScope, _ ActionID, input json.RawMessage) (ActionReviewMaterial, error) {
	a.reviews++
	commit := a.commit
	if commit == "" {
		commit = ActionInstall
	}
	return ActionReviewMaterial{CommitAction: commit, InputDigest: Digest(input), OwnerRevisionDigest: Digest([]byte("synthetic-owner-v1")), DisclosureDigest: Digest([]byte("synthetic-disclosure-v1"))}, nil
}
func (a *syntheticJourneyAdapter) PrepareCommit(context.Context, ReadScope, ActionID, json.RawMessage) (ActionReviewMaterial, error) {
	return ActionReviewMaterial{}, errors.New("not used")
}
func (a *syntheticJourneyAdapter) Commit(context.Context, ReadScope, ActionID, json.RawMessage, ActionReviewMaterial) (CanonicalResult, error) {
	return CanonicalResult{}, errors.New("not used")
}
func (a *syntheticJourneyAdapter) ConsequenceObserved(ActionID, CanonicalStepRead) bool { return false }

type relationshipStub struct {
	state *personalassistant.State
	err   error
}

func (stub *relationshipStub) GetState(context.Context, string) (*personalassistant.State, error) {
	if stub.err != nil {
		return nil, stub.err
	}
	return stub.state.Clone(), nil
}

func acceptedRelationship() *personalassistant.State {
	return &personalassistant.State{
		UserID: "local", AssistantID: "assistant-journey-1",
		Status:               personalassistant.StatusActive,
		SpecialistOfferState: personalassistant.SpecialistOfferAccepted,
		SpecialistSlug:       "music_production",
	}
}

// reaperQuestDeclaration is the installed plugin's four-step setup quest for the
// built-in reviewed REAPER integration.
func reaperQuestDeclaration() specialist.SetupJourney {
	return specialist.SetupJourney{
		SchemaVersion: specialist.SetupJourneySchemaVersion, Version: 1, ID: "reaper_setup",
		Title: "Set up REAPER", Description: "Connect a REAPER project and choose how Ori can help.",
		IntegrationKey: "ori_reaper", ExpectedBlueprintID: "reaper-song", ExpectedAssistantProgramID: "music-producer-assistant",
		Steps: []specialist.SetupJourneyStep{
			{ID: "project", Kind: specialist.SetupStepProjectConnect, Title: "Connect a project", Description: "Connect a project."},
			{ID: "workspace", Kind: specialist.SetupStepWorkspaceSetup, Title: "Choose how Ori works", Description: "Choose a mode."},
			{ID: "staffing", Kind: specialist.SetupStepAssistantProgramStaffing, Title: "Add your studio team", Description: "Add roles."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Review setup", Description: "Review setup."},
		},
		WorkspaceLaunch: &specialist.WorkspaceLaunchCopy{GroupTitle: "Build Your Music Production Group", GroupName: "Music Production"},
	}
}

// defaultCanonicalReads has the integration precondition met, so a project
// setup quest starts at its project step.
func defaultCanonicalReads() map[specialist.SetupStepKind]CanonicalStepRead {
	return map[specialist.SetupStepKind]CanonicalStepRead{
		specialist.SetupStepIntegrationInstall: {
			Complete: true, AvailableActions: []ActionID{ActionManageIntegration},
			Result: CanonicalResult{IntegrationPluginID: "reaper-plugin", IntegrationVersion: "0.6.0"},
		},
		specialist.SetupStepProjectConnect:           {AvailableActions: []ActionID{ActionReviewExistingProject, ActionReviewNewProject}},
		specialist.SetupStepWorkspaceSetup:           {AvailableActions: []ActionID{ActionOpenWorkspaceSetup}},
		specialist.SetupStepAssistantProgramStaffing: {AvailableActions: []ActionID{ActionReviewHomeStaffing}},
		specialist.SetupStepSummary:                  {AvailableActions: []ActionID{ActionReviewSetup}},
	}
}

func readerRegistryStub(t *testing.T, reads map[specialist.SetupStepKind]CanonicalStepRead, failures map[specialist.SetupStepKind]error, calls map[specialist.SetupStepKind]int, scopes map[specialist.SetupStepKind][]ReadScope) *ReaderRegistry {
	t.Helper()
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		kind := kind
		readers[kind] = CanonicalReaderFunc(func(_ context.Context, scope ReadScope) (CanonicalStepRead, error) {
			if calls != nil {
				calls[kind]++
			}
			if scopes != nil {
				scopes[kind] = append(scopes[kind], scope)
			}
			if failures != nil && failures[kind] != nil {
				return CanonicalStepRead{}, failures[kind]
			}
			return reads[kind], nil
		})
	}
	registry, err := NewReaderRegistry(readers)
	if err != nil {
		t.Fatalf("new reader registry: %v", err)
	}
	return registry
}

// standardQuestCatalog serves the generated install quests and the installed
// plugins' own quests from one plugin store.
func standardQuestCatalog(plugins *questPlugins) QuestCatalog {
	return CombineQuestCatalogs(
		NewIntegrationInstallQuestCatalog(reviewedintegration.All, plugins),
		NewInstalledQuestCatalog(plugins),
	)
}

// aliasService serves the accepted music specialist with the REAPER plugin
// installed, so the assistant alias resolves the plugin's four-step quest.
func aliasService(t *testing.T, store Store, relationships RelationshipReader, registry *ReaderRegistry) *Service {
	t.Helper()
	service, err := NewService(store, relationships, registry)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	service.SetQuestCatalog(standardQuestCatalog(&questPlugins{items: []plugin.InstalledPlugin{questPluginFixture(t)}}))
	return service
}

func serviceFixture(t *testing.T, reads map[specialist.SetupStepKind]CanonicalStepRead) (*Service, *SQLiteStore) {
	t.Helper()
	_, store := openTestStore(t)
	return aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, nil)), store
}

func TestReaderRegistryRequiresExactlyTheClosedV1Kinds(t *testing.T) {
	readers := make(map[specialist.SetupStepKind]CanonicalReader)
	for kind := range actionDefinitionsByKind {
		readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) {
			return CanonicalStepRead{}, nil
		})
	}
	if len(readers) != len(specialist.SetupStepKinds()) {
		t.Fatalf("action registry covers %d kinds, compiled shapes declare %d", len(readers), len(specialist.SetupStepKinds()))
	}
	for _, kind := range specialist.SetupStepKinds() {
		if _, ok := actionDefinitionsByKind[kind]; !ok {
			t.Fatalf("compiled kind %q has no action definitions", kind)
		}
	}
	delete(readers, specialist.SetupStepSummary)
	if _, err := NewReaderRegistry(readers); err == nil {
		t.Fatal("registry accepted a missing summary reader")
	}
	readers[specialist.SetupStepSummary] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) {
		return CanonicalStepRead{}, nil
	})
	accountLink := readers[specialist.SetupStepAccountLink]
	delete(readers, specialist.SetupStepAccountLink)
	if _, err := NewReaderRegistry(readers); err == nil {
		t.Fatal("registry accepted a missing account-link reader")
	}
	readers[specialist.SetupStepAccountLink] = accountLink
	readers[specialist.SetupStepKind("custom")] = readers[specialist.SetupStepSummary]
	if _, err := NewReaderRegistry(readers); err == nil {
		t.Fatal("registry accepted an extra executable step kind")
	}
}

func TestSyntheticNonDomainDeclarationUsesGenericSetupShell(t *testing.T) {
	_, store := openTestStore(t)
	journey, err := specialist.NormalizeSetupJourney(specialist.SetupJourney{
		SchemaVersion: 1, Version: 1, ID: "visual_archive_setup", Title: "Set up visual archives", Description: "Connect a reviewed archive workflow.",
		IntegrationKey: "archive_bridge", ExpectedBlueprintID: "visual_archive", ExpectedAssistantProgramID: "archive_assistant",
		Steps: []specialist.SetupJourneyStep{
			{ID: "project", Kind: specialist.SetupStepProjectConnect, Title: "Archive", Description: "Connect an archive."},
			{ID: "workspace", Kind: specialist.SetupStepWorkspaceSetup, Title: "Workspace", Description: "Choose workspace access."},
			{ID: "staffing", Kind: specialist.SetupStepAssistantProgramStaffing, Title: "Team", Description: "Review scoped roles."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Summary", Description: "Review the setup."},
		},
		WorkspaceLaunch: &specialist.WorkspaceLaunchCopy{GroupTitle: "Build your archive group", GroupName: "Archives"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journey.OwnerPluginID = "archive-plugin"
	reads := defaultCanonicalReads()
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{IntegrationPluginID: "archive-plugin", IntegrationVersion: "1.0.0"},
	}
	newScoped := func() (*Service, *syntheticJourneyAdapter) {
		service, err := NewService(store, &relationshipStub{err: errors.New("plugin quests need no assistant")}, readerRegistryStub(t, reads, nil, nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		service.SetQuestCatalog(fixedQuestCatalog{definition: QuestDefinition{Declaration: journey, Ownership: "plugin"}})
		adapter := &syntheticJourneyAdapter{commit: ActionCreateNewProject}
		if err := service.SetActionAdapter(specialist.SetupStepProjectConnect, adapter); err != nil {
			t.Fatal(err)
		}
		scoped, err := service.ForQuest(context.Background(), "local", journey.OwnerPluginID, journey.ID)
		if err != nil {
			t.Fatal(err)
		}
		return scoped, adapter
	}
	service, _ := newScoped()
	projection, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Journey.ID != journey.ID || len(projection.Steps) != 4 || projection.CurrentStepID != "project" || projection.Steps[3].Title != "Summary" {
		t.Fatalf("generic projection=%#v", projection)
	}
	opened, err := service.Open(context.Background(), "local", projection.RunID, PresentationMutation{IfRevision: projection.StateRevision, IdempotencyKey: "synthetic-open"})
	if err != nil || opened.FirstOpenedAt == nil {
		t.Fatalf("open=%#v err=%v", opened, err)
	}
	dismissed, err := service.Dismiss(context.Background(), "local", projection.RunID, PresentationMutation{IfRevision: opened.StateRevision, IdempotencyKey: "synthetic-dismiss"})
	if err != nil || !dismissed.Dismissed {
		t.Fatalf("dismiss=%#v err=%v", dismissed, err)
	}
	resumedService, resumedAdapter := newScoped()
	resumed, err := resumedService.Read(context.Background(), "local", projection.RunID)
	if err != nil || !resumed.Dismissed {
		t.Fatalf("resume=%#v err=%v", resumed, err)
	}
	action, err := resumedService.Mutate(context.Background(), "local", projection.RunID, ActionReviewNewProject, ActionMutation{IfRevision: resumed.StateRevision, IdempotencyKey: "synthetic-review", Input: json.RawMessage(`{}`)})
	if err != nil || action.Review == nil || resumedAdapter.reviews != 1 {
		t.Fatalf("generic action=%#v reviews=%d err=%v", action, resumedAdapter.reviews, err)
	}
	encoded, err := json.Marshal(action.Journey)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, leak := range []string{"reaper", "mix engineer", "songwriter"} {
		if strings.Contains(lower, leak) {
			t.Fatalf("synthetic projection leaked domain literal %q: %s", leak, encoded)
		}
	}
}

func TestServiceRequiresCurrentAcceptedActiveOrPausedRelationship(t *testing.T) {
	db, store := openTestStore(t)
	reads := defaultCanonicalReads()
	registry := readerRegistryStub(t, reads, nil, nil, nil)
	cases := map[string]*personalassistant.State{
		"unanswered": {
			UserID: "local", AssistantID: "assistant-1", Status: personalassistant.StatusActive,
			SpecialistOfferState: personalassistant.SpecialistOfferUnanswered,
		},
		"declined": {
			UserID: "local", AssistantID: "assistant-1", Status: personalassistant.StatusActive,
			SpecialistOfferState: personalassistant.SpecialistOfferDeclined,
		},
		"not active": {
			UserID: "local", AssistantID: "assistant-1", Status: personalassistant.StatusAwaitingHQ,
			SpecialistOfferState: personalassistant.SpecialistOfferAccepted, SpecialistSlug: "music_production",
		},
		"unknown built-in": {
			UserID: "local", AssistantID: "assistant-1", Status: personalassistant.StatusActive,
			SpecialistOfferState: personalassistant.SpecialistOfferAccepted, SpecialistSlug: "unregistered_domain",
		},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			service := aliasService(t, store, &relationshipStub{state: state}, registry)
			_, err := service.Read(context.Background(), "local", "")
			var publicFailure *Failure
			if !errors.As(err, &publicFailure) {
				t.Fatalf("error = %v; want safe Failure", err)
			}
			if publicFailure.ReasonCode != ReasonRelationshipNotAccepted && publicFailure.ReasonCode != ReasonJourneyUnavailable {
				t.Fatalf("unexpected closed reason: %#v", publicFailure)
			}
		})
	}
	// An accepted specialist without an integration, or a service without a
	// quest catalog, has no guided setup.
	withoutKey, err := newService(store, &relationshipStub{state: acceptedRelationship()}, registry,
		func(slug string) (specialist.Entry, bool) { return specialist.Entry{Slug: slug}, true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	withoutKey.SetQuestCatalog(standardQuestCatalog(&questPlugins{}))
	withoutCatalog, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err != nil {
		t.Fatal(err)
	}
	for name, service := range map[string]*Service{"no integration key": withoutKey, "no catalog": withoutCatalog} {
		var publicFailure *Failure
		if _, err := service.Read(context.Background(), "local", ""); !errors.As(err, &publicFailure) || publicFailure.ReasonCode != ReasonJourneyUnavailable {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM setup_journey_run`).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 0 {
		t.Fatalf("unqualified relationship created %d journey runs", count)
	}
}

func TestServiceReconcilesEveryOwnerAndSelectsFirstUnresolvedStep(t *testing.T) {
	_, store := openTestStore(t)
	reads := defaultCanonicalReads()
	reads[specialist.SetupStepWorkspaceSetup] = CanonicalStepRead{
		Complete: true, AvailableActions: []ActionID{ActionOpenWorkspaceSetup},
		Result: CanonicalResult{SelectedModeID: "file_only"},
	}
	reads[specialist.SetupStepAssistantProgramStaffing] = CanonicalStepRead{BlockedReason: ReasonStaffingRequired}
	// A summary reader cannot claim completion while a consequence is missing.
	reads[specialist.SetupStepSummary] = CanonicalStepRead{Complete: true, AvailableActions: []ActionID{ActionReviewSetup}}
	calls := make(map[specialist.SetupStepKind]int)
	service := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, calls, nil))

	projection, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read journey: %v", err)
	}
	// The integration precondition is read once per read, then each declared
	// step once; nothing else.
	declared := map[specialist.SetupStepKind]bool{specialist.SetupStepIntegrationInstall: true}
	for _, kind := range specialist.SetupJourneyShapeSteps(specialist.SetupJourneyShapeProjectSetup) {
		declared[kind] = true
	}
	for kind := range actionDefinitionsByKind {
		want := 0
		if declared[kind] {
			want = 1
		}
		if calls[kind] != want {
			t.Errorf("reader %s called %d times; want %d", kind, calls[kind], want)
		}
	}
	if projection.Lifecycle != LifecycleInProgress || projection.CurrentStepID != "project" || projection.Precondition != nil {
		t.Fatalf("unexpected first unresolved projection: %#v", projection)
	}
	if projection.Steps[0].Status != StepActive || projection.Steps[1].Status != StepComplete ||
		projection.Steps[2].Status != StepPending || projection.Steps[3].Status != StepPending {
		t.Fatalf("unexpected independently reconciled statuses: %#v", projection.Steps)
	}
	if len(projection.Steps[0].Actions) != 2 || len(projection.Steps[2].Actions) != 0 {
		t.Fatalf("actions were not limited to safe current/complete steps: %#v", projection.Steps)
	}
	if projection.Receipts.IntegrationPluginID != "reaper-plugin" || projection.Receipts.IntegrationVersion != "0.6.0" ||
		projection.Receipts.SelectedModeID != "file_only" {
		t.Fatalf("bounded canonical receipts missing: %#v", projection.Receipts)
	}
	firstRevision := projection.StateRevision
	second, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("repeat read: %v", err)
	}
	if second.StateRevision != firstRevision {
		t.Fatalf("unchanged GET advanced revision from %d to %d", firstRevision, second.StateRevision)
	}
	for kind := range actionDefinitionsByKind {
		want := 0
		if declared[kind] {
			want = 2
		}
		if calls[kind] != want {
			t.Errorf("repeat GET read %s %d times; want %d", kind, calls[kind], want)
		}
	}
}

func TestServiceConcurrentReadsConvergeOnOneRootAndProjection(t *testing.T) {
	db, store := openTestStore(t)
	reads := defaultCanonicalReads()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		kind := kind
		readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) {
			if kind == specialist.SetupStepIntegrationInstall {
				select {
				case entered <- struct{}{}:
					<-release
				default:
				}
			}
			return reads[kind], nil
		})
	}
	registry, err := NewReaderRegistry(readers)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	service := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, registry)
	type result struct {
		projection *JourneyProjection
		err        error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			projection, readErr := service.Read(context.Background(), "local", "")
			results <- result{projection: projection, err: readErr}
		}()
	}
	<-entered
	<-entered
	close(release)
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent reads failed: %v / %v", first.err, second.err)
	}
	if first.projection.RunID != second.projection.RunID ||
		first.projection.StateRevision != second.projection.StateRevision {
		t.Fatalf("concurrent projections diverged: %#v / %#v", first.projection, second.projection)
	}
	var roots int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM setup_journey_run WHERE run_kind = 'root'`).Scan(&roots); err != nil {
		t.Fatalf("count roots: %v", err)
	}
	if roots != 1 {
		t.Fatalf("concurrent reads created %d roots", roots)
	}
}

func TestServiceReadyThenNarrowRegressionPreservesHistoryAndDownstreamResults(t *testing.T) {
	reads := defaultCanonicalReads()
	reads[specialist.SetupStepProjectConnect] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{HomeWorkspaceID: "workspace-home", ProjectWorkspaceID: "workspace-project"},
	}
	reads[specialist.SetupStepWorkspaceSetup] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{SelectedModeID: "file_only"},
	}
	reads[specialist.SetupStepAssistantProgramStaffing] = CanonicalStepRead{Complete: true}
	service, _ := serviceFixture(t, reads)

	ready, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read ready journey: %v", err)
	}
	if ready.Lifecycle != LifecycleReady || ready.CurrentStepID != "" || ready.FirstCompletedAt == nil {
		t.Fatalf("journey did not become ready: %#v", ready)
	}
	completedAt := *ready.FirstCompletedAt

	reads[specialist.SetupStepWorkspaceSetup] = CanonicalStepRead{
		BlockedReason: ReasonRuntimeNeedsAttention, AvailableActions: []ActionID{ActionOpenWorkspaceSetup},
	}
	regressed, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read regressed journey: %v", err)
	}
	if regressed.Lifecycle != LifecycleNeedsAttention || regressed.CurrentStepID != "workspace" ||
		regressed.Steps[1].Status != StepBlocked || regressed.Steps[1].ReasonCode != ReasonRuntimeNeedsAttention {
		t.Fatalf("regression was not narrowed to workspace setup: %#v", regressed)
	}
	for _, index := range []int{0, 2} {
		if regressed.Steps[index].Status != StepComplete {
			t.Errorf("independent step %d was discarded: %#v", index, regressed.Steps[index])
		}
	}
	if regressed.FirstCompletedAt == nil || !regressed.FirstCompletedAt.Equal(completedAt) {
		t.Fatalf("historical completion changed: before=%v after=%v", completedAt, regressed.FirstCompletedAt)
	}
	if regressed.Receipts.IntegrationPluginID != "reaper-plugin" || regressed.Receipts.ProjectWorkspaceID != "workspace-project" {
		t.Fatalf("historical resume receipts were discarded: %#v", regressed.Receipts)
	}
}

func TestServiceDismissalNeverActsAsCompletionEvidence(t *testing.T) {
	reads := defaultCanonicalReads()
	service, store := serviceFixture(t, reads)
	ctx := context.Background()
	initial, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	run, err := store.GetRun(ctx, initial.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	dismissedAt := service.now().UTC()
	run.Dismissed = true
	run.LastDismissedAt = &dismissedAt
	if _, err := store.CompareAndSwapRun(ctx, run, run.StateRevision); err != nil {
		t.Fatalf("persist dismissal: %v", err)
	}
	for kind := range reads {
		if kind != specialist.SetupStepSummary {
			reads[kind] = CanonicalStepRead{Complete: true}
		}
	}
	ready, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatalf("read ready dismissed run: %v", err)
	}
	if !ready.Dismissed || ready.LastDismissedAt == nil || ready.Lifecycle != LifecycleReady {
		t.Fatalf("dismissal was conflated with readiness: %#v", ready)
	}
}

func TestServiceOwnerErrorsAndInvalidAdapterOutputStayClosed(t *testing.T) {
	reads := defaultCanonicalReads()
	reads[specialist.SetupStepProjectConnect] = CanonicalStepRead{
		AvailableActions: []ActionID{ActionID("client_chosen_adapter")},
	}
	failures := map[specialist.SetupStepKind]error{
		specialist.SetupStepWorkspaceSetup: fmt.Errorf("provider failed at /private/Music/song.rpp with secret-token"),
	}
	_, store := openTestStore(t)
	service := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, failures, nil, nil))
	projection, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("owner error escaped read: %v", err)
	}
	if projection.Steps[0].Status != StepBlocked || projection.Steps[0].ReasonCode != ReasonOwnerUnavailable {
		t.Fatalf("invalid adapter output was not normalized: %#v", projection.Steps[0])
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	for _, secret := range []string{"/private", "song.rpp", "secret-token", "client_chosen_adapter"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("projection leaked %q: %s", secret, encoded)
		}
	}

	// An integration owner error is the precondition's closed reason, never
	// its text.
	failures[specialist.SetupStepIntegrationInstall] = errors.New("manager exploded at /private/plugins")
	blocked, err := service.Read(context.Background(), "local", "")
	if err != nil || blocked.Precondition == nil || blocked.Precondition.ReasonCode != ReasonOwnerUnavailable {
		t.Fatalf("integration owner error = %#v, err = %v", blocked, err)
	}
	if encoded, _ := json.Marshal(blocked); strings.Contains(string(encoded), "/private") {
		t.Fatalf("precondition leaked owner text: %s", encoded)
	}
}

func TestServiceRepairsMalformedStructuralProgressFromCanonicalOwners(t *testing.T) {
	db, store := openTestStore(t)
	reads := defaultCanonicalReads()
	service := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, nil))
	first, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `
		UPDATE setup_journey_run SET step_states_json = '{broken', current_step_id = 'made_up'
		WHERE id = ?
	`, first.RunID); err != nil {
		t.Fatalf("seed malformed progress: %v", err)
	}
	repaired, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("repair read: %v", err)
	}
	if len(repaired.Steps) != 4 || repaired.CurrentStepID != "project" || repaired.Steps[0].Status != StepActive {
		t.Fatalf("malformed progress was not canonically rebuilt: %#v", repaired)
	}
	persisted, err := store.GetRun(context.Background(), first.RunID)
	if err != nil || persisted.NeedsNormalization {
		t.Fatalf("normalized progress was not persisted: %#v err=%v", persisted, err)
	}
}

func TestServiceIncompatibleDeclarationPreservesStoredProgressAndReceipts(t *testing.T) {
	db, store := openTestStore(t)
	reads := defaultCanonicalReads()
	service := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, nil))
	first, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	var beforeJSON string
	if err := db.QueryRowContext(context.Background(), `SELECT step_states_json FROM setup_journey_run WHERE id = ?`, first.RunID).Scan(&beforeJSON); err != nil {
		t.Fatalf("read stored progress: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `
		UPDATE setup_journey_run SET declaration_version = 99 WHERE id = ?
	`, first.RunID); err != nil {
		t.Fatalf("simulate future declaration record: %v", err)
	}

	incompatible, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read incompatible declaration: %v", err)
	}
	if !incompatible.DeclarationIncompatible || incompatible.Lifecycle != LifecycleNeedsAttention ||
		incompatible.Steps[0].ReasonCode != ReasonDeclarationInvalid {
		t.Fatalf("incompatible declaration did not fail closed: %#v", incompatible)
	}
	var version int
	var revision int64
	var afterJSON, pluginID string
	if err := db.QueryRowContext(context.Background(), `
		SELECT declaration_version, state_revision, step_states_json, integration_plugin_id
		FROM setup_journey_run WHERE id = ?
	`, first.RunID).Scan(&version, &revision, &afterJSON, &pluginID); err != nil {
		t.Fatalf("read preserved incompatible row: %v", err)
	}
	if version != 99 || revision != first.StateRevision || beforeJSON != afterJSON || pluginID != "reaper-plugin" {
		t.Fatalf("incompatible row was reinterpreted: version=%d revision=%d plugin=%q before=%q after=%q",
			version, revision, pluginID, beforeJSON, afterJSON)
	}
}

func TestServiceAppliesOnlyExactCompiledDeclarationMigration(t *testing.T) {
	db, store := openTestStore(t)
	reads := defaultCanonicalReads()
	baseService := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, nil))
	before, err := baseService.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("create v1 run: %v", err)
	}

	revised := questPluginFixture(t)
	revised.WorkspaceSurfaces.SetupQuests[0].Version = 2
	revised.WorkspaceSurfaces.SetupQuests[0].Steps[2].ID = "team"
	migrationKey := declarationMigrationKey{
		JourneyID:         "reaper_setup",
		FromSchemaVersion: 1, FromDeclarationVersion: 1,
		ToSchemaVersion: 1, ToDeclarationVersion: 2,
	}
	migrations := map[declarationMigrationKey]DeclarationMigration{
		migrationKey: {StepIDMap: map[string]string{
			"project": "project", "workspace": "workspace", "staffing": "team", "summary": "summary",
		}},
	}
	service, err := newService(
		store, &relationshipStub{state: acceptedRelationship()},
		readerRegistryStub(t, reads, nil, nil, nil), specialist.Get, migrations,
	)
	if err != nil {
		t.Fatalf("new migrating service: %v", err)
	}
	service.SetQuestCatalog(standardQuestCatalog(&questPlugins{items: []plugin.InstalledPlugin{revised}}))
	migrated, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read migrated declaration: %v", err)
	}
	if migrated.Journey.Version != 2 || migrated.Steps[2].ID != "team" || migrated.DeclarationIncompatible || migrated.RunID != before.RunID {
		t.Fatalf("exact migration was not applied: %#v", migrated)
	}
	if migrated.StateRevision <= before.StateRevision {
		t.Fatalf("migration did not advance CAS revision: before=%d after=%d", before.StateRevision, migrated.StateRevision)
	}
	var receiptCount int
	if err := db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM setup_journey_declaration_migration_receipt
		WHERE run_id = ? AND from_declaration_version = 1 AND to_declaration_version = 2
	`, before.RunID).Scan(&receiptCount); err != nil {
		t.Fatalf("read declaration migration receipt: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("migration receipt count = %d; want 1", receiptCount)
	}
}

func TestServiceOverviewReconcilesRootAndBoundedChildrenWithoutOpeningThem(t *testing.T) {
	reads := defaultCanonicalReads()
	service, store := serviceFixture(t, reads)
	ctx := context.Background()
	root, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := store.CreateOrGetChild(ctx, root.RunID)
	if err != nil {
		t.Fatal(err)
	}

	overview, err := service.Overview(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if overview.Root == nil || overview.Root.RunID != root.RunID || overview.ChildCount != 1 || overview.Truncated || len(overview.Children) != 1 || overview.Children[0].RunID != child.ID {
		t.Fatalf("overview = %+v", overview)
	}
	if overview.Root.FirstOpenedAt != nil || overview.Children[0].FirstOpenedAt != nil || overview.Root.Dismissed || overview.Children[0].Dismissed {
		t.Fatalf("reporting changed presentation state: root=%+v child=%+v", overview.Root, overview.Children[0])
	}
}

func TestServiceChildReadsReuseRootScopeWithoutCopyingSharedReceipts(t *testing.T) {
	_, store := openTestStore(t)
	reads := defaultCanonicalReads()
	reads[specialist.SetupStepProjectConnect] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{HomeWorkspaceID: "workspace-home", ProjectWorkspaceID: "workspace-first"},
	}
	scopes := make(map[specialist.SetupStepKind][]ReadScope)
	service := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, scopes))
	rootProjection, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	child, _, err := store.CreateOrGetChild(context.Background(), rootProjection.RunID)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	// A child project read supplies only the child's canonical project receipt.
	reads[specialist.SetupStepProjectConnect] = CanonicalStepRead{AvailableActions: []ActionID{ActionReviewExistingProject}}
	childProjection, err := service.Read(context.Background(), "local", child.ID)
	if err != nil {
		t.Fatalf("read child: %v", err)
	}
	if childProjection.Receipts.IntegrationPluginID != "" || childProjection.Receipts.HomeWorkspaceID != "" {
		t.Fatalf("child projection copied root-owned receipts: %#v", childProjection.Receipts)
	}
	for _, kind := range []specialist.SetupStepKind{specialist.SetupStepIntegrationInstall, specialist.SetupStepProjectConnect} {
		kindScopes := scopes[kind]
		lastScope := kindScopes[len(kindScopes)-1]
		if lastScope.RunKind != RunKindChild || lastScope.IntegrationPluginID != "reaper-plugin" || lastScope.HomeWorkspaceID != "workspace-home" {
			t.Fatalf("child %s reader did not receive bounded shared root scope: %#v", kind, lastScope)
		}
	}
	persisted, err := store.GetRun(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("read persisted child: %v", err)
	}
	if persisted.IntegrationPluginID != "" || persisted.IntegrationVersion != "" || persisted.HomeWorkspaceID != "" {
		t.Fatalf("child persisted shared root receipts: %#v", persisted)
	}
}
