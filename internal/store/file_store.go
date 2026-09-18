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
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

type fileStore struct {
	mu              sync.Mutex
	path            string
	agents          map[string]*agent.Agent
	defaultSettings types.Settings

	// definitionHashes holds, per agent, the SHA-256 of its definition file as
	// this store last read or wrote it. A write first re-hashes the file on disk:
	// a mismatch means something outside Ori (a text editor, a sync tool, git)
	// changed it, and the store must not silently overwrite that edit.
	definitionHashes map[string][sha256.Size]byte

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
	// try load (non-fatal if file doesn't exist yet)
	if err := fs.load(); err != nil && !os.IsNotExist(err) {
		logger.Verbosef("Warning: failed to load store from %s: %v", path, err)
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

	return fs, nil
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

	// Remove agent from memory
	delete(s.agents, name)
	delete(s.definitionHashes, name)

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

	if err := s.persistAgentUnlocked(newName); err != nil {
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
	return s.writeIndexUnlocked()
}

func (s *fileStore) GetAgent(name string) (*agent.Agent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ag, ok := s.agents[name]
	return ag, ok
}

// SetAgent replaces an agent's record and writes its definition.
//
// It refuses, with ErrAgentChangedOnDisk, when the definition file was changed
// outside Ori since this store last read or wrote it: ag was built from the old
// content, so writing it would silently discard that edit. The store reloads
// the agent from disk first, so a retry starts from what is really there.
func (s *fileStore) SetAgent(name string, ag *agent.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed, err := s.definitionChangedOnDiskUnlocked(name)
	if err != nil {
		return err
	}
	if changed {
		if err := s.reloadAgentUnlocked(name); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s", ErrAgentChangedOnDisk, name)
	}
	s.agents[name] = ag
	if err := s.initializeNewAgentSkillsStateUnlocked(name); err != nil {
		return fmt.Errorf("initialize skill defaults: %w", err)
	}
	if err := s.persistAgentUnlocked(name); err != nil {
		return err
	}
	return s.writeIndexUnlocked()
}

// UpdateAgent applies updateFn to an agent and writes its definition.
//
// Unlike SetAgent it can merge with an outside edit: when the file changed on
// disk, the agent is reloaded and updateFn runs on the reloaded record, so both
// the edit and the update survive.
func (s *fileStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	if err := s.persistAgentUnlocked(name); err != nil {
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

// persistedDefinition is what an agent's definition file holds.
//
// This is an explicit projection, not a marshal of agent.Agent, so a new
// first-class field is invisible on disk until it is listed here. Appearance
// is listed for exactly that reason (FR-1/FR-68).
type persistedDefinition struct {
	Role         types.AgentRole        `json:"role,omitempty"`
	Capabilities []string               `json:"capabilities,omitempty"`
	Settings     types.Settings         `json:"Settings"`
	Status       types.AgentStatus      `json:"status,omitempty"`
	Statistics   *types.AgentStatistics `json:"statistics,omitempty"`
	Metadata     *types.AgentMetadata   `json:"metadata,omitempty"`
	Appearance   *types.AgentAppearance `json:"appearance,omitempty"`
	Evolution    *types.AgentEvolution  `json:"evolution,omitempty"`
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
		Status:       ag.Status,
		Statistics:   ag.Statistics,
		Metadata:     ag.Metadata,
		Appearance:   ag.Appearance,
		Evolution:    ag.Evolution,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
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

// persistAgentUnlocked writes one agent's definition file if, and only if, its
// serialized content differs from what is on disk. Assumes the lock is held.
func (s *fileStore) persistAgentUnlocked(name string) error {
	ag, ok := s.agents[name]
	if !ok || ag == nil {
		return nil
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
	return nil
}

// writeIndexUnlocked keeps the minimal index file for compatibility with older
// tooling. Like the definitions, it is only written when it differs.
func (s *fileStore) writeIndexUnlocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	return writeFileIfChanged(s.path, []byte("{}\n"))
}

func (s *fileStore) rememberDefinitionUnlocked(name string, data []byte) {
	if s.definitionHashes == nil {
		s.definitionHashes = make(map[string][sha256.Size]byte)
	}
	s.definitionHashes[name] = sha256.Sum256(data)
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
		return nil
	}
	if err != nil {
		return err
	}
	var ag agent.Agent
	if err := json.Unmarshal(data, &ag); err != nil {
		return fmt.Errorf("read agent %q: %w", name, err)
	}
	s.recordLegacyTypeUnlocked(name, detectLegacyType(data))
	s.normalizeLoadedAgent(name, &ag)
	s.agents[name] = &ag
	s.stripLegacyAgentTypeUnlocked()
	s.rememberDefinitionUnlocked(name, data)
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
	b, readErr := os.ReadFile(s.path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
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
				if entry.IsDir() {
					// New nested structure: agents/{name}/agent_settings.json contains everything
					agentName := entry.Name()
					settingsPath := filepath.Join(agentsDir, agentName, definitionFileName)

					var ag agent.Agent

					// Load full agent settings so nested persistence remains forward-compatible.
					if settingsData, err := os.ReadFile(settingsPath); err == nil { // #nosec G304 -- a directory entry under the store's own agents folder
						s.rememberDefinitionUnlocked(agentName, settingsData)
						if err := json.Unmarshal(settingsData, &ag); err == nil {
							logger.Verbosef("✅ Loaded agent '%s' from %s", agentName, settingsPath)
							s.recordLegacyTypeUnlocked(agentName, detectLegacyType(settingsData))
						} else {
							logger.Verbosef("❌ Failed to unmarshal agent_settings.json for '%s': %v", agentName, err)
						}
					} else {
						logger.Verbosef("⚠️ Could not read agent_settings.json for '%s': %v", agentName, err)
					}

					s.normalizeLoadedAgent(agentName, &ag)
					s.agents[agentName] = &ag
				} else if filepath.Ext(entry.Name()) == ".json" {
					// Legacy flat structure: agents/agent.json
					agentName := entry.Name()[:len(entry.Name())-5] // remove .json
					agentPath := filepath.Join(agentsDir, entry.Name())

					agentData, err := os.ReadFile(agentPath)
					if err != nil {
						continue
					}

					var ag agent.Agent
					if err := json.Unmarshal(agentData, &ag); err != nil {
						continue
					}
					s.recordLegacyTypeUnlocked(agentName, detectLegacyType(agentData))

					s.normalizeLoadedAgent(agentName, &ag)
					s.agents[agentName] = &ag
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
