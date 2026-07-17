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
		RoutingProfile: &AgentRoutingProfile{
			MatchPhrases:    []string{"open my latest reaper project"},
			ExampleRequests: []string{"render stems from yesterday's session"},
			Domains:         []string{"reaper", "audio"},
			ExternalSystems: []string{"reaper"},
			SideEffects:     "local_app",
		},
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
	if decoded.RoutingProfile == nil {
		t.Fatal("RoutingProfile missing after serialization")
	}
	if len(decoded.RoutingProfile.MatchPhrases) != len(metadata.RoutingProfile.MatchPhrases) {
		t.Error("RoutingProfile.MatchPhrases length mismatch after serialization")
	}
	if decoded.RoutingProfile.SideEffects != metadata.RoutingProfile.SideEffects {
		t.Error("RoutingProfile.SideEffects mismatch after serialization")
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

func TestNewAgentEvolution(t *testing.T) {
	evolution := NewAgentEvolution()

	if evolution == nil {
		t.Fatal("NewAgentEvolution returned nil")
	}
	if evolution.Level != 0 {
		t.Errorf("expected level 0, got %d", evolution.Level)
	}
	if evolution.Experience != 0 {
		t.Errorf("expected experience 0, got %d", evolution.Experience)
	}
	if evolution.Stage != AgentStageSpark {
		t.Errorf("expected stage %q, got %q", AgentStageSpark, evolution.Stage)
	}
	if evolution.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestAgentEvolution_EnsureDefaults(t *testing.T) {
	evolution := &AgentEvolution{
		Level:      -1,
		Experience: -10,
		FeedCount:  -2,
	}

	evolution.EnsureDefaults()

	if evolution.Level != 0 {
		t.Errorf("expected level to be normalized to 0, got %d", evolution.Level)
	}
	if evolution.Experience != 0 {
		t.Errorf("expected experience to be normalized to 0, got %d", evolution.Experience)
	}
	if evolution.FeedCount != 0 {
		t.Errorf("expected feed_count to be normalized to 0, got %d", evolution.FeedCount)
	}
	if evolution.Stage != AgentStageSpark {
		t.Errorf("expected stage to default to %q, got %q", AgentStageSpark, evolution.Stage)
	}
	if evolution.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestAgentEvolution_JSONSerialization(t *testing.T) {
	evolution := &AgentEvolution{
		Level:      7,
		Experience: 980,
		Stage:      AgentStageLearner,
		Path:       AgentPathCoder,
		ParentID:   "commander-1",
		FeedCount:  3,
		UpdatedAt:  time.Now().UTC().Truncate(time.Second),
	}

	data, err := json.Marshal(evolution)
	if err != nil {
		t.Fatalf("failed to marshal evolution: %v", err)
	}

	var decoded AgentEvolution
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal evolution: %v", err)
	}

	if decoded.Level != evolution.Level {
		t.Errorf("expected level %d, got %d", evolution.Level, decoded.Level)
	}
	if decoded.Experience != evolution.Experience {
		t.Errorf("expected experience %d, got %d", evolution.Experience, decoded.Experience)
	}
	if decoded.Stage != evolution.Stage {
		t.Errorf("expected stage %q, got %q", evolution.Stage, decoded.Stage)
	}
	if decoded.Path != evolution.Path {
		t.Errorf("expected path %q, got %q", evolution.Path, decoded.Path)
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

func TestSkillSlotsForStage(t *testing.T) {
	cases := []struct {
		stage AgentStage
		want  int
	}{
		{AgentStageSpark, 2},
		{AgentStageInfant, 3},
		{AgentStageLearner, 4},
		{AgentStageExpert, 5},
		{AgentStageSentient, 6},
		{"", 2},            // unknown/empty defaults to the lowest cap
		{"bogus-stage", 2}, // unrecognized value defaults to the lowest cap
	}
	for _, tc := range cases {
		if got := SkillSlotsForStage(tc.stage); got != tc.want {
			t.Errorf("SkillSlotsForStage(%q) = %d, want %d", tc.stage, got, tc.want)
		}
	}
}

func TestAgentMetadata_IsExpertMode(t *testing.T) {
	trueVal := true
	falseVal := false

	cases := []struct {
		name string
		m    *AgentMetadata
		role AgentRole
		want bool
	}{
		{"nil metadata, general role -> ON (unspecialized default)", nil, RoleGeneral, true},
		{"nil metadata, empty role -> ON (unspecialized default)", nil, "", true},
		{"nil metadata, catalog role -> OFF", nil, RoleResearcher, false},
		{"unset flag, general role -> ON", &AgentMetadata{}, RoleGeneral, true},
		{"unset flag, catalog role -> OFF", &AgentMetadata{}, RoleOrchestrator, false},
		{"explicit true wins regardless of role", &AgentMetadata{ExpertMode: &trueVal}, RoleResearcher, true},
		{"explicit false wins regardless of role", &AgentMetadata{ExpertMode: &falseVal}, RoleGeneral, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.IsExpertMode(tc.role); got != tc.want {
				t.Errorf("IsExpertMode() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Helper function for float comparison
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
