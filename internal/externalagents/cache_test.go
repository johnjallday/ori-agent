package externalagents

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCacheLoad_MalformedClaudeJSONIsIsolated verifies that a malformed
// ~/.claude.json leaves the MCP-server and recent-project sections empty
// without breaking the agents that load from ~/.claude (PRD req 6 — each
// section fails independently).
func TestCacheLoad_MalformedClaudeJSONIsIsolated(t *testing.T) {
	tmpDir := t.TempDir()

	// Valid agent under ~/.claude/agents.
	agentsDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}
	agentContent := "---\nname: a\ndescription: d\nmodel: opus\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "a.md"), []byte(agentContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Malformed ~/.claude.json (test fixture lives at <baseDir>/.claude.json).
	if err := os.WriteFile(filepath.Join(tmpDir, ".claude.json"), []byte("{ not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	cache := NewCache(NewClaudeReader(tmpDir), nil)
	if err := cache.Load(); err != nil {
		t.Fatalf("Load() should not fail on malformed ~/.claude.json: %v", err)
	}

	if got := cache.GetClaudeAgents(); len(got) != 1 {
		t.Errorf("expected 1 claude agent despite malformed claude.json, got %d", len(got))
	}
	if got := cache.GetClaudeMCPServers(); len(got) != 0 {
		t.Errorf("expected 0 MCP servers from malformed claude.json, got %d", len(got))
	}
	if got := cache.GetClaudeRecentProjects(); len(got) != 0 {
		t.Errorf("expected 0 recent projects from malformed claude.json, got %d", len(got))
	}
}
