package skillshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type testSkillsProvider struct {
	name string
}

func (p *testSkillsProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "prompt body"}, nil
}

func (p *testSkillsProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func (p *testSkillsProvider) Name() string {
	return p.name
}

func (p *testSkillsProvider) Type() llm.ProviderType {
	return llm.ProviderTypeCloud
}

func (p *testSkillsProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{}
}

func (p *testSkillsProvider) ValidateConfig(_ llm.ProviderConfig) error {
	return nil
}

func (p *testSkillsProvider) DefaultModels() []string {
	return nil
}

type testAgentStore struct {
	current string
	agents  map[string]*agent.Agent
}

var _ store.Store = (*testAgentStore)(nil)

func (s *testAgentStore) ListAgents() []string {
	names := make([]string, 0, len(s.agents))
	for name := range s.agents {
		names = append(names, name)
	}
	return names
}

func (s *testAgentStore) CreateAgent(string, *store.CreateAgentConfig) error {
	return nil
}

func (s *testAgentStore) DeleteAgent(string) error {
	return nil
}

func (s *testAgentStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *testAgentStore) SetAgent(name string, ag *agent.Agent) error {
	if s.agents == nil {
		s.agents = map[string]*agent.Agent{}
	}
	s.agents[name] = ag
	return nil
}

func (s *testAgentStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	ag, ok := s.agents[name]
	if !ok {
		return nil
	}
	return updateFn(ag)
}

func (s *testAgentStore) ClearAgents() error {
	s.agents = map[string]*agent.Agent{}
	return nil
}

func (s *testAgentStore) Save() error {
	return nil
}

func TestCreateSkill_FallsBackToLocalCreationWhenSkillsCLIUnavailable(t *testing.T) {
	tmpDir := t.TempDir()
	manager := skills.NewManager(skills.ManagerConfig{
		AgentStorePath: filepath.Join(tmpDir, "agents", "index.json"),
	})
	store := &testAgentStore{current: "Ori"}
	handler := New(manager, store, nil, nil)
	handler.skillsCLIInDir = func(context.Context, string, ...string) (string, error) {
		return "", exec.ErrNotFound
	}

	body, err := json.Marshal(map[string]any{
		"agent":       "Ori",
		"name":        "demo-skill",
		"description": "Demo skill",
		"prompt":      "Do the thing.",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/skills", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.createSkill(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	skillPath := filepath.Join(tmpDir, "agents", "Ori", "skills", "demo-skill", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected local fallback skill at %s: %v", skillPath, err)
	}
}

func TestListSkills_IncludesAgentLoadout(t *testing.T) {
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents", "index.json")
	manager := skills.NewManager(skills.ManagerConfig{AgentStorePath: agentStorePath})

	// A repo skill so the loadout has something to reference.
	skillDir := filepath.Join(tmpDir, "agents", "skills", "repo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMd := "---\nname: repo-skill\ndescription: Repo skill\n---\nprompt\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	agentStore := &testAgentStore{agents: map[string]*agent.Agent{
		"Worker": {
			Role:      types.RoleResearcher, // catalog role -> expert defaults OFF
			Evolution: &types.AgentEvolution{Stage: types.AgentStageInfant},
			Metadata:  &types.AgentMetadata{},
		},
	}}
	handler := New(manager, agentStore, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/skills?agent=Worker", nil)
	rec := httptest.NewRecorder()
	handler.listSkills(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Loadout *struct {
			Stage      string `json:"stage"`
			SlotCap    int    `json:"slot_cap"`
			SlotsUsed  int    `json:"slots_used"`
			ExpertMode bool   `json:"expert_mode"`
		} `json:"loadout"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Loadout == nil {
		t.Fatal("expected loadout in response")
	}
	if resp.Loadout.Stage != string(types.AgentStageInfant) {
		t.Errorf("stage = %q, want infant", resp.Loadout.Stage)
	}
	if resp.Loadout.SlotCap != 3 {
		t.Errorf("slot_cap = %d, want 3 (infant)", resp.Loadout.SlotCap)
	}
	if resp.Loadout.SlotsUsed != 0 {
		t.Errorf("slots_used = %d, want 0 (repo skill disabled by default)", resp.Loadout.SlotsUsed)
	}
	if resp.Loadout.ExpertMode {
		t.Errorf("expert_mode = true, want false for a catalog-role agent with unset flag")
	}
}

func TestResolvePromptProvider_UsesModelOnlyAgentConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	if err := cfg.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	factory := llm.NewFactory()
	factory.Register("claude_code", &testSkillsProvider{name: "claude_code"})

	store := &testAgentStore{
		current: "Ori",
		agents: map[string]*agent.Agent{
			"Ori": {
				Settings: types.Settings{
					Model: "sonnet",
				},
			},
		},
	}

	handler := New(nil, store, factory, cfg)
	provider, model, reasoningEffort, err := handler.resolvePromptProvider("Ori")
	if err != nil {
		t.Fatalf("resolvePromptProvider failed: %v", err)
	}
	if provider.Name() != "claude_code" {
		t.Fatalf("expected claude_code provider, got %q", provider.Name())
	}
	if model != "sonnet" {
		t.Fatalf("expected sonnet model, got %q", model)
	}
	if reasoningEffort != "" {
		t.Fatalf("expected empty reasoning effort for agent-specific provider, got %q", reasoningEffort)
	}
}

func TestResolvePromptProvider_CorrectsStaleOpenAIProviderForClaudeAlias(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	if err := cfg.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	factory := llm.NewFactory()
	factory.Register("openai", &testSkillsProvider{name: "openai"})
	factory.Register("claude_code", &testSkillsProvider{name: "claude_code"})

	store := &testAgentStore{
		current: "Ori",
		agents: map[string]*agent.Agent{
			"Ori": {
				Settings: types.Settings{
					Provider: "openai",
					Model:    "sonnet",
				},
			},
		},
	}

	handler := New(nil, store, factory, cfg)
	provider, model, _, err := handler.resolvePromptProvider("Ori")
	if err != nil {
		t.Fatalf("resolvePromptProvider failed: %v", err)
	}
	if provider.Name() != "claude_code" {
		t.Fatalf("expected claude_code provider, got %q", provider.Name())
	}
	if model != "sonnet" {
		t.Fatalf("expected sonnet model, got %q", model)
	}
}
