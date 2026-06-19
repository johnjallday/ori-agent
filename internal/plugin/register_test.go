package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

func TestResolveCommand(t *testing.T) {
	cases := []struct {
		name     string
		spec     MCPServerSpec
		dir      string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:    "claude plugin-root var",
			spec:    MCPServerSpec{Command: "${CLAUDE_PLUGIN_ROOT}/bin/reaper-mcp", Args: []string{"--x"}},
			dir:     "/p",
			wantCmd: "/p/bin/reaper-mcp", wantArgs: []string{"--x"},
		},
		{
			name:    "codex relative command with cwd",
			spec:    MCPServerSpec{Command: "./app/bin", Cwd: "."},
			dir:     "/p",
			wantCmd: "/p/app/bin", wantArgs: []string{},
		},
		{
			name:    "bare PATH command untouched",
			spec:    MCPServerSpec{Command: "npx", Args: []string{"-y", "srv"}},
			dir:     "/p",
			wantCmd: "npx", wantArgs: []string{"-y", "srv"},
		},
		{
			name:    "absolute command untouched",
			spec:    MCPServerSpec{Command: "/usr/local/bin/srv"},
			dir:     "/p",
			wantCmd: "/usr/local/bin/srv", wantArgs: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, args := resolveCommand(tc.spec, tc.dir)
			if cmd != tc.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tc.wantCmd)
			}
			if len(args) != len(tc.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tc.wantArgs)
			}
			for i := range args {
				if args[i] != tc.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tc.wantArgs[i])
				}
			}
		})
	}
}

func TestToServerConfig(t *testing.T) {
	cfg := ToServerConfig("reaper", MCPServerSpec{Name: "ori-reaper", Command: "${CLAUDE_PLUGIN_ROOT}/bin/x"}, "/p")
	if cfg.Name != "reaper/ori-reaper" {
		t.Errorf("name = %q, want reaper/ori-reaper", cfg.Name)
	}
	if cfg.Command != "/p/bin/x" {
		t.Errorf("command = %q", cfg.Command)
	}
	if cfg.Transport != "stdio" {
		t.Errorf("transport = %q", cfg.Transport)
	}
	if cfg.Enabled {
		t.Errorf("server should be registered disabled")
	}
}

func TestCommandAvailable(t *testing.T) {
	// PATH command that exists everywhere.
	if !CommandAvailable("sh") {
		t.Errorf("sh should be available on PATH")
	}
	// Executable file.
	exe := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !CommandAvailable(exe) {
		t.Errorf("executable file should be available: %s", exe)
	}
	// Non-executable file.
	noexe := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(noexe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if CommandAvailable(noexe) {
		t.Errorf("non-executable file should be unavailable")
	}
	// Missing path.
	if CommandAvailable("/nope/missing-binary") {
		t.Errorf("missing path should be unavailable")
	}
}

// --- fakes for Register ---

type fakeRegistrar struct {
	added   map[string]mcp.ServerConfig
	removed []string
	failOn  string
}

func (f *fakeRegistrar) AddServer(cfg mcp.ServerConfig) error {
	if cfg.Name == f.failOn {
		return fmt.Errorf("add failed for %s", cfg.Name)
	}
	if f.added == nil {
		f.added = map[string]mcp.ServerConfig{}
	}
	f.added[cfg.Name] = cfg
	return nil
}

func (f *fakeRegistrar) RemoveServer(name string) error {
	f.removed = append(f.removed, name)
	delete(f.added, name)
	return nil
}

type fakeSkills struct {
	installed []string
	removed   []string
	failOn    string
}

func (f *fakeSkills) InstallSkill(_, name, _ string) error {
	if name == f.failOn {
		return fmt.Errorf("install failed for %s", name)
	}
	f.installed = append(f.installed, name)
	return nil
}

func (f *fakeSkills) RemoveSkill(_, name string) error {
	f.removed = append(f.removed, name)
	return nil
}

func TestRegisterSuccessWithBinaryWarning(t *testing.T) {
	d := PluginDescriptor{
		Name:       "reaper",
		InstallDir: "/p",
		MCPServers: []MCPServerSpec{{Name: "ori-reaper", Command: "/nope/missing"}},
		Skills:     []SkillSpec{{Name: "reaper-session-setup", Path: "/p/skills/reaper-session-setup"}},
	}
	reg := &fakeRegistrar{}
	sk := &fakeSkills{}

	res, err := Register(d, reg, sk)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(res.MCPServers) != 1 || res.MCPServers[0] != "reaper/ori-reaper" {
		t.Errorf("mcp = %v", res.MCPServers)
	}
	if len(res.Skills) != 1 {
		t.Errorf("skills = %v", res.Skills)
	}
	if len(res.BinaryWarnings) != 1 {
		t.Errorf("expected a binary-missing warning, got %v", res.BinaryWarnings)
	}
}

func TestRegisterRollbackOnSkillFailure(t *testing.T) {
	d := PluginDescriptor{
		Name:       "p",
		InstallDir: "/p",
		MCPServers: []MCPServerSpec{{Name: "srv", Command: "/usr/bin/true"}},
		Skills:     []SkillSpec{{Name: "bad", Path: "/p/skills/bad"}},
	}
	reg := &fakeRegistrar{}
	sk := &fakeSkills{failOn: "bad"}

	if _, err := Register(d, reg, sk); err == nil {
		t.Fatal("expected error from skill install")
	}
	if len(reg.removed) != 1 || reg.removed[0] != "p/srv" {
		t.Errorf("expected MCP server rollback, removed = %v", reg.removed)
	}
	if len(reg.added) != 0 {
		t.Errorf("server should have been rolled back, added = %v", reg.added)
	}
}
