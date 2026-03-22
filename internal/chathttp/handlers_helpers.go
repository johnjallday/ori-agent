package chathttp

import (
	"fmt"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/oriagent/ori-pluginapi"
)

// getPluginEmoji returns an appropriate emoji for a plugin based on its name
func getPluginEmoji(pluginName string) string {
	name := strings.ToLower(pluginName)

	// Music/Audio related
	if strings.Contains(name, "music") || strings.Contains(name, "reaper") || strings.Contains(name, "audio") {
		return "🎵"
	}

	// Development/Code related
	if strings.Contains(name, "code") || strings.Contains(name, "dev") || strings.Contains(name, "git") {
		return "💻"
	}

	// File/System related
	if strings.Contains(name, "file") || strings.Contains(name, "system") || strings.Contains(name, "manager") {
		return "📁"
	}

	// Data/Database related
	if strings.Contains(name, "data") || strings.Contains(name, "database") || strings.Contains(name, "sql") {
		return "📊"
	}

	// Network/Web related
	if strings.Contains(name, "web") || strings.Contains(name, "http") || strings.Contains(name, "api") {
		return "🌐"
	}

	// Default plugin emoji
	return "🔌"
}

// checkUninitializedPlugins checks which plugins need initialization
func (h *Handler) checkUninitializedPlugins(ag *agent.Agent, agentName string) []map[string]any {
	var uninitializedPlugins []map[string]any

	for name, plugin := range ag.Plugins {
		// Check if plugin supports initialization
		initProvider, supportsInit := plugin.Tool.(pluginapi.InitializationProvider)
		if !supportsInit {
			// Simplified: Skip plugins that don't support InitializationProvider
			continue
		}

		// Check if plugin is initialized by checking if settings file exists
		// Try multiple name variations (underscores vs hyphens) to handle naming inconsistencies
		normalizedName := registry.NormalizePluginName(name)
		settingsFilePath := fmt.Sprintf("agents/%s/%s_settings.json", agentName, name)
		_, err := os.Stat(settingsFilePath)
		isInitialized := err == nil
		if !isInitialized {
			// Try normalized name (hyphens)
			settingsFilePath = fmt.Sprintf("agents/%s/%s_settings.json", agentName, normalizedName)
			_, err = os.Stat(settingsFilePath)
			isInitialized = err == nil
		}

		if !isInitialized {
			// Get required config for this plugin
			configVars := initProvider.GetRequiredConfig()

			// Skip if plugin has no required configuration (e.g., simple plugins like math)
			// This handles the case where RPC clients always implement InitializationProvider
			// but the underlying plugin doesn't actually need configuration
			if len(configVars) == 0 {
				continue
			}

			// Get fresh definition for description
			def := plugin.Definition
			if plugin.Tool != nil {
				def = plugin.Tool.Definition()
			}

			uninitializedPlugins = append(uninitializedPlugins, map[string]any{
				"name":            name,
				"description":     def.Description,
				"required_config": configVars,
			})
		}
	}
	return uninitializedPlugins
}

// generateInitializationPrompt creates a user-friendly prompt for plugin initialization
func (h *Handler) generateInitializationPrompt(uninitializedPlugins []map[string]any) string {
	if len(uninitializedPlugins) == 0 {
		return ""
	}

	var prompt strings.Builder

	if len(uninitializedPlugins) == 1 {
		plugin := uninitializedPlugins[0]
		prompt.WriteString("🔧 **Plugin Setup Required**\n\n")
		prompt.WriteString(fmt.Sprintf("The **%s** plugin needs to be configured before you can use it.\n\n", plugin["name"]))
		prompt.WriteString(fmt.Sprintf("**Description:** %s\n\n", plugin["description"]))

		if configVars, ok := plugin["required_config"].([]pluginapi.ConfigVariable); ok && len(configVars) > 0 {
			prompt.WriteString("**Required configuration:**\n")
			for _, configVar := range configVars {
				prompt.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", configVar.Name, configVar.Type, configVar.Description))
			}
		}

		prompt.WriteString("\n**Please click the 'Configure Plugin' button to set up this plugin.**")
	} else {
		prompt.WriteString("🔧 **Plugin Setup Required**\n\n")
		prompt.WriteString(fmt.Sprintf("You have %d plugins that need to be configured before you can use them:\n\n", len(uninitializedPlugins)))

		for i, plugin := range uninitializedPlugins {
			prompt.WriteString(fmt.Sprintf("%d. **%s** - %s\n", i+1, plugin["name"], plugin["description"]))
		}

		prompt.WriteString("\n**Please configure these plugins to unlock their full functionality.**")
	}

	return prompt.String()
}

// trackAgentStatistics records message usage metrics and awards evolution XP.
func (h *Handler) trackAgentStatistics(ag *agent.Agent, agentName string, tokenCount int, provider string, model string, userMessage string) {
	// Initialize statistics if needed
	ag.InitializeStatistics()

	// Calculate cost estimate (this is a simple estimation, actual costs tracked by cost tracker)
	var costPerToken float64
	switch provider {
	case "ollama":
		costPerToken = 0.0 // Ollama is free/local
	default:
		switch {
		case strings.Contains(model, "gpt-4"):
			costPerToken = 0.00003 // ~$0.03 per 1K tokens (average of input/output)
		case strings.Contains(model, "gpt-3.5"):
			costPerToken = 0.000002 // ~$0.002 per 1K tokens
		case strings.Contains(model, "claude"):
			costPerToken = 0.00003 // ~$0.03 per 1K tokens (average)
		default:
			costPerToken = 0.00001 // Default estimate
		}
	}

	estimatedCost := float64(tokenCount) * costPerToken

	// Record the message with tokens and cost
	ag.Statistics.RecordMessage(tokenCount, estimatedCost)

	if h.evolutionSvc != nil && agentName != "" {
		if err := h.evolutionSvc.AwardMessageXP(agentName, tokenCount, userMessage); err != nil {
			logger.Warn("Failed to award evolution XP", logger.Fields{
				"agent": agentName,
				"error": err,
			})
		}
	}

	logger.Debug("Statistics updated", logger.Fields{
		"messages":   ag.Statistics.MessageCount,
		"tokens":     ag.Statistics.TokenUsage,
		"total_cost": ag.Statistics.TotalCost,
	})
}
