package agent

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

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
