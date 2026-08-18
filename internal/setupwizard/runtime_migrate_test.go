package setupwizard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func runtimeMigrationContract() *workspace.RuntimeRequirementsContract {
	return &workspace.RuntimeRequirementsContract{
		SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
		OperatingModes: []workspace.RuntimeOperatingMode{
			{ID: "file_only", Label: "File-only", Description: "Use files."},
			{ID: "ori_assisted", Label: "Ori-assisted", Description: "Use runtime.", Requires: []string{"runtime"}},
		},
		Requirements: []workspace.RuntimeRequirement{{Key: "runtime", Label: "Runtime", Description: "Configure it.", Adapter: "reaper_live_control"}},
	}
}

func runtimeMigrationWizard() *workspace.SetupWizard {
	return &workspace.SetupWizard{
		Version: workspace.SetupWizardSchemaVersion,
		Title:   "Set up runtime",
		Steps: []workspace.SetupWizardStep{
			{ID: "mode", Kind: workspace.SetupStepKindRuntimeMode, Required: true},
			{ID: "live-control", Kind: workspace.SetupStepKindRuntimeReadiness, RequirementKey: "runtime", Required: true},
			{ID: "summary", Kind: workspace.SetupStepKindSummary, Required: true},
		},
	}
}

func runtimeMigrationWorkspace() *workspace.Workspace {
	ws := legacyWorkspace("reaper-song")
	ws.Name = "Preserved song"
	ws.Description = "Keep me"
	ws.ProjectPath = "project"
	ws.SharedData = map[string]any{workspace.ProjectEntryPathKey: "Preserved song.rpp"}
	ws.Tasks = []workspace.Task{{ID: "real-work", Description: "Sketch arrangement", Status: workspace.TaskStatusPending}}
	ws.AgentInstances = []workspace.AgentInstance{{ID: "agent-1", Name: "Producer"}}
	provenance := ws.GetTemplateProvenance()
	provenance.TemplateName = "Reaper Song"
	provenance.Version = 7
	provenance.Plugins = []string{"reaper-plugin"}
	provenance.SetupWizard = &workspace.SetupWizard{
		Version: workspace.SetupWizardSchemaVersion,
		Title:   "Old setup",
		Steps: []workspace.SetupWizardStep{
			{ID: "mode", Kind: workspace.SetupStepKindPluginReadiness, RequirementKey: "reaper-plugin", Adapter: "reaper_song", Required: true},
			{ID: "readiness", Kind: workspace.SetupStepKindReadiness, Adapter: "reaper_song", Required: true},
			{ID: "summary", Kind: workspace.SetupStepKindSummary, Adapter: "reaper_song", Required: true},
		},
	}
	ws.SetTemplateProvenance(provenance)
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	opened := created.Add(time.Hour)
	dismissed := opened.Add(time.Hour)
	completed := dismissed.Add(time.Hour)
	ws.SetSetupWizardProgress(&workspace.SetupWizardProgress{
		WizardVersion: 1,
		State:         workspace.SetupWizardStateReady,
		CreatedAt:     created,
		FirstOpenedAt: &opened,
		DismissedAt:   &dismissed,
		CompletedAt:   &completed,
		Steps: []workspace.SetupStepProgress{
			{StepID: "mode", Status: workspace.SetupStepStatusComplete, SelectedOption: "file_only", CompletedAt: &completed},
			{StepID: "readiness", Status: workspace.SetupStepStatusComplete, CompletedAt: &completed},
			{StepID: "summary", Status: workspace.SetupStepStatusComplete, CompletedAt: &completed},
		},
	})
	ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{
		Grants: []workspace.RuntimeCapabilityGrant{{CapabilityKey: "unrelated", AgentInstanceID: "agent-1", GrantedAt: created}},
	})
	return ws
}

func runtimeBlueprint(planner RuntimeMigrationPlanner) Blueprint {
	return Blueprint{
		ID:                  "reaper-song",
		Name:                "Reaper Song",
		Version:             8,
		Wizard:              runtimeMigrationWizard(),
		RuntimeRequirements: runtimeMigrationContract(),
		RuntimeMigration:    planner,
		Plugins:             []string{"reaper-plugin"},
	}
}

type verificationRequiredRuntimeAdapter struct{}

func (verificationRequiredRuntimeAdapter) ID() string { return "reaper_live_control" }
func (verificationRequiredRuntimeAdapter) EvaluateDurable(context.Context, runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	return runtimecapability.DurableResult{
		State:                runtimecapability.DurableConfigured,
		VerificationRequired: true,
		Summary:              "Finish REAPER verification.",
	}, nil
}

