package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

type fileStore struct {
	mu sync.Mutex
	// path is the index file (agents.json). Empty for a store that keeps no
	// index: the folder listing alone says which agents exist.
	path            string
	agents          map[string]*agent.Agent
	defaultSettings types.Settings

	// dir is the folder holding one folder per agent. Empty means derive it
	// from path (see agentsDir), which is how the data-dir store is built.
	dir string

	// definitionRequired skips a folder with no agent_settings.json instead of
	// loading it as an empty agent. A user-visible agents folder can hold
	// folders that are not agents, and Ori must not write definitions into them.
	definitionRequired bool

	// unreadable holds agents whose definition file is not valid JSON. They
	// are kept out of agents entirely, so nothing can overwrite the file, and
	// every write naming them is refused until the file is fixed.
	unreadable map[string]UnreadableAgent

	// definitionHashes holds, per agent, the SHA-256 of its definition file as
	// this store last read or wrote it. A write first re-hashes the file on disk:
	// a mismatch means something outside Ori (a text editor, a sync tool, git)
	// changed it, and the store must not silently overwrite that edit.
	definitionHashes map[string][sha256.Size]byte

	// knownDefinitions holds, per agent, the serialized definition as this store
	// last read or wrote it. A write whose definition still equals it changed
	// only runtime state, so the definition file is left alone.
	knownDefinitions map[string][]byte

	// state keeps status, statistics, and evolution out of the definition file.
	// Nil means "derive it from the agents folder" (see stateStore).
	state *RuntimeStateStore

	// legacyWorkspaceManagerAgents holds the names of agents whose on-disk
	// record still carries the retired "type": "workspace-manager" value.
	// agent.Agent no longer has a Type field, so this is captured by a second,
	// shim-typed decode of the raw bytes at load time (see detectLegacyType)
	// and consumed by stripLegacyAgentTypeUnlocked to preserve the metadata
	// tag that isStaleWorkspaceManagerAgent relies on.
	legacyWorkspaceManagerAgents map[string]bool
}

// legacyAgentTypeShim decodes only the retired "type" key from a raw agent
// record, independent of agent.Agent's current field set.
type legacyAgentTypeShim struct {
	Type string `json:"type"`
}

// detectLegacyType reports the raw "type" value (if any) still present in an
// agent record on disk. agent.Agent dropped the Type field, so this is the
// only way to see it.
func detectLegacyType(raw []byte) string {
	var shim legacyAgentTypeShim
	if err := json.Unmarshal(raw, &shim); err != nil {
		return ""
	}
	return shim.Type
}

func NewFileStore(path string, defaultSettings types.Settings) (Store, error) {
	fs := &fileStore{
		path:            path,
		agents:          make(map[string]*agent.Agent),
		defaultSettings: defaultSettings,
	}
	fs.open()
	return fs, nil
}

// NewSystemFileStore opens the data-dir store the composite store uses for the
// built-in assistant. Unlike NewFileStore it ignores a folder that holds no
// agent_settings.json: a stray skill-state folder must not become an empty
// agent that shadows a real one elsewhere.
func NewSystemFileStore(path string, defaultSettings types.Settings) (Store, error) {
	fs := &fileStore{
		path:               path,
		agents:             make(map[string]*agent.Agent),
		defaultSettings:    defaultSettings,
		definitionRequired: true,
	}
	fs.open()
	return fs, nil
}

// newDirStore opens a store kept directly in agentsDir, with runtime state in
// state and no index file: the folder listing is the only record of which
// agents exist. This is the shape of <workspace root>/Agents.
func newDirStore(agentsDir string, state *RuntimeStateStore, defaultSettings types.Settings) *fileStore {
	fs := &fileStore{
		dir:                agentsDir,
		agents:             make(map[string]*agent.Agent),
		defaultSettings:    defaultSettings,
		state:              state,
		definitionRequired: true,
	}
	fs.open()
	return fs
}

