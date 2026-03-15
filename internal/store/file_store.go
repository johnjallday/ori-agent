package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	current         string
	defaultSettings types.Settings
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

	// Migrate existing agents to have types
	fs.migrateAgentTypesUnlocked()

	if err := fs.saveUnlocked(); err != nil {
		logger.Verbosef("Warning: failed to save store during initialization: %v", err)
	}

	// Write agents.json for plugins on startup
	if err := fs.writeAgentsJSON(); err != nil {
		logger.Verbosef("Warning: failed to write agents.json during initialization: %v", err)
	}

	return fs, nil
}

func (s *fileStore) ListAgents() (names []string, current string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names = make([]string, 0, len(s.agents))
	for n := range s.agents {
		names = append(names, n)
	}
	return names, s.current
}

func (s *fileStore) SetCurrentAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.agents[name]; !exists {
		return fmt.Errorf("agent not found")
	}

	s.current = name
	return s.saveUnlocked()
}

func (s *fileStore) CreateAgent(name string, config *CreateAgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	created := false
	if _, exists := s.agents[name]; !exists {
		// Get default settings - either from current agent or use hardcoded defaults
		var defaultSettings types.Settings
		if s.current != "" && s.agents[s.current] != nil {
			// Copy from current agent (preserve provider/max tokens too)
			defaultSettings = s.agents[s.current].Settings
		} else {
			// Use hardcoded defaults if no current agent exists
			defaultSettings = s.defaultSettings
		}

		// Apply config overrides if provided
		agentType := agent.TypeToolCalling // Default to cheapest tier
		if config != nil {
			if config.Type != "" {
				agentType = config.Type
			}
			if config.Model != "" {
				defaultSettings.Model = config.Model
				// Auto-detect agent type from model if type not explicitly provided
				if config.Type == "" {
					agentType = agent.GetTypeForModel(config.Model)
				}
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
		if defaultSettings.EffectiveReasoningEffort(defaultSettings.Provider) == "" {
			defaultSettings.ReasoningEffort = ""
		}

		newAgent := &agent.Agent{
			Type:         agentType,
			Role:         types.RoleGeneral, // Default role
			Capabilities: []string{},        // Empty capabilities by default
			Settings:     defaultSettings,
			Plugins:      make(map[string]types.LoadedPlugin),
			Status:       types.AgentStatusActive, // New agents start as active
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
	}

	return s.saveUnlocked()
}

func (s *fileStore) initializeNewAgentSkillsStateUnlocked(agentName string) error {
	if strings.TrimSpace(agentName) == "" {
		return fmt.Errorf("agent name is required")
	}

	baseDir := filepath.Dir(s.path)
	var agentsDir string
	if strings.Contains(s.path, "/agents/") || strings.Contains(s.path, "\\agents\\") {
		agentsDirIndex := strings.LastIndex(s.path, "/agents/")
		if agentsDirIndex == -1 {
			agentsDirIndex = strings.LastIndex(s.path, "\\agents\\")
		}
		if agentsDirIndex != -1 {
			agentsDir = s.path[:agentsDirIndex+7]
		} else {
			agentsDir = filepath.Join(baseDir, "agents")
		}
	} else {
		agentsDir = filepath.Join(baseDir, "agents")
	}

	skillsStatePath := filepath.Join(agentsDir, agentName, "skills_state.json")
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

func (s *fileStore) DeleteAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove agent from memory
	delete(s.agents, name)

	// Update current agent if it was deleted
	if s.current == name {
		s.current = ""
		for k := range s.agents {
			s.current = k
			break
		}
	}

	// Delete the agent folder from filesystem
	baseDir := filepath.Dir(s.path)
	var agentsDir string
	if strings.Contains(s.path, "/agents/") || strings.Contains(s.path, "\\agents\\") {
		// Path already contains agents directory structure
		// Find the agents directory and get its parent + "agents"
		agentsDirIndex := strings.LastIndex(s.path, "/agents/")
		if agentsDirIndex == -1 {
			agentsDirIndex = strings.LastIndex(s.path, "\\agents\\")
		}
		if agentsDirIndex != -1 {
			agentsDir = s.path[:agentsDirIndex+7] // +7 to include "/agents"
		} else {
			agentsDir = filepath.Join(baseDir, "agents")
		}
	} else {
		// Path is something like config.json, need to create agents subdir
		agentsDir = filepath.Join(baseDir, "agents")
	}
	agentFolder := filepath.Join(agentsDir, name)
	if err := os.RemoveAll(agentFolder); err != nil && !os.IsNotExist(err) {
		// Log error but don't fail the operation since agent is already removed from memory
		logger.Verbosef("Warning: failed to remove agent folder %s: %v", agentFolder, err)
	}

	return s.saveUnlocked()
}

func (s *fileStore) ClearAgents() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents = make(map[string]*agent.Agent)
	s.current = ""
	return s.saveUnlocked()
}

func (s *fileStore) GetAgent(name string) (*agent.Agent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *fileStore) SetAgent(name string, ag *agent.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[name] = ag
	return s.saveUnlocked()
}

func (s *fileStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ag, ok := s.agents[name]
	if !ok || ag == nil {
		return fmt.Errorf("agent %q not found", name)
	}

	if err := updateFn(ag); err != nil {
		return err
	}

	return s.saveUnlocked()
}

func (s *fileStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked()
}

// writeAgentsJSON writes agents.json in the current working directory for plugins
func (s *fileStore) writeAgentsJSON() error {
	agentsConfig := struct {
		Current string `json:"current"`
	}{
		Current: s.current,
	}

	data, err := json.MarshalIndent(agentsConfig, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile("agents.json", data, 0o644)
}

// ---------- persistence helpers (no Messages persisted) ----------

func (s *fileStore) saveUnlocked() error {
	// Ensure base directory exists
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	// Create agents directory - handle case where path already includes agents/
	baseDir := filepath.Dir(s.path)
	var agentsDir string
	if strings.Contains(s.path, "/agents/") || strings.Contains(s.path, "\\agents\\") {
		// Path already contains agents directory structure
		// Find the agents directory and get its parent + "agents"
		agentsDirIndex := strings.LastIndex(s.path, "/agents/")
		if agentsDirIndex == -1 {
			agentsDirIndex = strings.LastIndex(s.path, "\\agents\\")
		}
		if agentsDirIndex != -1 {
			agentsDir = s.path[:agentsDirIndex+7] // +7 to include "/agents"
		} else {
			agentsDir = filepath.Join(baseDir, "agents")
		}
	} else {
		// Path is something like config.json, need to create agents subdir
		agentsDir = filepath.Join(baseDir, "agents")
	}
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return err
	}

	// Save individual agent files in nested structure
	type persistSettings struct {
		Type         string                        `json:"type"` // Agent type
		Role         types.AgentRole               `json:"role,omitempty"`
		Capabilities []string                      `json:"capabilities,omitempty"`
		Settings     types.Settings                `json:"Settings"`
		Plugins      map[string]types.LoadedPlugin `json:"Plugins"`
		Status       types.AgentStatus             `json:"status,omitempty"`
		Statistics   *types.AgentStatistics        `json:"statistics,omitempty"`
		Metadata     *types.AgentMetadata          `json:"metadata,omitempty"`
		Evolution    *types.AgentEvolution         `json:"evolution,omitempty"`
	}

	for agentName, agent := range s.agents {
		// Create agent-specific directory
		agentSpecificDir := filepath.Join(agentsDir, agentName)
		if err := os.MkdirAll(agentSpecificDir, 0o755); err != nil {
			return err
		}

		// Only save agent_settings.json with everything (Type + Settings + Plugins)
		// Don't create config.json unless necessary
		agentSettings := persistSettings{
			Type:         agent.Type,
			Role:         agent.Role,
			Capabilities: agent.Capabilities,
			Settings:     agent.Settings,
			Plugins:      agent.Plugins,
			Status:       agent.Status,
			Statistics:   agent.Statistics,
			Metadata:     agent.Metadata,
			Evolution:    agent.Evolution,
		}

		settingsData, err := json.MarshalIndent(agentSettings, "", "  ")
		if err != nil {
			return err
		}

		settingsPath := filepath.Join(agentSpecificDir, "agent_settings.json")
		tmpSettingsPath := settingsPath + ".tmp"
		if err := os.WriteFile(tmpSettingsPath, settingsData, 0644); err != nil {
			return err
		}
		if err := os.Rename(tmpSettingsPath, settingsPath); err != nil {
			return err
		}
	}

	// Save main index file with just current agent pointer

	indexConfig := struct {
		Current string `json:"current"`
	}{
		Current: s.current,
	}

	data, err := json.MarshalIndent(indexConfig, "", "  ")
	if err != nil {
		return err
	}

	// Use atomic write: write to .tmp then rename
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func (s *fileStore) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Initialize agents map if nil
	if s.agents == nil {
		s.agents = make(map[string]*agent.Agent)
	}

	// Try to parse the JSON first
	var rawConfig map[string]interface{}
	if err := json.Unmarshal(b, &rawConfig); err != nil {
		return err
	}

	// Check if this is the old format with "agents" key
	if _, hasAgents := rawConfig["agents"]; hasAgents {
		// Old format: {"agents": {...}, "current": "..."}
		var in struct {
			Agents  map[string]*agent.Agent `json:"agents"`
			Current string                  `json:"current"`
		}
		if err := json.Unmarshal(b, &in); err != nil {
			return err
		}
		if in.Agents != nil {
			s.agents = in.Agents
		}
		s.current = in.Current
		// Normalize migrated agents from legacy schema.
		for _, ag := range s.agents {
			s.normalizeLoadedAgent(ag)
		}
		return nil
	}

	// New format: just {"current": "..."}
	var indexConfig struct {
		Current string `json:"current"`
	}
	if err := json.Unmarshal(b, &indexConfig); err != nil {
		return err
	}

	s.current = indexConfig.Current

	// Load individual agent files from agents/ directory (nested structure)
	baseDir := filepath.Dir(s.path)
	var agentsDir string
	if strings.Contains(s.path, "/agents/") || strings.Contains(s.path, "\\agents\\") {
		// Path already contains agents directory structure
		// Find the agents directory and get its parent + "agents"
		agentsDirIndex := strings.LastIndex(s.path, "/agents/")
		if agentsDirIndex == -1 {
			agentsDirIndex = strings.LastIndex(s.path, "\\agents\\")
		}
		if agentsDirIndex != -1 {
			agentsDir = s.path[:agentsDirIndex+7] // +7 to include "/agents"
		} else {
			agentsDir = filepath.Join(baseDir, "agents")
		}
	} else {
		// Path is something like config.json, need to create agents subdir
		agentsDir = filepath.Join(baseDir, "agents")
	}
	if _, err := os.Stat(agentsDir); err == nil {
		// agents/ directory exists, check for nested structure
		entries, err := os.ReadDir(agentsDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					// New nested structure: agents/{name}/agent_settings.json contains everything
					agentName := entry.Name()
					settingsPath := filepath.Join(agentsDir, agentName, "agent_settings.json")

					var ag agent.Agent

					// Load full agent settings so nested persistence remains forward-compatible.
					if settingsData, err := os.ReadFile(settingsPath); err == nil {
						if err := json.Unmarshal(settingsData, &ag); err == nil {
							logger.Verbosef("✅ Loaded agent '%s' with %d plugins from %s", agentName, len(ag.Plugins), settingsPath)
						} else {
							logger.Verbosef("❌ Failed to unmarshal agent_settings.json for '%s': %v", agentName, err)
						}
					} else {
						logger.Verbosef("⚠️ Could not read agent_settings.json for '%s': %v", agentName, err)
					}

					s.normalizeLoadedAgent(&ag)
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

					s.normalizeLoadedAgent(&ag)
					s.agents[agentName] = &ag
				}
			}
		}
	}

	return nil
}

// normalizeLoadedAgent applies defaults for backward compatibility while preserving
// explicit values from disk.
func (s *fileStore) normalizeLoadedAgent(ag *agent.Agent) {
	if ag == nil {
		return
	}

	if ag.Plugins == nil {
		ag.Plugins = make(map[string]types.LoadedPlugin)
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
}

// migrateAgentTypesUnlocked migrates existing agents to have types based on their current model
// Assumes lock is already held
func (s *fileStore) migrateAgentTypesUnlocked() {
	for _, ag := range s.agents {
		// If agent already has a type, skip migration
		if ag.Type != "" {
			continue
		}

		// Determine type based on current model
		ag.Type = agent.GetTypeForModel(ag.Settings.Model)

		// If model wasn't found in any tier, set it to default cheap model
		if ag.Type == agent.TypeToolCalling && !agent.IsModelAllowedForType(ag.Settings.Model, agent.TypeToolCalling) {
			ag.Settings.Model = "gpt-5-nano"
		}
	}
}
