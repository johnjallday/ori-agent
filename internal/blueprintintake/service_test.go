package blueprintintake

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type cannedExecutor struct{ output string }

func (e cannedExecutor) ExecuteTask(context.Context, string, workspace.Task) (string, error) {
	return e.output, nil
}

func TestIntakeServiceRunsReviewsAndAppliesThroughTicketService(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Course"})
	ws.ID = "course"
	ws.SharedData = map[string]any{"entry_agent_name": "Course Manager"}
	ws.AgentInstances = []workspace.AgentInstance{{ID: "manager", Name: "Course Manager", NodeID: "manager-node", EntryPoint: true}}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{IntakeRequirements: []workspace.IntakeRequirement{{
		Key: "materials", Skill: "syllabus-intake", Sources: workspace.IntakeSources{Files: true},
		AcceptedExtensions: []string{".txt"}, ProposalKinds: []string{"ticket"},
	}}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	sources := NewSourceService(store, store)
	sources.SetProviderResolver(func(context.Context, string) (ModelProvider, error) { return ModelProvider{Name: "openai"}, nil })
	if _, err := sources.AddFile(context.Background(), ws.ID, "materials", "schedule.txt", strings.NewReader("Quiz 1 is due 2026-10-01")); err != nil {
		t.Fatal(err)
	}
	if _, err := sources.AcceptConsent(context.Background(), ws.ID, "materials", "local"); err != nil {
		t.Fatal(err)
	}
	runner := NewIntakeRunner(store, sources, runnerSkillCatalog{skill: &skills.Skill{Name: "syllabus-intake", Prompt: "extract dates", Enabled: true, Trusted: true}}, cannedExecutor{output: `{"items":[{"kind":"ticket","key":"quiz-1","title":"Quiz 1","due_at":"2026-10-01","source":{"source_id":"SOURCE","quote":"Quiz 1 is due 2026-10-01"}}]}`})
	// The canned output needs the host-generated source id, just as a real prompt does.
	records, err := sources.ListSources(ws.ID, "materials")
	if err != nil || len(records) != 1 {
		t.Fatalf("sources = %+v, %v", records, err)
	}
	runner.executor = cannedExecutor{output: strings.ReplaceAll(cannedExecutor{output: `{"items":[{"kind":"ticket","key":"quiz-1","title":"Quiz 1","due_at":"2026-10-01","source":{"source_id":"SOURCE","quote":"Quiz 1 is due 2026-10-01"}}]}`}.output, "SOURCE", records[0].ID)}
	proposals := NewProposalStore(store)
	service := NewService(sources, runner, proposals, NewApplyService(proposals, workspace.NewTicketService(store)))
	if _, err := service.Start(ws.ID, "materials", time.UTC); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var run RunStatus
	for time.Now().Before(deadline) {
		run, err = service.Status(ws.ID, "materials")
		if err == nil && run.State != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.State != "done" || run.Proposal == nil || len(run.Proposal.Items) != 1 {
		t.Fatalf("run = %+v", run)
	}
	applied, err := service.Apply(ws.ID, "materials", ApplyRequest{ProposalHash: run.Proposal.Hash, Items: []ApplyChoice{{Key: "quiz-1", Selected: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Results) != 1 || applied.Results[0].Status != "created" {
		t.Fatalf("apply = %+v", applied)
	}
	reloaded, err := store.GetFolderWorkspace(ws.ID)
	if err != nil || len(reloaded.Tasks) != 1 || reloaded.Tasks[0].DueDate == nil || reloaded.Tasks[0].SourceType != workspace.TicketSourceBlueprintIntake {
		t.Fatalf("ticket was not created through the canonical store: %+v, %v", reloaded, err)
	}
}