// open loads the store and runs the startup normalization.
func (fs *fileStore) open() {
	// try load (non-fatal if file doesn't exist yet)
	if err := fs.load(); err != nil && !os.IsNotExist(err) {
		logger.Verbosef("Warning: failed to load store from %s: %v", fs.agentsDir(), err)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Strip the retired "type" field, preserving the workspace-manager tag
	fs.stripLegacyAgentTypeUnlocked()

	if err := fs.initializeMissingAgentSkillsStateUnlocked(); err != nil {
		logger.Verbosef("Warning: failed to initialize missing agent skills state: %v", err)
	}

	// Only agents whose normalized form differs from what is on disk are
	// written, so an unchanged folder keeps its modification times.
	if err := fs.saveUnlocked(); err != nil {
		logger.Verbosef("Warning: failed to save store during initialization: %v", err)
	}
}

// stateStore returns where this store keeps runtime state. Unless one was
// given, it is <parent>/agent_state/<root key>, where <parent> holds the agents
// folder: for the data-dir store that is <data dir>/agent_state.
func (s *fileStore) stateStore() *RuntimeStateStore {
	if s.state == nil {
		parent := filepath.Dir(s.agentsDir())
		s.state = NewRuntimeStateStore(filepath.Join(parent, config.AgentStateDirName), parent)
	}
	return s.state
}

// PersistencePaths reports the same paths used by this owner's writers.
//
// The third path used to be a copy of the index written into the process
// working directory for the retired plugin system. Nothing reads it any more,
// so it now names the index itself; reset removes each path once.
func (s *fileStore) PersistencePaths() (index, profiles, projection string) {
	return s.path, s.agentsDir(), s.path
}

func (s *fileStore) ListAgents() (names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names = make([]string, 0, len(s.agents))
	for n := range s.agents {
		names = append(names, n)
	}
	return names
}

func (s *fileStore) CreateAgent(name string, config *CreateAgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.unreadableErrorUnlocked(name); err != nil {
		return err
	}
	if _, exists := s.agents[name]; !exists {
		// A definition that appeared on disk since the store loaded is an
		// existing agent, not a name free to be overwritten with defaults.
		changed, err := s.definitionChangedOnDiskUnlocked(name)
		if err != nil {
			return err
		}
		if changed {
			if err := s.reloadAgentUnlocked(name); err != nil {
				return err
			}
		}
	}
	created := false
	if _, exists := s.agents[name]; !exists {
		defaultSettings := s.defaultSettings

		// Apply config overrides if provided
		role := types.RoleGeneral
		if config != nil {
			if config.Role != "" {
				role = config.Role
			}
			if config.Model != "" {
				defaultSettings.Model = config.Model
			}
			if config.Temperature > 0 {
				defaultSettings.Temperature = config.Temperature
			}
			if config.SystemPrompt != "" {
				defaultSettings.SystemPrompt = config.SystemPrompt
			}
			if config.LLMProvider != "" {
				defaultSettings.Provider = config.LLMProvider
			}
			if config.ReasoningEffort != "" {
				defaultSettings.ReasoningEffort = config.ReasoningEffort
			}
			if config.MaxOutputTokens > 0 {
				defaultSettings.MaxOutputTokens = config.MaxOutputTokens
			}
			if config.AllowWebSearch != nil {
				allow := *config.AllowWebSearch
				defaultSettings.AllowWebSearch = &allow
			}
		}
		if role == "" {
			role = types.RoleGeneral
		}
		// Keep only a level this provider/model accepts (Codex has no "max";
		// API providers take none).
		defaultSettings.ReasoningEffort = types.NormalizeReasoningEffortFor(
			defaultSettings.Provider, defaultSettings.Model, defaultSettings.ReasoningEffort)

		newAgent := &agent.Agent{
			Role:         role,
			Capabilities: []string{}, // Empty capabilities by default
			Settings:     defaultSettings,
			Status:       types.AgentStatusActive, // New agents start as active
		}
		if config != nil && config.Appearance != nil {
			newAgent.Appearance = config.Appearance.Clone()
			newAgent.Appearance.Normalize()
		}
		// Initialize statistics for the new agent
		newAgent.InitializeStatistics()
		// Initialize evolution defaults for the new agent
		newAgent.InitializeEvolution()
		s.agents[name] = newAgent
		created = true
	}

	if created {
		if err := s.initializeNewAgentSkillsStateUnlocked(name); err != nil {
			return fmt.Errorf("initialize skill defaults: %w", err)
		}
		if err := s.persistAgentUnlocked(name); err != nil {
			return err
		}
	}

	return s.writeIndexUnlocked()
}

// agentsDir resolves the directory that holds one folder per agent.
//
// s.path may already point inside an agents/ tree (in which case that tree is
// the answer) or at a plain index file beside it. Both shapes are in the wild,
// so every caller has to resolve the same way.
func (s *fileStore) agentsDir() string {
	if s.dir != "" {
		return s.dir
	}
	if strings.Contains(s.path, "/agents/") || strings.Contains(s.path, "\\agents\\") {
		agentsDirIndex := strings.LastIndex(s.path, "/agents/")
		if agentsDirIndex == -1 {
			agentsDirIndex = strings.LastIndex(s.path, "\\agents\\")
		}
		if agentsDirIndex != -1 {
			return s.path[:agentsDirIndex+7] // +7 to include "/agents"
		}
	}
	return filepath.Join(filepath.Dir(s.path), "agents")
}

func (s *fileStore) initializeNewAgentSkillsStateUnlocked(agentName string) error {
	if strings.TrimSpace(agentName) == "" {
		return fmt.Errorf("agent name is required")
	}

	skillsStatePath := filepath.Join(s.agentsDir(), agentName, "skills_state.json")
	if _, err := os.Stat(skillsStatePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	type persistedSkillState struct {
		Enabled   bool      `json:"enabled"`
		Trusted   bool      `json:"trusted"`
		UpdatedAt time.Time `json:"updated_at,omitempty"`
	}
	type persistedSkillRegistry struct {
		Skills map[string]persistedSkillState `json:"skills"`
	}

	registry := persistedSkillRegistry{
		Skills: map[string]persistedSkillState{
			"*": {
				Enabled:   false,
				Trusted:   false,
				UpdatedAt: time.Now().UTC(),
			},
		},
	}

	payload, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(skillsStatePath), 0o755); err != nil {
		return err
	}

	return os.WriteFile(skillsStatePath, payload, 0o644)
}

func (s *fileStore) initializeMissingAgentSkillsStateUnlocked() error {
	for agentName := range s.agents {
		if err := s.initializeNewAgentSkillsStateUnlocked(agentName); err != nil {
			return fmt.Errorf("%s: %w", agentName, err)
		}
	}
	return nil
}

func (s *fileStore) DeleteAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Never remove the folder of an agent whose file could not be read: the
	// user fixes that file, Ori does not throw it away.
	if err := s.unreadableErrorUnlocked(name); err != nil {
		return err
	}

	// Remove agent from memory
	delete(s.agents, name)
	delete(s.definitionHashes, name)
	delete(s.knownDefinitions, name)
	if err := s.stateStore().Delete(name); err != nil {
		logger.Verbosef("Warning: failed to remove runtime state for %s: %v", name, err)
	}

	// Delete the agent folder from filesystem
	agentFolder := filepath.Join(s.agentsDir(), name)
	if err := os.RemoveAll(agentFolder); err != nil && !os.IsNotExist(err) {
		// Log error but don't fail the operation since agent is already removed from memory
		logger.Verbosef("Warning: failed to remove agent folder %s: %v", agentFolder, err)
	}

	return s.writeIndexUnlocked()
}

