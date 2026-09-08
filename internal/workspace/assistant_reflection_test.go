package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type reflectionModelFunc func(context.Context, AssistantReflectionModelRequest) (string, error)

func (fn reflectionModelFunc) GenerateAssistantReflection(ctx context.Context, request AssistantReflectionModelRequest) (string, error) {
	return fn(ctx, request)
}

func reflectionFixture(t *testing.T) (Store, *Workspace, *AssistantLearningStore) {
	t.Helper()
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	projects := make([]*Workspace, 0, 3)
	var station *Workspace
	for index, name := range []string{"Alpha", "Beta", "Gamma"} {
		project := assistantProject(t, store, name)
		project.FolderSlug = name
		now := time.Date(2026, time.January, index+1, 10, 0, 0, 0, time.UTC)
		project.Tasks = []Task{{
			ID: "accepted-" + name, WorkspaceID: project.ID, Description: "Prepare a concise preflight checklist",
			Status: TaskStatusCompleted, Result: "The user kept the checklist to three items.", CreatedAt: now, CompletedAt: &now,
		}}
		if err := store.Save(project); err != nil {
			t.Fatal(err)
		}
		var err error
		station, _, err = service.EnsureProjectStation(project.ID)
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, project)
	}
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.Hired = true
		state.StageID = "helper"
		state.Level = 1
		state.Reflection.ScheduleTaskID = AssistantReflectionScheduleID(station.ID)
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		if _, _, err := service.RecordAcceptedCompletion(project.ID, "task:"+project.Tasks[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	learnings := NewAssistantLearningStore(testFolderResolver{root: t.TempDir()})
	return store, station, learnings
}

func TestAssistantReflectionScheduleArmsOnlyAfterThreeHiredProjects(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	first := assistantProject(t, store, "One")
	station, _, err := service.EnsureProjectStation(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.Hired = true
		state.StageID = "helper"
		state.Level = 1
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reflection := NewAssistantReflectionService(store, NewAssistantLearningStore(testFolderResolver{root: t.TempDir()}), nil)
	if err := reflection.EnsureSchedule(station.ID); err != nil {
		t.Fatal(err)
	}
	station, _ = store.Get(station.ID)
	if station.GetAssistantProgramState().Reflection.ScheduleTaskID != "" {
		t.Fatal("reflection scheduled below the minimum project count")
	}
	for _, name := range []string{"Two", "Three"} {
		project := assistantProject(t, store, name)
		if _, _, err := service.EnsureProjectStation(project.ID); err != nil {
			t.Fatal(err)
		}
	}
	station, _ = store.Get(station.ID)
	state := station.GetAssistantProgramState()
	if state.Reflection.ScheduleTaskID != AssistantReflectionScheduleID(station.ID) || state.Reflection.NextEligibleAt == nil {
		t.Fatalf("reflection schedule = %+v", state.Reflection)
	}
}

func TestAssistantReflectionSchedulePausesWhenLinkedProjectCountDrops(t *testing.T) {
	store, station, learnings := reflectionFixture(t)
	state := station.GetAssistantProgramState()
	removedProjectID := state.LinkedProjectIDs[0]
	if err := store.Update(removedProjectID, func(current *Workspace) error {
		current.SetAssistantProjectLink(nil)
		current.ParentID = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(removedProjectID); err != nil {
		t.Fatal(err)
	}
	model := reflectionModelFunc(func(context.Context, AssistantReflectionModelRequest) (string, error) {
		t.Fatal("model ran below the minimum project count")
		return "", nil
	})
	if _, err := NewAssistantReflectionService(store, learnings, model).Run(context.Background(), station.ID); !errors.Is(err, ErrAssistantReflectionUnavailable) {
		t.Fatalf("run error = %v", err)
	}
	station, _ = store.Get(station.ID)
	reflection := station.GetAssistantProgramState().Reflection
	if reflection.ScheduleTaskID != "" || reflection.NextEligibleAt != nil {
		t.Fatalf("schedule did not pause: %+v", reflection)
	}
}

func TestAssistantReflectionService_BoundedStructuredRunCreatesPendingCandidate(t *testing.T) {
	store, station, learnings := reflectionFixture(t)
	model := reflectionModelFunc(func(_ context.Context, request AssistantReflectionModelRequest) (string, error) {
		if request.SchemaName != assistantReflectionSchemaName || len(request.Snapshot.Events) != 3 {
			t.Fatalf("reflection request = %+v", request)
		}
		sources := make([]string, 0, len(request.Snapshot.Events))
		for _, event := range request.Snapshot.Events {
			sources = append(sources, event.SourceID)
			if event.Summary == "" || event.ProjectID == "" {
				t.Fatalf("unresolved evidence event = %+v", event)
			}
		}
		payload := map[string]any{"candidates": []any{map[string]any{
			"type": "workflow", "text": "Use a concise three-item preflight checklist.", "confidence": "high", "evidence_source_ids": sources,
		}}}
		data, _ := json.Marshal(payload)
		return string(data), nil
	})
	result, err := NewAssistantReflectionService(store, learnings, model).Run(context.Background(), station.ID)
	if err != nil || result.Status != "completed" || result.CandidateCount != 1 {
		t.Fatalf("run = (%+v, %v)", result, err)
	}
	document, err := learnings.Read(station.ID)
	if err != nil || len(document.Candidates) != 1 || len(document.Learnings) != 0 || len(document.Runs) != 1 {
		t.Fatalf("reflection document = (%+v, %v)", document, err)
	}
	if document.Candidates[0].ApprovedLearningID != "" {
		t.Fatal("reflection bypassed approval")
	}
	state, _ := store.Get(station.ID)
	if reflection := state.GetAssistantProgramState().Reflection; reflection.InFlightRunID != "" || reflection.LastCompletedRunID != result.RunID || reflection.NextEligibleAt == nil {
		t.Fatalf("reflection state = %+v", reflection)
	}
}

func TestAssistantReflectionService_InvalidOrFailedRunWritesNoCandidates(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		model reflectionModelFunc
	}{
		{name: "unresolved evidence", model: func(_ context.Context, _ AssistantReflectionModelRequest) (string, error) {
			return `{"candidates":[{"type":"workflow","text":"Unsafe unsupported pattern","confidence":"high","evidence_source_ids":["missing-a","missing-b","missing-c"]}]}`, nil
		}},
		{name: "model failure", model: func(_ context.Context, _ AssistantReflectionModelRequest) (string, error) {
			return "", errors.New("provider offline")
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, station, learnings := reflectionFixture(t)
			result, err := NewAssistantReflectionService(store, learnings, testCase.model).Run(context.Background(), station.ID)
			if err == nil || result.Status != "failed" {
				t.Fatalf("run = (%+v, %v)", result, err)
			}
			document, readErr := learnings.Read(station.ID)
			if readErr != nil || len(document.Candidates) != 0 || len(document.Learnings) != 0 || len(document.Runs) != 1 || document.Runs[0].Status != "failed" {
				t.Fatalf("failed run left partial state = (%+v, %v)", document, readErr)
			}
		})
	}
}

func TestAssistantReflectionSnapshotFailsClosedWhenSecretEvidenceBreaksMinimum(t *testing.T) {
	store, station, learnings := reflectionFixture(t)
	state := station.GetAssistantProgramState()
	if err := store.Update(state.LinkedProjectIDs[0], func(project *Workspace) error {
		project.Tasks[0].Result = "token sk-abcdefghijklmnop"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewAssistantReflectionService(store, learnings, nil)
	if _, err := service.snapshot(station, state, "secret-run"); !errors.Is(err, ErrAssistantReflectionUnavailable) {
		t.Fatalf("snapshot error = %v, want unavailable", err)
	}
}

func TestDecodeReflectionCandidatesRejectsInstructionEchoAndNearDuplicate(t *testing.T) {
	now := time.Now().UTC()
	snapshot := AssistantReflectionSnapshot{
		RunID: "run", Rubric: "Always keep every preflight checklist concise.",
		ApprovedLearnings: []AssistantReflectionApprovedLearning{{Type: "preference", Text: "Keep preflight reviews concise"}},
	}
	for _, projectID := range []string{"one", "two", "three"} {
		snapshot.Events = append(snapshot.Events, AssistantReflectionEvent{SourceID: projectID + ":task", ProjectID: projectID, ObservedAt: now})
	}
	config := AssistantReflectionConfig{MinimumProjects: 3, MaxCandidates: 4, MaxEvidence: 8}
	for name, candidateText := range map[string]string{
		"instruction echo": "Always keep every preflight checklist concise.",
		"additive noise":   "Keep preflight reviews concise always",
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"candidates": []map[string]any{{
				"type": "preference", "text": candidateText, "confidence": "high",
				"evidence_source_ids": []string{"one:task", "two:task", "three:task"},
			}}})
			if _, err := decodeReflectionCandidates(string(raw), snapshot, config); !errors.Is(err, ErrAssistantReflectionInvalid) {
				t.Fatalf("decode error = %v, want invalid", err)
			}
		})
	}
}

func TestAssistantReflectionService_UsesStationProviderAndModel(t *testing.T) {
	store, station, learnings := reflectionFixture(t)
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.Provider = "chosen-provider"
		state.Model = "chosen-model"
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	model := reflectionModelFunc(func(_ context.Context, request AssistantReflectionModelRequest) (string, error) {
		if request.Provider != "chosen-provider" || request.Model != "chosen-model" {
			t.Fatalf("model selection = %q/%q", request.Provider, request.Model)
		}
		return `{"candidates":[]}`, nil
	})
	if _, err := NewAssistantReflectionService(store, learnings, model).Run(context.Background(), station.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantReflectionService_EmptyCandidateRunIsValid(t *testing.T) {
	store, station, learnings := reflectionFixture(t)
	model := reflectionModelFunc(func(_ context.Context, _ AssistantReflectionModelRequest) (string, error) {
		return `{"candidates":[]}`, nil
	})
	result, err := NewAssistantReflectionService(store, learnings, model).Run(context.Background(), station.ID)
	if err != nil || result.Status != "completed" || result.CandidateCount != 0 {
		t.Fatalf("empty run = (%+v, %v)", result, err)
	}
}
