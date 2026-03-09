package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestFileStore_SaveLoad_NestedRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	stats := types.NewAgentStatistics()
	stats.MessageCount = 42
	stats.TokenUsage = 2048
	stats.TotalCost = 1.25
	stats.InputTokens = 1200
	stats.OutputTokens = 848
	stats.AverageTokens = 48.76
	stats.LastActive = time.Now().UTC().Truncate(time.Second)
	stats.CreatedAt = stats.LastActive.Add(-1 * time.Hour)
	stats.UpdatedAt = stats.LastActive

	source := &fileStore{
		path:    indexPath,
		current: "alpha",
		agents: map[string]*agent.Agent{
			"alpha": {
				Type:         agent.TypeGeneral,
				Role:         types.RoleAnalyzer,
				Capabilities: []string{types.CapabilityWebSearch, types.CapabilityCodeAnalysis},
				Settings: types.Settings{
					Model:           "gpt-5.4",
					Temperature:     0.6,
					SystemPrompt:    "You are an analytical assistant.",
					Provider:        "codex",
					ReasoningEffort: "high",
					MaxOutputTokens: 1200,
				},
				Plugins: map[string]types.LoadedPlugin{
					"demo-plugin": {
						Path:    "uploaded_plugins/demo-plugin",
						Version: "1.0.0",
					},
				},
				MCPServers: []string{"filesystem", "github"},
				Status:     types.AgentStatusActive,
				Statistics: stats,
				Metadata: &types.AgentMetadata{
					Description: "Primary analysis agent",
					Tags:        []string{"analysis", "primary"},
					AvatarColor: "#3366ff",
					Favorite:    true,
				},
			},
		},
	}

	if err := source.saveUnlocked(); err != nil {
		t.Fatalf("saveUnlocked() failed: %v", err)
	}

	loaded := &fileStore{
		path:   indexPath,
		agents: make(map[string]*agent.Agent),
	}
	if err := loaded.load(); err != nil {
		t.Fatalf("load() failed: %v", err)
	}

	if loaded.current != "alpha" {
		t.Fatalf("expected current agent alpha, got %q", loaded.current)
	}

	got, ok := loaded.agents["alpha"]
	if !ok || got == nil {
		t.Fatal("expected agent alpha to be loaded")
	}

	if got.Type != agent.TypeGeneral {
		t.Errorf("expected type %q, got %q", agent.TypeGeneral, got.Type)
	}
	if got.Role != types.RoleAnalyzer {
		t.Errorf("expected role %q, got %q", types.RoleAnalyzer, got.Role)
	}
	if got.Status != types.AgentStatusActive {
		t.Errorf("expected status %q, got %q", types.AgentStatusActive, got.Status)
	}
	if len(got.Capabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(got.Capabilities))
	}
	if got.Settings.Model != "gpt-5.4" {
		t.Errorf("expected model gpt-5.4, got %q", got.Settings.Model)
	}
	if got.Settings.Provider != "codex" {
		t.Errorf("expected provider codex, got %q", got.Settings.Provider)
	}
	if got.Settings.ReasoningEffort != "high" {
		t.Errorf("expected reasoning_effort high, got %q", got.Settings.ReasoningEffort)
	}
	if got.Settings.MaxOutputTokens != 1200 {
		t.Errorf("expected max_output_tokens 1200, got %d", got.Settings.MaxOutputTokens)
	}
	if len(got.Plugins) != 1 {
		t.Errorf("expected 1 plugin, got %d", len(got.Plugins))
	}
	if len(got.MCPServers) != 2 {
		t.Errorf("expected 2 MCP servers, got %d", len(got.MCPServers))
	}
	if got.Statistics == nil {
		t.Fatal("expected statistics to be loaded")
	}
	if got.Statistics.MessageCount != 42 {
		t.Errorf("expected message_count 42, got %d", got.Statistics.MessageCount)
	}
	if got.Statistics.TokenUsage != 2048 {
		t.Errorf("expected token_usage 2048, got %d", got.Statistics.TokenUsage)
	}
	if got.Metadata == nil {
		t.Fatal("expected metadata to be loaded")
	}
	if got.Metadata.Description != "Primary analysis agent" {
		t.Errorf("expected metadata description to persist, got %q", got.Metadata.Description)
	}
	if !got.Metadata.Favorite {
		t.Error("expected metadata favorite to persist")
	}
}

