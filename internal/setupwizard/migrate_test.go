package setupwizard

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// legacyWorkspace is a workspace as it exists before this feature: created from
// a built-in blueprint, with provenance recorded and no wizard anywhere.
func legacyWorkspace(templateID string) *workspace.Workspace {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Existing"})
	ws.ID = "ws-1"
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:   templateID,
		TemplateName: "Downloads Janitor",
		Builtin:      true,
		Version:      1,
	})
	return ws
}

// migrationWizard mirrors the shipped manifests, where every required step is
// adapter-backed. That matters here: a step with no adapter has no external
// truth to check, so only the user can complete it — and a workspace that was
// configured by hand long ago would sit forever on a consent step it can never
// pass by itself.
func migrationWizard() *workspace.SetupWizard {
	return &workspace.SetupWizard{
		Version: workspace.SetupWizardSchemaVersion,
		Title:   "Set up Downloads Janitor",
		Steps: []workspace.SetupWizardStep{
			{ID: "folder", Kind: workspace.SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true, Adapter: "downloads_janitor"},
			{ID: "automation", Kind: workspace.SetupStepKindAutomationReview, RequirementKey: "downloads-root", Required: true, Adapter: "downloads_janitor"},
			{ID: "summary", Kind: workspace.SetupStepKindSummary, Required: false, Adapter: "downloads_janitor"},
		},
	}
}

// blueprintLibrary is the lookup a real server backs with the template library.
func blueprintLibrary(id string) BlueprintLookup {
	return func(templateID string) (Blueprint, bool) {
		if templateID != id {
			return Blueprint{}, false
		}
		return Blueprint{
			ID:      id,
			Name:    "Downloads Janitor",
			Version: 2,
			Wizard:  migrationWizard(),
			DirectoryRequirements: []workspace.DirectoryRequirement{
				{Key: "downloads-root", Label: "Downloads folder", SuggestedPath: "~/Downloads", AccessDisclosure: "Ori lists files here."},
			},
			AutomationRecipes: []workspace.AutomationRecipe{
				{DirectoryKey: "downloads-root", Watch: &workspace.WatchRecipe{Events: []string{"create"}}},
			},
		}, true
	}
}

