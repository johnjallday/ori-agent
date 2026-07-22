package chathttp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/toolapi"
)

type mockPluginTool struct {
	name string
}

func (m mockPluginTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
		Name:        m.name,
		Description: "mock tool",
		Parameters: map[string]any{
			"type": "object",
		},
	}
}

func (m mockPluginTool) Call(_ context.Context, _ string) (string, error) {
	return "ok", nil
}

type mockMCPRegistry struct {
	getCalls   int
	startCalls int
	getFn      func(server string) ([]toolapi.Tool, error)
	startFn    func(server string) error
}

func (m *mockMCPRegistry) GetToolsForServer(server string) ([]toolapi.Tool, error) {
	m.getCalls++
	if m.getFn == nil {
		return nil, nil
	}
	return m.getFn(server)
}

func (m *mockMCPRegistry) GetAllTools() []toolapi.Tool {
	return nil
}

func (m *mockMCPRegistry) StartServer(server string) error {
	m.startCalls++
	if m.startFn == nil {
		return nil
	}
	return m.startFn(server)
}

func TestGetMCPToolsForServer_StartsStoppedServer(t *testing.T) {
	reg := &mockMCPRegistry{}
	reg.getFn = func(server string) ([]toolapi.Tool, error) {
		if reg.getCalls == 1 {
			return nil, errors.New("server " + server + " is not running")
		}
		return []toolapi.Tool{mockPluginTool{name: "list_directory"}}, nil
	}
	reg.startFn = func(server string) error {
		if server != "filesystem" {
			t.Fatalf("unexpected server start call: %s", server)
		}
		return nil
	}

	h := &Handler{mcpRegistry: reg}
	tools, err := h.getMCPToolsForServer("filesystem")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if reg.startCalls != 1 {
		t.Fatalf("expected 1 start call, got %d", reg.startCalls)
	}
	if len(tools) != 1 || tools[0].Definition().Name != "list_directory" {
		t.Fatalf("unexpected tools result: %+v", tools)
	}
}

func TestGetMCPToolsForServer_DoesNotStartOnOtherErrors(t *testing.T) {
	reg := &mockMCPRegistry{
		getFn: func(server string) ([]toolapi.Tool, error) {
			return nil, errors.New("server not found")
		},
	}
	h := &Handler{mcpRegistry: reg}

	_, err := h.getMCPToolsForServer("filesystem")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if reg.startCalls != 0 {
		t.Fatalf("expected 0 start calls, got %d", reg.startCalls)
	}
}

func TestGetMCPToolsForServer_ReturnsStartError(t *testing.T) {
	reg := &mockMCPRegistry{
		getFn: func(server string) ([]toolapi.Tool, error) {
			return nil, errors.New("server " + server + " is not running")
		},
		startFn: func(server string) error {
			return errors.New("npx not found")
		},
	}
	h := &Handler{mcpRegistry: reg}

	_, err := h.getMCPToolsForServer("filesystem")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to start MCP server") {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.startCalls != 1 {
		t.Fatalf("expected 1 start call, got %d", reg.startCalls)
	}
}

func TestFilterAllowedMCPTools(t *testing.T) {
	tools := []toolapi.Tool{
		mockPluginTool{name: "list_events"},
		mockPluginTool{name: "list_calendars"},
		mockPluginTool{name: "delete_event"},
	}

	t.Run("nil allowlist keeps legacy all-tools behavior", func(t *testing.T) {
		got := filterAllowedMCPTools(tools, nil, "calendar-mcp")
		if len(got) != 3 {
			t.Fatalf("got %d tools, want 3", len(got))
		}
	})

	t.Run("server absent from allowlist keeps all its tools", func(t *testing.T) {
		got := filterAllowedMCPTools(tools, map[string][]string{"other-server": {"x"}}, "calendar-mcp")
		if len(got) != 3 {
			t.Fatalf("got %d tools, want 3", len(got))
		}
	})

	t.Run("restricts to allowed names case-insensitively", func(t *testing.T) {
		got := filterAllowedMCPTools(tools, map[string][]string{"calendar-mcp": {"List_Events", "list_calendars"}}, "calendar-mcp")
		if len(got) != 2 {
			t.Fatalf("got %d tools, want 2: %+v", len(got), got)
		}
		for _, tool := range got {
			name := tool.Definition().Name
			if name == "delete_event" {
				t.Fatalf("delete_event must be filtered out, got %+v", got)
			}
		}
	})

	t.Run("empty allowlist entry denies all tools for that server", func(t *testing.T) {
		got := filterAllowedMCPTools(tools, map[string][]string{"calendar-mcp": {}}, "calendar-mcp")
		if len(got) != 0 {
			t.Fatalf("got %d tools, want 0: %+v", len(got), got)
		}
	})
}

func TestFindMCPToolByName_HonorsAllowlist(t *testing.T) {
	reg := &mockMCPRegistry{getFn: func(string) ([]toolapi.Tool, error) {
		return []toolapi.Tool{
			mockPluginTool{name: "list_events"},
			mockPluginTool{name: "delete_event"},
		}, nil
	}}
	h := &Handler{mcpRegistry: reg}
	ag := &resolvedChatAgent{
		MCPServers:       []string{"calendar-mcp"},
		MCPToolAllowlist: map[string][]string{"calendar-mcp": {"list_events"}},
	}

	if _, ok := h.findMCPToolByName(ag, "list_events"); !ok {
		t.Error("expected list_events to resolve (allowlisted)")
	}
	if _, ok := h.findMCPToolByName(ag, "delete_event"); ok {
		t.Error("expected delete_event to be blocked by the binding's AllowedTools")
	}
}
