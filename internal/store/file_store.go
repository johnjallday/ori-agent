package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/johnjallday/dolphin-agent/internal/types"
)

type fileStore struct {
	mu      sync.Mutex
	path    string
	agents  map[string]*types.Agent
	current string
}

func NewFileStore(path string, defaultSettings types.Settings) (Store, error) {
	fs := &fileStore{
		path:   path,
		agents: make(map[string]*types.Agent),
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
		fs.agents[fs.current] = &types.Agent{
			Settings: defaultSettings,
			Plugins:  make(map[string]types.LoadedPlugin),
		}
	}
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

func (s *fileStore) CreateAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.agents[name]; !exists {
		s.agents[name] = &types.Agent{
			Settings: s.agents[s.current].Settings, // copy defaults
			Plugins:  make(map[string]types.LoadedPlugin),
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

func (s *fileStore) GetAgent(name string) (*types.Agent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *fileStore) SetAgent(name string, ag *types.Agent) error {
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
	type persistAgent struct {
		Settings types.Settings                `json:"Settings"`
		Plugins  map[string]types.LoadedPlugin `json:"Plugins"`
	}
	
	for agentName, agent := range s.agents {
		// Create agent-specific directory
		agentSpecificDir := filepath.Join(agentsDir, agentName)
		if err := os.MkdirAll(agentSpecificDir, 0o755); err != nil {
			return err
		}
		
		agentConfig := persistAgent{
			Settings: agent.Settings,
			Plugins:  agent.Plugins,
		}
		
		agentData, err := json.MarshalIndent(agentConfig, "", "  ")
		if err != nil {
			return err
		}
		
		// Save config.json in agent-specific directory
		configPath := filepath.Join(agentSpecificDir, "config.json")
		if err := os.WriteFile(configPath, agentData, 0o644); err != nil {
			return err
		}
		
		// Create empty agent_settings.json if it doesn't exist
		settingsPath := filepath.Join(agentSpecificDir, "agent_settings.json")
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			emptySettings := map[string]interface{}{}
			settingsData, err := json.MarshalIndent(emptySettings, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(settingsPath, settingsData, 0o644); err != nil {
				return err
			}
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
		s.agents = make(map[string]*types.Agent)
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
			Agents  map[string]*types.Agent `json:"agents"`
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
					// New nested structure: agents/{name}/config.json
					agentName := entry.Name()
					configPath := filepath.Join(agentsDir, agentName, "config.json")
					
					if agentData, err := os.ReadFile(configPath); err == nil {
						var agent types.Agent
						if err := json.Unmarshal(agentData, &agent); err == nil {
							// ensure maps
							if agent.Plugins == nil {
								agent.Plugins = make(map[string]types.LoadedPlugin)
							}
							s.agents[agentName] = &agent
						}
					}
				} else if filepath.Ext(entry.Name()) == ".json" {
					// Legacy flat structure: agents/agent.json
					agentName := entry.Name()[:len(entry.Name())-5] // remove .json
					agentPath := filepath.Join(agentsDir, entry.Name())
					
					agentData, err := os.ReadFile(agentPath)
					if err != nil {
						continue
					}
					
					var agent types.Agent
					if err := json.Unmarshal(agentData, &agent); err != nil {
						continue
					}
					
					// ensure maps
					if agent.Plugins == nil {
						agent.Plugins = make(map[string]types.LoadedPlugin)
					}
					
					s.agents[agentName] = &agent
				}
			}
		}
	}
	
	return nil
}
