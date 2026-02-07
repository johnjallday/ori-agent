package agent

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestGetTypeForModel(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		// Tool-calling models
		{"gpt-4o-mini", TypeToolCalling},
		{"claude-3-haiku-20240307", TypeToolCalling},
		// General models
		{"gpt-4o", TypeGeneral},
		{"claude-3-5-sonnet-20241022", TypeGeneral},
		// Research models
		{"gpt-5", TypeResearch},
		{"claude-opus-4-1", TypeResearch},
		// Unknown model defaults to tool-calling
		{"unknown-model", TypeToolCalling},
		{"", TypeToolCalling},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := GetTypeForModel(tt.model)
			if result != tt.expected {
				t.Errorf("GetTypeForModel(%q) = %q, want %q", tt.model, result, tt.expected)
			}
		})
	}
}

func TestIsModelAllowedForType(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		agentType string
		expected  bool
	}{
		{"tool-calling model for tool-calling type", "gpt-4o-mini", TypeToolCalling, true},
		{"general model for general type", "gpt-4o", TypeGeneral, true},
		{"research model for research type", "gpt-5", TypeResearch, true},
		{"tool-calling model for general type", "gpt-4o-mini", TypeGeneral, true}, // gpt-4o-mini is also in general
		{"research model for tool-calling type", "gpt-5", TypeToolCalling, false},
		{"unknown model for any type", "unknown-model", TypeToolCalling, false},
		{"valid model for unknown type", "gpt-4o", "unknown-type", false},
		{"empty model", "", TypeToolCalling, false},
		{"empty type", "gpt-4o", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsModelAllowedForType(tt.model, tt.agentType)
			if result != tt.expected {
				t.Errorf("IsModelAllowedForType(%q, %q) = %v, want %v", tt.model, tt.agentType, result, tt.expected)
			}
		})
	}
}

func TestAgent_InitializeStatistics(t *testing.T) {
	t.Run("initializes nil statistics", func(t *testing.T) {
		agent := &Agent{}
		if agent.Statistics != nil {
			t.Fatal("Statistics should be nil initially")
		}

		agent.InitializeStatistics()

		if agent.Statistics == nil {
			t.Error("Statistics should not be nil after InitializeStatistics()")
		}
	})

	t.Run("does not overwrite existing statistics", func(t *testing.T) {
		agent := &Agent{}
		agent.InitializeStatistics()
		originalStats := agent.Statistics

		// Modify statistics
		agent.Statistics.MessageCount = 100

		// Call again
		agent.InitializeStatistics()

		if agent.Statistics != originalStats {
			t.Error("InitializeStatistics() should not replace existing statistics")
		}
		if agent.Statistics.MessageCount != 100 {
			t.Error("Statistics should retain existing values")
		}
	})
}

func TestAgent_UpdateLastActive(t *testing.T) {
	t.Run("does nothing when statistics is nil", func(t *testing.T) {
		agent := &Agent{}
		// Should not panic
		agent.UpdateLastActive()
	})

	t.Run("updates last active time", func(t *testing.T) {
		agent := &Agent{}
		agent.InitializeStatistics()

		beforeUpdate := agent.Statistics.LastActive
		time.Sleep(10 * time.Millisecond) // Small delay to ensure time difference

		agent.UpdateLastActive()

		if !agent.Statistics.LastActive.After(beforeUpdate) {
			t.Error("LastActive should be updated to a later time")
		}
	})
}

func TestAgent_InitializeEvolution(t *testing.T) {
	t.Run("initializes nil evolution", func(t *testing.T) {
		agent := &Agent{}
		if agent.Evolution != nil {
			t.Fatal("Evolution should be nil initially")
		}

		agent.InitializeEvolution()

		if agent.Evolution == nil {
			t.Error("Evolution should not be nil after InitializeEvolution()")
		}
		if agent.Evolution.Stage != types.AgentStageSpark {
			t.Errorf("expected default stage %q, got %q", types.AgentStageSpark, agent.Evolution.Stage)
		}
	})

	t.Run("normalizes existing evolution defaults", func(t *testing.T) {
		agent := &Agent{
			Evolution: &types.AgentEvolution{
				Level:      -1,
				Experience: -5,
			},
		}

		agent.InitializeEvolution()

		if agent.Evolution.Level != 0 {
			t.Errorf("expected normalized level 0, got %d", agent.Evolution.Level)
		}
		if agent.Evolution.Experience != 0 {
			t.Errorf("expected normalized experience 0, got %d", agent.Evolution.Experience)
		}
		if agent.Evolution.Stage != types.AgentStageSpark {
			t.Errorf("expected default stage %q, got %q", types.AgentStageSpark, agent.Evolution.Stage)
		}
	})
}

func TestTypeConstants(t *testing.T) {
	// Verify type constants have expected values
	if TypeToolCalling != "tool-calling" {
		t.Errorf("TypeToolCalling = %q, want %q", TypeToolCalling, "tool-calling")
	}
	if TypeGeneral != "general" {
		t.Errorf("TypeGeneral = %q, want %q", TypeGeneral, "general")
	}
	if TypeResearch != "research" {
		t.Errorf("TypeResearch = %q, want %q", TypeResearch, "research")
	}
}

func TestTypeModels(t *testing.T) {
	// Verify each type has models defined
	types := []string{TypeToolCalling, TypeGeneral, TypeResearch}

	for _, agentType := range types {
		t.Run(agentType, func(t *testing.T) {
			models, exists := TypeModels[agentType]
			if !exists {
				t.Errorf("TypeModels[%q] should exist", agentType)
				return
			}
			if len(models) == 0 {
				t.Errorf("TypeModels[%q] should have at least one model", agentType)
			}
		})
	}
}
