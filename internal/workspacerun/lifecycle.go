package workspacerun

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type WorkspaceRootResolver func(workspaceID string) []string

type Service struct {
	store          Store
	profiles       *ProfileRegistry
	executors      *ExecutorRegistry
	environments   EnvironmentManager
	validator      *Validator
	resolveRoots   WorkspaceRootResolver
	mu             sync.Mutex
	runningCancels map[string]context.CancelFunc
}

type CreateRunRequest struct {
	ParentRunID       string             `json:"parent_run_id,omitempty"`
	ProfileID         string             `json:"profile_id"`
	Executor          Executor           `json:"executor"`
	Prompt            string             `json:"prompt"`
	Scope             Scope              `json:"scope"`
	Policy            Policy             `json:"policy"`
	Environment       Environment        `json:"environment"`
	ContextPlan       ContextPlan        `json:"context_plan,omitempty"`
	ValidationRequest *ValidationRequest `json:"validation_request,omitempty"`
	// OriginType marks why the run was created (e.g. OriginMission for
	// workspace mission triggers). Empty for normal runs.
	OriginType OriginType `json:"origin_type,omitempty"`
	// CycleOrdinal is meaningful only when OriginType == OriginMission; it
	// records which mission cycle this run is (1 = baseline).
	CycleOrdinal int `json:"cycle_ordinal,omitempty"`
}

func NewService(store Store, profiles *ProfileRegistry, executors *ExecutorRegistry, environments EnvironmentManager, validator *Validator, resolveRoots WorkspaceRootResolver) *Service {
	if profiles == nil {
		profiles = NewProfileRegistry()
	}
	if executors == nil {
		executors = NewExecutorRegistry()
	}
	if environments == nil {
		environments = NewLocalEnvironmentManager("")
	}
	if validator == nil {
		validator = NewValidator()
	}
	return &Service{
		store:          store,
		profiles:       profiles,
		executors:      executors,
		environments:   environments,
		validator:      validator,
		resolveRoots:   resolveRoots,
		runningCancels: make(map[string]context.CancelFunc),
	}
}

func (s *Service) CreateRun(ctx context.Context, workspaceID string, req CreateRunRequest) (*Run, error) {
	if s.store == nil {
		return nil, fmt.Errorf("workspace run store is nil")
	}
	req.Executor.Kind = NormalizeExecutorKind(string(req.Executor.Kind))
	if _, err := s.executors.Get(req.Executor.Kind); err != nil {
		return nil, err
	}
	profile, err := s.profiles.Snapshot(req.ProfileID)
	if err != nil {
		return nil, err
	}
	policy := MergePolicy(profile.DefaultPolicy, req.Policy)
	scope := req.Scope
	if s.resolveRoots != nil {
		scope, err = CanonicalizeScope(req.Scope, s.resolveRoots(workspaceID))
		if err != nil {
			return nil, err
		}
	}
	run := &Run{
		WorkspaceID:       workspaceID,
		ParentRunID:       req.ParentRunID,
		OriginType:        req.OriginType,
		CycleOrdinal:      req.CycleOrdinal,
		ProfileID:         profile.ID,
		ProfileVersion:    profile.Version,
		ProfileSnapshot:   profile,
		Executor:          req.Executor,
		Scope:             scope,
		Policy:            policy,
		Environment:       req.Environment,
		ContextPlan:       req.ContextPlan,
		Prompt:            req.Prompt,
		Status:            RunStatusPending,
		CreatedAt:         time.Now(),
		ValidationRequest: req.ValidationRequest,
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	_ = s.appendStatusTrace(ctx, run.WorkspaceID, run.ID, RunStatusPending, "Run created")
	return s.store.GetRun(ctx, workspaceID, run.ID)
}

func (s *Service) ExecuteRun(ctx context.Context, workspaceID, runID string) error {
	run, err := s.store.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return err
	}
	runner, err := s.executors.Get(run.Executor.Kind)
	if err != nil {
		_ = s.failRun(ctx, run, err)
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.runningCancels[runKey(workspaceID, runID)] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.runningCancels, runKey(workspaceID, runID))
		s.mu.Unlock()
		cancel()
	}()

	if err := s.transition(ctx, run, RunStatusPreparing, "Preparing run environment"); err != nil {
		return err
	}
	env, err := s.environments.Prepare(runCtx, run)
	if err != nil {
		_ = s.failRun(ctx, run, err)
		return err
	}
	if env != nil {
		run.Environment = *env
		_ = s.store.UpdateEnvironment(ctx, workspaceID, runID, *env)
	}
	teardown := true
	defer func() {
		if teardown {
			_ = s.environments.TearDown(context.Background(), run, env)
		}
	}()

	if preparer, ok := runner.(ContextPreparer); ok {
		if err := s.transition(ctx, run, RunStatusPreparingContext, "Preparing run context"); err != nil {
			return err
		}
		prepared, err := preparer.PrepareContext(runCtx, run)
		if err != nil {
			_ = s.failRun(ctx, run, err)
			return err
		}
		if prepared != nil {
			run.PreparedContext = prepared
			if err := s.store.SetPreparedContext(ctx, workspaceID, runID, *prepared); err != nil {
				_ = s.failRun(ctx, run, err)
				return err
			}
		}
	}

	if err := s.transition(ctx, run, RunStatusExecuting, "Executing run"); err != nil {
		return err
	}
	if err := runner.Execute(runCtx, run); err != nil {
		_ = s.failRun(ctx, run, err)
		return err
	}
	if emitter, ok := runner.(TraceEmitter); ok {
		traceEvents, traceErr := emitter.TraceEvents(ctx, run)
		if traceErr != nil {
			_ = s.failRun(ctx, run, traceErr)
			return traceErr
		}
		for _, event := range traceEvents {
			_, _ = s.store.AppendTrace(ctx, workspaceID, runID, event)
		}
	}
	artifacts, err := runner.Artifacts(ctx, run)
	if err != nil {
		_ = s.failRun(ctx, run, err)
		return err
	}
	for _, artifact := range artifacts {
		stored, addErr := s.store.AddArtifact(ctx, workspaceID, runID, artifact)
		if addErr == nil {
			_, _ = s.store.AppendTrace(ctx, workspaceID, runID, ArtifactCapturedTrace(runID, stored.ID, stored.Kind))
		}
	}
	if run.Cost != nil {
		_ = s.store.SetCost(ctx, workspaceID, runID, *run.Cost)
	}

	if err := s.transition(ctx, run, RunStatusValidating, "Validating run"); err != nil {
		return err
	}
	validation, validationArtifacts, err := s.validator.Validate(ctx, run, artifacts)
	if err != nil {
		_ = s.failRun(ctx, run, err)
		return err
	}
	for _, artifact := range validationArtifacts {
		stored, addErr := s.store.AddArtifact(ctx, workspaceID, runID, artifact)
		if addErr == nil {
			_, _ = s.store.AppendTrace(ctx, workspaceID, runID, ArtifactCapturedTrace(runID, stored.ID, stored.Kind))
		}
	}
	if validation != nil {
		_ = s.store.SetValidationResult(ctx, workspaceID, runID, *validation)
		for _, check := range validation.Checks {
			_, _ = s.store.AppendTrace(ctx, workspaceID, runID, ValidationTrace(runID, check.Name, check.Status))
		}
	}
	allArtifacts, _ := s.store.ListArtifacts(ctx, workspaceID, runID)
	report := NewReport("Run completed", allArtifacts, validation)
	_ = s.store.SetReport(ctx, workspaceID, runID, report)

	if !ValidationAcceptable(validation) {
		_ = s.store.SetError(ctx, workspaceID, runID, "validation failed")
		return s.transition(ctx, run, RunStatusFailed, "Validation failed")
	}
	if run.Policy.Approval == PolicyApprovalFinalOnly || run.Policy.Approval == PolicyApprovalPerTool {
		teardown = false
		return s.transition(ctx, run, RunStatusAwaitingApproval, "Awaiting final approval")
	}
	return s.transition(ctx, run, RunStatusSucceeded, "Run succeeded")
}

