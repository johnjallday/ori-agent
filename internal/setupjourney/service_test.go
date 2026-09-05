package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

type syntheticJourneyAdapter struct{ reviews int }

func (a *syntheticJourneyAdapter) InputDigest(_ ActionID, input json.RawMessage) (string, error) {
	return Digest(input), nil
}
func (a *syntheticJourneyAdapter) Review(_ context.Context, _ ReadScope, _ ActionID, input json.RawMessage) (ActionReviewMaterial, error) {
	a.reviews++
	return ActionReviewMaterial{CommitAction: ActionInstall, InputDigest: Digest(input), OwnerRevisionDigest: Digest([]byte("synthetic-owner-v1")), DisclosureDigest: Digest([]byte("synthetic-disclosure-v1"))}, nil
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

func defaultCanonicalReads() map[specialist.SetupStepKind]CanonicalStepRead {
	return map[specialist.SetupStepKind]CanonicalStepRead{
		specialist.SetupStepIntegrationInstall:       {AvailableActions: []ActionID{ActionReviewInstall}},
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

func serviceFixture(t *testing.T, reads map[specialist.SetupStepKind]CanonicalStepRead) (*Service, *SQLiteStore) {
	t.Helper()
	_, store := openTestStore(t)
	service, err := NewService(
		store,
		&relationshipStub{state: acceptedRelationship()},
		readerRegistryStub(t, reads, nil, nil, nil),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service, store
}

func TestReaderRegistryRequiresExactlyTheClosedV1Kinds(t *testing.T) {
	readers := make(map[specialist.SetupStepKind]CanonicalReader)
	for kind := range actionDefinitionsByKind {
		readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) {
			return CanonicalStepRead{}, nil
		})
	}
	delete(readers, specialist.SetupStepSummary)
	if _, err := NewReaderRegistry(readers); err == nil {
		t.Fatal("registry accepted a missing summary reader")
	}
	readers[specialist.SetupStepSummary] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) {
		return CanonicalStepRead{}, nil
	})
	readers[specialist.SetupStepKind("custom")] = readers[specialist.SetupStepSummary]
	if _, err := NewReaderRegistry(readers); err == nil {
		t.Fatal("registry accepted an extra executable step kind")
	}
}

