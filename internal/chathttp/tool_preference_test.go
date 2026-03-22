package chathttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/toolapi"
)

type mockNamedTool struct {
	name   string
	result string
}

func (m mockNamedTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
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
		getFn: func(server string) ([]toolapi.Tool, error) {
			if server != "playwright" {
				return nil, nil
			}
			return []toolapi.Tool{
				mockPluginTool{name: "browser"},
			}, nil
		},
	}

	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
		mcpRegistry:     registry,
	}

	ag := runtimeTestAgent(&agent.Agent{}, "playwright")

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
		getFn: func(server string) ([]toolapi.Tool, error) {
			switch server {
			case "playwright":
				return []toolapi.Tool{
					mockNamedTool{name: "browser", result: "playwright"},
				}, nil
			case "puppeteer":
				return []toolapi.Tool{
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

	ag := runtimeTestAgent(&agent.Agent{}, "puppeteer", "playwright")

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

func TestFindTool_BrowserHonorsBrowserbasePreference(t *testing.T) {
	registry := &mockMCPRegistry{
		getFn: func(server string) ([]toolapi.Tool, error) {
			switch server {
			case "playwright":
				return []toolapi.Tool{
					mockNamedTool{name: "browser", result: "playwright"},
				}, nil
			case "browserbase":
				return []toolapi.Tool{
					mockNamedTool{name: "browser", result: "browserbase"},
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
	h.SetBrowserMCPPreference("browserbase")

	ag := runtimeTestAgent(&agent.Agent{}, "playwright", "browserbase")

	tool, found := h.findTool(ag, "Email Triage Assistant", "browser")
	if !found {
		t.Fatal("expected browser tool to be found")
	}

	result, err := tool.Call(context.Background(), `{"action":"open_url","url":"https://instagram.com"}`)
	if err != nil {
		t.Fatalf("unexpected error from preferred MCP browser tool: %v", err)
	}
	if result != "browserbase" {
		t.Fatalf("expected browserbase browser tool to be preferred, got %q", result)
	}
}

func TestFindTool_BrowserFallsBackToBrowserNavigateAlias(t *testing.T) {
	registry := &mockMCPRegistry{
		getFn: func(server string) ([]toolapi.Tool, error) {
			if server != "playwright" {
				return nil, nil
			}
			return []toolapi.Tool{
				mockNamedTool{name: "browser_navigate", result: "playwright_navigate"},
			}, nil
		},
	}

	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
		mcpRegistry:     registry,
	}

	ag := runtimeTestAgent(&agent.Agent{}, "playwright")

	tool, found := h.findTool(ag, "Email Triage Assistant", "browser")
	if !found {
		t.Fatal("expected browser alias tool to be found")
	}
	if got := strings.ToLower(strings.TrimSpace(tool.Definition().Name)); got != "browser_navigate" {
		t.Fatalf("expected browser_navigate alias tool, got %q", got)
	}
}

func TestAdaptBrowserToolArgsForDefinition_BrowserNavigate(t *testing.T) {
	adapted, err := adaptBrowserToolArgsForDefinition("browser_navigate", `{"action":"open_url","url":"youtube.com"}`)
	if err != nil {
		t.Fatalf("unexpected adaptation error: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(adapted), &payload); err != nil {
		t.Fatalf("failed to parse adapted args: %v", err)
	}
	if payload["url"] != "https://youtube.com" {
		t.Fatalf("expected normalized https url, got %q", payload["url"])
	}
}

func TestFindTool_BrowserSuppressedWhenBrowserMCPConfiguredButNoMatchingTool(t *testing.T) {
	registry := &mockMCPRegistry{
		getFn: func(server string) ([]toolapi.Tool, error) {
			if server != "playwright" {
				return nil, nil
			}
			return []toolapi.Tool{
				mockPluginTool{name: "navigate"},
			}, nil
		},
	}

	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
		mcpRegistry:     registry,
	}

	ag := runtimeTestAgent(&agent.Agent{}, "playwright")

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

	ag := runtimeTestAgent(&agent.Agent{})

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
