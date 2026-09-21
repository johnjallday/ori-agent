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
func NewDemoIntakeRunner(workspaces WorkspaceReader, sources *SourceService, skillCatalog SkillCatalog) *IntakeRunner {
	if skillCatalog == nil {
		skillCatalog = demoSkillCatalog{}
	}
	return NewIntakeRunner(workspaces, sources, skillCatalog, demoTaskExecutor{})
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
		{"kind": "memory", "key": "late-policy", "text": "Late work is accepted for up to two days with a 10% deduction per day.", "memory_type": "fact", "source": map[string]string{"source_id": sourceID, "quote": "Late work: two days, with 10% deducted per day."}},
		{"kind": "note", "key": "course-outline", "title": "Course outline", "body": "The course includes weekly lectures, quizzes, problem sets, and a final project.", "source": map[string]string{"source_id": sourceID, "quote": "Weekly lectures, quizzes, problem sets, and a final project."}},
		{"kind": "calendar_event", "key": "quiz-1-event", "title": "Quiz 1", "all_day": "2026-10-01", "source": map[string]string{"source_id": sourceID, "quote": "Quiz 1 is due October 1."}},
		{"kind": "calendar_event", "key": "project-milestone-event", "title": "Project milestone", "start": "2026-10-15T17:00:00-04:00", "end": "2026-10-15T18:00:00-04:00", "source": map[string]string{"source_id": sourceID, "quote": "Project milestone: October 15 at 5 PM."}},
	}}
	data, err := json.Marshal(payload)
	return string(data), err
}