func TestSyntheticNonDomainDeclarationUsesGenericSetupShell(t *testing.T) {
	_, store := openTestStore(t)
	journey, err := specialist.NormalizeSetupJourney(specialist.SetupJourney{SchemaVersion: 1, Version: 1, ID: "visual_archive_setup", Title: "Set up visual archives", Description: "Connect a reviewed archive workflow.", IntegrationKey: "archive_bridge", ExpectedBlueprintID: "visual_archive", ExpectedAssistantProgramID: "archive_assistant", Steps: []specialist.SetupJourneyStep{{ID: "integration", Kind: specialist.SetupStepIntegrationInstall, Title: "Integration", Description: "Review the archive bridge."}, {ID: "project", Kind: specialist.SetupStepProjectConnect, Title: "Archive", Description: "Connect an archive."}, {ID: "workspace", Kind: specialist.SetupStepWorkspaceSetup, Title: "Workspace", Description: "Choose workspace access."}, {ID: "staffing", Kind: specialist.SetupStepAssistantProgramStaffing, Title: "Team", Description: "Review scoped roles."}, {ID: "summary", Kind: specialist.SetupStepSummary, Title: "Summary", Description: "Review the setup."}}})
	if err != nil {
		t.Fatal(err)
	}
	entry := specialist.Entry{Slug: "visual_archive", DisplayName: "visual archives", SetupJourney: journey}
	relationship := acceptedRelationship()
	relationship.SpecialistSlug = entry.Slug
	service, err := newService(store, &relationshipStub{state: relationship}, readerRegistryStub(t, defaultCanonicalReads(), nil, nil, nil), func(slug string) (specialist.Entry, bool) { return entry, slug == entry.Slug }, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &syntheticJourneyAdapter{}
	if err = service.SetActionAdapter(specialist.SetupStepIntegrationInstall, adapter); err != nil {
		t.Fatal(err)
	}
	projection, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Journey.ID != journey.ID || len(projection.Steps) != specialist.SetupJourneyRequiredSteps || projection.CurrentStepID != "integration" || projection.Steps[4].Title != "Summary" {
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
	resumedService, err := newService(store, &relationshipStub{state: relationship}, readerRegistryStub(t, defaultCanonicalReads(), nil, nil, nil), func(slug string) (specialist.Entry, bool) { return entry, slug == entry.Slug }, nil)
	if err != nil {
		t.Fatal(err)
	}
	resumedAdapter := &syntheticJourneyAdapter{}
	if err = resumedService.SetActionAdapter(specialist.SetupStepIntegrationInstall, resumedAdapter); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumedService.Read(context.Background(), "local", projection.RunID)
	if err != nil || !resumed.Dismissed {
		t.Fatalf("resume=%#v err=%v", resumed, err)
	}
	action, err := resumedService.Mutate(context.Background(), "local", projection.RunID, ActionReviewInstall, ActionMutation{IfRevision: resumed.StateRevision, IdempotencyKey: "synthetic-review", Input: json.RawMessage(`{}`)})
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
			service, err := NewService(store, &relationshipStub{state: state}, registry)
			if err != nil {
				t.Fatalf("new service: %v", err)
			}
			_, err = service.Read(context.Background(), "local", "")
			var publicFailure *Failure
			if !errors.As(err, &publicFailure) {
				t.Fatalf("error = %v; want safe Failure", err)
			}
			if publicFailure.ReasonCode != ReasonRelationshipNotAccepted && publicFailure.ReasonCode != ReasonJourneyUnavailable {
				t.Fatalf("unexpected closed reason: %#v", publicFailure)
			}
		})
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
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{
		Complete: true, AvailableActions: []ActionID{ActionManageIntegration},
		Result: CanonicalResult{IntegrationPluginID: "com.ori.reaper", IntegrationVersion: "0.5.0"},
	}
	reads[specialist.SetupStepWorkspaceSetup] = CanonicalStepRead{
		Complete: true, AvailableActions: []ActionID{ActionOpenWorkspaceSetup},
		Result: CanonicalResult{SelectedModeID: "file_only"},
	}
	reads[specialist.SetupStepAssistantProgramStaffing] = CanonicalStepRead{BlockedReason: ReasonStaffingRequired}
	// A summary reader cannot claim completion while a consequence is missing.
	reads[specialist.SetupStepSummary] = CanonicalStepRead{Complete: true, AvailableActions: []ActionID{ActionReviewSetup}}
	calls := make(map[specialist.SetupStepKind]int)
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, calls, nil))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	projection, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read journey: %v", err)
	}
	for kind := range actionDefinitionsByKind {
		if calls[kind] != 1 {
			t.Errorf("reader %s called %d times; want 1", kind, calls[kind])
		}
	}
	if projection.Lifecycle != LifecycleInProgress || projection.CurrentStepID != "project" {
		t.Fatalf("unexpected first unresolved projection: %#v", projection)
	}
	if projection.Steps[0].Status != StepComplete || projection.Steps[1].Status != StepActive ||
		projection.Steps[2].Status != StepComplete || projection.Steps[3].Status != StepPending ||
		projection.Steps[4].Status != StepPending {
		t.Fatalf("unexpected independently reconciled statuses: %#v", projection.Steps)
	}
	if len(projection.Steps[1].Actions) != 2 || len(projection.Steps[3].Actions) != 0 {
		t.Fatalf("actions were not limited to safe current/complete steps: %#v", projection.Steps)
	}
	if projection.Receipts.IntegrationPluginID != "com.ori.reaper" || projection.Receipts.SelectedModeID != "file_only" {
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
		if calls[kind] != 2 {
			t.Errorf("repeat GET did not reread %s: calls=%d", kind, calls[kind])
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
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
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
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{IntegrationPluginID: "com.ori.reaper", IntegrationVersion: "0.5.0"},
	}
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

	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{
		BlockedReason: ReasonIntegrationDisabled, AvailableActions: []ActionID{ActionReviewEnable},
	}
	regressed, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read regressed journey: %v", err)
	}
	if regressed.Lifecycle != LifecycleNeedsAttention || regressed.CurrentStepID != "integration" ||
		regressed.Steps[0].Status != StepBlocked || regressed.Steps[0].ReasonCode != ReasonIntegrationDisabled {
		t.Fatalf("regression was not narrowed to integration: %#v", regressed)
	}
	for _, index := range []int{1, 2, 3} {
		if regressed.Steps[index].Status != StepComplete {
			t.Errorf("downstream step %d was discarded: %#v", index, regressed.Steps[index])
		}
	}
	if regressed.FirstCompletedAt == nil || !regressed.FirstCompletedAt.Equal(completedAt) {
		t.Fatalf("historical completion changed: before=%v after=%v", completedAt, regressed.FirstCompletedAt)
	}
	if regressed.Receipts.IntegrationPluginID != "com.ori.reaper" || regressed.Receipts.ProjectWorkspaceID != "workspace-project" {
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
		specialist.SetupStepIntegrationInstall: fmt.Errorf("provider failed at /private/Music/song.rpp with secret-token"),
	}
	_, store := openTestStore(t)
	service, err := NewService(
		store,
		&relationshipStub{state: acceptedRelationship()},
		readerRegistryStub(t, reads, failures, nil, nil),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	projection, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("owner error escaped read: %v", err)
	}
	if projection.Steps[0].Status != StepBlocked || projection.Steps[0].ReasonCode != ReasonOwnerUnavailable {
		t.Fatalf("owner error was not normalized: %#v", projection.Steps[0])
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
}

