package chathttp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/oriagent/ori-pluginapi"
)

type mockNamedTool struct {
	name   string
	result string
}

func (m mockNamedTool) Definition() pluginapi.Tool {
	return pluginapi.Tool{
		Name:        m.name,
		Description: "mock named tool",
		Parameters: map[string]interface{}{
			"type": "object",
		},
	}
}

func (m mockNamedTool) Call(_ context.Context, _ string) (string, error) {
	if strings.TrimSpace(m.result) == "" {
		return "ok", nil
	}
	return m.result, nil
}

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

func TestFindTool_BrowserPrefersPlaywrightServer(t *testing.T) {
	registry := &mockMCPRegistry{
		getFn: func(server string) ([]pluginapi.PluginTool, error) {
			switch server {
			case "playwright":
				return []pluginapi.PluginTool{
					mockNamedTool{name: "browser", result: "playwright"},
				}, nil
			case "puppeteer":
				return []pluginapi.PluginTool{
					mockNamedTool{name: "browser", result: "puppeteer"},
				}, nil
			default:
				return nil, nil
			}
		},
	}

	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
		mcpRegistry:     registry,
	}

	ag := &agent.Agent{
		MCPServers: []string{"puppeteer", "playwright"},
	}

	tool, found := h.findTool(ag, "Email Triage Assistant", "browser")
	if !found {
		t.Fatal("expected browser tool to be found")
	}

	result, err := tool.Call(context.Background(), `{"action":"open_url","url":"https://instagram.com"}`)
	if err != nil {
		t.Fatalf("unexpected error from preferred MCP browser tool: %v", err)
	}
	if result != "playwright" {
		t.Fatalf("expected playwright browser tool to be preferred, got %q", result)
	}
}

func TestFindTool_BrowserSuppressedWhenBrowserMCPConfiguredButNoMatchingTool(t *testing.T) {
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

	_, found := h.findTool(ag, "Email Triage Assistant", "browser")
	if found {
		t.Fatal("expected browser utility to be suppressed when browser MCP is configured")
	}
	if registry.getCalls == 0 {
		t.Fatal("expected MCP registry lookup before suppression")
	}
}

func TestFindTool_BrowserUsesUtilityWhenNoBrowserMCPConfigured(t *testing.T) {
	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
	}

	ag := &agent.Agent{}

	tool, found := h.findTool(ag, "General Agent", "browser")
	if !found {
		t.Fatal("expected native browser utility when no browser MCP is configured")
	}

	def := tool.Definition()
	if !strings.Contains(strings.ToLower(def.Description), "browser automation") {
		t.Fatalf("expected utility browser definition, got description %q", def.Description)
	}

	_, err := tool.Call(context.Background(), `{}`)
	if !errors.Is(err, ErrUtilityInvalidInput) {
		t.Fatalf("expected utility browser invalid-input behavior, got %v", err)
	}
}
