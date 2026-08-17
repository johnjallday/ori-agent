package setupwizard

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func runtimeReferenceProvenance() *workspace.TemplateProvenance {
	return &workspace.TemplateProvenance{
		TemplateID: "runtime-demo",
		RuntimeRequirements: &workspace.RuntimeRequirementsContract{
			SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
			OperatingModes: []workspace.RuntimeOperatingMode{{
				ID:          "assisted",
				Label:       "Assisted",
				Description: "Use live control.",
				Requires:    []string{"runtime"},
			}},
			Requirements: []workspace.RuntimeRequirement{{
				Key:         "runtime",
				Label:       "Runtime",
				Description: "Configure it.",
				Adapter:     "reaper_live_control",
			}},
		},
		SetupWizard: &workspace.SetupWizard{
			Version: 1,
			Title:   "Set up runtime",
			Steps: []workspace.SetupWizardStep{{
				ID:             "runtime",
				Kind:           workspace.SetupStepKindRuntimeReadiness,
				RequirementKey: "runtime",
				Required:       true,
			}},
		},
	}
}

func TestRuntimeRequirementReferenceResolvesOnlyFromPersistedContract(t *testing.T) {
	provenance := runtimeReferenceProvenance()
	ref := workspace.SetupStepReference{Scope: workspace.SetupStepReferenceRuntimeRequirement, Key: "runtime"}
	if !referenceResolves(provenance, ref) {
		t.Fatal("declared runtime requirement should resolve")
	}
	if referenceResolves(provenance, workspace.SetupStepReference{Scope: workspace.SetupStepReferenceRuntimeRequirement, Key: "missing"}) {
		t.Fatal("undeclared runtime requirement must not resolve")
	}

	resolved := resolvedWizard{wizard: provenance.SetupWizard, provenance: provenance}
	request := resolved.request("ws-1", provenance.SetupWizard.Steps[0])
	if request.RuntimeRequirement == nil || request.RuntimeRequirement.Key != "runtime" || request.RuntimeRequirement.Adapter != "reaper_live_control" {
		t.Fatalf("request did not project the persisted runtime requirement: %+v", request.RuntimeRequirement)
	}
	request.RuntimeRequirement.Label = "Changed"
	if provenance.RuntimeRequirements.Requirements[0].Label != "Runtime" {
		t.Fatal("request exposed the persisted runtime requirement by reference")
	}
}

func TestResolveFailsClosedWhenRuntimeReferenceLeavesSnapshot(t *testing.T) {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Runtime"})
	provenance := runtimeReferenceProvenance()
	ws.SetTemplateProvenance(provenance)
	service := &Service{}
	if _, err := service.resolve(ws); err != nil {
		t.Fatalf("valid runtime reference should resolve: %v", err)
	}

	provenance.SetupWizard.Steps[0].RequirementKey = "missing"
	ws.SetTemplateProvenance(provenance)
	if _, err := service.resolve(ws); err == nil {
		t.Fatal("runtime step that leaves the persisted snapshot must fail closed")
	}
}
