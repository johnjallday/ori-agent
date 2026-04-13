package cliagent

import "testing"

func TestBudgetEnforcer_Record(t *testing.T) {
	b := NewBudgetEnforcer()
	b.Record(StepUsage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01})
	b.Record(StepUsage{InputTokens: 200, OutputTokens: 80, CostUSD: 0.02})

	total := b.TotalUsage()
	if total.InputTokens != 300 {
		t.Errorf("expected 300 input tokens, got %d", total.InputTokens)
	}
	if total.OutputTokens != 130 {
		t.Errorf("expected 130 output tokens, got %d", total.OutputTokens)
	}
	if total.TotalTokens() != 430 {
		t.Errorf("expected 430 total tokens, got %d", total.TotalTokens())
	}
	if total.CostUSD != 0.03 {
		t.Errorf("expected cost 0.03, got %f", total.CostUSD)
	}
}

func TestBudgetEnforcer_Check_WithinBudget(t *testing.T) {
	b := NewBudgetEnforcer()
	b.Record(StepUsage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01})

	status := b.Check(1000, 0.50)
	if !status.WithinBudget {
		t.Error("should be within budget")
	}
	if status.TokensRemaining != 850 {
		t.Errorf("expected 850 remaining, got %d", status.TokensRemaining)
	}
}

func TestBudgetEnforcer_Check_OverTokenBudget(t *testing.T) {
	b := NewBudgetEnforcer()
	b.Record(StepUsage{InputTokens: 600, OutputTokens: 500, CostUSD: 0.01})

	status := b.Check(1000, 1.00)
	if status.WithinBudget {
		t.Error("should be over token budget")
	}
}

func TestBudgetEnforcer_Check_OverCostBudget(t *testing.T) {
	b := NewBudgetEnforcer()
	b.Record(StepUsage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.60})

	status := b.Check(10000, 0.50)
	if status.WithinBudget {
		t.Error("should be over cost budget")
	}
}

func TestBudgetEnforcer_Check_Unlimited(t *testing.T) {
	b := NewBudgetEnforcer()
	b.Record(StepUsage{InputTokens: 999999, OutputTokens: 999999, CostUSD: 999.0})

	status := b.Check(0, 0)
	if !status.WithinBudget {
		t.Error("zero limits should mean unlimited")
	}
	if status.TokensRemaining != -1 {
		t.Errorf("expected -1 remaining for unlimited, got %d", status.TokensRemaining)
	}
	if status.CostRemaining != -1 {
		t.Errorf("expected -1 remaining for unlimited, got %f", status.CostRemaining)
	}
}

func TestBudgetEnforcer_RemainingTokens(t *testing.T) {
	b := NewBudgetEnforcer()
	b.Record(StepUsage{InputTokens: 300, OutputTokens: 200})

	if got := b.RemainingTokens(1000); got != 500 {
		t.Errorf("expected 500, got %d", got)
	}
	if got := b.RemainingTokens(0); got != 0 {
		t.Errorf("unlimited should return 0, got %d", got)
	}

	// Over budget
	b.Record(StepUsage{InputTokens: 600, OutputTokens: 0})
	if got := b.RemainingTokens(1000); got != 0 {
		t.Errorf("over budget should return 0, got %d", got)
	}
}

func TestBudgetEnforcer_RemainingCostUSD(t *testing.T) {
	b := NewBudgetEnforcer()
	b.Record(StepUsage{CostUSD: 0.30})

	if got := b.RemainingCostUSD(0.50); got < 0.199 || got > 0.201 {
		t.Errorf("expected ~0.20, got %f", got)
	}
	if got := b.RemainingCostUSD(0); got != 0 {
		t.Errorf("unlimited should return 0, got %f", got)
	}
}
