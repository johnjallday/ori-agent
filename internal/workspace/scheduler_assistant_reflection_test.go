package workspace

import (
	"context"
	"testing"
	"time"
)

type stubAssistantReflectionTrigger struct{}

func (stubAssistantReflectionTrigger) TriggerAssistantReflection(context.Context, string) error {
	return nil
}

func TestTaskSchedulerAssistantReflectionDueRequiresHiredAvailableSchedule(t *testing.T) {
	now := time.Now().UTC()
	station := NewWorkspace(CreateWorkspaceParams{Name: "Station"})
	station.SetAssistantProgramState(&AssistantProgramState{
		Hired: true, PluginAvailable: true, Declaration: neutralAssistantDeclaration(),
		Reflection: AssistantReflectionState{ScheduleTaskID: AssistantReflectionScheduleID(station.ID), NextEligibleAt: &now},
	})
	scheduler := NewTaskScheduler(NewInMemoryStore(), SchedulerConfig{})
	scheduler.SetAssistantReflectionTrigger(stubAssistantReflectionTrigger{})
	if !scheduler.assistantReflectionDue(station, now) {
		t.Fatal("eligible station was not due")
	}
	state := station.GetAssistantProgramState()
	state.PluginAvailable = false
	station.SetAssistantProgramState(state)
	if scheduler.assistantReflectionDue(station, now) {
		t.Fatal("disabled contribution scheduled reflection")
	}
}

func TestTaskSchedulerAssistantReflectionClaimIsSingleFlight(t *testing.T) {
	scheduler := NewTaskScheduler(NewInMemoryStore(), SchedulerConfig{})
	if !scheduler.claimAssistantReflection("station") || scheduler.claimAssistantReflection("station") {
		t.Fatal("reflection claim was not single-flight")
	}
	scheduler.releaseAssistantReflection("station")
	if !scheduler.claimAssistantReflection("station") {
		t.Fatal("reflection claim did not release")
	}
}
