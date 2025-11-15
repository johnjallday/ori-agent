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

// ComputeAgentStatistics calculates derived statistics for a single agent
// This can be used to enrich agent data with computed fields
func ComputeAgentStatistics(ag *agent.Agent) map[string]interface{} {
	if ag == nil || ag.Statistics == nil {
		return map[string]interface{}{
			"average_tokens_per_message": 0.0,
			"cost_per_message":           0.0,
			"cost_per_thousand_tokens":   0.0,
		}
	}

	result := make(map[string]interface{})

	// Average tokens per message
	if ag.Statistics.MessageCount > 0 {
		result["average_tokens_per_message"] = float64(ag.Statistics.TokenUsage) / float64(ag.Statistics.MessageCount)
		result["cost_per_message"] = ag.Statistics.TotalCost / float64(ag.Statistics.MessageCount)
	} else {
		result["average_tokens_per_message"] = 0.0
		result["cost_per_message"] = 0.0
	}

	// Cost per thousand tokens
	if ag.Statistics.TokenUsage > 0 {
		result["cost_per_thousand_tokens"] = (ag.Statistics.TotalCost / float64(ag.Statistics.TokenUsage)) * 1000
	} else {
		result["cost_per_thousand_tokens"] = 0.0
	}

	return result
}

// FilterAgentsByStatus filters agents by their operational status
func FilterAgentsByStatus(agents map[string]*agent.Agent, status types.AgentStatus) map[string]*agent.Agent {
	filtered := make(map[string]*agent.Agent)

	for name, ag := range agents {
		if ag.Status == status {
			filtered[name] = ag
		}
	}

	return filtered
}

// FilterAgentsByTag filters agents that have a specific tag in their metadata
func FilterAgentsByTag(agents map[string]*agent.Agent, tag string) map[string]*agent.Agent {
	filtered := make(map[string]*agent.Agent)

	for name, ag := range agents {
		if ag.Metadata != nil {
			for _, t := range ag.Metadata.Tags {
				if t == tag {
					filtered[name] = ag
					break
				}
			}
		}
	}

	return filtered
}

// SortAgentsByActivity returns agent names sorted by most recent activity
// Returns slice of agent names in descending order (most recent first)
func SortAgentsByActivity(agents map[string]*agent.Agent) []string {
	type agentActivity struct {
		name      string
		timestamp int64
	}

	activities := make([]agentActivity, 0, len(agents))

	for name, ag := range agents {
		timestamp := int64(0)
		if ag.Statistics != nil {
			timestamp = ag.Statistics.LastActive.Unix()
		}
		activities = append(activities, agentActivity{name: name, timestamp: timestamp})
	}

	// Sort by timestamp descending
	for i := 0; i < len(activities); i++ {
		for j := i + 1; j < len(activities); j++ {
			if activities[j].timestamp > activities[i].timestamp {
				activities[i], activities[j] = activities[j], activities[i]
			}
		}
	}

	// Extract names
	result := make([]string, len(activities))
	for i, a := range activities {
		result[i] = a.name
	}

	return result
}

// GetTopAgentsByCost returns the N most costly agents
func GetTopAgentsByCost(agents map[string]*agent.Agent, n int) []string {
	type agentCost struct {
		name string
		cost float64
	}

	costs := make([]agentCost, 0, len(agents))

	for name, ag := range agents {
		cost := 0.0
		if ag.Statistics != nil {
			cost = ag.Statistics.TotalCost
		}
		costs = append(costs, agentCost{name: name, cost: cost})
	}

	// Sort by cost descending
	for i := 0; i < len(costs); i++ {
		for j := i + 1; j < len(costs); j++ {
			if costs[j].cost > costs[i].cost {
				costs[i], costs[j] = costs[j], costs[i]
			}
		}
	}

	// Return top N
	limit := n
	if limit > len(costs) {
		limit = len(costs)
	}

	result := make([]string, limit)
	for i := 0; i < limit; i++ {
		result[i] = costs[i].name
	}

	return result
}
