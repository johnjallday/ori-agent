package externalagentshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/externalagents"
)

func setupTestCache(t *testing.T) (*externalagents.Cache, *config.Manager, string) {
	tmpDir := t.TempDir()

	// Create Claude test data
	agentsDir := filepath.Join(tmpDir, "claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}

	agentContent := `---
name: test-agent
description: A test agent
model: opus
color: blue
---

Test system prompt.
`
	if err := os.WriteFile(filepath.Join(agentsDir, "test-agent.md"), []byte(agentContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create Codex test data
	codexDir := filepath.Join(tmpDir, "codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}

	configContent := `model = "gpt-4"
model_reasoning_effort = "high"
`
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	claudeReader := externalagents.NewClaudeReader(filepath.Join(tmpDir, "claude"))
	codexReader := externalagents.NewCodexReader(filepath.Join(tmpDir, "codex"))

	cache := externalagents.NewCache(claudeReader, codexReader)
	if err := cache.Load(); err != nil {
		t.Fatal(err)
	}

	// Create config manager with external agents enabled
	configManager := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	_ = configManager.Load() // Ignore error in tests - may not exist yet
	configManager.SetExternalAgentsClaudeEnabled(true)
	configManager.SetExternalAgentsCodexEnabled(true)

	return cache, configManager, tmpDir
}

func TestGetAll(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)
	handler := New(cache, configManager, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/external-agents", nil)
	w := httptest.NewRecorder()

	handler.GetAll(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var data struct {
		ClaudeEnabled bool                       `json:"claude_enabled"`
		CodexEnabled  bool                       `json:"codex_enabled"`
		Claude        *externalagents.ClaudeData `json:"claude"`
		Codex         *externalagents.CodexData  `json:"codex"`
	}
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !data.ClaudeEnabled {
		t.Error("claude_enabled should be true")
	}
	if !data.CodexEnabled {
		t.Error("codex_enabled should be true")
	}
	if data.Claude == nil {
		t.Error("Claude data should not be nil")
	}
	if len(data.Claude.Agents) != 1 {
		t.Errorf("expected 1 Claude agent, got %d", len(data.Claude.Agents))
	}
	if data.Codex == nil {
		t.Error("Codex data should not be nil")
	}
	if data.Codex.Config == nil {
		t.Error("Codex config should not be nil")
	}
}

func TestGetAll_MethodNotAllowed(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)
	handler := New(cache, configManager, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/external-agents", nil)
	w := httptest.NewRecorder()

	handler.GetAll(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestGetClaude(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)
	handler := New(cache, configManager, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/external-agents/claude", nil)
	w := httptest.NewRecorder()

	handler.GetClaude(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var data externalagents.ClaudeData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(data.Agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(data.Agents))
	}
	if data.Agents[0].Name != "test-agent" {
		t.Errorf("expected agent name 'test-agent', got %q", data.Agents[0].Name)
	}
}

func TestGetCodex(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)
	handler := New(cache, configManager, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/external-agents/codex", nil)
	w := httptest.NewRecorder()

	handler.GetCodex(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var data externalagents.CodexData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if data.Config == nil {
		t.Fatal("config should not be nil")
	}
	if data.Config.Model != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", data.Config.Model)
	}
}

func TestRefresh(t *testing.T) {
	cache, configManager, tmpDir := setupTestCache(t)
	handler := New(cache, configManager, nil)

	// Verify initial state
	agents := cache.GetClaudeAgents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent initially, got %d", len(agents))
	}

	// Add a new agent file
	newAgentContent := `---
name: new-agent
description: A new agent
model: sonnet
color: green
---

New agent prompt.
`
	agentsDir := filepath.Join(tmpDir, "claude", "agents")
	if err := os.WriteFile(filepath.Join(agentsDir, "new-agent.md"), []byte(newAgentContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Refresh
	req := httptest.NewRequest(http.MethodPost, "/api/external-agents/refresh", nil)
	w := httptest.NewRecorder()

	handler.Refresh(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify new agent is now in cache
	agents = cache.GetClaudeAgents()
	if len(agents) != 2 {
		t.Errorf("expected 2 agents after refresh, got %d", len(agents))
	}
}

func TestRefresh_MethodNotAllowed(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)
	handler := New(cache, configManager, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/external-agents/refresh", nil)
	w := httptest.NewRecorder()

	handler.Refresh(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestGetAll_Disabled(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)
	configManager.SetExternalAgentsClaudeEnabled(false)
	configManager.SetExternalAgentsCodexEnabled(false)
	handler := New(cache, configManager, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/external-agents", nil)
	w := httptest.NewRecorder()

	handler.GetAll(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var data struct {
		ClaudeEnabled bool                       `json:"claude_enabled"`
		CodexEnabled  bool                       `json:"codex_enabled"`
		Claude        *externalagents.ClaudeData `json:"claude"`
		Codex         *externalagents.CodexData  `json:"codex"`
	}
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if data.ClaudeEnabled {
		t.Error("claude_enabled should be false")
	}
	if data.CodexEnabled {
		t.Error("codex_enabled should be false")
	}
	if data.Claude != nil {
		t.Error("Claude data should be nil when disabled")
	}
	if data.Codex != nil {
		t.Error("Codex data should be nil when disabled")
	}
}

func TestClaudeSyncData(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)

	// Explicit opt-out -> nil even when the CLI is detected.
	configManager.SetExternalAgentsClaudeEnabled(false)
	configManager.SetExternalAgentsClaudeDisabled(true)
	h := New(cache, configManager, func() bool { return true })
	if h.ClaudeSyncData() != nil {
		t.Error("expected nil sync data when opted out")
	}

	// Force-enabled -> returns the cached ClaudeData (with the test agent).
	configManager.SetExternalAgentsClaudeDisabled(false)
	configManager.SetExternalAgentsClaudeEnabled(true)
	h = New(cache, configManager, func() bool { return false })
	data := h.ClaudeSyncData()
	if data == nil {
		t.Fatal("expected sync data when enabled")
	}
	cd, ok := data.(*externalagents.ClaudeData)
	if !ok {
		t.Fatalf("expected *externalagents.ClaudeData, got %T", data)
	}
	if len(cd.Agents) != 1 {
		t.Errorf("expected 1 claude agent, got %d", len(cd.Agents))
	}

	// Auto-enable purely via CLI detection -> returns data.
	configManager.SetExternalAgentsClaudeEnabled(false)
	h = New(cache, configManager, func() bool { return true })
	if h.ClaudeSyncData() == nil {
		t.Error("expected sync data when CLI detected (auto-enable)")
	}
}

func TestClaudeEffectiveEnabled_TruthTable(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)
	configManager.SetExternalAgentsCodexEnabled(false)

	cases := []struct {
		name        string
		enabled     bool
		disabled    bool
		cliDetected bool
		want        bool
	}{
		{"unset, no CLI", false, false, false, false},
		{"unset, CLI detected -> auto-enable", false, false, true, true},
		{"force-enabled, no CLI", true, false, false, true},
		{"force-enabled, CLI detected", true, false, true, true},
		{"opt-out beats CLI detection", false, true, true, false},
		{"opt-out beats force-enable", true, true, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configManager.SetExternalAgentsClaudeEnabled(tc.enabled)
			configManager.SetExternalAgentsClaudeDisabled(tc.disabled)
			handler := New(cache, configManager, func() bool { return tc.cliDetected })

			req := httptest.NewRequest(http.MethodGet, "/api/external-agents", nil)
			w := httptest.NewRecorder()
			handler.GetAll(w, req)

			var data struct {
				ClaudeEnabled bool                       `json:"claude_enabled"`
				Claude        *externalagents.ClaudeData `json:"claude"`
			}
			if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if data.ClaudeEnabled != tc.want {
				t.Errorf("claude_enabled = %v, want %v", data.ClaudeEnabled, tc.want)
			}
			// Data must only be present when effectively enabled.
			if (data.Claude != nil) != tc.want {
				t.Errorf("claude data present = %v, want %v", data.Claude != nil, tc.want)
			}
		})
	}
}

func TestGetAll_PartialEnabled(t *testing.T) {
	cache, configManager, _ := setupTestCache(t)
	configManager.SetExternalAgentsClaudeEnabled(true)
	configManager.SetExternalAgentsCodexEnabled(false)
	handler := New(cache, configManager, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/external-agents", nil)
	w := httptest.NewRecorder()

	handler.GetAll(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var data struct {
		ClaudeEnabled bool                       `json:"claude_enabled"`
		CodexEnabled  bool                       `json:"codex_enabled"`
		Claude        *externalagents.ClaudeData `json:"claude"`
		Codex         *externalagents.CodexData  `json:"codex"`
	}
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !data.ClaudeEnabled {
		t.Error("claude_enabled should be true")
	}
	if data.CodexEnabled {
		t.Error("codex_enabled should be false")
	}
	if data.Claude == nil {
		t.Error("Claude data should not be nil when enabled")
	}
	if data.Codex != nil {
		t.Error("Codex data should be nil when disabled")
	}
}