// RenameAgent moves an agent record and its entire on-disk folder to a new name.
//
// This exists because the obvious spelling — SetAgent(newName, record) followed
// by DeleteAgent(oldName) — is lossy. SetAgent only carries the in-memory
// agent.Agent, while the agent folder also holds skills_state.json and the
// per-agent skills/ tree; DeleteAgent then os.RemoveAll's all of it. Moving the
// folder keeps every sidecar, including ones added after this code was written.
//
// It refuses to overwrite an existing agent: resolving a name collision is the
// caller's decision, and it must not be made destructively (FR55/FR60).
func (s *fileStore) RenameAgent(oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return fmt.Errorf("both the current and new agent name are required")
	}
	for _, name := range []string{oldName, newName} {
		if err := s.unreadableErrorUnlocked(name); err != nil {
			return err
		}
	}

	// The folder moves as it is on disk, so the record that follows it must be
	// the one on disk too; otherwise the save below would undo an outside edit.
	changed, err := s.definitionChangedOnDiskUnlocked(oldName)
	if err != nil {
		return err
	}
	if changed {
		if err := s.reloadAgentUnlocked(oldName); err != nil {
			return err
		}
	}

	record, exists := s.agents[oldName]
	if !exists {
		return fmt.Errorf("agent %q not found", oldName)
	}
	if oldName == newName {
		return nil
	}
	if _, taken := s.agents[newName]; taken {
		return fmt.Errorf("agent %q already exists", newName)
	}

	agentsDir := s.agentsDir()
	oldFolder := filepath.Join(agentsDir, oldName)
	newFolder := filepath.Join(agentsDir, newName)

	// Never merge into an existing folder — a stale directory under the target
	// name would silently mix two agents' sidecars. A case-only rename on a
	// case-insensitive filesystem is the one exception: both paths identify the
	// same directory, and os.Rename safely updates its presentation casing.
	oldInfo, oldStatErr := os.Stat(oldFolder)
	if newInfo, err := os.Stat(newFolder); err == nil {
		if oldStatErr != nil || !os.SameFile(oldInfo, newInfo) {
			return fmt.Errorf("agent folder %q already exists", newFolder)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// The folder is only moved when it exists; a record can legitimately live in
	// memory before anything has forced a save.
	if oldStatErr == nil {
		// 0o750, not the 0o755 the older writes in this file use: an agent folder
		// holds the agent's prompt and its per-agent skill state, which no other
		// user on the machine needs to read.
		if err := os.MkdirAll(filepath.Dir(newFolder), 0o750); err != nil {
			return err
		}
		if err := os.Rename(oldFolder, newFolder); err != nil {
			return fmt.Errorf("move agent folder: %w", err)
		}
	} else if !os.IsNotExist(oldStatErr) {
		return oldStatErr
	}

	s.agents[newName] = record
	delete(s.agents, oldName)
	if hash, known := s.definitionHashes[oldName]; known {
		delete(s.definitionHashes, oldName)
		s.definitionHashes[newName] = hash
	}
	if definition, known := s.knownDefinitions[oldName]; known {
		delete(s.knownDefinitions, oldName)
		s.knownDefinitions[newName] = definition
	}
	if err := s.stateStore().Rename(oldName, newName); err != nil {
		return fmt.Errorf("move runtime state: %w", err)
	}

	if err := s.writeAgentUnlocked(newName); err != nil {
		return err
	}
	if err := s.writeIndexUnlocked(); err != nil {
		return err
	}

	// Verify the destination is durable before reporting success, so a caller
	// running a migration knows the move actually landed (FR60).
	if _, err := os.Stat(filepath.Join(newFolder, "agent_settings.json")); err != nil {
		return fmt.Errorf("verify migrated agent %q: %w", newName, err)
	}
	return nil
}

func (s *fileStore) ClearAgents() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents = make(map[string]*agent.Agent)
	s.definitionHashes = nil
	s.knownDefinitions = nil
	return s.writeIndexUnlocked()
}

