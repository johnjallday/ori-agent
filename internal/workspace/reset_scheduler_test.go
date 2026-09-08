package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type resetPausedSchedules struct {
	missionStarted    chan context.Context
	reflectionStarted chan context.Context
	missionRelease    chan struct{}
	reflectionRelease chan struct{}
}

func (p *resetPausedSchedules) TriggerMissionRun(ctx context.Context, _ string, _ int) (string, error) {
	p.missionStarted <- ctx
	<-p.missionRelease
	return "owned-run", nil
}
func (p *resetPausedSchedules) TriggerAssistantReflection(ctx context.Context, _ string) error {
	p.reflectionStarted <- ctx
	<-p.reflectionRelease
	return nil
}

func TestResetAdmissionSchedulerRegistersDetachedWorkBeforePollReturns(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	ws := NewWorkspace(CreateWorkspaceParams{Name: "owned schedule"})
	past := time.Now().Add(-time.Hour)
	ws.Status = StatusActive
	ws.MissionEnabled = true
	ws.NextMissionRunAt = &past
	ws.Cadence = &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"}
	ws.SetAssistantProgramState(&AssistantProgramState{
		Hired: true, PluginAvailable: true, Declaration: neutralAssistantDeclaration(),
		Reflection: AssistantReflectionState{ScheduleTaskID: AssistantReflectionScheduleID(ws.ID), NextEligibleAt: &past},
	})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	g := &resetstate.WorkGate{}
	scheduler := NewTaskScheduler(store, SchedulerConfig{AdmissionGate: g})
	paused := &resetPausedSchedules{missionStarted: make(chan context.Context, 1), reflectionStarted: make(chan context.Context, 1), missionRelease: make(chan struct{}), reflectionRelease: make(chan struct{})}
	scheduler.SetMissionTrigger(paused)
	scheduler.SetAssistantReflectionTrigger(paused)
	release := sync.OnceFunc(func() { close(paused.missionRelease); close(paused.reflectionRelease) })
	t.Cleanup(func() { release(); scheduler.wg.Wait() })
	scheduler.checkScheduledTasks()
	// The tick has returned, potentially before either child has run. Both
	// permits must already exist: registration inside the goroutine is too late.
	if got := g.Snapshot().Active; got != 2 {
		t.Fatalf("detached schedule permits=%d, want 2", got)
	}
	missionCtx := resetWait(t, paused.missionStarted)
	reflectionCtx := resetWait(t, paused.reflectionStarted)
	if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("active schedule: %v", err)
	}
	if missionCtx.Err() != nil || reflectionCtx.Err() != nil {
		t.Fatal("reset cancelled scheduled work")
	}
	release()
	scheduler.wg.Wait()
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	// No second poll may discover owners, update schedules or program OS wake.
	scheduler.workspaceStore = nil
	scheduler.checkScheduledTasks()
}
