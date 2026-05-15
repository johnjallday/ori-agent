package workspacerun

import (
	"context"
	"os"
	"path/filepath"
)

type EnvironmentManager interface {
	Prepare(ctx context.Context, run *Run) (*Environment, error)
	TearDown(ctx context.Context, run *Run, env *Environment) error
}

type LocalEnvironmentManager struct {
	BaseTempDir string
}

func NewLocalEnvironmentManager(baseTempDir string) *LocalEnvironmentManager {
	return &LocalEnvironmentManager{BaseTempDir: baseTempDir}
}

func (m *LocalEnvironmentManager) Prepare(_ context.Context, run *Run) (*Environment, error) {
	env := run.Environment
	base := m.BaseTempDir
	if base == "" {
		base = os.TempDir()
	}
	tempDir, err := os.MkdirTemp(base, "ori-workspace-run-*")
	if err != nil {
		return nil, err
	}
	env.TempDir = tempDir
	env.LogPath = filepath.Join(tempDir, "run.log")
	return &env, nil
}

func (m *LocalEnvironmentManager) TearDown(_ context.Context, _ *Run, env *Environment) error {
	if env == nil || env.TempDir == "" {
		return nil
	}
	return os.RemoveAll(env.TempDir)
}
