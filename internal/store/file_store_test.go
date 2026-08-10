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
		path: indexPath,
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
				Status:     types.AgentStatusActive,
				Statistics: stats,
				Appearance: &types.AgentAppearance{
					Mode:      types.AppearanceModeGenerated,
					Generated: &types.GeneratedAppearance{Color: "#3366ff"},
				},
				Metadata: &types.AgentMetadata{
					Description: "Primary analysis agent",
					Tags:        []string{"analysis", "primary"},
					Favorite:    true,
					RoutingProfile: &types.AgentRoutingProfile{
						MatchPhrases:    []string{"open my latest reaper project"},
						ExampleRequests: []string{"render stems from yesterday's session"},
						Domains:         []string{"reaper", "audio"},
						ExternalSystems: []string{"reaper"},
						SideEffects:     "local_app",
					},
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
	if got.Metadata.RoutingProfile == nil {
		t.Fatal("expected routing profile to be loaded")
	}
	if len(got.Metadata.RoutingProfile.ExampleRequests) != 1 {
		t.Errorf("expected routing profile examples to persist, got %d", len(got.Metadata.RoutingProfile.ExampleRequests))
	}
	// The persistence projection is explicit, so a first-class field that is not
	// listed in it round-trips as nil. Appearance is asserted here rather than
	// only in the migration tests because that omission is silent (FR-1/FR-68).
	if got.Appearance == nil {
		t.Fatal("expected appearance to be loaded")
	}
	if got.Appearance.Mode != types.AppearanceModeGenerated {
		t.Errorf("expected generated mode to persist, got %q", got.Appearance.Mode)
	}
	if got.Appearance.GeneratedColor() != "#3366ff" {
		t.Errorf("expected generated colour to persist, got %q", got.Appearance.GeneratedColor())
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

	if got.Capabilities == nil {
		t.Fatal("expected capabilities slice to be initialized")
	}
	if got.Statistics == nil {
		t.Fatal("expected statistics to be initialized")
	}
}

func TestFileStore_Load_NestedAgents_NoIndexFile(t *testing.T) {
	// The agents/ directory is the source of truth. A missing index file
	// (agents.json) must NOT prevent agents on disk from loading — this is the
	// regression that made freshly-adopted legacy agents invisible on first start.
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents.json") // intentionally not created

	agentDir := filepath.Join(tempDir, "agents", "orphan")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("failed creating agent directory: %v", err)
	}
	settings := `{"type":"general","Settings":{"model":"gpt-4o-mini","temperature":1}}`
	if err := os.WriteFile(filepath.Join(agentDir, "agent_settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatalf("failed writing agent settings: %v", err)
	}

	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: index file should not exist (err=%v)", err)
	}

	fs := &fileStore{path: indexPath, agents: make(map[string]*agent.Agent)}
	if err := fs.load(); err != nil {
		t.Fatalf("load() failed: %v", err)
	}
	if got, ok := fs.agents["orphan"]; !ok || got == nil {
		t.Fatalf("expected orphan agent to load without an index file; agents=%v", fs.ListAgents())
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

	if got.Statistics == nil {
		t.Fatal("expected statistics to be initialized")
	}
}

func TestFileStore_Load_LegacyFlatAgentFile_IgnoresLegacyMCPOverrideAndDefaults(t *testing.T) {
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

	if got.Status != types.AgentStatusIdle {
		t.Errorf("expected default status idle, got %q", got.Status)
	}
	if got.Statistics == nil {
		t.Fatal("expected statistics to be initialized")
	}
}

func readSkillsStateDefault(t *testing.T, skillsStatePath string) (enabled bool, trusted bool) {
	t.Helper()

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
	return defaultState.Enabled, defaultState.Trusted
}

func TestFileStore_NewFileStore_BackfillsMissingSkillsStateForLoadedAgent(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	if err := os.WriteFile(indexPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("failed writing index file: %v", err)
	}

	agentDir := filepath.Join(tempDir, "agents", "restored-agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("failed creating agent directory: %v", err)
	}
	settings := `{
		"type":"orchestration",
		"Settings":{"model":"gpt-4o-mini","temperature":1}
	}`
	if err := os.WriteFile(filepath.Join(agentDir, "agent_settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatalf("failed writing agent settings: %v", err)
	}

	if _, err := NewFileStore(indexPath, types.Settings{}); err != nil {
		t.Fatalf("NewFileStore() failed: %v", err)
	}

	enabled, trusted := readSkillsStateDefault(t, filepath.Join(agentDir, "skills_state.json"))
	if enabled {
		t.Fatalf("expected backfilled wildcard default state to start disabled")
	}
	if trusted {
		t.Fatalf("expected backfilled wildcard trusted state to start false")
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

	enabled, trusted := readSkillsStateDefault(t, filepath.Join(tempDir, "agents", "new-agent", "skills_state.json"))
	if enabled {
		t.Fatalf("expected wildcard default state to start disabled")
	}
	if trusted {
		t.Fatalf("expected wildcard default trusted to start false")
	}
}

func TestFileStore_SetAgent_InitializesSkillsStateWithDisabledDefault(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	fs, err := NewFileStore(indexPath, types.Settings{
		Model:       "gpt-4o-mini",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("NewFileStore() failed: %v", err)
	}

	if err := fs.SetAgent("snapshot-agent", &agent.Agent{
		Type: agent.TypeGeneral,
		Settings: types.Settings{
			Model:       "gpt-4o-mini",
			Temperature: 1.0,
		},
	}); err != nil {
		t.Fatalf("SetAgent() failed: %v", err)
	}

	enabled, trusted := readSkillsStateDefault(t, filepath.Join(tempDir, "agents", "snapshot-agent", "skills_state.json"))
	if enabled {
		t.Fatalf("expected wildcard default state to start disabled")
	}
	if trusted {
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

func TestFileStore_CreateAgent_OrchestrationDefaultsToOrchestratorRole(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")

	fs, err := NewFileStore(indexPath, types.Settings{
		Model:       "gpt-5",
		Temperature: 0.5,
	})
	if err != nil {
		t.Fatalf("NewFileStore() failed: %v", err)
	}

	if err := fs.CreateAgent("orchestration-agent", &CreateAgentConfig{
		Type: "orchestration",
	}); err != nil {
		t.Fatalf("CreateAgent() failed: %v", err)
	}

	created, ok := fs.GetAgent("orchestration-agent")
	if !ok || created == nil {
		t.Fatalf("expected created agent to exist")
	}
	if created.Type != "orchestration" {
		t.Fatalf("expected type orchestration, got %q", created.Type)
	}
	if created.Role != types.RoleOrchestrator {
		t.Fatalf("expected role %q, got %q", types.RoleOrchestrator, created.Role)
	}
}
