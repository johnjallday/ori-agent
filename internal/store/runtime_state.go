package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/types"
)

// RuntimeState is what Ori records about an agent as it works: its status and
// its accumulated statistics and progression. It is kept apart from the
// agent's definition so that using an agent never rewrites the file the user
// authored.
//
// Status holds only active, idle, or error. A paused agent is a definition
// fact (the "paused" key), so disabled is never stored here.
type RuntimeState struct {
	Status     types.AgentStatus      `json:"status,omitempty"`
	Statistics *types.AgentStatistics `json:"statistics,omitempty"`
	Evolution  *types.AgentEvolution  `json:"evolution,omitempty"`
}

// rootKeyLength is how many hex characters of the path hash name a root's
// state folder: short enough to read, long enough never to collide in practice.
const rootKeyLength = 12

// RootKey names the state folder for the agents kept under dir, the folder
// that contains the agents folder. Two roots holding a same-named agent get
// different keys, so their statistics never mix.
func RootKey(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return hex.EncodeToString(sum[:])[:rootKeyLength]
}

// RuntimeStateStore keeps one state file per agent at
// <state dir>/<root key>/<Agent Name>.json. Writes are change-only and atomic,
// like the definition files.
type RuntimeStateStore struct {
	dir string
}

// NewRuntimeStateStore returns the state store for the agents kept under
// agentsParent, filed under stateRoot (normally <data dir>/agent_state).
func NewRuntimeStateStore(stateRoot, agentsParent string) *RuntimeStateStore {
	return &RuntimeStateStore{dir: filepath.Join(stateRoot, RootKey(agentsParent))}
}

// Dir is the folder holding this root's state files.
func (r *RuntimeStateStore) Dir() string {
	return r.dir
}

func (r *RuntimeStateStore) path(name string) string {
	return filepath.Join(r.dir, name+".json")
}

// Load returns an agent's saved state, and false when none is saved.
func (r *RuntimeStateStore) Load(name string) (RuntimeState, bool, error) {
	data, err := os.ReadFile(r.path(name)) // #nosec G304 -- inside the store's own state folder
	if errors.Is(err, os.ErrNotExist) {
		return RuntimeState{}, false, nil
	}
	if err != nil {
		return RuntimeState{}, false, err
	}
	var state RuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return RuntimeState{}, false, fmt.Errorf("read runtime state for %q: %w", name, err)
	}
	return state, true, nil
}

// Save writes an agent's state, unless the file already holds exactly it.
func (r *RuntimeStateStore) Save(name string, state RuntimeState) error {
	if state.Status == types.AgentStatusDisabled {
		state.Status = ""
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.dir, 0o750); err != nil {
		return err
	}
	return writeFileIfChanged(r.path(name), append(data, '\n'))
}

// Rename moves an agent's state to a new name. A missing state file is not an
// error: there is simply nothing to carry.
func (r *RuntimeStateStore) Rename(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	err := os.Rename(r.path(oldName), r.path(newName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Delete removes an agent's state.
func (r *RuntimeStateStore) Delete(name string) error {
	err := os.Remove(r.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