func TestFileStore_Load_NestedMinimalSettings_MigratesDefaults(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	if err := os.WriteFile(indexPath, []byte(`{"current":"legacy"}`), 0o644); err != nil {
		t.Fatalf("failed writing index file: %v", err)
	}

	agentDir := filepath.Join(tempDir, "agents", "legacy")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("failed creating agent directory: %v", err)
	}

	minimal := `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini","temperature":1},
		"Plugins":null
	}`
	if err := os.WriteFile(filepath.Join(agentDir, "agent_settings.json"), []byte(minimal), 0o644); err != nil {
		t.Fatalf("failed writing minimal agent settings: %v", err)
	}

	fs := &fileStore{
		path:   indexPath,
		agents: make(map[string]*agent.Agent),
	}
	if err := fs.load(); err != nil {
		t.Fatalf("load() failed: %v", err)
	}

	got, ok := fs.agents["legacy"]
	if !ok || got == nil {
		t.Fatal("expected legacy agent to load")
	}
	if got.Status != types.AgentStatusIdle {
		t.Errorf("expected default status idle, got %q", got.Status)
	}
	if got.Plugins == nil {
		t.Fatal("expected plugins map to be initialized")
	}
	if got.Capabilities == nil {
		t.Fatal("expected capabilities slice to be initialized")
	}
	if got.Statistics == nil {
		t.Fatal("expected statistics to be initialized")
	}
}

func TestFileStore_Load_OldTopLevelFormat_MigratesDefaults(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	payload := struct {
		Agents  map[string]*agent.Agent `json:"agents"`
		Current string                  `json:"current"`
	}{
		Agents: map[string]*agent.Agent{
			"legacy-top": {
				Type: "general",
				Settings: types.Settings{
					Model:       "gpt-4o-mini",
					Temperature: 1.0,
				},
				Plugins: nil,
			},
		},
		Current: "legacy-top",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed marshaling legacy payload: %v", err)
	}
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatalf("failed writing legacy index file: %v", err)
	}

	fs := &fileStore{
		path:   indexPath,
		agents: make(map[string]*agent.Agent),
	}
	if err := fs.load(); err != nil {
		t.Fatalf("load() failed: %v", err)
	}

	got, ok := fs.agents["legacy-top"]
	if !ok || got == nil {
		t.Fatal("expected legacy-top agent to load")
	}
	if got.Status != types.AgentStatusIdle {
		t.Errorf("expected default status idle, got %q", got.Status)
	}
	if got.Plugins == nil {
		t.Fatal("expected plugins map to be initialized")
	}
	if got.Statistics == nil {
		t.Fatal("expected statistics to be initialized")
	}
}

