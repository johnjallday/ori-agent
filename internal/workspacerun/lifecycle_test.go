package workspacerun

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceExecuteRunPausesForFinalApprovalThenApproves(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindSystemTool, &stubLifecycleExecutor{})
	env := &stubEnvironmentManager{}
	service := NewService(store, NewProfileRegistry(), executors, env, NewValidator(), nil)

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
		Policy:    Policy{Approval: PolicyApprovalFinalOnly},
		ValidationRequest: &ValidationRequest{
			Profile: ValidationProfileNone,
		},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := service.ExecuteRun(ctx, "workspace-1", run.ID); err != nil {
		t.Fatalf("execute run: %v", err)
	}

	got, err := store.GetRun(ctx, "workspace-1", run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != RunStatusAwaitingApproval {
		t.Fatalf("Status = %q, want awaiting_approval", got.Status)
	}
	if env.tornDown {
		t.Fatal("environment should not be torn down before final approval")
	}

	if err := service.ApproveRun(ctx, "workspace-1", run.ID); err != nil {
		t.Fatalf("approve run: %v", err)
	}
	got, err = store.GetRun(ctx, "workspace-1", run.ID)
	if err != nil {
		t.Fatalf("get approved run: %v", err)
	}
	if got.Status != RunStatusSucceeded {
		t.Fatalf("Status = %q, want succeeded", got.Status)
	}
	if !env.tornDown {
		t.Fatal("environment should be torn down after approval")
	}

	page, err := store.ListTrace(ctx, "workspace-1", run.ID, 0, 100)
	if err != nil {
		t.Fatalf("list trace: %v", err)
	}
	gotStatuses := statusTraceValues(page.Events)
	wantStatuses := []RunStatus{
		RunStatusPending,
		RunStatusPreparing,
		RunStatusExecuting,
		RunStatusValidating,
		RunStatusAwaitingApproval,
		RunStatusSucceeded,
	}
	if !sameStatuses(gotStatuses, wantStatuses) {
		t.Fatalf("status trace = %v, want %v", gotStatuses, wantStatuses)
	}
}

func TestServiceCreateRunSnapshotsReferenceURLAndAllowlistsHost(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindSystemTool, &stubLifecycleExecutor{})
	service := NewService(store, NewProfileRegistry(), executors, &stubEnvironmentManager{}, NewValidator(), nil)

	referenceByTask := map[string]string{
		"task-1": "https://example.com/spec",
	}
	service.SetTaskReferenceURLResolver(func(_ context.Context, _ string, taskID string) (string, error) {
		return referenceByTask[taskID], nil
	})

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
		Scope: Scope{
			TargetTaskID:     "task-1",
			NetworkAllowlist: []string{"api.example.com"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.ReferenceURL != "https://example.com/spec" {
		t.Fatalf("ReferenceURL = %q, want inherited task URL", run.ReferenceURL)
	}
	if !stringSliceContains(run.Scope.NetworkAllowlist, "example.com") || !stringSliceContains(run.Scope.NetworkAllowlist, "api.example.com") {
		t.Fatalf("NetworkAllowlist = %v, want existing host and reference host", run.Scope.NetworkAllowlist)
	}

	referenceByTask["task-1"] = "https://changed.example.com/spec"
	got, err := store.GetRun(ctx, "workspace-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.ReferenceURL != "https://example.com/spec" {
		t.Fatalf("persisted ReferenceURL = %q, want snapshot", got.ReferenceURL)
	}
}

func TestServiceCreateRunExplicitReferenceURLOverridesTask(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindSystemTool, &stubLifecycleExecutor{})
	service := NewService(store, NewProfileRegistry(), executors, &stubEnvironmentManager{}, NewValidator(), nil)
	service.SetTaskReferenceURLResolver(func(context.Context, string, string) (string, error) {
		return "https://task.example.com/spec", nil
	})

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID:    ProfileGeneral,
		Executor:     Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:       "do work",
		ReferenceURL: " https://run.example.com/spec ",
		Scope:        Scope{TargetTaskID: "task-1"},
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.ReferenceURL != "https://run.example.com/spec" {
		t.Fatalf("ReferenceURL = %q, want explicit run URL", run.ReferenceURL)
	}
	if !stringSliceContains(run.Scope.NetworkAllowlist, "run.example.com") {
		t.Fatalf("NetworkAllowlist = %v, want explicit reference host", run.Scope.NetworkAllowlist)
	}
}

