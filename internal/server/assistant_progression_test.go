package server

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestSubscribeAssistantProgressionCountsAcceptedCompletionOnceAcrossRestart(t *testing.T) {
	store := workspace.NewInMemoryStore()
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:  "reaper-song",
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper", BlueprintID: "reaper-song", BlueprintVersion: 3},
		AssistantProgram: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion,
			ID:            "producer",
			StationName:   "Producer Home",
			Roles:         []workspace.AssistantProgramRoleSpec{{ID: "producer", Label: "Producer", Primary: true}},
			Stages: []workspace.AssistantProgramStageSpec{
				{ID: "helper", Label: "Helper"},
				{ID: "collaborator", Label: "Collaborator", AcceptedCompletionThreshold: 2},
			},
			Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 8, MaxEventsPerProject: 16, MaxCandidates: 4, MaxEvidence: 8, Rubric: "Find repeated patterns."},
		},
	})
	if err := project.AddTask(workspace.Task{ID: "task-1", Description: "Accepted work", Status: workspace.TaskStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	programs := workspace.NewAssistantProgramStore(store)
	station, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.Hired = true
		state.StageID = "helper"
		state.Level = 1
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	publish := func() {
		bus := workspace.NewEventBus(8, 8)
		defer bus.Shutdown()
		workspace.SubscribeAssistantProgression(bus, store)
		bus.Publish(workspace.NewTaskEvent(workspace.EventTaskCompleted, project.ID, "task-1", "user", map[string]any{"accepted": true}))
		eventuallyAssistantProgress(t, func() bool {
			current, _ := store.Get(station.ID)
			return current.GetAssistantProgramState().AcceptedCompletions == 1
		})
		// On replay the expected count already holds before delivery. Give the
		// non-blocking subscriber time to prove it remains unchanged.
		time.Sleep(50 * time.Millisecond)
	}

	publish()
	// A fresh subscriber simulates a process restart and redelivery. The task's
	// durable station marker prevents a second progression increment.
	publish()
	current, _ := store.Get(station.ID)
	if got := current.GetAssistantProgramState().AcceptedCompletions; got != 1 {
		t.Fatalf("accepted completions = %d, want 1", got)
	}
}

func eventuallyAssistantProgress(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("assistant progression event was not persisted")
}
