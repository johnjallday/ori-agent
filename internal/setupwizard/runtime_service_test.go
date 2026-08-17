package setupwizard

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type runtimeSetupStore struct{ ws *workspace.Workspace }

func (s *runtimeSetupStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	if s.ws == nil || s.ws.ID != id {
		return nil, errors.New("not found")
	}
	return s.ws, nil
}
func (s *runtimeSetupStore) Update(id string, mutate func(*workspace.Workspace) error) error {
	if s.ws == nil || s.ws.ID != id {
		return errors.New("not found")
	}
	return mutate(s.ws)
}

type setupRuntimeAdapter struct{ configured bool }

func (a *setupRuntimeAdapter) ID() string { return "fixture_adapter" }
func (a *setupRuntimeAdapter) EvaluateDurable(context.Context, runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	if a.configured {
		return runtimecapability.DurableResult{State: runtimecapability.DurableConfigured, Summary: "Runtime configured."}, nil
	}
	return runtimecapability.DurableResult{State: runtimecapability.DurableInProgress, ReasonCode: "runtime_missing", Summary: "Configure runtime."}, nil
}

func runtimeSetupWorkspace() *workspace.Workspace {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Runtime Setup"})
	ws.ID = "ws-runtime-setup"
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "runtime-fixture",
		RuntimeRequirements: &workspace.RuntimeRequirementsContract{
			SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
			OperatingModes: []workspace.RuntimeOperatingMode{
				{ID: "limited", Label: "Limited", Description: "Use files."},
				{ID: "assisted", Label: "Assisted", Description: "Use runtime.", Requires: []string{"runtime"}},
			},
			Requirements: []workspace.RuntimeRequirement{{Key: "runtime", Label: "Runtime", Description: "Configure it.", Adapter: "fixture_adapter"}},
		},
		SetupWizard: &workspace.SetupWizard{
			Version: workspace.SetupWizardSchemaVersion,
			Title:   "Set up runtime",
			Steps: []workspace.SetupWizardStep{
				{ID: "mode", Kind: workspace.SetupStepKindRuntimeMode, Required: true},
				{ID: "runtime", Kind: workspace.SetupStepKindRuntimeReadiness, RequirementKey: "runtime", Required: true},
				{ID: "summary", Kind: workspace.SetupStepKindSummary, Required: false},
			},
		},
	})
	return ws
}

func TestSetupWizardProjectsAuthoritativeRuntimeModeAndReadiness(t *testing.T) {
	store := &runtimeSetupStore{ws: runtimeSetupWorkspace()}
	runtimeAdapter := &setupRuntimeAdapter{}
	runtimeRegistry := runtimecapability.NewRegistry()
	if err := runtimeRegistry.Register(runtimeAdapter); err != nil {
		t.Fatal(err)
	}
	runtimeService := runtimecapability.NewService(store, runtimeRegistry)
	setupService := NewService(store, NewRegistry())
	setupService.SetRuntimeService(runtimeService)

	initial, err := setupService.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.State != workspace.SetupWizardStateNotStarted || initial.CurrentStepID != "mode" {
		t.Fatalf("initial setup = %+v", initial)
	}
	if len(initial.Steps[0].Options) != 2 || initial.Steps[0].SelectedOption != "" || initial.Steps[0].Action != StepActionConfirm {
		t.Fatalf("runtime mode step = %+v", initial.Steps[0])
	}
	if initial.Steps[1].RuntimeRequirementKey != "runtime" {
		t.Fatalf("runtime readiness did not project its reference: %+v", initial.Steps[1])
	}

	limited, err := setupService.Confirm(context.Background(), store.ws.ID, "mode", StepAction{Type: ActionConfirm, Option: "limited"})
	if err != nil {
		t.Fatal(err)
	}
	if limited.State != workspace.SetupWizardStateReady || limited.Steps[0].SelectedOption != "limited" || limited.Steps[1].Status != workspace.SetupStepStatusComplete {
		t.Fatalf("limited mode should complete without runtime probes: %+v", limited)
	}
	if got := store.ws.GetRuntimeState(); got == nil || got.SelectedModeID != "limited" {
		t.Fatalf("Setup Wizard did not persist authoritative runtime mode: %+v", got)
	}

	// A mode change outside Setup Wizard is authoritative and projects back into
	// its progress rather than being inferred from missing tools or stale choice.
	if _, err := runtimeService.SelectMode(context.Background(), store.ws.ID, "assisted"); err != nil {
		t.Fatal(err)
	}
	assisted, err := setupService.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if assisted.Steps[0].SelectedOption != "assisted" || assisted.Steps[1].Status != workspace.SetupStepStatusBlocked || assisted.State != workspace.SetupWizardStateNeedsAttention {
		t.Fatalf("assisted blocker was not projected: %+v", assisted)
	}
	if progress := store.ws.GetSetupWizardProgress(); progress == nil || progress.StepStatus("mode") != workspace.SetupStepStatusComplete {
		t.Fatalf("runtime projection did not retain setup progress: %+v", progress)
	}

	runtimeAdapter.configured = true
	configured, err := setupService.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if configured.State != workspace.SetupWizardStateReady || configured.Steps[1].Status != workspace.SetupStepStatusComplete || configured.Steps[0].SelectedOption != "assisted" {
		t.Fatalf("configured runtime did not complete setup: %+v", configured)
	}
}

func TestSetupWizardRuntimeModeRejectsUnrecordedOption(t *testing.T) {
	store := &runtimeSetupStore{ws: runtimeSetupWorkspace()}
	runtimeService := runtimecapability.NewService(store, runtimecapability.NewRegistry())
	setupService := NewService(store, NewRegistry())
	setupService.SetRuntimeService(runtimeService)
	if _, err := setupService.Confirm(context.Background(), store.ws.ID, "mode", StepAction{Type: ActionConfirm, Option: "invented"}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("invented runtime mode error = %v", err)
	}
}