func (s *Service) StopRun(ctx context.Context, workspaceID, runID string) error {
	key := runKey(workspaceID, runID)
	s.mu.Lock()
	if cancel, ok := s.runningCancels[key]; ok {
		cancel()
	}
	s.mu.Unlock()
	run, err := s.store.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return err
	}
	if runner, err := s.executors.Get(run.Executor.Kind); err == nil {
		_ = runner.Cancel(ctx, run)
	}
	return s.transition(ctx, run, RunStatusCancelled, "Run cancelled")
}

func (s *Service) ApproveRun(ctx context.Context, workspaceID, runID string) error {
	run, err := s.store.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return err
	}
	if run.Status != RunStatusAwaitingApproval {
		return fmt.Errorf("run %s is not awaiting approval", runID)
	}
	defer func() { _ = s.environments.TearDown(context.Background(), run, &run.Environment) }()
	return s.transition(ctx, run, RunStatusSucceeded, "Run approved")
}

func (s *Service) RejectRun(ctx context.Context, workspaceID, runID, reason string) error {
	run, err := s.store.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return err
	}
	if runner, err := s.executors.Get(run.Executor.Kind); err == nil {
		_ = runner.Cancel(ctx, run)
	}
	_ = s.store.SetError(ctx, workspaceID, runID, reason)
	defer func() { _ = s.environments.TearDown(context.Background(), run, &run.Environment) }()
	return s.transition(ctx, run, RunStatusRejected, "Run rejected")
}

func (s *Service) transition(ctx context.Context, run *Run, status RunStatus, message string) error {
	if err := s.store.UpdateStatus(ctx, run.WorkspaceID, run.ID, status, ""); err != nil {
		return err
	}
	run.Status = status
	return s.appendStatusTrace(ctx, run.WorkspaceID, run.ID, status, message)
}

func (s *Service) failRun(ctx context.Context, run *Run, err error) error {
	if err == nil {
		return nil
	}
	_ = s.store.SetError(ctx, run.WorkspaceID, run.ID, err.Error())
	_ = s.store.UpdateStatus(ctx, run.WorkspaceID, run.ID, RunStatusFailed, "")
	_, _ = s.store.AppendTrace(ctx, run.WorkspaceID, run.ID, ErrorTrace(run.ID, "lifecycle", err.Error()))
	return err
}

func (s *Service) appendStatusTrace(ctx context.Context, workspaceID, runID string, status RunStatus, message string) error {
	_, err := s.store.AppendTrace(ctx, workspaceID, runID, StatusTrace(runID, status, message))
	return err
}
