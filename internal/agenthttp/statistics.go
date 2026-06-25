package agenthttp

import (
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ComputeOverallStatistics aggregates statistics across all agents
// Returns comprehensive dashboard statistics including totals and averages
func ComputeOverallStatistics(agents map[string]*agent.Agent) *types.DashboardStats {
	stats := &types.DashboardStats{
		TotalAgents:             0,
		ActiveAgents:            0,
		IdleAgents:              0,
		DisabledAgents:          0,
		ErrorAgents:             0,
		TotalMessages:           0,
		TotalTokens:             0,
		TotalCost:               0.0,
		MostActiveAgent:         "",
		MostCostlyAgent:         "",
		NewestAgent:             "",
		AverageMessagesPerAgent: 0.0,
		AverageCostPerAgent:     0.0,
	}

	// Handle empty agent list
	if len(agents) == 0 {
		return stats
	}

	var maxMessages int64
	var maxCost float64
	var newestTime int64 // Unix timestamp for comparison

	// Aggregate statistics
	for name, ag := range agents {
		stats.TotalAgents++

		// Count by status
		switch ag.Status {
		case types.AgentStatusActive:
			stats.ActiveAgents++
		case types.AgentStatusIdle:
			stats.IdleAgents++
		case types.AgentStatusDisabled:
			stats.DisabledAgents++
		case types.AgentStatusError:
			stats.ErrorAgents++
		default:
			// Default to idle if status not set
			stats.IdleAgents++
		}

		// Aggregate usage statistics
		if ag.Statistics != nil {
			stats.TotalMessages += ag.Statistics.MessageCount
			stats.TotalTokens += ag.Statistics.TokenUsage
			stats.TotalCost += ag.Statistics.TotalCost

			// Track most active agent
			if ag.Statistics.MessageCount > maxMessages {
				maxMessages = ag.Statistics.MessageCount
				stats.MostActiveAgent = name
			}

			// Track most costly agent
			if ag.Statistics.TotalCost > maxCost {
				maxCost = ag.Statistics.TotalCost
				stats.MostCostlyAgent = name
			}

			// Track newest agent
			createdTimestamp := ag.Statistics.CreatedAt.Unix()
			if createdTimestamp > newestTime {
				newestTime = createdTimestamp
				stats.NewestAgent = name
			}
		}
	}

	// Calculate averages
	if stats.TotalAgents > 0 {
		stats.AverageMessagesPerAgent = float64(stats.TotalMessages) / float64(stats.TotalAgents)
		stats.AverageCostPerAgent = stats.TotalCost / float64(stats.TotalAgents)
	}

	return stats
}
