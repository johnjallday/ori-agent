package reapersetup

import (
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func legacyMigrationContract() *workspace.RuntimeRequirementsContract {
	return &workspace.RuntimeRequirementsContract{
		SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
		OperatingModes: []workspace.RuntimeOperatingMode{
			{ID: ModeFileOnly, Label: "File-only", Description: "Use files."},
			{ID: ModeOriAssisted, Label: "Ori-assisted", Description: "Use REAPER.", Requires: []string{ReaperLiveControlCapability}},
		},
		Requirements: []workspace.RuntimeRequirement{{Key: ReaperLiveControlCapability, Label: "REAPER", Description: "Control REAPER.", Adapter: ReaperLiveControlCapability}},
	}
}

func legacyMigrationWorkspace() *workspace.Workspace {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Legacy song"})
	ws.ID = "legacy-song"
	ws.ProjectPath = "project"
	ws.SharedData = map[string]any{workspace.ProjectEntryPathKey: "Legacy song.rpp"}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "reaper-song",
		Builtin:    true,
		Version:    7,
		SetupWizard: &workspace.SetupWizard{Version: 1, Title: "Old REAPER setup", Steps: []workspace.SetupWizardStep{
			{ID: "mode", Kind: workspace.SetupStepKindPluginReadiness, RequirementKey: "reaper-plugin", Adapter: SetupAdapterID, Required: true},
		}},
	})
	return ws
}

func migrationInput(ws *workspace.Workspace) setupwizard.RuntimeMigrationInput {
	entry, _ := workspace.GetProjectEntryPath(ws.SharedData)
	return setupwizard.RuntimeMigrationInput{
		ProjectPath: ws.ProjectPath, ProjectEntryPath: entry,
		Provenance: ws.GetTemplateProvenance(), Progress: ws.GetSetupWizardProgress(), RuntimeState: ws.GetRuntimeState(),
	}
}

func TestPlanLegacyRuntimeMigrationUsesRecordedModeWithoutInventingVerification(t *testing.T) {
	for _, mode := range []string{ModeFileOnly, ModeOriAssisted} {
		t.Run(mode, func(t *testing.T) {
			ws := legacyMigrationWorkspace()
			ws.SetSetupWizardProgress(&workspace.SetupWizardProgress{Steps: []workspace.SetupStepProgress{{StepID: "mode", SelectedOption: mode}}})
			plan := PlanLegacyRuntimeMigration(migrationInput(ws), legacyMigrationContract())
			if plan.SelectedModeID != mode || plan.Diagnostic != "" {
				t.Fatalf("plan = %+v", plan)
			}
			if state := ws.GetRuntimeState(); state != nil {
				t.Fatalf("read-only plan changed runtime state: %+v", state)
			}
		})
	}
}

func TestPlanLegacyRuntimeMigrationMapsCompletedInferredOriReadyToAssisted(t *testing.T) {
	ws := legacyMigrationWorkspace()
	completed := time.Now().UTC()
	ws.SetSetupWizardProgress(&workspace.SetupWizardProgress{State: workspace.SetupWizardStateReady, CompletedAt: &completed})
	plan := PlanLegacyRuntimeMigration(migrationInput(ws), legacyMigrationContract())
	if plan.SelectedModeID != ModeOriAssisted || plan.Diagnostic != "" {
		t.Fatalf("completed inferred ori_ready plan = %+v", plan)
	}
}

func TestPlanLegacyRuntimeMigrationFailsClosedOnMissingOrAmbiguousEvidence(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*workspace.Workspace)
		want   string
	}{
		{name: "missing project entry", mutate: func(ws *workspace.Workspace) { delete(ws.SharedData, workspace.ProjectEntryPathKey) }, want: "authoritative REAPER project"},
		{name: "missing prior mode", mutate: func(*workspace.Workspace) {}, want: "File-only or Ori-assisted"},
		{name: "conflicting prior modes", mutate: func(ws *workspace.Workspace) {
			ws.SetSetupWizardProgress(&workspace.SetupWizardProgress{Steps: []workspace.SetupStepProgress{
				{StepID: "one", SelectedOption: ModeFileOnly},
				{StepID: "two", SelectedOption: ModeOriAssisted},
			}})
		}, want: "File-only or Ori-assisted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := legacyMigrationWorkspace()
			before := ws.GetTemplateProvenance()
			tc.mutate(ws)
			plan := PlanLegacyRuntimeMigration(migrationInput(ws), legacyMigrationContract())
			if plan.SelectedModeID != "" || !strings.Contains(plan.Diagnostic, tc.want) {
				t.Fatalf("plan = %+v", plan)
			}
			if after := ws.GetTemplateProvenance(); after.Version != before.Version || after.RuntimeRequirements != nil {
				t.Fatalf("planner mutated provenance: %+v", after)
			}
		})
	}
}
