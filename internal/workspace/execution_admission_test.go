package workspace

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

type executionAdmissionTestStore struct {
	Store
	automatic bool
	manual    bool
	writes    int
}

func (s *executionAdmissionTestStore) CheckWorkspaceExecution(_ context.Context, _ string, automatic bool) error {
	if automatic && s.automatic || !automatic && s.manual {
		return nil
	}
	return ErrWorkspaceExecutionInactive
}
func (s *executionAdmissionTestStore) Save(ws *Workspace) error { s.writes++; return s.Store.Save(ws) }
func (s *executionAdmissionTestStore) Update(id string, fn func(*Workspace) error) error {
	s.writes++
	return s.Store.Update(id, fn)
}

func TestExecutionAdmissionPrecedesBootPollWorkflowAndScheduleEffects(t *testing.T) {
	base, id, workflowID, stepID := newSeededStore(t)
	ws, err := base.Get(id)
	localConfigMust(t, err)
	ws.Tasks = []Task{{ID: "assigned", To: "agent-a", Status: TaskStatusAssigned}, {ID: "running", Status: TaskStatusInProgress}}
	past := time.Now().Add(-time.Hour)
	ws.MissionEnabled = true
	ws.NextMissionRunAt = &past
	localConfigMust(t, base.Save(ws))
	store := &executionAdmissionTestStore{Store: base, manual: true}
	handler := &fakeTaskHandler{}
	executor := NewTaskExecutor(store, handler, ExecutorConfig{})
	executor.reconcileTasksAtBoot()
	executor.checkAndExecuteTasks()
	executor.executeTask(ws, ws.Tasks[0], TaskProviderProfile{})
	steps := NewStepExecutor(store, handler, StepExecutorConfig{})
	steps.checkAndExecuteSteps()
	steps.processWorkflow(ws, workflowID)
	workflow := ws.Workflows[workflowID]
	if workflow.Steps[0].ID != stepID {
		t.Fatal("wrong workflow fixture")
	}
	steps.executeStep(ws, &workflow, &workflow.Steps[0])
	scheduler := NewTaskScheduler(store, SchedulerConfig{})
	scheduler.checkScheduledTasks()
	scheduler.executeTaskSchedule(ws, &ws.Tasks[0], time.Now())
	scheduler.executeScheduledTask(ws, &ScheduledTask{})
	scheduler.checkMissionCadence(ws, time.Now())
	if store.writes != 0 || atomic.LoadInt64(&handler.calls) != 0 || executor.GetRunningTaskCount() != 0 {
		t.Fatal("inactive automatic work reached mutation, execution or a live claim")
	}
	if ws.Tasks[1].Status != TaskStatusInProgress {
		t.Fatal("boot reconciliation rewrote unadmitted evidence")
	}
	localConfigMust(t, RequireWorkspaceExecution(t.Context(), store, id, false))
	if err := RequireWorkspaceExecution(t.Context(), store, id, true); !errors.Is(err, ErrWorkspaceExecutionInactive) {
		t.Fatal(err)
	}
}

func TestExecutionAdmissionUsesLocalAttachmentNotPortableEnabledFlags(t *testing.T) {
	local, old, ws, _ := localConfigFixture(t)
	files, err := NewFileStoreWithLocalConfig(old.BasePath(), local)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	composed := NewAgentSnapshotStore(NewSyncStore(files, files), nil)
	for _, state := range []workspacecontinuity.AttachmentState{workspacecontinuity.Native, workspacecontinuity.ImportedInactive, workspacecontinuity.ImportedActive, workspacecontinuity.Restoring, workspacecontinuity.Unreviewed, workspacecontinuity.Detached} {
		_, err := local.db.ExecContext(t.Context(), `UPDATE continuity_attachments SET state=? WHERE workspace_id=?`, state, ws.ID)
		localConfigMust(t, err)
		for _, automatic := range []bool{false, true} {
			err := RequireWorkspaceExecution(t.Context(), composed, ws.ID, automatic)
			allowed := state == workspacecontinuity.Native || state == workspacecontinuity.ImportedActive || state == workspacecontinuity.ImportedInactive && !automatic
			if (err == nil) != allowed {
				t.Fatalf("state %s automatic %t: %v", state, automatic, err)
			}
		}
	}
	// SQL ownership remains authoritative even if an attachment state is active.
	_, err = local.db.ExecContext(t.Context(), `UPDATE continuity_attachments SET state='native' WHERE workspace_id=?`, ws.ID)
	localConfigMust(t, err)
	_, err = local.db.ExecContext(t.Context(), `UPDATE workspaces SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, ws.ID)
	localConfigMust(t, err)
	if err := RequireWorkspaceExecution(t.Context(), composed, ws.ID, false); !errors.Is(err, ErrWorkspaceExecutionInactive) {
		t.Fatal("deleted canonical owner retained execution", err)
	}
}