func TestRuntimeMigrationProjectsFileOnlyCompleteAndAssistedNeedsVerification(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		wantState string
	}{
		{name: "recorded File-only remains complete", mode: "file_only", wantState: workspace.SetupWizardStateReady},
		{name: "old assisted readiness needs new verification", mode: "ori_assisted", wantState: workspace.SetupWizardStateNeedsAttention},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := runtimeMigrationWorkspace()
			store := &fakeStore{ws: ws}
			service := NewService(store, NewRegistry())
			service.SetBlueprintLookup(func(string) (Blueprint, bool) {
				return runtimeBlueprint(func(RuntimeMigrationInput, *workspace.RuntimeRequirementsContract) RuntimeMigrationPlan {
					return RuntimeMigrationPlan{SelectedModeID: tc.mode}
				}), true
			})
			registry := runtimecapability.NewRegistry()
			if err := registry.Register(verificationRequiredRuntimeAdapter{}); err != nil {
				t.Fatal(err)
			}
			service.SetRuntimeService(runtimecapability.NewService(store, registry))

			status, err := service.Status(context.Background(), ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != tc.wantState || status.AutoOpen {
				t.Fatalf("migrated status = %+v", status)
			}
			if status.Steps[0].SelectedOption != tc.mode {
				t.Fatalf("selected mode = %+v", status.Steps[0])
			}
			if tc.mode == "file_only" {
				if status.Steps[1].Status != workspace.SetupStepStatusComplete {
					t.Fatalf("File-only runtime step = %+v", status.Steps[1])
				}
			} else {
				if status.CurrentStepID != "live-control" || !strings.Contains(status.Steps[1].Summary, "verification") {
					t.Fatalf("assisted runtime step = %+v", status.Steps[1])
				}
				state := store.ws.GetRuntimeState()
				if len(state.RequirementStates) != 1 || state.RequirementStates[0].FirstVerifiedAt != nil {
					t.Fatalf("old readiness became verification proof: %+v", state)
				}
			}
		})
	}
}

func TestRuntimeMigrationAtomicallyPreservesWorkspaceAndSelectsMode(t *testing.T) {
	ws := runtimeMigrationWorkspace()
	before := cloneWorkspace(ws)
	store := &fakeStore{ws: ws}
	service := NewService(store, NewRegistry())
	service.SetBlueprintLookup(func(string) (Blueprint, bool) {
		return runtimeBlueprint(func(RuntimeMigrationInput, *workspace.RuntimeRequirementsContract) RuntimeMigrationPlan {
			return RuntimeMigrationPlan{SelectedModeID: "file_only"}
		}), true
	})
	migrationHooks := 0
	service.SetMigrationHook(func(_ context.Context, _ string) { migrationHooks++ })

	if !service.migrateIfNeeded(ws) {
		t.Fatal("runtime snapshot was not migrated")
	}
	after := store.ws
	provenance := after.GetTemplateProvenance()
	if provenance.Version != 8 || !after.HasRuntimeRequirements() || provenance.SetupWizard.Steps[0].Kind != workspace.SetupStepKindRuntimeMode {
		t.Fatalf("runtime snapshot was not activated: %+v", provenance)
	}
	state := after.GetRuntimeState()
	if state == nil || state.SelectedModeID != "file_only" {
		t.Fatalf("selected mode = %+v", state)
	}
	if len(state.Grants) != 1 || state.Grants[0].CapabilityKey != "unrelated" || len(state.RequirementStates) != 0 {
		t.Fatalf("migration changed grants or invented verification: %+v", state)
	}
	progress := after.GetSetupWizardProgress()
	if progress.CompletedAt == nil || progress.FirstOpenedAt == nil || progress.DismissedAt == nil || !progress.WasMigrated() {
		t.Fatalf("setup history/dismissal was not preserved: %+v", progress)
	}
	if step, ok := progress.Step("mode"); !ok || step.SelectedOption != "file_only" {
		t.Fatalf("mode progress = %+v", progress.Steps)
	}
	if after.Name != before.Name || after.Description != before.Description || len(after.Tasks) != len(before.Tasks) || len(after.AgentInstances) != len(before.AgentInstances) {
		t.Fatal("migration changed workspace identity, tasks, or agents")
	}
	if migrationHooks != 1 {
		t.Fatalf("migration hook calls = %d", migrationHooks)
	}

	writes := store.updates
	if service.migrateIfNeeded(store.ws) {
		t.Fatal("repeated migration changed the workspace")
	}
	if store.updates != writes || migrationHooks != 1 {
		t.Fatalf("repeated migration wrote or re-fired hook: writes %d -> %d, hooks %d", writes, store.updates, migrationHooks)
	}
}

