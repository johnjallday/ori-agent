package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

// FR 18: a complete integration read fills the root's integration receipts,
// and every step reader, including the project-template resolver behind the
// project step, sees them on the very first read.
func TestPreconditionFillsIntegrationReceiptsBeforeStepReaders(t *testing.T) {
	ctx := context.Background()
	_, store := openTestStore(t)
	reads := defaultCanonicalReads()
	scopes := make(map[specialist.SetupStepKind][]ReadScope)
	service := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, scopes))

	projection, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Precondition != nil || projection.Receipts.IntegrationPluginID != "reaper-plugin" || projection.Receipts.IntegrationVersion != "0.6.0" {
		t.Fatalf("receipts = %+v precondition = %+v", projection.Receipts, projection.Precondition)
	}
	for _, kind := range specialist.SetupJourneyShapeSteps(specialist.SetupJourneyShapeProjectSetup) {
		first := scopes[kind][0]
		if first.IntegrationPluginID != "reaper-plugin" || first.IntegrationVersion != "0.6.0" ||
			first.Shape != specialist.SetupJourneyShapeProjectSetup || first.IntegrationKey != "ori_reaper" {
			t.Fatalf("%s first scope lacked verified integration receipts: %+v", kind, first)
		}
	}
	root, err := store.GetRun(ctx, projection.RunID)
	if err != nil || root.IntegrationPluginID != "reaper-plugin" || root.IntegrationVersion != "0.6.0" {
		t.Fatalf("root receipts were not persisted: %+v err=%v", root, err)
	}
}

// FR 18, FR 28: while the integration is not ready every step is blocked with
// its reason, the run needs attention, the projection names the install quest,
// no step reader runs, and nothing is changed or offered.
func TestPreconditionBlocksEveryStepUntilTheIntegrationIsReady(t *testing.T) {
	ctx := context.Background()
	_, store := openTestStore(t)
	reads := defaultCanonicalReads()
	calls := make(map[specialist.SetupStepKind]int)
	service := aliasService(t, store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, calls, nil))
	integrationAdapter := &syntheticJourneyAdapter{}
	projectAdapter := &syntheticJourneyAdapter{commit: ActionCreateNewProject}
	if err := service.SetActionAdapter(specialist.SetupStepIntegrationInstall, integrationAdapter); err != nil {
		t.Fatal(err)
	}
	if err := service.SetActionAdapter(specialist.SetupStepProjectConnect, projectAdapter); err != nil {
		t.Fatal(err)
	}
	ready, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}

	for kind := range calls {
		delete(calls, kind)
	}
	integration := &IntegrationProjection{
		Key: "ori_reaper", PluginID: "reaper-plugin", Publisher: "Ori", SourceLabel: "johnjallday/reaper-plugin",
		ExpectedVersion: "0.6.0", InstalledVersion: "0.6.0", Enabled: false, ReleaseReady: true, Verified: true,
		ExpectedBlueprintID: "reaper-song", ExpectedProgramID: "music-producer-assistant",
		RequiredHostFeatures: []string{"setup_quests_v2"}, ExpectedProtocol: 1, SupportedPlatforms: []string{"darwin/arm64"},
		StateRevision: Digest([]byte("installed-disabled")),
	}
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{
		BlockedReason: ReasonIntegrationDisabled, AvailableActions: []ActionID{ActionReviewEnable}, Integration: integration,
	}
	blocked, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.RunID != ready.RunID || blocked.Lifecycle != LifecycleNeedsAttention || blocked.CurrentStepID != "project" {
		t.Fatalf("blocked projection = %+v", blocked)
	}
	for _, step := range blocked.Steps {
		if step.Status != StepBlocked || step.ReasonCode != ReasonIntegrationDisabled || len(step.Actions) != 0 || step.Guidance == "" {
			t.Fatalf("step %s was not blocked by the precondition: %+v", step.ID, step)
		}
	}
	precondition := blocked.Precondition
	if precondition == nil || precondition.ReasonCode != ReasonIntegrationDisabled || precondition.InstallQuestID != "install_ori_reaper" ||
		precondition.Guidance == "" || precondition.Integration == nil || precondition.Integration.PluginID != "reaper-plugin" || precondition.Integration.Enabled {
		t.Fatalf("precondition = %+v", precondition)
	}
	if encoded, _ := json.Marshal(blocked); !json.Valid(encoded) {
		t.Fatal("blocked projection does not encode")
	}
	for _, kind := range specialist.SetupJourneyShapeSteps(specialist.SetupJourneyShapeProjectSetup) {
		if calls[kind] != 0 {
			t.Fatalf("step reader %s ran while the precondition was not met", kind)
		}
	}
	if calls[specialist.SetupStepIntegrationInstall] != 1 {
		t.Fatalf("integration read %d times, want once", calls[specialist.SetupStepIntegrationInstall])
	}

	// Nothing is offered or executed while blocked.
	_, err = service.Mutate(ctx, "local", blocked.RunID, ActionReviewNewProject, ActionMutation{
		IfRevision: blocked.StateRevision, IdempotencyKey: "blocked-project", Input: json.RawMessage(`{}`),
	})
	var public *Failure
	if !errors.As(err, &public) || public.ReasonCode != ReasonActionUnavailable || projectAdapter.reviews != 0 {
		t.Fatalf("blocked project review = %v reviews=%d", err, projectAdapter.reviews)
	}
	if _, err := service.Mutate(ctx, "local", blocked.RunID, ActionReviewEnable, ActionMutation{
		IfRevision: blocked.StateRevision, IdempotencyKey: "blocked-enable", Input: json.RawMessage(`{}`),
	}); err == nil || integrationAdapter.reviews != 0 {
		t.Fatalf("the plugin quest offered the install quest's action: %v reviews=%d", err, integrationAdapter.reviews)
	}
	root, err := store.GetRun(ctx, blocked.RunID)
	if err != nil || root.IntegrationPluginID != "reaper-plugin" || root.FirstCompletedAt != nil {
		t.Fatalf("blocked read changed root receipts or history: %+v err=%v", root, err)
	}

	// Re-enabling restores the steps from their own reads.
	reads[specialist.SetupStepIntegrationInstall] = defaultCanonicalReads()[specialist.SetupStepIntegrationInstall]
	restored, err := service.Read(ctx, "local", "")
	if err != nil || restored.Precondition != nil || restored.Steps[0].Status != StepActive || restored.CurrentStepID != "project" {
		t.Fatalf("restored projection = %+v err=%v", restored, err)
	}
}

// Host install and account-link quests have no precondition; only the
// project_setup shape reads the integration first.
func TestPreconditionAppliesOnlyToProjectSetupQuests(t *testing.T) {
	ctx := context.Background()
	service, _, _ := installAliasFixture(t)
	install, err := service.Read(ctx, "local", "")
	if err != nil || install.Journey.ID != "install_ori_reaper" || install.Precondition != nil {
		t.Fatalf("install quest = %+v err=%v", install, err)
	}
	if install.Steps[0].Status != StepActive || install.Steps[0].Kind != specialist.SetupStepIntegrationInstall {
		t.Fatalf("install step = %+v", install.Steps[0])
	}
}