func TestServiceRejectRunMarksRejectedAndTearsDown(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executor := &stubLifecycleExecutor{}
	executors.Register(ExecutorKindSystemTool, executor)
	env := &stubEnvironmentManager{}
	service := NewService(store, NewProfileRegistry(), executors, env, NewValidator(), nil)

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
		Policy:    Policy{Approval: PolicyApprovalFinalOnly},
		ValidationRequest: &ValidationRequest{
			Profile: ValidationProfileNone,
		},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := service.RejectRun(ctx, "workspace-1", run.ID, "not acceptable"); err != nil {
		t.Fatalf("reject run: %v", err)
	}
	got, err := store.GetRun(ctx, "workspace-1", run.ID)
	if err != nil {
		t.Fatalf("get rejected run: %v", err)
	}
	if got.Status != RunStatusRejected {
		t.Fatalf("Status = %q, want rejected", got.Status)
	}
	if got.Error != "not acceptable" {
		t.Fatalf("Error = %q, want reason", got.Error)
	}
	if !executor.cancelled {
		t.Fatal("executor cancel was not called")
	}
	if !env.tornDown {
		t.Fatal("environment should be torn down after rejection")
	}
}

func TestServiceExecuteRunValidationFailurePreventsSuccess(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindSystemTool, &stubLifecycleExecutor{})
	env := &stubEnvironmentManager{}
	service := NewService(store, NewProfileRegistry(), executors, env, NewValidator(), nil)

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileEngineering,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
		Policy:    Policy{Approval: PolicyApprovalNone},
		ValidationRequest: &ValidationRequest{
			Profile: ValidationProfileNone,
		},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := service.ExecuteRun(ctx, "workspace-1", run.ID); err != nil {
		t.Fatalf("execute run: %v", err)
	}

	got, err := store.GetRun(ctx, "workspace-1", run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != RunStatusFailed {
		t.Fatalf("Status = %q, want failed", got.Status)
	}
	if got.ValidationResult == nil || !hasCheck(got.ValidationResult, "change_evidence_present", CheckStatusFailed) {
		t.Fatalf("ValidationResult = %+v, want failed engineering evidence", got.ValidationResult)
	}
	if got.Report == nil || got.Report.ValidationStatus != ValidationStatusFailed {
		t.Fatalf("Report = %+v, want failed validation report", got.Report)
	}
	if !env.tornDown {
		t.Fatal("environment should be torn down after validation failure")
	}
}

func TestServiceExecuteRunExecutorFailureTearsDown(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindSystemTool, &stubLifecycleExecutor{executeErr: errors.New("executor failed")})
	env := &stubEnvironmentManager{}
	service := NewService(store, NewProfileRegistry(), executors, env, NewValidator(), nil)

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := service.ExecuteRun(ctx, "workspace-1", run.ID); err == nil {
		t.Fatal("ExecuteRun returned nil, want executor error")
	}

	got, err := store.GetRun(ctx, "workspace-1", run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != RunStatusFailed || got.Error != "executor failed" {
		t.Fatalf("run = %+v, want failed with executor error", got)
	}
	if !env.tornDown {
		t.Fatal("environment should be torn down after executor failure")
	}
}