func TestServiceRepairsMalformedStructuralProgressFromCanonicalOwners(t *testing.T) {
	db, store := openTestStore(t)
	reads := defaultCanonicalReads()
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, nil))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
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
	if len(repaired.Steps) != specialist.SetupJourneyRequiredSteps || repaired.CurrentStepID != "integration" ||
		repaired.Steps[0].Status != StepActive {
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
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{IntegrationPluginID: "com.ori.reaper", IntegrationVersion: "0.5.0"},
	}
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, nil))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
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
	if version != 99 || revision != first.StateRevision || beforeJSON != afterJSON || pluginID != "com.ori.reaper" {
		t.Fatalf("incompatible row was reinterpreted: version=%d revision=%d plugin=%q before=%q after=%q",
			version, revision, pluginID, beforeJSON, afterJSON)
	}
}

func TestServiceAppliesOnlyExactCompiledDeclarationMigration(t *testing.T) {
	db, store := openTestStore(t)
	reads := defaultCanonicalReads()
	baseService, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, nil))
	if err != nil {
		t.Fatalf("new base service: %v", err)
	}
	before, err := baseService.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("create v1 run: %v", err)
	}

	entry, ok := specialist.Get("music_production")
	if !ok {
		t.Fatal("music specialist fixture missing")
	}
	entry.SetupJourney.Version = 2
	entry.SetupJourney.Steps[3].ID = "team"
	resolver := func(slug string) (specialist.Entry, bool) {
		if slug != entry.Slug {
			return specialist.Entry{}, false
		}
		return entry, true
	}
	migrationKey := declarationMigrationKey{
		JourneyID:         entry.SetupJourney.ID,
		FromSchemaVersion: 1, FromDeclarationVersion: 1,
		ToSchemaVersion: 1, ToDeclarationVersion: 2,
	}
	migrations := map[declarationMigrationKey]DeclarationMigration{
		migrationKey: {StepIDMap: map[string]string{
			"integration": "integration", "project": "project", "workspace": "workspace",
			"staffing": "team", "summary": "summary",
		}},
	}
	service, err := newService(
		store, &relationshipStub{state: acceptedRelationship()},
		readerRegistryStub(t, reads, nil, nil, nil), resolver, migrations,
	)
	if err != nil {
		t.Fatalf("new migrating service: %v", err)
	}
	migrated, err := service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatalf("read migrated declaration: %v", err)
	}
	if migrated.Journey.Version != 2 || migrated.Steps[3].ID != "team" || migrated.DeclarationIncompatible {
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
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{IntegrationPluginID: "com.ori.reaper", IntegrationVersion: "0.5.0"},
	}
	reads[specialist.SetupStepProjectConnect] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{HomeWorkspaceID: "workspace-home", ProjectWorkspaceID: "workspace-first"},
	}
	scopes := make(map[specialist.SetupStepKind][]ReadScope)
	service, err := NewService(
		store, &relationshipStub{state: acceptedRelationship()},
		readerRegistryStub(t, reads, nil, nil, scopes),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
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
	integrationScopes := scopes[specialist.SetupStepIntegrationInstall]
	lastScope := integrationScopes[len(integrationScopes)-1]
	if lastScope.RunKind != RunKindChild || lastScope.IntegrationPluginID != "com.ori.reaper" || lastScope.HomeWorkspaceID != "workspace-home" {
		t.Fatalf("child reader did not receive bounded shared root scope: %#v", lastScope)
	}
	persisted, err := store.GetRun(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("read persisted child: %v", err)
	}
	if persisted.IntegrationPluginID != "" || persisted.IntegrationVersion != "" || persisted.HomeWorkspaceID != "" {
		t.Fatalf("child persisted shared root receipts: %#v", persisted)
	}
}
