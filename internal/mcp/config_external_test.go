package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportExternalGlobalServers_ImportsCodexServers(t *testing.T) {
	baseDir := t.TempDir()
	homeDir := t.TempDir()
	setTestHome(t, homeDir)

	codexConfigPath := filepath.Join(homeDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexConfigPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	content := `
[mcp_servers.ori-reaper]
command = "/tmp/reaper-mcp"
args = ["--stdio"]

[mcp_servers.invalid]
transport = "stdio"

[mcp_servers.remote]
url = "https://example.com/sse"
transport = "sse"
`
	if err := os.WriteFile(codexConfigPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write codex config failed: %v", err)
	}

	cm := NewConfigManager(baseDir)
	if err := cm.InitializeDefaultServers(); err != nil {
		t.Fatalf("InitializeDefaultServers failed: %v", err)
	}

	added, err := cm.ImportExternalGlobalServers()
	if err != nil {
		t.Fatalf("ImportExternalGlobalServers failed: %v", err)
	}
	if added != 1 {
		t.Fatalf("expected 1 imported server, got %d", added)
	}

	cfg, err := cm.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig failed: %v", err)
	}

	server, ok := findServerByName(cfg.Servers, "ori-reaper")
	if !ok {
		t.Fatalf("expected ori-reaper in global config")
	}
	if server.Command != "/tmp/reaper-mcp" {
		t.Fatalf("unexpected command: %q", server.Command)
	}
	if server.Transport != "stdio" {
		t.Fatalf("unexpected transport: %q", server.Transport)
	}
}

func TestImportExternalGlobalServers_DoesNotOverwriteExisting(t *testing.T) {
	baseDir := t.TempDir()
	homeDir := t.TempDir()
	setTestHome(t, homeDir)

	codexConfigPath := filepath.Join(homeDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexConfigPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	content := `
[mcp_servers.duplicate]
command = "/tmp/external-command"
`
	if err := os.WriteFile(codexConfigPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write codex config failed: %v", err)
	}

	cm := NewConfigManager(baseDir)
	if err := cm.SaveGlobalConfig(&GlobalConfig{
		Servers: []ServerConfig{
			{
				Name:      "duplicate",
				Command:   "/tmp/local-command",
				Transport: "stdio",
				Env:       map[string]string{},
			},
		},
	}); err != nil {
		t.Fatalf("SaveGlobalConfig failed: %v", err)
	}

	added, err := cm.ImportExternalGlobalServers()
	if err != nil {
		t.Fatalf("ImportExternalGlobalServers failed: %v", err)
	}
	if added != 0 {
		t.Fatalf("expected 0 imported servers, got %d", added)
	}

	cfg, err := cm.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig failed: %v", err)
	}

	server, ok := findServerByName(cfg.Servers, "duplicate")
	if !ok {
		t.Fatalf("expected duplicate in global config")
	}
	if server.Command != "/tmp/local-command" {
		t.Fatalf("existing server command was overwritten: %q", server.Command)
	}
}

func TestImportExternalGlobalServers_ImportsClaudeDesktopServers(t *testing.T) {
	baseDir := t.TempDir()
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	claudePath := filepath.Join(appData, "Claude", "claude_desktop_config.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	content := `{
  "mcpServers": {
    "desktop-files": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    },
    "remote-server": {
      "transport": "sse",
      "url": "https://example.com/mcp"
    }
  }
}`
	if err := os.WriteFile(claudePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write claude desktop config failed: %v", err)
	}

	cm := NewConfigManager(baseDir)
	added, err := cm.ImportExternalGlobalServers()
	if err != nil {
		t.Fatalf("ImportExternalGlobalServers failed: %v", err)
	}
	if added != 1 {
		t.Fatalf("expected 1 imported server, got %d", added)
	}

	cfg, err := cm.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig failed: %v", err)
	}

	server, ok := findServerByName(cfg.Servers, "desktop-files")
	if !ok {
		t.Fatalf("expected desktop-files in global config")
	}
	if server.Command != "npx" {
		t.Fatalf("unexpected command: %q", server.Command)
	}
	if len(server.Args) != 3 {
		t.Fatalf("unexpected args length: %d", len(server.Args))
	}
}

func setTestHome(t *testing.T, homeDir string) {
	t.Helper()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
}

func findServerByName(servers []ServerConfig, name string) (ServerConfig, bool) {
	for _, server := range servers {
		if server.Name == name {
			return server, true
		}
	}
	return ServerConfig{}, false
}