func TestFileStore_Load_LegacyFlatAgentFile_MCPOverrideAndDefaults(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	if err := os.WriteFile(indexPath, []byte(`{"current":"flat"}`), 0o644); err != nil {
		t.Fatalf("failed writing index file: %v", err)
	}

	agentsDir := filepath.Join(tempDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("failed creating agents directory: %v", err)
	}

	legacyAgent := agent.Agent{
		Type: "research",
		Settings: types.Settings{
			Model:       "gpt-5",
			Temperature: 0.2,
		},
		Plugins: nil,
	}
	agentData, err := json.Marshal(legacyAgent)
	if err != nil {
		t.Fatalf("failed marshaling flat legacy agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "flat.json"), agentData, 0o644); err != nil {
		t.Fatalf("failed writing flat legacy agent: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(agentsDir, "flat"), 0o755); err != nil {
		t.Fatalf("failed creating flat mcp directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(agentsDir, "flat", "mcp_servers.json"),
		[]byte(`{"enabled_servers":["filesystem","git"]}`),
		0o644,
	); err != nil {
		t.Fatalf("failed writing mcp servers file: %v", err)
	}

	fs := &fileStore{
		path:   indexPath,
		agents: make(map[string]*agent.Agent),
	}
	if err := fs.load(); err != nil {
		t.Fatalf("load() failed: %v", err)
	}

	got, ok := fs.agents["flat"]
	if !ok || got == nil {
		t.Fatal("expected flat agent to load")
	}
	if got.Type != "research" {
		t.Errorf("expected type research, got %q", got.Type)
	}
	if got.Plugins == nil {
		t.Fatal("expected plugins map to be initialized")
	}
	if got.Status != types.AgentStatusIdle {
		t.Errorf("expected default status idle, got %q", got.Status)
	}
	if got.Statistics == nil {
		t.Fatal("expected statistics to be initialized")
	}
	if len(got.MCPServers) != 2 {
		t.Fatalf("expected 2 MCP servers from legacy override, got %d", len(got.MCPServers))
	}
}

func TestFileStore_CreateAgent_InitializesSkillsStateWithDisabledDefault(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	fs, err := NewFileStore(indexPath, types.Settings{
		Model:       "gpt-4o-mini",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("NewFileStore() failed: %v", err)
	}

	if err := fs.CreateAgent("new-agent", &CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("CreateAgent() failed: %v", err)
	}

	skillsStatePath := filepath.Join(tempDir, "agents", "new-agent", "skills_state.json")
	data, err := os.ReadFile(skillsStatePath)
	if err != nil {
		t.Fatalf("failed reading skills state: %v", err)
	}

	var registry struct {
		Skills map[string]struct {
			Enabled bool `json:"enabled"`
			Trusted bool `json:"trusted"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatalf("failed decoding skills state: %v", err)
	}

	defaultState, ok := registry.Skills["*"]
	if !ok {
		t.Fatalf("expected wildcard default state entry to exist")
	}
	if defaultState.Enabled {
		t.Fatalf("expected wildcard default state to start disabled")
	}
	if defaultState.Trusted {
		t.Fatalf("expected wildcard default trusted to start false")
	}
}

func TestFileStore_CreateAgent_AppliesAllowWebSearchOverride(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	fs, err := NewFileStore(indexPath, types.Settings{
		Model:       "gpt-4o-mini",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("NewFileStore() failed: %v", err)
	}

	allowWebSearch := false
	if err := fs.CreateAgent("restricted-agent", &CreateAgentConfig{
		Type:           agent.TypeGeneral,
		AllowWebSearch: &allowWebSearch,
	}); err != nil {
		t.Fatalf("CreateAgent() failed: %v", err)
	}

	created, ok := fs.GetAgent("restricted-agent")
	if !ok || created == nil {
		t.Fatalf("expected created agent to exist")
	}
	if created.Settings.AllowWebSearch == nil {
		t.Fatalf("expected allow_web_search to be persisted on agent settings")
	}
	if created.Settings.IsWebSearchAllowed() {
		t.Fatalf("expected web search to be disabled")
	}
}

func TestFileStore_CreateAgent_AppliesReasoningEffortOverride(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	fs, err := NewFileStore(indexPath, types.Settings{
		Model:       "gpt-5.4",
		Provider:    "codex",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("NewFileStore() failed: %v", err)
	}

	if err := fs.CreateAgent("codex-agent", &CreateAgentConfig{
		Type:            agent.TypeResearch,
		Model:           "gpt-5.4",
		LLMProvider:     "codex",
		ReasoningEffort: "xhigh",
	}); err != nil {
		t.Fatalf("CreateAgent() failed: %v", err)
	}

	created, ok := fs.GetAgent("codex-agent")
	if !ok || created == nil {
		t.Fatalf("expected created agent to exist")
	}
	if created.Settings.ReasoningEffort != "xhigh" {
		t.Fatalf("expected reasoning_effort xhigh, got %q", created.Settings.ReasoningEffort)
	}
}
