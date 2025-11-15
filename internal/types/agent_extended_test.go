package types

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestNewAgentStatistics(t *testing.T) {
	stats := NewAgentStatistics()

	if stats == nil {
		t.Fatal("NewAgentStatistics returned nil")
	}

	if stats.MessageCount != 0 {
		t.Errorf("Expected MessageCount to be 0, got %d", stats.MessageCount)
	}

	if stats.TokenUsage != 0 {
		t.Errorf("Expected TokenUsage to be 0, got %d", stats.TokenUsage)
	}

	if stats.TotalCost != 0.0 {
		t.Errorf("Expected TotalCost to be 0.0, got %f", stats.TotalCost)
	}

	if stats.LastActive.IsZero() {
		t.Error("Expected LastActive to be set")
	}

	if stats.CreatedAt.IsZero() {
		t.Error("Expected CreatedAt to be set")
	}

	if stats.UpdatedAt.IsZero() {
		t.Error("Expected UpdatedAt to be set")
	}
}

func TestAgentStatistics_RecordMessage(t *testing.T) {
	stats := NewAgentStatistics()
	initialLastActive := stats.LastActive

	// Sleep a bit to ensure timestamp changes
	time.Sleep(10 * time.Millisecond)

	// Record a message
	stats.RecordMessage(100, 0.005)

	if stats.MessageCount != 1 {
		t.Errorf("Expected MessageCount to be 1, got %d", stats.MessageCount)
	}

	if stats.TokenUsage != 100 {
		t.Errorf("Expected TokenUsage to be 100, got %d", stats.TokenUsage)
	}

	if stats.TotalCost != 0.005 {
		t.Errorf("Expected TotalCost to be 0.005, got %f", stats.TotalCost)
	}

	if !stats.LastActive.After(initialLastActive) {
		t.Error("Expected LastActive to be updated")
	}

	if stats.AverageTokens != 100.0 {
		t.Errorf("Expected AverageTokens to be 100.0, got %f", stats.AverageTokens)
	}

	// Record another message
	stats.RecordMessage(200, 0.010)

	if stats.MessageCount != 2 {
		t.Errorf("Expected MessageCount to be 2, got %d", stats.MessageCount)
	}

	if stats.TokenUsage != 300 {
		t.Errorf("Expected TokenUsage to be 300, got %d", stats.TokenUsage)
	}

	if stats.TotalCost != 0.015 {
		t.Errorf("Expected TotalCost to be 0.015, got %f", stats.TotalCost)
	}

	expectedAvg := 150.0
	if stats.AverageTokens != expectedAvg {
		t.Errorf("Expected AverageTokens to be %f, got %f", expectedAvg, stats.AverageTokens)
	}
}

func TestAgentStatistics_RecordTokens(t *testing.T) {
	stats := NewAgentStatistics()

	stats.RecordTokens(50, 150, 0.008)

	if stats.MessageCount != 1 {
		t.Errorf("Expected MessageCount to be 1, got %d", stats.MessageCount)
	}

	if stats.InputTokens != 50 {
		t.Errorf("Expected InputTokens to be 50, got %d", stats.InputTokens)
	}

	if stats.OutputTokens != 150 {
		t.Errorf("Expected OutputTokens to be 150, got %d", stats.OutputTokens)
	}

	if stats.TokenUsage != 200 {
		t.Errorf("Expected TokenUsage to be 200, got %d", stats.TokenUsage)
	}

	if stats.TotalCost != 0.008 {
		t.Errorf("Expected TotalCost to be 0.008, got %f", stats.TotalCost)
	}
}

func TestAgentStatistics_RecordMessage_NilSafety(t *testing.T) {
	var stats *AgentStatistics

	// Should not panic when nil
	stats.RecordMessage(100, 0.005)

	// Verify it's still nil and didn't crash
	if stats != nil {
		t.Error("Expected stats to remain nil")
	}
}

func TestAgentStatistics_UpdateLastActive(t *testing.T) {
	stats := NewAgentStatistics()
	initialLastActive := stats.LastActive

	time.Sleep(10 * time.Millisecond)

	stats.UpdateLastActive()

	if !stats.LastActive.After(initialLastActive) {
		t.Error("Expected LastActive to be updated")
	}
}

func TestAgentStatistics_UpdateLastActive_NilSafety(t *testing.T) {
	var stats *AgentStatistics

	// Should not panic when nil
	stats.UpdateLastActive()

	if stats != nil {
		t.Error("Expected stats to remain nil")
	}
}

