package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
	// try load
	_ = fs.load()

	// ensure at least one agent exists
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.current == "" {
		fs.current = "default"
	}
	if _, ok := fs.agents[fs.current]; !ok {
		fs.agents[fs.current] = &agent.Agent{
			Type:     agent.TypeToolCalling, // Default to cheapest tier
			Settings: defaultSettings,
			Plugins:  make(map[string]types.LoadedPlugin),
		}
	}

	// Migrate existing agents to have types
	fs.migrateAgentTypesUnlocked()

	_ = fs.saveUnlocked()

	// Write agents.json for plugins on startup
	_ = fs.writeAgentsJSON()

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

func (s *fileStore) CreateAgent(name string, config *CreateAgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.agents[name]; !exists {
		// Get default settings - either from current agent or use hardcoded defaults
		var defaultSettings types.Settings
		if s.current != "" && s.agents[s.current] != nil {
			// Copy from current agent
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
		}

		s.agents[name] = &agent.Agent{
			Type:         agentType,
			Role:         types.RoleGeneral, // Default role
			Capabilities: []string{},        // Empty capabilities by default
			Settings:     defaultSettings,
			Plugins:      make(map[string]types.LoadedPlugin),
		}
	}
	return s.saveUnlocked()
}

func (s *fileStore) SwitchAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.agents[name]; !exists {
		return errors.New("agent not found")
	}
	s.current = name

	// Save main store
	if err := s.saveUnlocked(); err != nil {
		return err
	}

	// Also write agents.json in current working directory for plugins
	return s.writeAgentsJSON()
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
		// In a production app, you might want to handle this differently
		_ = err
	}
	
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
		Type     string                         `json:"type"`     // Agent type
		Settings types.Settings                `json:"Settings"`
		Plugins  map[string]types.LoadedPlugin `json:"Plugins"`
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
			Type:     agent.Type,
			Settings: agent.Settings,
			Plugins:  agent.Plugins,
		}

		settingsData, err := json.MarshalIndent(agentSettings, "", "  ")
		if err != nil {
			return err
		}

		settingsPath := filepath.Join(agentSpecificDir, "agent_settings.json")
		if err := os.WriteFile(settingsPath, settingsData, 0o644); err != nil {
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
	
	return os.WriteFile(s.path, data, 0o644)
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
		// ensure maps
		for _, ag := range s.agents {
			if ag.Plugins == nil {
				ag.Plugins = make(map[string]types.LoadedPlugin)
			}
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

					// Load agent_settings.json (Type + Settings + Plugins)
					if settingsData, err := os.ReadFile(settingsPath); err == nil {
						var settings struct {
							Type     string                         `json:"type"`
							Settings types.Settings                `json:"Settings"`
							Plugins  map[string]types.LoadedPlugin `json:"Plugins"`
						}
						if err := json.Unmarshal(settingsData, &settings); err == nil {
							ag.Type = settings.Type
							ag.Settings = settings.Settings
							ag.Plugins = settings.Plugins
							logger.Verbosef("✅ Loaded agent '%s' with %d plugins from %s", agentName, len(settings.Plugins), settingsPath)
						} else {
							logger.Verbosef("❌ Failed to unmarshal agent_settings.json for '%s': %v", agentName, err)
						}
					} else {
						logger.Verbosef("⚠️ Could not read agent_settings.json for '%s': %v", agentName, err)
					}

					// Ensure maps are initialized
					if ag.Plugins == nil {
						ag.Plugins = make(map[string]types.LoadedPlugin)
					}

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

					// ensure maps
					if ag.Plugins == nil {
						ag.Plugins = make(map[string]types.LoadedPlugin)
					}

					s.agents[agentName] = &ag
				}
			}
		}
	}

	return nil
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
