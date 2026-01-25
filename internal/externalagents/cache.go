package externalagents

import (
	"sync"
)

// Cache provides thread-safe caching for external agent data.
type Cache struct {
	mu sync.RWMutex

	claudeReader ClaudeReader
	codexReader  CodexReader

	claudeAgents   []ExternalAgent
	claudeSettings *ClaudeSettings
	claudePlugins  []ClaudePlugin
	codexAgents    []ExternalAgent
	codexConfig    *CodexConfig
	codexSkills    []CodexSkill
	codexRules     []CodexRule

	loaded bool
}

// NewCache creates a new Cache with the given readers.
func NewCache(claudeReader ClaudeReader, codexReader CodexReader) *Cache {
	return &Cache{
		claudeReader: claudeReader,
		codexReader:  codexReader,
	}
}

// Load populates the cache by reading from all sources.
// This should be called on startup.
func (c *Cache) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.loadLocked()
}

// loadLocked performs the actual loading. Caller must hold the lock.
func (c *Cache) loadLocked() error {
	// Read Claude data
	if c.claudeReader != nil {
		agents, err := c.claudeReader.ReadAgents()
		if err != nil {
			return err
		}
		c.claudeAgents = agents

		settings, err := c.claudeReader.ReadSettings()
		if err != nil {
			return err
		}
		c.claudeSettings = settings

		plugins, err := c.claudeReader.ReadPlugins()
		if err != nil {
			return err
		}
		c.claudePlugins = plugins
	}

	// Read Codex data
	if c.codexReader != nil {
		agents, err := c.codexReader.ReadAgents()
		if err != nil {
			return err
		}
		c.codexAgents = agents

		config, err := c.codexReader.ReadConfig()
		if err != nil {
			return err
		}
		c.codexConfig = config

		skills, err := c.codexReader.ReadSkills()
		if err != nil {
			return err
		}
		c.codexSkills = skills

		rules, err := c.codexReader.ReadRules()
		if err != nil {
			return err
		}
		c.codexRules = rules
	}

	c.loaded = true
	return nil
}

// Refresh invalidates and reloads the cache.
func (c *Cache) Refresh() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clear existing data
	c.claudeAgents = nil
	c.claudeSettings = nil
	c.claudePlugins = nil
	c.codexAgents = nil
	c.codexConfig = nil
	c.codexSkills = nil
	c.codexRules = nil
	c.loaded = false

	return c.loadLocked()
}

// GetClaudeAgents returns cached Claude agents.
func (c *Cache) GetClaudeAgents() []ExternalAgent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.claudeAgents
}

// GetClaudeSettings returns cached Claude settings.
func (c *Cache) GetClaudeSettings() *ClaudeSettings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.claudeSettings
}

// GetClaudePlugins returns cached Claude plugins.
func (c *Cache) GetClaudePlugins() []ClaudePlugin {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.claudePlugins
}

// GetCodexAgents returns cached Codex agents (skills).
func (c *Cache) GetCodexAgents() []ExternalAgent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.codexAgents
}

// GetCodexConfig returns cached Codex configuration.
func (c *Cache) GetCodexConfig() *CodexConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.codexConfig
}

// GetCodexSkills returns cached Codex skills.
func (c *Cache) GetCodexSkills() []CodexSkill {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.codexSkills
}

// GetCodexRules returns cached Codex rules.
func (c *Cache) GetCodexRules() []CodexRule {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.codexRules
}

// GetClaudeData returns all cached Claude data.
func (c *Cache) GetClaudeData() *ClaudeData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return &ClaudeData{
		Agents:   c.claudeAgents,
		Settings: c.claudeSettings,
		Plugins:  c.claudePlugins,
	}
}

// GetCodexData returns all cached Codex data.
func (c *Cache) GetCodexData() *CodexData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return &CodexData{
		Agents: c.codexAgents,
		Config: c.codexConfig,
		Skills: c.codexSkills,
		Rules:  c.codexRules,
	}
}

// GetAll returns all cached external agent data.
func (c *Cache) GetAll() *ExternalAgentsData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return &ExternalAgentsData{
		Claude: &ClaudeData{
			Agents:   c.claudeAgents,
			Settings: c.claudeSettings,
			Plugins:  c.claudePlugins,
		},
		Codex: &CodexData{
			Agents: c.codexAgents,
			Config: c.codexConfig,
			Skills: c.codexSkills,
			Rules:  c.codexRules,
		},
	}
}

// IsLoaded returns whether the cache has been loaded.
func (c *Cache) IsLoaded() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded
}
