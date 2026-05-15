package workspacerun

func AddCost(a, b CostSummary) CostSummary {
	return CostSummary{
		InputTokens:  a.InputTokens + b.InputTokens,
		OutputTokens: a.OutputTokens + b.OutputTokens,
		TotalTokens:  a.TotalTokens + b.TotalTokens,
		USD:          a.USD + b.USD,
	}
}

func NormalizeCost(cost CostSummary) CostSummary {
	if cost.TotalTokens == 0 {
		cost.TotalTokens = cost.InputTokens + cost.OutputTokens
	}
	return cost
}
