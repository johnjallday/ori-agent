package setupjourney

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// restartReads has the integration installed and the Home, project and team
// already set up, so only the summary is left for a fresh root.
func restartReads() map[specialist.SetupStepKind]CanonicalStepRead {
	reads := defaultCanonicalReads()
	reads[specialist.SetupStepProjectConnect] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{HomeWorkspaceID: "workspace-home", ProjectWorkspaceID: "workspace-project"},
	}
	reads[specialist.SetupStepWorkspaceSetup] = CanonicalStepRead{
		Complete: true, Result: CanonicalResult{SelectedModeID: "file_only"},
	}
	reads[specialist.SetupStepAssistantProgramStaffing] = CanonicalStepRead{Complete: true}
	return reads
}

// restartService serves the alias with the plugin's quest at declaration
// version 2, the version a pre-split five-step root cannot migrate to.
func restartService(t *testing.T, store Store, reads map[specialist.SetupStepKind]CanonicalStepRead) *Service {
	t.Helper()
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, readerRegistryStub(t, reads, nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	current := questPluginFixture(t)
	current.WorkspaceSurfaces.SetupQuests[0].Version = 2
	service.SetQuestCatalog(standardQuestCatalog(&questPlugins{items: []plugin.InstalledPlugin{current}}))
	return service
}

// seedRetiredFiveStepRoot rewrites the service's root into the record a
// setup_quests_v1 plugin left behind: declaration version 1 with five steps,
// the first of them the install step.
func seedRetiredFiveStepRoot(t *testing.T, service *Service, store *SQLiteStore) string {
	t.Helper()
	ctx := context.Background()
	created, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	states, err := encodeStepStates([]StepState{
		{StepID: "integration", Status: StepComplete}, {StepID: "project", Status: StepComplete},
		{StepID: "workspace", Status: StepComplete}, {StepID: "staffing", Status: StepActive},
		{StepID: "summary", Status: StepPending},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE setup_journey_run
		SET declaration_version = 1, step_states_json = ?, current_step_id = 'staffing'
		WHERE id = ?
	`, states, created.RunID); err != nil {
		t.Fatal(err)
	}
	return created.RunID
}

// FR 36/39: a user whose saved progress is on the retired five-step layout
// starts over onto a fresh four-step root, and the Home, project and team they
// already have read as complete again, so the fresh root is ready at once.
func TestServiceRestartReplacesAnIncompatibleRootWithAFreshFourStepRoot(t *testing.T) {
	ctx := context.Background()
	_, store := openTestStore(t)
	service := restartService(t, store, restartReads())
	oldRunID := seedRetiredFiveStepRoot(t, service, store)

	incompatible, err := service.Read(ctx, "local", "")
	if err != nil || !incompatible.DeclarationIncompatible || incompatible.RunID != oldRunID {
		t.Fatalf("seeded root is not incompatible: %#v err=%v", incompatible, err)
	}

	restarted, err := service.Restart(ctx, "local")
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if restarted.DeclarationIncompatible || restarted.RunID == oldRunID || restarted.RunKind != RunKindRoot ||
		restarted.Journey.ID != "reaper_setup" || restarted.Journey.Version != 2 || len(restarted.Steps) != 4 {
		t.Fatalf("restart did not create a fresh four-step root: %#v", restarted)
	}
	for index, want := range []StepStatus{StepComplete, StepComplete, StepComplete} {
		if restarted.Steps[index].Status != want {
			t.Fatalf("step %s = %s; want %s", restarted.Steps[index].ID, restarted.Steps[index].Status, want)
		}
	}
	if restarted.Lifecycle != LifecycleReady || restarted.Steps[3].ID != "summary" {
		t.Fatalf("fresh root with existing resources is not ready: %#v", restarted)
	}
	if _, err := store.GetRun(ctx, oldRunID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old root still reads: %v", err)
	}
	var roots int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM setup_journey_run WHERE run_kind = 'root'`).Scan(&roots); err != nil || roots != 1 {
		t.Fatalf("roots after restart = %d err=%v", roots, err)
	}

	// A second restart is refused: the fresh root is compatible.
	if _, err := service.Restart(ctx, "local"); !hasReason(err, ReasonActionUnavailable) {
		t.Fatalf("second restart error = %v", err)
	}
}

// Opening or dismissing the modal on an incompatible root must not reconcile
// it onto the current declaration, or Start over would never be shown.
func TestServicePresentationLeavesAnIncompatibleRootIncompatible(t *testing.T) {
	ctx := context.Background()
	_, store := openTestStore(t)
	service := restartService(t, store, restartReads())
	runID := seedRetiredFiveStepRoot(t, service, store)
	incompatible, err := service.Read(ctx, "local", "")
	if err != nil || !incompatible.DeclarationIncompatible {
		t.Fatalf("seed = %#v err=%v", incompatible, err)
	}
	for name, mutate := range map[string]func(context.Context, string, string, PresentationMutation) (*JourneyProjection, error){
		"open": service.Open, "dismiss": service.Dismiss,
	} {
		projection, err := mutate(ctx, "local", "", PresentationMutation{
			IfRevision: incompatible.StateRevision, IdempotencyKey: name + "-incompatible",
		})
		if err != nil || !projection.DeclarationIncompatible || projection.RunID != runID {
			t.Fatalf("%s = %#v err=%v", name, projection, err)
		}
		stored, err := store.GetRun(ctx, runID)
		if err != nil || stored.DeclarationVersion != 1 || stored.StateRevision != incompatible.StateRevision {
			t.Fatalf("%s rewrote the incompatible record: %#v err=%v", name, stored, err)
		}
	}
}

// FR 36: Start over is offered only for an incompatible root. A compatible
// root and a user-template quest are refused and keep their progress.
func TestServiceRestartRefusesCompatibleRootsAndUserTemplateQuests(t *testing.T) {
	ctx := context.Background()
	_, store := openTestStore(t)
	service := restartService(t, store, restartReads())
	compatible, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Restart(ctx, "local"); !hasReason(err, ReasonActionUnavailable) {
		t.Fatalf("compatible restart error = %v", err)
	}
	if _, err := store.GetRun(ctx, compatible.RunID); err != nil {
		t.Fatalf("refused restart removed the root: %v", err)
	}

	template, library := userQuestCatalogFixture(t)
	userService := restartService(t, store, restartReads())
	userService.SetQuestCatalog(CombineQuestCatalogs(
		standardQuestCatalog(&questPlugins{items: []plugin.InstalledPlugin{questPluginFixture(t)}}),
		NewUserTemplateQuestCatalog(library, &questPlugins{items: []plugin.InstalledPlugin{questPluginFixture(t)}}),
	))
	scoped, err := userService.ForUserTemplateQuest(ctx, "local", template.ID, template.UserSetupQuest.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.Restart(ctx, "local"); !hasReason(err, ReasonActionUnavailable) {
		t.Fatalf("user-template restart error = %v", err)
	}

	var nilService *Service
	if _, err := nilService.Restart(ctx, "local"); !hasReason(err, ReasonJourneyUnavailable) {
		t.Fatalf("nil service restart error = %v", err)
	}
}

func hasReason(err error, reason ReasonCode) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.ReasonCode == reason
}