func (s *fileStore) GetAgent(name string) (*agent.Agent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ag, ok := s.agents[name]
	return ag, ok
}

// SetAgent replaces an agent's record and writes what changed: its runtime
// state always, its definition only when the definition changed.
//
// When the definition file was changed outside Ori since this store last read
// or wrote it, ag was built from the old content. If ag's definition is that
// old content unchanged, the caller only updated runtime state (a chat turn
// counting statistics, say): the edit on disk is reloaded and only the state
// is written. Otherwise writing ag would silently discard the edit, so SetAgent
// reloads the agent and refuses with ErrAgentChangedOnDisk; a retry then
// starts from what is really there.
func (s *fileStore) SetAgent(name string, ag *agent.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.unreadableErrorUnlocked(name); err != nil {
		return err
	}
	changed, err := s.definitionChangedOnDiskUnlocked(name)
	if err != nil {
		return err
	}
	if changed {
		stateOnly, err := s.definitionUnchangedUnlocked(name, ag)
		if err != nil {
			return err
		}
		state := runtimeStateOf(ag)
		if err := s.reloadAgentUnlocked(name); err != nil {
			return err
		}
		current, exists := s.agents[name]
		if !stateOnly || !exists {
			return fmt.Errorf("%w: %s", ErrAgentChangedOnDisk, name)
		}
		applyRuntimeState(current, state)
		return s.persistStateUnlocked(name)
	}
	s.agents[name] = ag
	if err := s.initializeNewAgentSkillsStateUnlocked(name); err != nil {
		return fmt.Errorf("initialize skill defaults: %w", err)
	}
	if err := s.writeAgentUnlocked(name); err != nil {
		return err
	}
	return s.writeIndexUnlocked()
}

// UpdateAgent applies updateFn to an agent and writes what changed.
//
// Unlike SetAgent it can merge with an outside edit: when the file changed on
// disk, the agent is reloaded and updateFn runs on the reloaded record, so both
// the edit and the update survive.
func (s *fileStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.unreadableErrorUnlocked(name); err != nil {
		return err
	}

	changed, err := s.definitionChangedOnDiskUnlocked(name)
	if err != nil {
		return err
	}
	if changed {
		if err := s.reloadAgentUnlocked(name); err != nil {
			return err
		}
	}

	ag, ok := s.agents[name]
	if !ok || ag == nil {
		return fmt.Errorf("agent %q not found", name)
	}

	if err := updateFn(ag); err != nil {
		return err
	}

	if err := s.writeAgentUnlocked(name); err != nil {
		return err
	}
	return s.writeIndexUnlocked()
}

func (s *fileStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked()
}

// ---------- persistence helpers (no Messages persisted) ----------

// definitionFileName is the per-agent definition file inside an agent folder.
const definitionFileName = "agent_settings.json"