// migrationService wires a legacy workspace, a library that offers it a wizard,
// and whatever adapter verdict the case under test needs.
func migrationService(t *testing.T, ws *workspace.Workspace, lookup BlueprintLookup, adapters ...Adapter) (*Service, *fakeStore) {
	t.Helper()
	store := &fakeStore{ws: ws}
	registry := NewRegistry()
	for _, adapter := range adapters {
		if err := registry.Register(adapter); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	service := NewService(store, registry)
	service.SetBlueprintLookup(lookup)
	return service, store
}

// TestMigration_BackfillsOnFirstLookAndOnlyOnce is the core of the backfill:
// an existing workspace gains the wizard its blueprint has since declared, from
// one snapshot, and looking again changes nothing.
func TestMigration_BackfillsOnFirstLookAndOnlyOnce(t *testing.T) {
	service, store := migrationService(t, legacyWorkspace("downloads-janitor"),
		blueprintLibrary("downloads-janitor"),
		&fakeAdapter{id: "downloads_janitor"})

	status, err := service.Status(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Applicable {
		t.Fatalf("the workspace did not gain its blueprint's wizard: %+v", status)
	}
	if status.BlueprintID != "downloads-janitor" || len(status.Steps) != 3 {
		t.Fatalf("unexpected migrated wizard: %+v", status)
	}
	// The requirement its steps reference came along, or the steps would not
	// resolve at all.
	provenance := store.ws.GetTemplateProvenance()
	if len(provenance.DirectoryRequirements) != 1 {
		t.Fatalf("the referenced requirement was not snapshotted: %+v", provenance)
	}
	if provenance.Version != 2 {
		t.Fatalf("provenance version = %d, want the blueprint's 2", provenance.Version)
	}
	if !store.ws.GetSetupWizardProgress().WasMigrated() {
		t.Fatal("a backfilled workspace must be marked as one")
	}

	writes := store.updates
	for i := 0; i < 3; i++ {
		if _, err := service.Status(context.Background(), "ws-1"); err != nil {
			t.Fatalf("Status: %v", err)
		}
	}
	if store.updates != writes {
		t.Fatalf("repeated reads wrote %d more times; migration must be idempotent", store.updates-writes)
	}
	if len(store.ws.GetTemplateProvenance().DirectoryRequirements) != 1 {
		t.Fatal("repeated migration duplicated the workspace's requirements")
	}
}

// TestMigration_NeverAutoOpens is the difference between a workspace someone
// just created and one they have had for months. The second is in the middle of
// being used; setup arriving as a modal over it is an ambush.
func TestMigration_NeverAutoOpens(t *testing.T) {
	service, _ := migrationService(t, legacyWorkspace("downloads-janitor"),
		blueprintLibrary("downloads-janitor"),
		&fakeAdapter{id: "downloads_janitor"})

	status, err := service.Status(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.AutoOpen {
		t.Fatalf("a backfilled workspace must not open the dialog by itself: %+v", status)
	}
	// It is still visibly unfinished, and still resumable — the banner is the
	// entry point, and it points at the first outstanding step.
	if status.State == workspace.SetupWizardStateReady {
		t.Fatal("nothing was configured, so nothing is ready")
	}
	if status.CurrentStepID != "folder" {
		t.Fatalf("current step = %q, want the first unresolved one", status.CurrentStepID)
	}
}

// TestMigration_HealthyWorkspaceIsReadyWithoutBeingAsked covers FR-120: someone
// who set this workspace up by hand, long ago, must not be asked to do it again
// — and must not have their setup help task reopened.
func TestMigration_HealthyWorkspaceIsReadyWithoutBeingAsked(t *testing.T) {
	completions := 0
	service, store := migrationService(t, legacyWorkspace("downloads-janitor"),
		blueprintLibrary("downloads-janitor"),
		&fakeAdapter{id: "downloads_janitor", ready: true})
	service.SetCompletionHook(func(context.Context, string) { completions++ })

	status, err := service.Status(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != workspace.SetupWizardStateReady {
		t.Fatalf("a healthy workspace migrates straight to ready: %+v", status)
	}
	if status.AutoOpen {
		t.Fatal("a ready workspace must never auto-open")
	}
	if status.CurrentStepID != "" {
		t.Fatalf("nothing is outstanding, so there is no step to resume at: %q", status.CurrentStepID)
	}
	// The hook fires once — it completes the blueprint's help task, and a
	// second firing on every page load would keep rewriting it.
	if completions != 1 {
		t.Fatalf("completion hook fired %d times, want exactly 1", completions)
	}
	if _, err := service.Status(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if completions != 1 {
		t.Fatalf("completion hook fired again on a later read (%d)", completions)
	}
	if store.ws.GetSetupWizardProgress().CompletedAt == nil {
		t.Fatal("a migrated-ready workspace records when it was found complete")
	}
}

// TestMigration_BrokenSetupAsksForRepairNotSetup is FR-122. The two states look
// the same to a naive implementation — neither is ready — but they say opposite
// things to a user: "finish setting this up" versus "something you already had
// has stopped working".
func TestMigration_BrokenSetupAsksForRepairNotSetup(t *testing.T) {
	cases := []struct {
		name     string
		category string
		want     string
	}{
		{
			name:     "a revoked permission is evidence it was granted once",
			category: ErrorCategoryPermissionRequired,
			want:     workspace.SetupWizardStateNeedsAttention,
		},
		{
			name:     "a failing domain call is evidence something is there to fail",
			category: ErrorCategoryDomainError,
			want:     workspace.SetupWizardStateNeedsAttention,
		},
		{
			// Not needs_attention: there is nothing to repair, only setup to do.
			name:     "not_configured is the signature of a workspace nobody set up",
			category: ErrorCategoryNotConfigured,
			want:     workspace.SetupWizardStateNotStarted,
		},
		{
			// Blocked, but not evidence about the user: the build cannot answer
			// the question, so the workspace is unfinished rather than broken.
			name:     "an unwired adapter says nothing about the user's setup",
			category: ErrorCategoryUnavailable,
			want:     workspace.SetupWizardStateInProgress,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &categoryAdapter{id: "downloads_janitor", category: tc.category}
			service, _ := migrationService(t, legacyWorkspace("downloads-janitor"),
				blueprintLibrary("downloads-janitor"), adapter)

			status, err := service.Status(context.Background(), "ws-1")
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if status.State != tc.want {
				t.Fatalf("state = %q, want %q (%+v)", status.State, tc.want, status)
			}
			if status.AutoOpen {
				t.Fatal("neither state opens itself on a workspace already in use")
			}
			// Either way the workspace keeps working and nothing was reset.
			if status.CurrentStepID == "" {
				t.Fatal("an unfinished workspace must offer somewhere to resume")
			}
		})
	}
}

// TestMigration_LeavesEverythingElseAlone is FR-127 plus the identification rule
// in FR-118: only a workspace that recorded the blueprint gets its wizard, and
// identification never comes from a name.
func TestMigration_LeavesEverythingElseAlone(t *testing.T) {
	cases := []struct {
		name      string
		workspace func() *workspace.Workspace
	}{
		{
			name: "a plain workspace the user made themselves",
			workspace: func() *workspace.Workspace {
				ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Downloads Janitor"})
				ws.ID = "ws-1"
				return ws
			},
		},
		{
			name: "a workspace from an unrelated blueprint",
			workspace: func() *workspace.Workspace {
				ws := legacyWorkspace("personal-hq")
				return ws
			},
		},
		{
			name: "provenance with no template id to trust",
			workspace: func() *workspace.Workspace {
				ws := legacyWorkspace("")
				return ws
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := tc.workspace()
			service, store := migrationService(t, ws, blueprintLibrary("downloads-janitor"),
				&fakeAdapter{id: "downloads_janitor"})

			status, err := service.Status(context.Background(), "ws-1")
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if status.Applicable || status.State != workspace.SetupWizardStateNotApplicable {
				t.Fatalf("this workspace must be left alone: %+v", status)
			}
			if store.updates != 0 {
				t.Fatalf("an ineligible workspace was written to %d times", store.updates)
			}
		})
	}
}

// TestMigration_NeverReplacesWhatTheWorkspaceAlreadyRecorded protects the two
// ways a backfill could destroy state: overwriting a wizard whose steps the user
// has already answered, and re-pointing requirements their existing setup was
// done against.
func TestMigration_NeverReplacesWhatTheWorkspaceAlreadyRecorded(t *testing.T) {
	ws := legacyWorkspace("downloads-janitor")
	provenance := ws.GetTemplateProvenance()
	provenance.SetupWizard = migrationWizard()
	provenance.DirectoryRequirements = []workspace.DirectoryRequirement{
		{Key: "downloads-root", Label: "The folder they actually chose", SuggestedPath: "~/Old", AccessDisclosure: "d"},
	}
	ws.SetTemplateProvenance(provenance)
	ws.SetSetupWizardProgress(&workspace.SetupWizardProgress{
		WizardVersion: workspace.SetupWizardSchemaVersion,
		State:         workspace.SetupWizardStateInProgress,
		Steps: []workspace.SetupStepProgress{
			{StepID: "folder", Status: workspace.SetupStepStatusComplete, SelectedOption: "keep"},
		},
	})

	service, store := migrationService(t, ws, blueprintLibrary("downloads-janitor"),
		&fakeAdapter{id: "downloads_janitor"})
	if _, err := service.Status(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}

	after := store.ws.GetTemplateProvenance()
	if after.DirectoryRequirements[0].Label != "The folder they actually chose" {
		t.Fatalf("the workspace's own requirement was replaced: %+v", after.DirectoryRequirements[0])
	}
	if store.ws.GetSetupWizardProgress().WasMigrated() {
		t.Fatal("a workspace that already had a wizard was marked as backfilled")
	}
	// The step's status is re-derived from the adapter every time — that is the
	// server deciding readiness, not migration discarding it. What migration
	// must not touch is the answer the user gave.
	if step, _ := store.ws.GetSetupWizardProgress().Step("folder"); step.SelectedOption != "keep" {
		t.Fatalf("the recorded answer was lost: %+v", step)
	}
}

// TestMigration_TouchesOnlyTheSnapshotAndItsMarker is the no-side-effects
// requirement (FR-123/124/125) stated as an assertion about the record: after a
// backfill, everything a user could recognize as their workspace is byte-for-byte
// what it was. Nothing was chosen, installed, granted, registered, or seeded,
// because a migration that could do any of those would have to write it here.
func TestMigration_TouchesOnlyTheSnapshotAndItsMarker(t *testing.T) {
	ws := legacyWorkspace("downloads-janitor")
	ws.Description = "Kept"
	ws.Tags = []string{"tidy"}
	before := cloneWorkspace(ws)

	service, store := migrationService(t, ws, blueprintLibrary("downloads-janitor"),
		&fakeAdapter{id: "downloads_janitor"})
	if _, err := service.Status(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}

	after := store.ws
	if after.Name != before.Name || after.Description != before.Description {
		t.Fatal("migration changed the workspace's identity")
	}
	if len(after.Tags) != len(before.Tags) {
		t.Fatal("migration changed the workspace's tags")
	}
	if len(after.Tasks) != len(before.Tasks) {
		t.Fatalf("migration changed tasks: %d -> %d", len(before.Tasks), len(after.Tasks))
	}
	if len(after.MCPBindings) != len(before.MCPBindings) {
		t.Fatal("migration changed the workspace's MCP bindings")
	}
	if len(after.DirectoryReferences) != len(before.DirectoryReferences) {
		t.Fatal("migration gave the workspace a folder it never chose")
	}
	if len(after.ScheduledTasks) != len(before.ScheduledTasks) {
		t.Fatal("migration registered automation nobody asked for")
	}
	if len(after.AgentInstances) != len(before.AgentInstances) {
		t.Fatal("migration changed the workspace's agents")
	}
	// The one requirement it may fill in is the one the wizard's steps
	// reference, and it may only fill it in because the workspace had none.
	if len(after.GetTemplateProvenance().DirectoryRequirements) != 1 {
		t.Fatal("the referenced requirement should have been snapshotted")
	}
}

// TestMigration_EvaluatesWithoutMutating pins that the first look at a migrated
// workspace asks its adapters what is true and never tells them to do anything.
func TestMigration_EvaluatesWithoutMutating(t *testing.T) {
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, _ := migrationService(t, legacyWorkspace("downloads-janitor"),
		blueprintLibrary("downloads-janitor"), adapter)

	if _, err := service.Status(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if adapter.evals == 0 {
		t.Fatal("the migrated workspace was never evaluated")
	}
	if adapter.confirms != 0 {
		t.Fatalf("migration confirmed %d steps; it must only look", adapter.confirms)
	}
}

// TestMigration_StaleWriteCannotLoseTheSnapshot exercises the store shape that
// has broken this kind of write before: Update hands back a copy with the
// workspace.json-only fields missing. A backfill written into that copy and then
// re-hydrated from the stale record would vanish on the next read.
func TestMigration_StaleWriteCannotLoseTheSnapshot(t *testing.T) {
	service, store := migrationService(t, legacyWorkspace("downloads-janitor"),
		blueprintLibrary("downloads-janitor"),
		&fakeAdapter{id: "downloads_janitor", ready: true})

	if _, err := service.Status(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	reread, err := store.GetFolderWorkspace("ws-1")
	if err != nil {
		t.Fatalf("GetFolderWorkspace: %v", err)
	}
	if reread.GetTemplateProvenance().SetupWizard.IsEmpty() {
		t.Fatal("the backfilled snapshot did not survive the round trip")
	}
	if reread.GetSetupWizardProgress().State != workspace.SetupWizardStateReady {
		t.Fatalf("derived state did not survive: %+v", reread.GetSetupWizardProgress())
	}
}

// TestMigration_BlueprintWithoutAWizardChangesNothing covers the ordinary case
// for every blueprint this feature does not migrate.
func TestMigration_BlueprintWithoutAWizardChangesNothing(t *testing.T) {
	service, store := migrationService(t, legacyWorkspace("some-other-blueprint"),
		func(string) (Blueprint, bool) { return Blueprint{}, false },
		&fakeAdapter{id: "downloads_janitor"})

	status, err := service.Status(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Applicable {
		t.Fatalf("a blueprint with no wizard must not gain one: %+v", status)
	}
	if store.updates != 0 {
		t.Fatal("nothing should have been written")
	}
}

// TestMigration_MarkerSurvivesLaterProgress guards a subtle regression: the
// migrated marker is what keeps the dialog from auto-opening, so a later write
// must not drop it.
func TestMigration_MarkerSurvivesLaterProgress(t *testing.T) {
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, store := migrationService(t, legacyWorkspace("downloads-janitor"),
		blueprintLibrary("downloads-janitor"), adapter)

	if _, err := service.Status(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := service.Dismiss(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if _, err := service.Open(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	progress := store.ws.GetSetupWizardProgress()
	if !progress.WasMigrated() {
		t.Fatal("the migrated marker was dropped by a later write")
	}
	if progress.MigratedAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("migrated_at is in the future: %v", progress.MigratedAt)
	}
}

// categoryAdapter blocks with one chosen safe category, which is the only
// signal a backfill has for telling "never set up" from "was set up and broke".
type categoryAdapter struct {
	id       string
	category string
}

func (a *categoryAdapter) ID() string { return a.id }

func (a *categoryAdapter) Evaluate(context.Context, StepRequest) (StepReadiness, error) {
	return StepReadiness{
		Blocked:       a.category != ErrorCategoryNotConfigured,
		Summary:       "Not usable right now.",
		ErrorCategory: a.category,
	}, nil
}

func (a *categoryAdapter) Confirm(context.Context, StepRequest, StepAction) (StepReadiness, error) {
	return StepReadiness{}, nil
}
