package workspacerun

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrRunNotFound = errors.New("workspace run not found")
	ErrRunExists   = errors.New("workspace run already exists")
)

type Store interface {
	CreateRun(ctx context.Context, run *Run) error
	GetRun(ctx context.Context, workspaceID, runID string) (*Run, error)
	ListRuns(ctx context.Context, workspaceID string) ([]*Run, error)
	UpdateStatus(ctx context.Context, workspaceID, runID string, status RunStatus, message string) error
	UpdateEnvironment(ctx context.Context, workspaceID, runID string, env Environment) error
	AppendTrace(ctx context.Context, workspaceID, runID string, event TraceEvent) (TraceEvent, error)
	ListTrace(ctx context.Context, workspaceID, runID string, since int64, limit int) (TracePage, error)
	AddArtifact(ctx context.Context, workspaceID, runID string, artifact Artifact) (Artifact, error)
	ListArtifacts(ctx context.Context, workspaceID, runID string) ([]Artifact, error)
	SetPreparedContext(ctx context.Context, workspaceID, runID string, prepared PreparedContext) error
	SetValidationResult(ctx context.Context, workspaceID, runID string, result ValidationResult) error
	SetReport(ctx context.Context, workspaceID, runID string, report Report) error
	SetCost(ctx context.Context, workspaceID, runID string, cost CostSummary) error
	SetError(ctx context.Context, workspaceID, runID, errMessage string) error
}

type MemoryStore struct {
	mu        sync.Mutex
	runs      map[string]*Run
	trace     map[string][]TraceEvent
	artifacts map[string][]Artifact
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runs:      make(map[string]*Run),
		trace:     make(map[string][]TraceEvent),
		artifacts: make(map[string][]Artifact),
	}
}

func (s *MemoryStore) CreateRun(_ context.Context, run *Run) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	if run.WorkspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}
	key := runKey(run.WorkspaceID, run.ID)
	if _, ok := s.runs[key]; ok {
		return ErrRunExists
	}
	if run.Status == "" {
		run.Status = RunStatusPending
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	s.runs[key] = CloneRun(run)
	return nil
}

func (s *MemoryStore) GetRun(_ context.Context, workspaceID, runID string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, err := s.getLocked(workspaceID, runID)
	if err != nil {
		return nil, err
	}
	out := CloneRun(run)
	out.TraceTail = TraceTail(s.trace[runKey(workspaceID, runID)], DefaultTraceTailLimit)
	out.Artifacts = CloneArtifacts(s.artifacts[runKey(workspaceID, runID)])
	return out, nil
}

func (s *MemoryStore) ListRuns(_ context.Context, workspaceID string) ([]*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var runs []*Run
	for _, run := range s.runs {
		if run.WorkspaceID != workspaceID {
			continue
		}
		out := CloneRun(run)
		out.TraceTail = TraceTail(s.trace[runKey(run.WorkspaceID, run.ID)], DefaultTraceTailLimit)
		out.Artifacts = CloneArtifacts(s.artifacts[runKey(run.WorkspaceID, run.ID)])
		runs = append(runs, out)
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs, nil
}

func (s *MemoryStore) UpdateStatus(_ context.Context, workspaceID, runID string, status RunStatus, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, err := s.getLocked(workspaceID, runID)
	if err != nil {
		return err
	}
	run.Status = status
	if message != "" {
		run.Error = message
	}
	return nil
}

func (s *MemoryStore) UpdateEnvironment(_ context.Context, workspaceID, runID string, env Environment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, err := s.getLocked(workspaceID, runID)
	if err != nil {
		return err
	}
	run.Environment = cloneEnvironment(env)
	return nil
}

func (s *MemoryStore) AppendTrace(_ context.Context, workspaceID, runID string, event TraceEvent) (TraceEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getLocked(workspaceID, runID); err != nil {
		return TraceEvent{}, err
	}
	key := runKey(workspaceID, runID)
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	event.RunID = runID
	event.Sequence = int64(len(s.trace[key]) + 1)
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	event = CloneTraceEvent(event)
	s.trace[key] = append(s.trace[key], event)
	return CloneTraceEvent(event), nil
}

func (s *MemoryStore) ListTrace(_ context.Context, workspaceID, runID string, since int64, limit int) (TracePage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getLocked(workspaceID, runID); err != nil {
		return TracePage{}, err
	}
	return TracePageAfter(s.trace[runKey(workspaceID, runID)], since, limit), nil
}

func (s *MemoryStore) AddArtifact(_ context.Context, workspaceID, runID string, artifact Artifact) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getLocked(workspaceID, runID); err != nil {
		return Artifact{}, err
	}
	if artifact.ID == "" {
		artifact.ID = uuid.New().String()
	}
	artifact.RunID = runID
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now()
	}
	artifact = CloneArtifact(artifact)
	key := runKey(workspaceID, runID)
	s.artifacts[key] = append(s.artifacts[key], artifact)
	return CloneArtifact(artifact), nil
}

func (s *MemoryStore) ListArtifacts(_ context.Context, workspaceID, runID string) ([]Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getLocked(workspaceID, runID); err != nil {
		return nil, err
	}
	return CloneArtifacts(s.artifacts[runKey(workspaceID, runID)]), nil
}

func (s *MemoryStore) SetValidationResult(_ context.Context, workspaceID, runID string, result ValidationResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, err := s.getLocked(workspaceID, runID)
	if err != nil {
		return err
	}
	result.Checks = append([]CheckResult(nil), result.Checks...)
	run.ValidationResult = &result
	return nil
}

func (s *MemoryStore) SetPreparedContext(_ context.Context, workspaceID, runID string, prepared PreparedContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, err := s.getLocked(workspaceID, runID)
	if err != nil {
		return err
	}
	prepared.Items = clonePreparedContextItems(prepared.Items)
	prepared.AvailableTools = cloneStrings(prepared.AvailableTools)
	run.PreparedContext = &prepared
	return nil
}

func (s *MemoryStore) SetReport(_ context.Context, workspaceID, runID string, report Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, err := s.getLocked(workspaceID, runID)
	if err != nil {
		return err
	}
	report.ChangedFiles = cloneStrings(report.ChangedFiles)
	report.FollowUps = cloneStrings(report.FollowUps)
	run.Report = &report
	return nil
}

func (s *MemoryStore) SetCost(_ context.Context, workspaceID, runID string, cost CostSummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, err := s.getLocked(workspaceID, runID)
	if err != nil {
		return err
	}
	cost = NormalizeCost(cost)
	run.Cost = &cost
	return nil
}

func (s *MemoryStore) SetError(_ context.Context, workspaceID, runID, errMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, err := s.getLocked(workspaceID, runID)
	if err != nil {
		return err
	}
	run.Error = errMessage
	return nil
}

func (s *MemoryStore) getLocked(workspaceID, runID string) (*Run, error) {
	run, ok := s.runs[runKey(workspaceID, runID)]
	if !ok {
		return nil, ErrRunNotFound
	}
	return run, nil
}

func runKey(workspaceID, runID string) string {
	return workspaceID + "\x00" + runID
}

func cloneEnvironment(env Environment) Environment {
	env.EnvVars = cloneStringMap(env.EnvVars)
	return env
}