func TestRuntimeMigrationFailurePreservesOldSnapshotAndSurfacesDiagnostic(t *testing.T) {
	ws := runtimeMigrationWorkspace()
	oldWizard := ws.SetupWizardSnapshot()
	store := &fakeStore{ws: ws}
	service := NewService(store, NewRegistry())
	service.SetBlueprintLookup(func(string) (Blueprint, bool) {
		return runtimeBlueprint(func(RuntimeMigrationInput, *workspace.RuntimeRequirementsContract) RuntimeMigrationPlan {
			return RuntimeMigrationPlan{Diagnostic: "The prior mode is ambiguous; review it to continue."}
		}), true
	})

	if !service.migrateIfNeeded(ws) {
		t.Fatal("the diagnostic marker was not persisted")
	}
	provenance := store.ws.GetTemplateProvenance()
	if provenance.Version != 7 || provenance.RuntimeRequirements != nil || provenance.SetupWizard.Title != oldWizard.Title {
		t.Fatalf("failed migration replaced prior data: %+v", provenance)
	}
	progress := store.ws.GetSetupWizardProgress()
	if progress.MigrationDiagnostic == "" || !progress.WasMigrated() {
		t.Fatalf("migration diagnostic not surfaced: %+v", progress)
	}
	writes := store.updates
	if service.migrateIfNeeded(store.ws) || store.updates != writes {
		t.Fatal("an unchanged migration diagnostic must not rewrite on every read")
	}
}

func TestRuntimeMigrationInterruptedWriteRetriesWithoutPartialState(t *testing.T) {
	ws := runtimeMigrationWorkspace()
	store := &fakeStore{ws: ws, failNth: 1}
	service := NewService(store, NewRegistry())
	service.SetBlueprintLookup(func(string) (Blueprint, bool) {
		return runtimeBlueprint(func(RuntimeMigrationInput, *workspace.RuntimeRequirementsContract) RuntimeMigrationPlan {
			return RuntimeMigrationPlan{SelectedModeID: "ori_assisted"}
		}), true
	})

	if service.migrateIfNeeded(ws) {
		t.Fatal("failed store update reported a completed migration")
	}
	if got := store.ws.GetTemplateProvenance(); got.Version != 7 || got.RuntimeRequirements != nil {
		t.Fatalf("interrupted migration left partial snapshot: %+v", got)
	}
	if !service.migrateIfNeeded(store.ws) {
		t.Fatal("migration did not retry after interrupted save")
	}
	if got := store.ws.GetRuntimeState(); got == nil || got.SelectedModeID != "ori_assisted" {
		t.Fatalf("retried migration state = %+v", got)
	}
}

func TestCurrentUnsupportedRuntimeSnapshotFailsClosedWithoutDowngradeRewrite(t *testing.T) {
	ws := runtimeMigrationWorkspace()
	provenance := ws.GetTemplateProvenance()
	provenance.Version = 8
	provenance.SetupWizard = runtimeMigrationWizard()
	unsupported := runtimeMigrationContract()
	unsupported.SchemaVersion = workspace.RuntimeRequirementsSchemaVersion + 1
	provenance.RuntimeRequirements = unsupported
	ws.SetTemplateProvenance(provenance)
	store := &fakeStore{ws: ws}
	service := NewService(store, NewRegistry())
	service.SetBlueprintLookup(func(string) (Blueprint, bool) { return runtimeBlueprint(nil), true })

	status, err := service.Status(context.Background(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Diagnostic == "" || len(status.Steps) != 0 {
		t.Fatalf("unsupported snapshot did not fail closed: %+v", status)
	}
	if store.updates != 0 || store.ws.GetTemplateProvenance().RuntimeRequirements.SchemaVersion != unsupported.SchemaVersion {
		t.Fatal("downgrade read rewrote an unsupported runtime snapshot")
	}
}

func TestRuntimeMigrationRefusesPartialSnapshotWithoutDataLoss(t *testing.T) {
	ws := runtimeMigrationWorkspace()
	provenance := ws.GetTemplateProvenance()
	partial := runtimeMigrationContract()
	partial.SchemaVersion = 99
	provenance.RuntimeRequirements = partial
	ws.SetTemplateProvenance(provenance)
	store := &fakeStore{ws: ws}
	service := NewService(store, NewRegistry())
	service.SetBlueprintLookup(func(string) (Blueprint, bool) {
		return runtimeBlueprint(func(RuntimeMigrationInput, *workspace.RuntimeRequirementsContract) RuntimeMigrationPlan {
			return RuntimeMigrationPlan{SelectedModeID: "file_only"}
		}), true
	})

	if !service.migrateIfNeeded(ws) {
		t.Fatal("partial-snapshot diagnostic was not persisted")
	}
	after := store.ws.GetTemplateProvenance()
	if after.Version != 7 || after.RuntimeRequirements == nil || after.RuntimeRequirements.SchemaVersion != 99 {
		t.Fatalf("partial snapshot was overwritten: %+v", after)
	}
	if store.ws.GetSetupWizardProgress().MigrationDiagnostic == "" {
		t.Fatal("partial snapshot did not surface a repair diagnostic")
	}
}
