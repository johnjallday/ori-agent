package cliagent

import (
	"sync"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// BudgetStatus indicates whether a task is within budget.
type BudgetStatus struct {
	WithinBudget    bool    `json:"within_budget"`
	TokensUsed      int     `json:"tokens_used"`
	TokensRemaining int     `json:"tokens_remaining"` // -1 if unlimited
	CostUsed        float64 `json:"cost_used"`
	CostRemaining   float64 `json:"cost_remaining"` // -1 if unlimited
}

// BudgetEnforcer tracks cumulative token and cost usage for a task and
// enforces configured limits.
type BudgetEnforcer struct {
	mu          sync.Mutex
	totalTokens int
	totalCost   float64
	steps       []StepUsage
}

// NewBudgetEnforcer creates a new BudgetEnforcer.
func NewBudgetEnforcer() *BudgetEnforcer {
	return &BudgetEnforcer{}
}

// Record adds a step's usage to the running totals.
func (b *BudgetEnforcer) Record(usage StepUsage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.totalTokens += usage.TotalTokens()
	b.totalCost += usage.CostUSD
	b.steps = append(b.steps, usage)
}

// Check evaluates whether the task is within the given budget limits.
// Zero-value limits mean unlimited.
func (b *BudgetEnforcer) Check(tokenLimit int, costLimit float64) BudgetStatus {
	b.mu.Lock()
	defer b.mu.Unlock()

	status := BudgetStatus{
		WithinBudget:    true,
		TokensUsed:      b.totalTokens,
		CostUsed:        b.totalCost,
		TokensRemaining: -1,
		CostRemaining:   -1,
	}

	if tokenLimit > 0 {
		status.TokensRemaining = tokenLimit - b.totalTokens
		if b.totalTokens >= tokenLimit {
			status.WithinBudget = false
		}
	}
	if costLimit > 0 {
		status.CostRemaining = costLimit - b.totalCost
		if b.totalCost >= costLimit {
			status.WithinBudget = false
		}
	}

	return status
}

// RemainingTokens returns how many tokens are left, or 0 if limit is 0 (unlimited).
func (b *BudgetEnforcer) RemainingTokens(limit int) int {
	if limit <= 0 {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := limit - b.totalTokens
	if remaining < 0 {
		return 0
	}
	return remaining
}

// RemainingCostUSD returns how much cost budget remains, or 0 if unlimited.
func (b *BudgetEnforcer) RemainingCostUSD(limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := limit - b.totalCost
	if remaining < 0 {
		return 0
	}
	return remaining
}

// TotalUsage returns the accumulated usage across all steps.
func (b *BudgetEnforcer) TotalUsage() StepUsage {
	b.mu.Lock()
	defer b.mu.Unlock()

	var total StepUsage
	for _, s := range b.steps {
		total.InputTokens += s.InputTokens
		total.OutputTokens += s.OutputTokens
		total.CostUSD += s.CostUSD
	}
	return total
}

// ReportToCostTracker sends accumulated usage to the Ori cost tracker.
func (b *BudgetEnforcer) ReportToCostTracker(tracker *llm.CostTracker, provider, model, agentName string) {
	if tracker == nil {
		return
	}
	total := b.TotalUsage()
	usage := llm.Usage{
		PromptTokens:     total.InputTokens,
		CompletionTokens: total.OutputTokens,
		TotalTokens:      total.TotalTokens(),
	}
	_ = tracker.TrackUsage(provider, model, agentName, usage, "")
}
