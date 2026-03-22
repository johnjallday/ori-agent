package chathttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
)

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
