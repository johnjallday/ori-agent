package templates

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestInstantiateTemplate_RendersFieldsAndPreservesOrchestrationConfig(t *testing.T) {
	manager := NewTemplateManager(t.TempDir())
	manager.templates["release-review"] = &WorkflowTemplate{
		ID:          "release-review",
		Name:        "Release Review",
		Description: "Review release plan for {{.topic}}",
		Parameters: []TemplateParameter{
			{Name: "topic", Type: "string", Required: true},
			{Name: "source", Type: "string", Required: true},
		},
		OrchestrationMode:      workspace.TaskOrchestrationModeGraph,
		ResultCombinationMode:  workspace.TaskResultCombinationStructuredOutput,
		CombinationInstruction: "Combine all {{.topic}} findings into one recommendation.",
		OutputSchema: &workspace.TaskOutputSchema{
			Name:   "release_review",
			Strict: true,
			Fields: []workspace.TaskOutputField{
				{Name: "decision", Type: "string", Required: true},
			},
		},
		Steps: []WorkflowStep{
			{
				ID:          "research",
				Name:        "Research {{.topic}}",
				Description: "Investigate {{.topic}}",
				Details:     "Start with {{.source}}",
				Context: map[string]interface{}{
					"brief": "Use {{.source}} for {{.topic}}",
				},
				OutputSchema: &workspace.TaskOutputSchema{
					Fields: []workspace.TaskOutputField{
						{Name: "summary", Type: "string", Required: true},
					},
				},
			},
		},
	}

	instance, err := manager.InstantiateTemplate("release-review", map[string]interface{}{
		"topic":  "workspace orchestration",
		"source": "internal docs",
	})
	if err != nil {
		t.Fatalf("InstantiateTemplate failed: %v", err)
	}

	if instance.TemplateDescription != "Review release plan for workspace orchestration" {
		t.Fatalf("unexpected rendered description %q", instance.TemplateDescription)
	}
	if instance.OrchestrationMode != workspace.TaskOrchestrationModeGraph {
		t.Fatalf("expected graph orchestration mode, got %q", instance.OrchestrationMode)
	}
	if instance.ResultCombinationMode != workspace.TaskResultCombinationStructuredOutput {
		t.Fatalf("expected structured_outputs combination mode, got %q", instance.ResultCombinationMode)
	}
	if instance.CombinationInstruction != "Combine all workspace orchestration findings into one recommendation." {
		t.Fatalf("unexpected combination instruction %q", instance.CombinationInstruction)
	}
	if instance.OutputSchema == nil || instance.OutputSchema.Name != "release_review" {
		t.Fatalf("expected normalized parent output schema, got %#v", instance.OutputSchema)
	}
	if len(instance.Steps) != 1 {
		t.Fatalf("expected one rendered step, got %d", len(instance.Steps))
	}
	if instance.Steps[0].Name != "Research workspace orchestration" {
		t.Fatalf("unexpected rendered step name %q", instance.Steps[0].Name)
	}
	if instance.Steps[0].Description != "Investigate workspace orchestration" {
		t.Fatalf("unexpected rendered step description %q", instance.Steps[0].Description)
	}
	if instance.Steps[0].Details != "Start with internal docs" {
		t.Fatalf("unexpected rendered step details %q", instance.Steps[0].Details)
	}
	if got := instance.Steps[0].Context["brief"]; got != "Use internal docs for workspace orchestration" {
		t.Fatalf("unexpected rendered step context %v", got)
	}
	if instance.Steps[0].OutputSchema == nil || len(instance.Steps[0].OutputSchema.Fields) != 1 {
		t.Fatalf("expected normalized step output schema, got %#v", instance.Steps[0].OutputSchema)
	}
}
