package setupwizard

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestResolve_IntakeReferenceComesFromWorkspaceSnapshot(t *testing.T) {
	provenance := &workspace.TemplateProvenance{
		IntakeRequirements: []workspace.IntakeRequirement{{Key: "materials", Label: "Materials", Skill: "syllabus", Sources: workspace.IntakeSources{Files: true}, ProposalKinds: []string{"ticket"}}},
	}
	ref := workspace.SetupStepReference{Scope: workspace.SetupStepReferenceIntake, Key: "materials"}
	if !referenceResolves(provenance, ref) {
		t.Fatal("snapshotted intake reference did not resolve")
	}
	resolved := resolvedWizard{provenance: provenance}
	req := resolved.request("workspace-1", workspace.SetupWizardStep{Kind: workspace.SetupStepKindIntake, RequirementKey: "materials"})
	if req.Intake == nil || req.Intake.Label != "Materials" {
		t.Fatalf("intake request = %+v", req.Intake)
	}
}

func TestAdapterFor_IntakeUsesCompiledDefault(t *testing.T) {
	registry := NewRegistry()
	adapter := &fakeAdapter{id: "blueprint_intake"}
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, registry)
	got, err := service.adapterFor(workspace.SetupWizardStep{ID: "materials", Kind: workspace.SetupStepKindIntake})
	if err != nil {
		t.Fatal(err)
	}
	if got != adapter {
		t.Fatalf("adapterFor(intake) = %#v, want compiled blueprint_intake adapter", got)
	}
}
