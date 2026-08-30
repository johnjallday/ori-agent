package workspace

import (
	"errors"
	"sync"
	"testing"
)

func suggestionFixture(t *testing.T) (Store, *Workspace, *Workspace, *AssistantLearningStore, AssistantLearningDocument) {
	t.Helper()
	store, station, learnings := reflectionFixture(t)
	state := station.GetAssistantProgramState()
	project, err := store.Get(state.LinkedProjectIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(current *Workspace) error {
		program := current.GetAssistantProgramState()
		program.StageID = "collaborator"
		program.Level = 2
		program.PrimaryName = "Guide"
		program.Declaration.SuggestionRequiredCapabilities = []string{"live_control"}
		current.SetAssistantProgramState(program)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	document, err := learnings.AddCandidates(station.ID, 0, []AssistantLearningCandidate{{
		Fingerprint: "approved-pattern", Type: "workflow", Text: "Use a three-item preflight checklist.", Confidence: "high",
		Evidence: learningEvidence(), SourceRunID: "reflection-1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := learnings.ApproveCandidate(station.ID, document.Candidates[0].ID, document.Version); err != nil {
		t.Fatal(err)
	}
	document, err = learnings.Read(station.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, station, project, learnings, document
}

func TestAssistantSuggestionService_GenerateAcceptUsesApprovedEvidenceAndOrdinaryTask(t *testing.T) {
	store, station, project, learnings, document := suggestionFixture(t)
	service := NewAssistantSuggestionService(store, learnings)
	document, err := service.Generate(station.ID, project.ID, document.Version)
	if err != nil || len(document.Suggestions) != 1 {
		t.Fatalf("generate = (%+v, %v)", document.Suggestions, err)
	}
	suggestion := document.Suggestions[0]
	if suggestion.AcceptedAt != nil || suggestion.TaskID != "" || suggestion.OpportunityID == "" || len(suggestion.Evidence) != 3 {
		t.Fatalf("generated suggestion had consequences or missing Action Center evidence: %+v", suggestion)
	}
	opportunity, err := NewOpportunityStore(store).Get(project.ID, suggestion.OpportunityID)
	if err != nil || opportunity.Status != OpportunityNew || opportunity.SourceRunID != suggestion.ID {
		t.Fatalf("Action Center opportunity = (%+v, %v)", opportunity, err)
	}
	if opportunity.SourceType != OpportunitySourceAssistantSuggestion || opportunity.SourceID != suggestion.ID ||
		opportunity.SourceLabel != "Guide" || opportunity.SourceURL != "/workspaces/"+project.FolderSlug+"/assistant" || opportunity.Evidence == "" {
		t.Fatalf("Action Center suggestion provenance = %+v", opportunity)
	}
	accepted, err := service.Accept(station.ID, suggestion.ID, document.Version)
	if err != nil || accepted.AcceptedAt == nil || accepted.TaskID == "" {
		t.Fatalf("accept = (%+v, %v)", accepted, err)
	}
	savedProject, _ := store.Get(project.ID)
	task, err := savedProject.GetTask(accepted.TaskID)
	if err != nil || task.Status != TaskStatusBacklog || task.Context["requires_confirmation"] != true {
		t.Fatalf("accepted suggestion backlog task = (%+v, %v)", task, err)
	}
	opportunity, _ = NewOpportunityStore(store).Get(project.ID, suggestion.OpportunityID)
	if opportunity.Status != OpportunityPlanned || opportunity.LinkedTaskID != task.ID {
		t.Fatalf("planned opportunity = %+v", opportunity)
	}
	capabilities, ok := task.Context["required_capabilities"].([]string)
	if !ok || len(capabilities) != 1 || capabilities[0] != "live_control" {
		t.Fatalf("task did not retain declared capability gate: %#v", task.Context)
	}
	// Retry repairs/returns the same deterministic task and never duplicates it.
	if _, err := service.Accept(station.ID, suggestion.ID, 0); err != nil {
		t.Fatalf("accept retry: %v", err)
	}
	savedProject, _ = store.Get(project.ID)
	matches := 0
	for _, candidate := range savedProject.Tasks {
		if candidate.ID == accepted.TaskID {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("accepted suggestion task count = %d", matches)
	}
}

func TestAssistantSuggestionService_ConcurrentAcceptanceCreatesOneBacklogTask(t *testing.T) {
	store, station, project, learnings, document := suggestionFixture(t)
	service := NewAssistantSuggestionService(store, learnings)
	document, err := service.Generate(station.ID, project.ID, document.Version)
	if err != nil {
		t.Fatal(err)
	}
	suggestion := document.Suggestions[0]
	var wait sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, acceptErr := service.Accept(station.ID, suggestion.ID, document.Version)
			errs <- acceptErr
		}()
	}
	wait.Wait()
	close(errs)
	for acceptErr := range errs {
		if acceptErr != nil {
			t.Fatalf("concurrent accept: %v", acceptErr)
		}
	}
	saved, _ := store.Get(project.ID)
	matches := 0
	for _, task := range saved.Tasks {
		if task.SourceType == BacklogSourceActionCenter && task.SourceID == suggestion.OpportunityID {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("backlog tasks from suggestion = %d, want 1", matches)
	}
}

func TestAssistantSuggestionService_HelperAndDisabledStagesFailClosed(t *testing.T) {
	store, station, project, learnings, document := suggestionFixture(t)
	service := NewAssistantSuggestionService(store, learnings)
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.StageID = "helper"
		state.Level = 1
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Generate(station.ID, project.ID, document.Version); !errors.Is(err, ErrAssistantSuggestionUnavailable) {
		t.Fatalf("helper generation error = %v", err)
	}
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.StageID = "collaborator"
		state.Level = 2
		state.PluginAvailable = false
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Generate(station.ID, project.ID, document.Version); !errors.Is(err, ErrAssistantSuggestionUnavailable) {
		t.Fatalf("disabled generation error = %v", err)
	}
}