func TestAgentStatistics_ConcurrentUpdates(t *testing.T) {
	stats := NewAgentStatistics()
	var wg sync.WaitGroup

	// Run 100 concurrent message recordings
	numGoroutines := 100
	messagesPerGoroutine := 10

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				stats.RecordMessage(50, 0.001)
			}
		}()
	}

	wg.Wait()

	expectedMessages := int64(numGoroutines * messagesPerGoroutine)
	expectedTokens := expectedMessages * 50
	expectedCost := float64(expectedMessages) * 0.001

	if stats.MessageCount != expectedMessages {
		t.Errorf("Expected MessageCount to be %d, got %d", expectedMessages, stats.MessageCount)
	}

	if stats.TokenUsage != expectedTokens {
		t.Errorf("Expected TokenUsage to be %d, got %d", expectedTokens, stats.TokenUsage)
	}

	// Use approximate comparison for float
	if abs(stats.TotalCost-expectedCost) > 0.0001 {
		t.Errorf("Expected TotalCost to be approximately %f, got %f", expectedCost, stats.TotalCost)
	}
}

func TestAgentStatistics_GetSafeStats(t *testing.T) {
	stats := NewAgentStatistics()
	stats.RecordMessage(100, 0.005)

	safeCopy := stats.GetSafeStats()

	// Verify copy has same values
	if safeCopy.MessageCount != stats.MessageCount {
		t.Errorf("Expected MessageCount to match, got %d vs %d", safeCopy.MessageCount, stats.MessageCount)
	}

	if safeCopy.TokenUsage != stats.TokenUsage {
		t.Errorf("Expected TokenUsage to match, got %d vs %d", safeCopy.TokenUsage, stats.TokenUsage)
	}

	// Modify original
	stats.RecordMessage(200, 0.010)

	// Verify copy is unchanged
	if safeCopy.MessageCount != 1 {
		t.Error("Expected safe copy to be independent of original")
	}
}

func TestAgentStatistics_GetSafeStats_NilSafety(t *testing.T) {
	var stats *AgentStatistics

	safeCopy := stats.GetSafeStats()

	// Should return empty struct, not panic
	if safeCopy.MessageCount != 0 {
		t.Error("Expected empty statistics")
	}
}

func TestAgentStatistics_JSONSerialization(t *testing.T) {
	stats := NewAgentStatistics()
	stats.RecordMessage(100, 0.005)
	stats.RecordTokens(50, 150, 0.008)

	// Serialize
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Failed to marshal statistics: %v", err)
	}

	// Deserialize
	var decoded AgentStatistics
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal statistics: %v", err)
	}

	// Verify values (note: mutex won't be serialized)
	if decoded.MessageCount != stats.MessageCount {
		t.Error("MessageCount mismatch after serialization")
	}

	if decoded.TokenUsage != stats.TokenUsage {
		t.Error("TokenUsage mismatch after serialization")
	}

	if decoded.TotalCost != stats.TotalCost {
		t.Error("TotalCost mismatch after serialization")
	}
}

func TestAgentMetadata_JSONSerialization(t *testing.T) {
	metadata := &AgentMetadata{
		Description: "Test agent",
		Tags:        []string{"test", "development"},
		AvatarColor: "#3498db",
		Favorite:    true,
	}

	// Serialize
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Failed to marshal metadata: %v", err)
	}

	// Deserialize
	var decoded AgentMetadata
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal metadata: %v", err)
	}

	// Verify values
	if decoded.Description != metadata.Description {
		t.Error("Description mismatch after serialization")
	}

	if len(decoded.Tags) != len(metadata.Tags) {
		t.Error("Tags length mismatch after serialization")
	}

	if decoded.AvatarColor != metadata.AvatarColor {
		t.Error("AvatarColor mismatch after serialization")
	}

	if decoded.Favorite != metadata.Favorite {
		t.Error("Favorite mismatch after serialization")
	}
}

func TestAgentStatus_Constants(t *testing.T) {
	// Verify status constants are defined
	statuses := []AgentStatus{
		AgentStatusActive,
		AgentStatusIdle,
		AgentStatusError,
		AgentStatusDisabled,
	}

	for _, status := range statuses {
		if status == "" {
			t.Error("Status constant is empty")
		}
	}
}

func TestDashboardStats_Struct(t *testing.T) {
	stats := &DashboardStats{
		TotalAgents:             10,
		ActiveAgents:            7,
		IdleAgents:              2,
		DisabledAgents:          1,
		ErrorAgents:             0,
		TotalMessages:           1000,
		TotalTokens:             50000,
		TotalCost:               25.50,
		MostActiveAgent:         "agent-1",
		MostCostlyAgent:         "agent-2",
		NewestAgent:             "agent-3",
		AverageMessagesPerAgent: 100.0,
		AverageCostPerAgent:     2.55,
	}

	// Serialize and deserialize
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Failed to marshal dashboard stats: %v", err)
	}

	var decoded DashboardStats
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal dashboard stats: %v", err)
	}

	// Verify key fields
	if decoded.TotalAgents != stats.TotalAgents {
		t.Error("TotalAgents mismatch")
	}

	if decoded.ActiveAgents != stats.ActiveAgents {
		t.Error("ActiveAgents mismatch")
	}

	if decoded.TotalMessages != stats.TotalMessages {
		t.Error("TotalMessages mismatch")
	}
}

// Helper function for float comparison
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