func TestServiceExecuteRunPersistsPreparedContextBeforeExecution(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindSystemTool, &stubContextLifecycleExecutor{})
	service := NewService(store, NewProfileRegistry(), executors, &stubEnvironmentManager{}, NewValidator(), nil)

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
		ValidationRequest: &ValidationRequest{
			Profile: ValidationProfileNone,
		},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := service.ExecuteRun(ctx, "workspace-1", run.ID); err != nil {
		t.Fatalf("execute run: %v", err)
	}

	got, err := store.GetRun(ctx, "workspace-1", run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.PreparedContext == nil || got.PreparedContext.Summary != "prepared" {
		t.Fatalf("PreparedContext = %+v, want persisted context", got.PreparedContext)
	}

	page, err := store.ListTrace(ctx, "workspace-1", run.ID, 0, 100)
	if err != nil {
		t.Fatalf("list trace: %v", err)
	}
	gotStatuses := statusTraceValues(page.Events)
	wantStatuses := []RunStatus{
		RunStatusPending,
		RunStatusPreparing,
		RunStatusPreparingContext,
		RunStatusExecuting,
		RunStatusValidating,
		RunStatusSucceeded,
	}
	if !sameStatuses(gotStatuses, wantStatuses) {
		t.Fatalf("status trace = %v, want %v", gotStatuses, wantStatuses)
	}
}

func TestServiceStopRunCancelsExecutor(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	executors := NewExecutorRegistry()
	executor := &stubLifecycleExecutor{}
	executors.Register(ExecutorKindSystemTool, executor)
	service := NewService(store, NewProfileRegistry(), executors, &stubEnvironmentManager{}, NewValidator(), nil)

	run, err := service.CreateRun(ctx, "workspace-1", CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor:  Executor{Kind: ExecutorKindSystemTool, Ref: "stub"},
		Prompt:    "do work",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := service.StopRun(ctx, "workspace-1", run.ID); err != nil {
		t.Fatalf("stop run: %v", err)
	}
	got, err := store.GetRun(ctx, "workspace-1", run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != RunStatusCancelled {
		t.Fatalf("Status = %q, want cancelled", got.Status)
	}
	if !executor.cancelled {
		t.Fatal("executor cancel was not called")
	}
}

type stubLifecycleExecutor struct {
	cancelled  bool
	executeErr error
	artifacts  []Artifact
}

type stubContextLifecycleExecutor struct {
	stubLifecycleExecutor
}

func (e *stubContextLifecycleExecutor) PrepareContext(_ context.Context, _ *Run) (*PreparedContext, error) {
	return &PreparedContext{
		Strategy:   "stub",
		Summary:    "prepared",
		PreparedAt: time.Now(),
		Items: []PreparedContextItem{
			{Kind: "workspace_snapshot", Access: PreparedContextAccessInjected},
		},
	}, nil
}

func (e *stubLifecycleExecutor) Execute(_ context.Context, run *Run) error {
	if e.executeErr != nil {
		return e.executeErr
	}
	run.Cost = &CostSummary{InputTokens: 1, OutputTokens: 2}
	return nil
}

func (e *stubLifecycleExecutor) Cancel(_ context.Context, _ *Run) error {
	e.cancelled = true
	return nil
}

func (e *stubLifecycleExecutor) Artifacts(_ context.Context, run *Run) ([]Artifact, error) {
	if e.artifacts != nil {
		return e.artifacts, nil
	}
	return []Artifact{NewArtifact(run.ID, ArtifactLog, ArtifactInline([]byte("done")))}, nil
}

type stubEnvironmentManager struct {
	tornDown bool
}

func (m *stubEnvironmentManager) Prepare(_ context.Context, run *Run) (*Environment, error) {
	env := run.Environment
	env.TempDir = "/tmp/run"
	return &env, nil
}

func (m *stubEnvironmentManager) TearDown(_ context.Context, _ *Run, _ *Environment) error {
	m.tornDown = true
	return nil
}

func statusTraceValues(events []TraceEvent) []RunStatus {
	statuses := make([]RunStatus, 0, len(events))
	for _, event := range events {
		if event.Kind == TraceStatusChange {
			statuses = append(statuses, RunStatus(event.Status))
		}
	}
	return statuses
}

func sameStatuses(got, want []RunStatus) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