// persistedDefinition is what an agent's definition file holds: only what the
// user authored. Status, statistics, and evolution are runtime state and live
// in the RuntimeStateStore, so using an agent never rewrites this file.
//
// This is an explicit projection, not a marshal of agent.Agent, so a new
// first-class field is invisible on disk until it is listed here. Appearance
// is listed for exactly that reason (FR-1/FR-68).
type persistedDefinition struct {
	Role         types.AgentRole        `json:"role,omitempty"`
	Capabilities []string               `json:"capabilities,omitempty"`
	Settings     types.Settings         `json:"Settings"`
	Metadata     *types.AgentMetadata   `json:"metadata,omitempty"`
	Appearance   *types.AgentAppearance `json:"appearance,omitempty"`
	// Paused travels with the agent. In memory it is Status == disabled, which
	// every runtime check already refuses.
	Paused bool `json:"paused,omitempty"`
}

// encodeDefinition serializes an agent in the stable on-disk form: two-space
// indent, struct-order keys (maps sort their keys), one trailing newline.
func encodeDefinition(ag *agent.Agent) ([]byte, error) {
	// Canonicalize immediately before serializing, so a record written by any
	// code path — not just the migrating load path — is canonical on disk.
	ag.EnsureAppearance()
	data, err := json.MarshalIndent(persistedDefinition{
		Role:         ag.Role,
		Capabilities: ag.Capabilities,
		Settings:     ag.Settings,
		Metadata:     ag.Metadata,
		Appearance:   ag.Appearance,
		Paused:       ag.Status == types.AgentStatusDisabled,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// runtimeStateOf is the part of an agent that is runtime state, not definition.
func runtimeStateOf(ag *agent.Agent) RuntimeState {
	state := RuntimeState{Status: ag.Status, Statistics: ag.Statistics, Evolution: ag.Evolution}
	if state.Status == types.AgentStatusDisabled {
		state.Status = ""
	}
	return state
}

// applyRuntimeState puts saved runtime state on an agent. A paused agent stays
// paused: that is a definition fact the state cannot override.
func applyRuntimeState(ag *agent.Agent, state RuntimeState) {
	if state.Statistics != nil {
		ag.Statistics = state.Statistics
	}
	if state.Evolution != nil {
		ag.Evolution = state.Evolution
	}
	if ag.Status == types.AgentStatusDisabled {
		return
	}
	if state.Status != "" {
		ag.Status = state.Status
	} else if ag.Status == "" {
		ag.Status = types.AgentStatusIdle
	}
}

// pausedShim reads only the "paused" key of a definition file.
type pausedShim struct {
	Paused bool `json:"paused"`
}

// decodeAgentUnlocked builds an agent from its definition file plus its saved
// runtime state. A legacy file that still carries status, statistics, or
// evolution supplies them as the initial state until a state file exists; it
// is rewritten in the new shape by the next save. Assumes the lock is held.
func (s *fileStore) decodeAgentUnlocked(name string, data []byte) (*agent.Agent, error) {
	var ag agent.Agent
	if err := json.Unmarshal(data, &ag); err != nil {
		return nil, err
	}
	var shim pausedShim
	_ = json.Unmarshal(data, &shim)
	if shim.Paused {
		ag.Status = types.AgentStatusDisabled
	}
	state, ok, err := s.stateStore().Load(name)
	if err != nil {
		logger.Warn("Agent runtime state could not be read; starting it fresh", logger.Fields{
			"agent": name,
			"error": err.Error(),
		})
	} else if ok {
		applyRuntimeState(&ag, state)
	}
	s.recordLegacyTypeUnlocked(name, detectLegacyType(data))
	s.normalizeLoadedAgent(name, &ag)
	return &ag, nil
}

func (s *fileStore) definitionPath(name string) string {
	return filepath.Join(s.agentsDir(), name, definitionFileName)
}

// saveUnlocked writes every agent, each only if its content changed. An agent
// whose file was changed outside Ori is reloaded instead: the disk wins.
func (s *fileStore) saveUnlocked() error {
	names := make([]string, 0, len(s.agents))
	for name := range s.agents {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		changed, err := s.definitionChangedOnDiskUnlocked(name)
		if err != nil {
			return err
		}
		if changed {
			if err := s.reloadAgentUnlocked(name); err != nil {
				logger.Warn("Agent definition changed on disk and could not be reloaded", logger.Fields{
					"agent": name,
					"error": err.Error(),
				})
			}
			continue
		}
		if err := s.persistAgentUnlocked(name); err != nil {
			return err
		}
	}
	return s.writeIndexUnlocked()
}

// writeAgentUnlocked writes what an operation changed: the runtime state
// always, the definition file only when the definition itself changed. A
// statistics update therefore leaves agent_settings.json byte-identical, even
// when the user formatted it differently. Assumes the lock is held.
func (s *fileStore) writeAgentUnlocked(name string) error {
	unchanged, err := s.definitionUnchangedUnlocked(name, s.agents[name])
	if err != nil {
		return err
	}
	if unchanged {
		return s.persistStateUnlocked(name)
	}
	return s.persistAgentUnlocked(name)
}

// definitionUnchangedUnlocked reports whether ag's definition is exactly the
// one this store last read or wrote for name.
func (s *fileStore) definitionUnchangedUnlocked(name string, ag *agent.Agent) (bool, error) {
	known, ok := s.knownDefinitions[name]
	if !ok || ag == nil {
		return false, nil
	}
	data, err := encodeDefinition(ag)
	if err != nil {
		return false, err
	}
	return bytes.Equal(data, known), nil
}

// persistAgentUnlocked writes one agent's runtime state and definition file,
// each if, and only if, its serialized content differs from what is on disk.
// A definition file in an older shape is rewritten in the current one here.
// The state goes first, so stripping legacy state keys never loses them.
// Assumes the lock is held.
func (s *fileStore) persistAgentUnlocked(name string) error {
	ag, ok := s.agents[name]
	if !ok || ag == nil {
		return nil
	}
	if err := s.persistStateUnlocked(name); err != nil {
		return err
	}
	data, err := encodeDefinition(ag)
	if err != nil {
		return err
	}
	path := s.definitionPath(name)
	// 0o750: an agent folder holds the agent's prompt and per-agent skill
	// state, which no other user on the machine needs to read.
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := writeFileIfChanged(path, data); err != nil {
		return err
	}
	s.rememberDefinitionUnlocked(name, data)
	s.rememberKnownDefinitionUnlocked(name, data)
	return nil
}

// persistStateUnlocked writes one agent's runtime state if it changed.
func (s *fileStore) persistStateUnlocked(name string) error {
	ag, ok := s.agents[name]
	if !ok || ag == nil {
		return nil
	}
	return s.stateStore().Save(name, runtimeStateOf(ag))
}

// writeIndexUnlocked keeps the minimal index file for compatibility with older
// tooling. Like the definitions, it is only written when it differs.
func (s *fileStore) writeIndexUnlocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	return writeFileIfChanged(s.path, []byte("{}\n"))
}

// markUnreadableUnlocked sets an agent aside because its definition file could
// not be decoded. Assumes the lock is held.
func (s *fileStore) markUnreadableUnlocked(name, path string, err error) {
	if s.unreadable == nil {
		s.unreadable = make(map[string]UnreadableAgent)
	}
	s.unreadable[name] = UnreadableAgent{Name: name, File: path, Error: err.Error()}
	logger.Warn("Agent definition could not be read; it is left untouched", logger.Fields{
		"agent": name,
		"file":  path,
		"error": err.Error(),
	})
}

// unreadableErrorUnlocked refuses a write naming an unreadable agent, or nil.
func (s *fileStore) unreadableErrorUnlocked(name string) error {
	if entry, ok := s.unreadable[name]; ok {
		return &UnreadableAgentError{UnreadableAgent: entry}
	}
	return nil
}

// UnreadableAgents lists the agents whose definition file could not be read.
func (s *fileStore) UnreadableAgents() []UnreadableAgent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UnreadableAgent, 0, len(s.unreadable))
	for _, entry := range s.unreadable {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// rememberDefinitionUnlocked records the bytes of an agent's definition file
// as this store last read or wrote them.
func (s *fileStore) rememberDefinitionUnlocked(name string, data []byte) {
	if s.definitionHashes == nil {
		s.definitionHashes = make(map[string][sha256.Size]byte)
	}
	s.definitionHashes[name] = sha256.Sum256(data)
}

// rememberKnownDefinitionUnlocked records an agent's serialized definition as
// it was last synchronized with disk.
func (s *fileStore) rememberKnownDefinitionUnlocked(name string, data []byte) {
	if s.knownDefinitions == nil {
		s.knownDefinitions = make(map[string][]byte)
	}
	s.knownDefinitions[name] = data
}

// rememberLoadedUnlocked records a freshly loaded agent: the file bytes for
// on-disk change detection, and its serialized definition as the known one.
func (s *fileStore) rememberLoadedUnlocked(name string, fileData []byte, ag *agent.Agent) {
	s.rememberDefinitionUnlocked(name, fileData)
	if encoded, err := encodeDefinition(ag); err == nil {
		s.rememberKnownDefinitionUnlocked(name, encoded)
	}
}

// definitionChangedOnDiskUnlocked reports whether an agent's definition file no
// longer matches what this store last read or wrote: edited, created, or
// deleted by something other than this store. Assumes the lock is held.
func (s *fileStore) definitionChangedOnDiskUnlocked(name string) (bool, error) {
	known, haveKnown := s.definitionHashes[name]
	data, err := os.ReadFile(s.definitionPath(name))
	if errors.Is(err, os.ErrNotExist) {
		// Gone since we last saw it, or never written: only the former counts.
		return haveKnown, nil
	}
	if err != nil {
		return false, err
	}
	return !haveKnown || sha256.Sum256(data) != known, nil
}

// reloadAgentUnlocked replaces one agent's in-memory record with what is on
// disk, and forgets the agent when its file is gone. Assumes the lock is held.
func (s *fileStore) reloadAgentUnlocked(name string) error {
	data, err := os.ReadFile(s.definitionPath(name))
	if errors.Is(err, os.ErrNotExist) {
		delete(s.agents, name)
		delete(s.definitionHashes, name)
		delete(s.knownDefinitions, name)
		return nil
	}
	if err != nil {
		return err
	}
	ag, err := s.decodeAgentUnlocked(name, data)
	if err != nil {
		delete(s.agents, name)
		s.markUnreadableUnlocked(name, s.definitionPath(name), err)
		return s.unreadableErrorUnlocked(name)
	}
	delete(s.unreadable, name)
	s.agents[name] = ag
	s.stripLegacyAgentTypeUnlocked()
	s.rememberLoadedUnlocked(name, data, ag)
	return nil
}

// writeFileIfChanged writes data to path atomically, unless the file already
// holds exactly those bytes. Skipping the identical write is what keeps an
// unchanged file's modification time, so git and sync tools see no change.
func writeFileIfChanged(path string, data []byte) error {
	current, err := os.ReadFile(path) // #nosec G304 -- path is built by the owning store, never taken from a request
	if err == nil && bytes.Equal(current, data) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeFileAtomic(path, data)
}

// writeFileAtomic writes to a temporary file in the target's own folder and
// renames it over the target, so a reader or a crash never sees a partial file.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func (s *fileStore) load() error {
	// The index file (s.path) is only a compatibility stub; the agents/ directory
	// is the source of truth. A missing index must therefore NOT abort loading —
	// otherwise agents present on disk (e.g. freshly adopted from a legacy
	// location) would be ignored until a later save recreated the index.
	var b []byte
	readErr := os.ErrNotExist
	if s.path != "" {
		b, readErr = os.ReadFile(s.path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Initialize agents map if nil
	if s.agents == nil {
		s.agents = make(map[string]*agent.Agent)
	}

	if readErr == nil {
		// Try to parse the JSON index.
		var rawConfig map[string]any
		if err := json.Unmarshal(b, &rawConfig); err != nil {
			return err
		}

		// Check if this is the old format with "agents" key
		if _, hasAgents := rawConfig["agents"]; hasAgents {
			// Old format: {"agents": {...}, "current": "..."}
			var in struct {
				Agents map[string]*agent.Agent `json:"agents"`
			}
			if err := json.Unmarshal(b, &in); err != nil {
				return err
			}
			var rawIn struct {
				Agents map[string]json.RawMessage `json:"agents"`
			}
			if err := json.Unmarshal(b, &rawIn); err == nil {
				for agentName, raw := range rawIn.Agents {
					s.recordLegacyTypeUnlocked(agentName, detectLegacyType(raw))
				}
			}
			if in.Agents != nil {
				s.agents = in.Agents
			}
			// Normalize migrated agents from legacy schema.
			for agentName, ag := range s.agents {
				s.normalizeLoadedAgent(agentName, ag)
			}
			return nil
		}
	}

	// Load individual agent files from agents/ directory (nested structure)
	agentsDir := s.agentsDir()
	if _, err := os.Stat(agentsDir); err == nil {
		// agents/ directory exists, check for nested structure
		entries, err := os.ReadDir(agentsDir)
		if err == nil {
			for _, entry := range entries {
				// Agent names cannot start with a dot, so a dot-folder is tooling
				// (.git, a sync tool's cache, an interrupted migration), never an agent.
				if strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				if entry.IsDir() {
					// New nested structure: agents/{name}/agent_settings.json contains everything
					agentName := entry.Name()
					settingsPath := filepath.Join(agentsDir, agentName, definitionFileName)
					if s.definitionRequired {
						if _, err := os.Stat(settingsPath); errors.Is(err, os.ErrNotExist) {
							continue
						}
					}

					var ag *agent.Agent

					// Load full agent settings so nested persistence remains forward-compatible.
					if settingsData, err := os.ReadFile(settingsPath); err == nil { // #nosec G304 -- a directory entry under the store's own agents folder
						if decoded, err := s.decodeAgentUnlocked(agentName, settingsData); err == nil {
							logger.Verbosef("✅ Loaded agent '%s' from %s", agentName, settingsPath)
							ag = decoded
							s.rememberLoadedUnlocked(agentName, settingsData, ag)
						} else {
							// Not loaded as an empty agent: that would be written back
							// over the user's file on the next save.
							s.markUnreadableUnlocked(agentName, settingsPath, err)
							continue
						}
					} else {
						logger.Verbosef("⚠️ Could not read agent_settings.json for '%s': %v", agentName, err)
					}

					if ag == nil {
						ag = &agent.Agent{}
						s.normalizeLoadedAgent(agentName, ag)
					}
					s.agents[agentName] = ag
				} else if filepath.Ext(entry.Name()) == ".json" {
					// Legacy flat structure: agents/agent.json
					agentName := entry.Name()[:len(entry.Name())-5] // remove .json
					agentPath := filepath.Join(agentsDir, entry.Name())

					agentData, err := os.ReadFile(agentPath) // #nosec G304 -- a directory entry under the store's own agents folder
					if err != nil {
						continue
					}

					ag, err := s.decodeAgentUnlocked(agentName, agentData)
					if err != nil {
						continue
					}
					s.agents[agentName] = ag
				}
			}
		}
	}

	return nil
}

// normalizeLoadedAgent applies defaults for backward compatibility while preserving
// explicit values from disk.
func (s *fileStore) normalizeLoadedAgent(name string, ag *agent.Agent) {
	if ag == nil {
		return
	}

	if ag.Capabilities == nil {
		ag.Capabilities = []string{}
	}
	if ag.Status == "" {
		ag.Status = types.AgentStatusIdle
	}
	if ag.Statistics == nil {
		ag.InitializeStatistics()
	}
	if ag.Evolution == nil {
		ag.InitializeEvolution()
	} else {
		ag.Evolution.EnsureDefaults()
	}

	// Appearance migration runs here rather than in a one-shot startup pass so
	// that any record reaching memory is canonical, whatever path loaded it. The
	// migrated value is only in memory at this point; it reaches disk through the
	// normal atomic save, so a crash mid-startup leaves the original file intact
	// (FR-75).
	result := ag.MigrateAppearance(agent.DefaultAppearanceEnvironment(agent.AppearanceUploadDir))
	if len(result.Reasons) > 0 {
		agent.RecordAppearanceMigrationNote(agent.AppearanceMigrationNote{
			Agent:   name,
			Scope:   "global",
			Reasons: result.Reasons,
		})
		logger.Warn("Agent appearance migrated with notes", logger.Fields{
			"agent":   name,
			"reasons": strings.Join(result.Reasons, ", "),
		})
	}
}

// recordLegacyTypeUnlocked notes that an agent's on-disk record still carries
// the retired "type": "workspace-manager" value, so stripLegacyAgentTypeUnlocked
// can preserve it as a metadata tag. Assumes the lock is already held.
func (s *fileStore) recordLegacyTypeUnlocked(agentName, rawType string) {
	if rawType != "workspace-manager" {
		return
	}
	if s.legacyWorkspaceManagerAgents == nil {
		s.legacyWorkspaceManagerAgents = make(map[string]bool)
	}
	s.legacyWorkspaceManagerAgents[agentName] = true
}

// stripLegacyAgentTypeUnlocked completes the removal of the retired "type"
// field. agent.Agent no longer has a Type field, so every agent file is
// already stripped of "type" as soon as it round-trips through load() and
// saveUnlocked(). The only remaining job is to preserve the one thing that
// field's value was used for: an agent whose raw "type" was
// "workspace-manager" gets that value added to Metadata.Tags, so
// isStaleWorkspaceManagerAgent keeps recognizing it (FR7, FR10). No other
// state is touched — a user's model is never rewritten. Idempotent: running
// it again on an already-stripped store is a no-op because the tag is
// already present and legacyWorkspaceManagerAgents is empty.
// Assumes the lock is already held.
func (s *fileStore) stripLegacyAgentTypeUnlocked() {
	for agentName := range s.legacyWorkspaceManagerAgents {
		ag, ok := s.agents[agentName]
		if !ok || ag == nil {
			continue
		}
		if ag.Metadata == nil {
			ag.Metadata = &types.AgentMetadata{}
		}
		hasTag := false
		for _, tag := range ag.Metadata.Tags {
			if strings.EqualFold(strings.TrimSpace(tag), "workspace-manager") {
				hasTag = true
				break
			}
		}
		if !hasTag {
			ag.Metadata.Tags = append(ag.Metadata.Tags, "workspace-manager")
		}
	}
}
