package cliagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaskConfig_Validate(t *testing.T) {
	tmpDir := t.TempDir()

	valid := TaskConfig{
		CLIBackend:    BackendClaude,
		Prompt:        "do something",
		WorkingDir:    tmpDir,
		TokenBudget:   1000,
		CostBudgetUSD: 0.50,
		MaxSteps:      5,
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}

	tests := []struct {
		name   string
		modify func(c *TaskConfig)
	}{
		{
			name:   "invalid backend",
			modify: func(c *TaskConfig) { c.CLIBackend = "invalid-backend" },
		},
		{
			name:   "empty backend",
			modify: func(c *TaskConfig) { c.CLIBackend = "" },
		},
		{
			name:   "empty prompt",
			modify: func(c *TaskConfig) { c.Prompt = "" },
		},
		{
			name:   "empty working dir",
			modify: func(c *TaskConfig) { c.WorkingDir = "" },
		},
		{
			name:   "nonexistent working dir",
			modify: func(c *TaskConfig) { c.WorkingDir = "/nonexistent/path/xyz" },
		},
		{
			name: "working dir is a file",
			modify: func(c *TaskConfig) {
				f := filepath.Join(tmpDir, "afile.txt")
				_ = os.WriteFile(f, []byte("hi"), 0644)
				c.WorkingDir = f
			},
		},
		{
			name:   "negative token budget",
			modify: func(c *TaskConfig) { c.TokenBudget = -1 },
		},
		{
			name:   "negative cost budget",
			modify: func(c *TaskConfig) { c.CostBudgetUSD = -0.01 },
		},
		{
			name:   "negative max steps",
			modify: func(c *TaskConfig) { c.MaxSteps = -1 },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid // copy
			tt.modify(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestTaskConfig_Validate_BothBackends(t *testing.T) {
	tmpDir := t.TempDir()
	for _, backend := range []string{BackendClaude, BackendCodex, BackendGemini} {
		c := TaskConfig{
			CLIBackend: backend,
			Prompt:     "test",
			WorkingDir: tmpDir,
		}
		if err := c.Validate(); err != nil {
			t.Errorf("backend %q should be valid: %v", backend, err)
		}
	}
}

func TestTaskConfig_EffectiveMaxSteps(t *testing.T) {
	c := TaskConfig{}
	if got := c.EffectiveMaxSteps(); got != DefaultMaxSteps {
		t.Errorf("expected %d, got %d", DefaultMaxSteps, got)
	}

	c.MaxSteps = 3
	if got := c.EffectiveMaxSteps(); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}

func TestStepUsage_TotalTokens(t *testing.T) {
	u := StepUsage{InputTokens: 100, OutputTokens: 50}
	if got := u.TotalTokens(); got != 150 {
		t.Errorf("expected 150, got %d", got)
	}
}
