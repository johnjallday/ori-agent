package externalagents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		wantFM       string
		wantBody     string
		wantContains string
	}{
		{
			name: "valid frontmatter",
			content: `---
name: test-agent
description: A test agent
model: opus
color: green
---

This is the system prompt.
`,
			wantFM:       "name: test-agent\ndescription: A test agent\nmodel: opus\ncolor: green",
			wantContains: "This is the system prompt.",
		},
		{
			name:     "no frontmatter",
			content:  "Just some content without frontmatter",
			wantFM:   "",
			wantBody: "Just some content without frontmatter",
		},
		{
			name: "frontmatter without closing delimiter",
			content: `---
name: test
This has no closing delimiter`,
			wantFM:   "",
			wantBody: "---\nname: test\nThis has no closing delimiter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := parseFrontmatter(tt.content)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantFM != "" && fm != tt.wantFM {
				t.Errorf("frontmatter = %q, want %q", fm, tt.wantFM)
			}
			if tt.wantBody != "" && body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
			if tt.wantContains != "" && !contains(body, tt.wantContains) {
				t.Errorf("body does not contain %q", tt.wantContains)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestReadAgents(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a test agent file
	agentContent := `---
name: code-reviewer
description: Reviews code for quality
model: opus
color: green
---

You are a code review specialist.
`
	if err := os.WriteFile(filepath.Join(agentsDir, "code-reviewer.md"), []byte(agentContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a non-md file that should be ignored
	if err := os.WriteFile(filepath.Join(agentsDir, "readme.txt"), []byte("ignore me"), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewClaudeReader(tmpDir)
	agents, err := reader.ReadAgents()
	if err != nil {
		t.Fatalf("ReadAgents() error = %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	agent := agents[0]
	if agent.Name != "code-reviewer" {
		t.Errorf("Name = %q, want %q", agent.Name, "code-reviewer")
	}
	if agent.Description != "Reviews code for quality" {
		t.Errorf("Description = %q, want %q", agent.Description, "Reviews code for quality")
	}
	if agent.Model != "opus" {
		t.Errorf("Model = %q, want %q", agent.Model, "opus")
	}
	if agent.Color != "green" {
		t.Errorf("Color = %q, want %q", agent.Color, "green")
	}
	if agent.Source != SourceClaude {
		t.Errorf("Source = %q, want %q", agent.Source, SourceClaude)
	}
	if agent.SystemPrompt != "You are a code review specialist." {
		t.Errorf("SystemPrompt = %q, want %q", agent.SystemPrompt, "You are a code review specialist.")
	}
}

func TestReadAgents_MissingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	reader := NewClaudeReader(tmpDir)

	agents, err := reader.ReadAgents()
	if err != nil {
		t.Fatalf("ReadAgents() should not error for missing directory, got: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestReadSettings(t *testing.T) {
	tmpDir := t.TempDir()

	settingsContent := `{
  "permissions": {
    "allow": ["Read(/Users/test/**)", "Write(/Users/test/**)"],
    "deny": ["Bash(rm -rf:*)"],
    "defaultMode": "acceptEdits"
  },
  "enabledPlugins": {
    "plugin-a": true,
    "plugin-b": false
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(settingsContent), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewClaudeReader(tmpDir)
	settings, err := reader.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings() error = %v", err)
	}

	if settings == nil {
		t.Fatal("settings should not be nil")
	}
	if len(settings.Permissions.Allow) != 2 {
		t.Errorf("expected 2 allow rules, got %d", len(settings.Permissions.Allow))
	}
	if settings.Permissions.DefaultMode != "acceptEdits" {
		t.Errorf("DefaultMode = %q, want %q", settings.Permissions.DefaultMode, "acceptEdits")
	}
	if !settings.EnabledPlugins["plugin-a"] {
		t.Error("plugin-a should be enabled")
	}
}

func TestReadSettings_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	reader := NewClaudeReader(tmpDir)

	settings, err := reader.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings() should not error for missing file, got: %v", err)
	}
	if settings != nil {
		t.Error("settings should be nil for missing file")
	}
}

func TestReadPlugins(t *testing.T) {
	tmpDir := t.TempDir()
	pluginsDir := filepath.Join(tmpDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		t.Fatal(err)
	}

	pluginsContent := `{
  "version": 2,
  "plugins": {
    "gopls-lsp@claude-plugins-official": [
      {
        "scope": "user",
        "installPath": "/Users/test/.claude/plugins/cache/gopls-lsp",
        "version": "1.0.0",
        "installedAt": "2024-01-18T00:44:29.821Z",
        "lastUpdated": "2024-01-18T00:44:29.821Z",
        "gitCommitSha": "abc123"
      }
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), []byte(pluginsContent), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewClaudeReader(tmpDir)
	plugins, err := reader.ReadPlugins()
	if err != nil {
		t.Fatalf("ReadPlugins() error = %v", err)
	}

	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}

	plugin := plugins[0]
	if plugin.Name != "gopls-lsp@claude-plugins-official" {
		t.Errorf("Name = %q, want %q", plugin.Name, "gopls-lsp@claude-plugins-official")
	}
	if plugin.Scope != "user" {
		t.Errorf("Scope = %q, want %q", plugin.Scope, "user")
	}
	if plugin.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", plugin.Version, "1.0.0")
	}
}

func TestReadPlugins_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	reader := NewClaudeReader(tmpDir)

	plugins, err := reader.ReadPlugins()
	if err != nil {
		t.Fatalf("ReadPlugins() should not error for missing file, got: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(plugins))
	}
}

func TestReadAgents_SpecialCharactersInDescription(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a test agent file with colons in description (like real Claude agents)
	// This breaks standard YAML parsing and requires the manual fallback parser
	agentContent := `---
name: code-reviewer
description: Use this agent when you need to review code. Examples:\n\nUser: "Please review this code"\nAssistant: "I'll review the code for you."
model: opus
color: green
---

You are a code review specialist.
`
	if err := os.WriteFile(filepath.Join(agentsDir, "code-reviewer.md"), []byte(agentContent), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewClaudeReader(tmpDir)
	agents, err := reader.ReadAgents()
	if err != nil {
		t.Fatalf("ReadAgents() error = %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	agent := agents[0]
	if agent.Name != "code-reviewer" {
		t.Errorf("Name = %q, want %q", agent.Name, "code-reviewer")
	}
	// Description should be parsed from the manual fallback parser
	if agent.Description == "" {
		t.Error("Description should not be empty")
	}
	if agent.Model != "opus" {
		t.Errorf("Model = %q, want %q", agent.Model, "opus")
	}
	if agent.Color != "green" {
		t.Errorf("Color = %q, want %q", agent.Color, "green")
	}
	if agent.Source != SourceClaude {
		t.Errorf("Source = %q, want %q", agent.Source, SourceClaude)
	}
}

func TestReadActualClaudeAgents(t *testing.T) {
	// This test reads real Claude agents from ~/.claude if they exist
	// It's useful for verifying the parser works with actual files
	reader := NewClaudeReader("")
	agents, err := reader.ReadAgents()
	if err != nil {
		t.Fatalf("ReadAgents() error = %v", err)
	}

	t.Logf("Found %d agents", len(agents))
	for i, agent := range agents {
		t.Logf("Agent %d: name=%s, model=%s, color=%s, source=%s",
			i+1, agent.Name, agent.Model, agent.Color, agent.Source)
		// Verify essential fields are populated
		if agent.Name == "" {
			t.Errorf("Agent %d has empty name", i+1)
		}
		if agent.Source != SourceClaude {
			t.Errorf("Agent %d has source=%s, want %s", i+1, agent.Source, SourceClaude)
		}
	}
}

func TestParseAgentFrontmatterManual(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		wantName    string
		wantDesc    string
		wantModel   string
		wantColor   string
	}{
		{
			name: "simple values",
			frontmatter: `name: test-agent
description: A simple description
model: opus
color: blue`,
			wantName:  "test-agent",
			wantDesc:  "A simple description",
			wantModel: "opus",
			wantColor: "blue",
		},
		{
			name: "description with colons",
			frontmatter: `name: code-reviewer
description: Use this agent. User: "Hello" Assistant: "Hi"
model: haiku
color: green`,
			wantName:  "code-reviewer",
			wantDesc:  "Use this agent. User: \"Hello\" Assistant: \"Hi\"",
			wantModel: "haiku",
			wantColor: "green",
		},
		{
			name: "quoted values",
			frontmatter: `name: "my-agent"
description: "A quoted description"
model: 'opus'
color: 'red'`,
			wantName:  "my-agent",
			wantDesc:  "A quoted description",
			wantModel: "opus",
			wantColor: "red",
		},
		{
			name: "escaped newlines in description",
			frontmatter: `name: test
description: Line 1\nLine 2\nLine 3
model: opus`,
			wantName:  "test",
			wantDesc:  "Line 1\nLine 2\nLine 3",
			wantModel: "opus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm := parseAgentFrontmatterManual(tt.frontmatter)
			if fm.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", fm.Name, tt.wantName)
			}
			if fm.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", fm.Description, tt.wantDesc)
			}
			if fm.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", fm.Model, tt.wantModel)
			}
			if tt.wantColor != "" && fm.Color != tt.wantColor {
				t.Errorf("Color = %q, want %q", fm.Color, tt.wantColor)
			}
		})
	}
}
