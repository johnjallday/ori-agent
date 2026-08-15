// Package state provides atomic, user-local persistence for bridge records.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

const FileName = "state.json"

type Store struct {
	dir  string
	path string
}

func New(dir string) *Store {
	return &Store{dir: dir, path: filepath.Join(dir, FileName)}
}

func (s *Store) Path() string { return s.path }

func (s *Store) Load() (model.BridgeState, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.NewBridgeState(), nil
		}
		return model.BridgeState{}, fmt.Errorf("read state: %w", err)
	}
	var state model.BridgeState
	if err := json.Unmarshal(data, &state); err != nil {
		return model.BridgeState{}, fmt.Errorf("parse state: %w", err)
	}
	if state.Version != model.StateVersion {
		return model.BridgeState{}, fmt.Errorf("unsupported state version %d", state.Version)
	}
	if state.Features == nil {
		state.Features = make(map[string]model.FeatureState)
	}
	// A state file written before Overnight Runs existed carries no runs key.
	// That is a normal, supported shape, not a migration.
	if state.Runs == nil {
		state.Runs = make(map[string]model.OvernightRun)
	}
	// Same reasoning for PlanningSessions, added later still: a state file
	// written before issue planning existed simply has no key here.
	if state.PlanningSessions == nil {
		state.PlanningSessions = make(map[string]model.PlanningSession)
	}
	return state, nil
}

func (s *Store) Save(state model.BridgeState) error {
	if state.Version == 0 {
		state.Version = model.StateVersion
	}
	if state.Version != model.StateVersion {
		return fmt.Errorf("unsupported state version %d", state.Version)
	}
	if state.Features == nil {
		state.Features = make(map[string]model.FeatureState)
	}
	if state.Runs == nil {
		state.Runs = make(map[string]model.OvernightRun)
	}
	if state.PlanningSessions == nil {
		state.PlanningSessions = make(map[string]model.PlanningSession)
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	temp, err := os.CreateTemp(s.dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set temporary state permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary state: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}
