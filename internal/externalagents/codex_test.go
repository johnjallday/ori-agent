package externalagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadConfig(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `model = "gpt-5.2-codex"
model_reasoning_effort = "xhigh"

[projects."/Users/test/Projects"]
trust_level = "trusted"
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewCodexReader(tmpDir)
	config, err := reader.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}

	if config == nil {
		t.Fatal("config should not be nil")
	}
	if config.Model != "gpt-5.2-codex" {
		t.Errorf("Model = %q, want %q", config.Model, "gpt-5.2-codex")
	}
	if config.ModelReasoningEffort != "xhigh" {
		t.Errorf("ModelReasoningEffort = %q, want %q", config.ModelReasoningEffort, "xhigh")
	}
}

func TestReadConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	reader := NewCodexReader(tmpDir)

	config, err := reader.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig() should not error for missing file, got: %v", err)
	}
	if config != nil {
		t.Error("config should be nil for missing file")
	}
}

func TestReadConfig_MinimalConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Minimal config with just model
	configContent := `model = "gpt-4"
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewCodexReader(tmpDir)
	config, err := reader.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}

	if config.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", config.Model, "gpt-4")
	}
	if config.ModelReasoningEffort != "" {
		t.Errorf("ModelReasoningEffort should be empty, got %q", config.ModelReasoningEffort)
	}
}

func TestReadCodexMCPServers_RedactsEnvValues(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `model = "gpt-5.2-codex"

[mcp_servers.local]
command = "node"
args = ["server.js"]

[mcp_servers.local.env]
API_KEY = "secret-value"
TOKEN = "another-secret"

[mcp_servers.remote]
transport = "sse"
url = "https://example.com/sse"
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewCodexReader(tmpDir)
	servers, err := reader.ReadMCPServers()
	if err != nil {
		t.Fatalf("ReadMCPServers() error = %v", err)
	}

	if len(servers) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(servers))
	}
	if servers[0].Name != "local" {
		t.Fatalf("first server = %q, want local", servers[0].Name)
	}
	if servers[0].Transport != "stdio" {
		t.Errorf("local transport = %q, want stdio", servers[0].Transport)
	}
	if got := strings.Join(servers[0].EnvNames, ","); got != "API_KEY,TOKEN" {
		t.Errorf("env names = %q, want API_KEY,TOKEN", got)
	}

	payload, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal servers: %v", err)
	}
	if strings.Contains(string(payload), "secret-value") || strings.Contains(string(payload), "another-secret") {
		t.Fatalf("MCP env secret value leaked in payload: %s", string(payload))
	}
}

func TestReadSkills(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create skill directories
	if err := os.MkdirAll(filepath.Join(skillsDir, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "custom"), 0755); err != nil {
		t.Fatal(err)
	}
	// Hidden directory should be ignored
	if err := os.MkdirAll(filepath.Join(skillsDir, ".system"), 0755); err != nil {
		t.Fatal(err)
	}
	// File should be ignored
	if err := os.WriteFile(filepath.Join(skillsDir, "readme.txt"), []byte("ignore"), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewCodexReader(tmpDir)
	skills, err := reader.ReadSkills()
	if err != nil {
		t.Fatalf("ReadSkills() error = %v", err)
	}

	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	// Check skills are present (order may vary)
	skillNames := make(map[string]bool)
	for _, s := range skills {
		skillNames[s.Name] = true
	}
	if !skillNames["public"] {
		t.Error("expected 'public' skill")
	}
	if !skillNames["custom"] {
		t.Error("expected 'custom' skill")
	}
}

func TestReadSkills_MissingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	reader := NewCodexReader(tmpDir)

	skills, err := reader.ReadSkills()
	if err != nil {
		t.Fatalf("ReadSkills() should not error for missing directory, got: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestReadRules(t *testing.T) {
	tmpDir := t.TempDir()
	rulesDir := filepath.Join(tmpDir, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create rule files
	if err := os.WriteFile(filepath.Join(rulesDir, "default.rules"), []byte("rule content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "custom.rules"), []byte("custom rule"), 0644); err != nil {
		t.Fatal(err)
	}
	// Non-.rules file should be ignored
	if err := os.WriteFile(filepath.Join(rulesDir, "readme.txt"), []byte("ignore"), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewCodexReader(tmpDir)
	rules, err := reader.ReadRules()
	if err != nil {
		t.Fatalf("ReadRules() error = %v", err)
	}

	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	// Check rules are present (order may vary)
	ruleNames := make(map[string]bool)
	for _, r := range rules {
		ruleNames[r.Name] = true
	}
	if !ruleNames["default"] {
		t.Error("expected 'default' rule")
	}
	if !ruleNames["custom"] {
		t.Error("expected 'custom' rule")
	}
}

func TestReadRules_MissingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	reader := NewCodexReader(tmpDir)

	rules, err := reader.ReadRules()
	if err != nil {
		t.Fatalf("ReadRules() should not error for missing directory, got: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}

func TestReadCodexAgents(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills", "public", "git-commit")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a SKILL.md file
	skillContent := `---
name: git-commit-message
description: Write Conventional Commit messages with subject+body using git context.
---

# Git Commit Message

Generate commit messages following Conventional Commits format.
`
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewCodexReader(tmpDir)
	agents, err := reader.ReadAgents()
	if err != nil {
		t.Fatalf("ReadAgents() error = %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	agent := agents[0]
	if agent.Name != "git-commit-message" {
		t.Errorf("Name = %q, want %q", agent.Name, "git-commit-message")
	}
	if agent.Description != "Write Conventional Commit messages with subject+body using git context." {
		t.Errorf("Description = %q", agent.Description)
	}
	if agent.Source != SourceCodex {
		t.Errorf("Source = %q, want %q", agent.Source, SourceCodex)
	}
	if agent.SystemPrompt == "" {
		t.Error("SystemPrompt should not be empty")
	}
}

func TestReadCodexAgents_MissingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	reader := NewCodexReader(tmpDir)

	agents, err := reader.ReadAgents()
	if err != nil {
		t.Fatalf("ReadAgents() should not error for missing directory, got: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestReadActualCodexAgents(t *testing.T) {
	// This test reads real Codex skills from ~/.codex if they exist
	reader := NewCodexReader("")
	agents, err := reader.ReadAgents()
	if err != nil {
		t.Fatalf("ReadAgents() error = %v", err)
	}

	t.Logf("Found %d Codex agents (skills)", len(agents))
	for i, agent := range agents {
		t.Logf("Agent %d: name=%s, source=%s", i+1, agent.Name, agent.Source)
		if agent.Source != SourceCodex {
			t.Errorf("Agent %d has source=%s, want %s", i+1, agent.Source, SourceCodex)
		}
	}
}
