package blueprintintake

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// NewDemoIntakeRunner returns a deterministic provider-free runner for isolated
// demos only. Server wiring requires ORI_DEV_BLUEPRINT_INTAKE_STUB=1; normal
// builds never select it.
func NewDemoIntakeRunner(workspaces WorkspaceReader, sources *SourceService) *IntakeRunner {
	return NewIntakeRunner(workspaces, sources, demoSkillCatalog{}, demoTaskExecutor{})
}

type demoSkillCatalog struct{}

func (demoSkillCatalog) GetSkill(_, skillName string) (*skills.Skill, bool, error) {
	if strings.TrimSpace(skillName) == "" {
		return nil, false, nil
	}
	return &skills.Skill{Name: skillName, Description: "Provider-free Blueprint Intake demo skill", Prompt: "Return the host-requested proposal JSON only.", Enabled: true, Trusted: true}, true, nil
}

type demoTaskExecutor struct{}

func (demoTaskExecutor) ExecuteTask(_ context.Context, _ string, task workspace.Task) (string, error) {
	marker := `"source_id":"`
	start := strings.Index(task.Details, marker)
	if start < 0 {
		return "", errors.New("demo task has no source id")
	}
	start += len(marker)
	end := strings.Index(task.Details[start:], `"`)
	if end < 0 {
		return "", errors.New("demo task has an invalid source id")
	}
	sourceID := task.Details[start : start+end]
	payload := map[string]any{"items": []map[string]any{
		{"kind": "ticket", "key": "quiz-1", "title": "Quiz 1", "due_at": "2026-10-01", "source": map[string]string{"source_id": sourceID, "quote": "Quiz 1 is due October 1."}},
		{"kind": "ticket", "key": "project-milestone", "title": "Project milestone", "due_at": "2026-10-15T17:00:00-04:00", "source": map[string]string{"source_id": sourceID, "quote": "Project milestone: October 15 at 5 PM."}},
	}}
	data, err := json.Marshal(payload)
	return string(data), err
}
