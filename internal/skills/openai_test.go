package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOpenAIMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skill")
	if err := os.MkdirAll(filepath.Join(skillDir, "agents"), 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}

	content := `display_name: Friendly
short_description: Short desc
icon: icon.svg
brand_color: "#ff00ff"
default_prompt: Do the thing
dependencies:
  tools:
    - tool_a
    - tool_b
  mcp_servers:
    - server_a
`
	if err := os.WriteFile(filepath.Join(skillDir, "agents", "openai.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write openai.yaml: %v", err)
	}

	meta, err := loadOpenAIMetadata(skillDir)
	if err != nil {
		t.Fatalf("loadOpenAIMetadata error: %v", err)
	}
	if meta == nil {
		t.Fatalf("expected metadata")
	}
	if meta.DisplayName != "Friendly" {
		t.Fatalf("display name = %q", meta.DisplayName)
	}
	if meta.ShortDescription != "Short desc" {
		t.Fatalf("short description = %q", meta.ShortDescription)
	}
	if meta.Icon != "icon.svg" {
		t.Fatalf("icon = %q", meta.Icon)
	}
	if meta.BrandColor != "#ff00ff" {
		t.Fatalf("brand color = %q", meta.BrandColor)
	}
	if meta.DefaultPrompt != "Do the thing" {
		t.Fatalf("default prompt = %q", meta.DefaultPrompt)
	}
	if len(meta.Tools) != 2 || meta.Tools[0] != "tool_a" {
		t.Fatalf("tools = %v", meta.Tools)
	}
	if len(meta.MCPServers) != 1 || meta.MCPServers[0] != "server_a" {
		t.Fatalf("mcp servers = %v", meta.MCPServers)
	}
}
