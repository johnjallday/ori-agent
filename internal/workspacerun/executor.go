package workspacerun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrExecutorNotRegistered = errors.New("workspace run executor not registered")

type ExecutorRunner interface {
	Execute(ctx context.Context, run *Run) error
	Cancel(ctx context.Context, run *Run) error
	Artifacts(ctx context.Context, run *Run) ([]Artifact, error)
}

type TraceEmitter interface {
	TraceEvents(ctx context.Context, run *Run) ([]TraceEvent, error)
}

type ExecutorRegistry struct {
	mu      sync.RWMutex
	runners map[ExecutorKind]ExecutorRunner
}

func NewExecutorRegistry() *ExecutorRegistry {
	return &ExecutorRegistry{runners: make(map[ExecutorKind]ExecutorRunner)}
}

func (r *ExecutorRegistry) Register(kind ExecutorKind, runner ExecutorRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runners == nil {
		r.runners = make(map[ExecutorKind]ExecutorRunner)
	}
	r.runners[kind] = runner
}

func (r *ExecutorRegistry) Get(kind ExecutorKind) (ExecutorRunner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	runner, ok := r.runners[kind]
	if !ok || runner == nil {
		return nil, fmt.Errorf("%w: %q", ErrExecutorNotRegistered, kind)
	}
	return runner, nil
}

func DecodeExecutorConfig[T any](executor Executor) (T, error) {
	var cfg T
	if executor.Config == nil || len(*executor.Config) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(*executor.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("decode %s executor config: %w", executor.Kind, err)
	}
	return cfg, nil
}

func NormalizeExecutorKind(value string) ExecutorKind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ExecutorKindOriAgent):
		return ExecutorKindOriAgent
	case string(ExecutorKindNativeCLI):
		return ExecutorKindNativeCLI
	case string(ExecutorKindWorkflow):
		return ExecutorKindWorkflow
	case string(ExecutorKindSystemTool):
		return ExecutorKindSystemTool
	default:
		return ExecutorKind(strings.ToLower(strings.TrimSpace(value)))
	}
}
