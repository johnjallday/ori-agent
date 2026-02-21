package chathttp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/oriagent/ori-pluginapi"
)

func TestFindTool_PrefersMCPBrowserOverNativeUtility(t *testing.T) {
	registry := &mockMCPRegistry{
		getFn: func(server string) ([]pluginapi.PluginTool, error) {
			if server != "playwright" {
				return nil, nil
			}
			return []pluginapi.PluginTool{
				mockPluginTool{name: "browser"},
			}, nil
		},
	}

	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
		mcpRegistry:     registry,
	}

	ag := &agent.Agent{
		MCPServers: []string{"playwright"},
	}

	tool, found := h.findTool(ag, "Email Triage Assistant", "browser")
	if !found {
		t.Fatal("expected browser tool to be found")
	}

	result, err := tool.Call(context.Background(), `{"action":"open_url","url":"https://mail.google.com"}`)
	if err != nil {
		t.Fatalf("expected MCP browser tool call to succeed, got %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected MCP mock tool result \"ok\", got %q", result)
	}
	if registry.getCalls == 0 {
		t.Fatal("expected MCP registry lookup to run for browser tool preference")
	}
}

func TestFindTool_BrowserFallsBackToUtilityWhenMCPBrowserMissing(t *testing.T) {
	registry := &mockMCPRegistry{
		getFn: func(server string) ([]pluginapi.PluginTool, error) {
			if server != "playwright" {
				return nil, nil
			}
			return []pluginapi.PluginTool{
				mockPluginTool{name: "navigate"},
			}, nil
		},
	}

	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
		mcpRegistry:     registry,
	}

	ag := &agent.Agent{
		MCPServers: []string{"playwright"},
	}

	tool, found := h.findTool(ag, "Email Triage Assistant", "browser")
	if !found {
		t.Fatal("expected browser tool to be found via utility fallback")
	}

	def := tool.Definition()
	if !strings.Contains(strings.ToLower(def.Description), "browser automation") {
		t.Fatalf("expected utility browser definition, got description %q", def.Description)
	}

	_, err := tool.Call(context.Background(), `{}`)
	if !errors.Is(err, ErrUtilityInvalidInput) {
		t.Fatalf("expected utility browser fallback with invalid-input behavior, got %v", err)
	}
	if registry.getCalls == 0 {
		t.Fatal("expected MCP registry lookup before utility fallback")
	}
}
