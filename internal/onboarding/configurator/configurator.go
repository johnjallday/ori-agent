// Package configurator applies onboarding configurations to create
// agents and suggest plugins based on the user's inferred profile.
package configurator

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/onboarding/profiler"
	"github.com/johnjallday/ori-agent/internal/store"
)

// SuggestedAgent represents an agent to be created during onboarding.
type SuggestedAgent struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"system_prompt"`
	Model        string   `json:"model"`
	Temperature  float64  `json:"temperature"`
	Plugins      []string `json:"plugins"`
}

// SuggestedPlugin represents a plugin recommendation.
type SuggestedPlugin struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"` // If true, highly recommended for the profile
}

// OnboardingConfig holds the complete configuration to apply.
type OnboardingConfig struct {
	// Profile is the inferred user profile.
	Profile *profiler.UserProfile `json:"profile"`

	// Agents are the suggested agents to create.
	Agents []SuggestedAgent `json:"agents"`

	// Plugins are the suggested plugins to install.
	Plugins []SuggestedPlugin `json:"plugins"`
}

// Configurator generates and applies onboarding configurations.
type Configurator struct {
	store store.Store
}

// New creates a new Configurator with the given store.
func New(s store.Store) *Configurator {
	return &Configurator{
		store: s,
	}
}

// GenerateConfig creates an onboarding configuration based on the user profile.
func (c *Configurator) GenerateConfig(profile *profiler.UserProfile) (*OnboardingConfig, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile is required")
	}

	config := &OnboardingConfig{
		Profile: profile,
	}

	// First, create agents from AI-suggested names (these are customized to the user)
	for _, agentName := range profile.SuggestedAgentNames {
		// Create a custom agent based on the AI suggestion
		agent := SuggestedAgent{
			Name:        agentName,
			Description: fmt.Sprintf("AI assistant for %s", agentName),
			SystemPrompt: fmt.Sprintf(`You are %s, a specialized AI assistant.

Based on the user's profile:
- Primary focus: %s
- Specializations: %v

Help the user with tasks related to your specialty. Be concise, practical, and provide actionable advice.`,
				agentName,
				profile.Summary,
				profile.Specializations,
			),
			Model:       "gpt-4",
			Temperature: 0.3,
			Plugins:     []string{"shell-executor"},
		}
		config.Agents = append(config.Agents, agent)
	}

	// If no AI-suggested agents, fall back to template-based agents
	if len(config.Agents) == 0 {
		templates := profiler.GetTemplatesForProfile(profile)
		for _, t := range templates {
			agent := SuggestedAgent{
				Name:         t.Name,
				Description:  t.Description,
				SystemPrompt: t.SystemPrompt,
				Model:        t.SuggestedModel,
				Temperature:  t.Temperature,
				Plugins:      t.Plugins,
			}

			// Customize agent name based on specializations if available
			if len(profile.Specializations) > 0 {
				spec := profile.Specializations[0]
				if t.Name == "Code Assistant" && spec != "" {
					agent.Name = fmt.Sprintf("%s Assistant", spec)
				}
			}

			config.Agents = append(config.Agents, agent)
		}
	}

	// Generate plugin suggestions
	templates := profiler.GetTemplatesForProfile(profile)
	config.Plugins = c.suggestPlugins(profile, templates)

	return config, nil
}

// suggestPlugins generates plugin recommendations based on profile and templates.
func (c *Configurator) suggestPlugins(profile *profiler.UserProfile, templates []profiler.AgentTemplate) []SuggestedPlugin {
	pluginSet := make(map[string]bool)
	var plugins []SuggestedPlugin

	// Collect plugins from templates
	for _, t := range templates {
		for _, p := range t.Plugins {
			if !pluginSet[p] {
				pluginSet[p] = true
				plugins = append(plugins, SuggestedPlugin{
					Name:        p,
					Description: getPluginDescription(p),
					Required:    true,
				})
			}
		}
	}

	// Add profile-specific suggestions from AI
	for _, suggested := range profile.SuggestedPlugins {
		if !pluginSet[suggested] {
			pluginSet[suggested] = true
			plugins = append(plugins, SuggestedPlugin{
				Name:        suggested,
				Description: getPluginDescription(suggested),
				Required:    false,
			})
		}
	}

	return plugins
}

// ApplyConfig creates the agents from the configuration.
func (c *Configurator) ApplyConfig(ctx context.Context, config *OnboardingConfig) error {
	if config == nil {
		return fmt.Errorf("config is required")
	}

	for _, agent := range config.Agents {
		// Check if agent already exists
		if _, exists := c.store.GetAgent(agent.Name); exists {
			// Skip existing agents or use a numbered suffix
			continue
		}

		createConfig := &store.CreateAgentConfig{
			Type:         "tool-calling",
			Model:        agent.Model,
			Temperature:  agent.Temperature,
			SystemPrompt: agent.SystemPrompt,
		}

		if err := c.store.CreateAgent(agent.Name, createConfig); err != nil {
			return fmt.Errorf("failed to create agent %s: %w", agent.Name, err)
		}
	}

	// Save the store
	if err := c.store.Save(); err != nil {
		return fmt.Errorf("failed to save store: %w", err)
	}

	return nil
}

// GetCreatedAgentNames returns the names of agents that would be created.
func (c *Configurator) GetCreatedAgentNames(config *OnboardingConfig) []string {
	var names []string
	for _, agent := range config.Agents {
		if _, exists := c.store.GetAgent(agent.Name); !exists {
			names = append(names, agent.Name)
		}
	}
	return names
}

// getPluginDescription returns a description for a known plugin.
func getPluginDescription(name string) string {
	descriptions := map[string]string{
		"shell-executor": "Execute shell commands and scripts",
		"git-tools":      "Git workflow automation and helpers",
		"web-search":     "Search the web for information",
		"file-manager":   "File and directory management",
		"code-runner":    "Run code in various languages",
		"http-client":    "Make HTTP requests",
		"database":       "Database queries and management",
		"docker":         "Docker container management",
	}

	if desc, ok := descriptions[name]; ok {
		return desc
	}
	return "Plugin for enhanced functionality"
}
