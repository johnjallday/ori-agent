package chathttp

import (
	"strings"
	"testing"
)

func TestClassifyUtilityRoute_TimeTokyo(t *testing.T) {
	decision := classifyUtilityRoute("What time is it in Tokyo")
	if decision.Mode != UtilityRouteDirect {
		t.Fatalf("expected utility_direct, got %q", decision.Mode)
	}
	if decision.ToolName != "time" {
		t.Fatalf("expected time tool, got %q", decision.ToolName)
	}
	if !strings.Contains(decision.ToolArgs, "Asia/Tokyo") {
		t.Fatalf("expected Asia/Tokyo in args, got %q", decision.ToolArgs)
	}
}

func TestClassifyUtilityRoute_Weather(t *testing.T) {
	decision := classifyUtilityRoute("weather in San Francisco")
	if decision.Mode != UtilityRouteDirect {
		t.Fatalf("expected utility_direct, got %q", decision.Mode)
	}
	if decision.ToolName != "weather" {
		t.Fatalf("expected weather tool, got %q", decision.ToolName)
	}
	if !strings.Contains(strings.ToLower(decision.ToolArgs), "san francisco") {
		t.Fatalf("expected location in args, got %q", decision.ToolArgs)
	}
}

func TestClassifyUtilityRoute_WebSearch(t *testing.T) {
	decision := classifyUtilityRoute("search weather on web")
	if decision.Mode != UtilityRouteDirect {
		t.Fatalf("expected utility_direct, got %q", decision.Mode)
	}
	if decision.ToolName != "web_search" {
		t.Fatalf("expected web_search tool, got %q", decision.ToolName)
	}
}

func TestClassifyUtilityRoute_WebFetch(t *testing.T) {
	decision := classifyUtilityRoute("fetch https://example.com and summarize")
	if decision.Mode != UtilityRouteDirect {
		t.Fatalf("expected utility_direct, got %q", decision.Mode)
	}
	if decision.ToolName != "web_fetch" {
		t.Fatalf("expected web_fetch tool, got %q", decision.ToolName)
	}
	if !strings.Contains(decision.ToolArgs, "https://example.com") {
		t.Fatalf("expected url in args, got %q", decision.ToolArgs)
	}
}

func TestClassifyUtilityRoute_BrowserOpen(t *testing.T) {
	decision := classifyUtilityRoute("browser open https://example.com")
	if decision.Mode != UtilityRouteDirect {
		t.Fatalf("expected utility_direct, got %q", decision.Mode)
	}
	if decision.ToolName != "browser" {
		t.Fatalf("expected browser tool, got %q", decision.ToolName)
	}
	if !strings.Contains(decision.ToolArgs, "open_url") {
		t.Fatalf("expected open_url action in args, got %q", decision.ToolArgs)
	}
}

func TestClassifyUtilityRoute_WorkspaceFallback(t *testing.T) {
	decision := classifyUtilityRoute("run tests in this repository")
	if decision.Mode != UtilityRouteWorkspace {
		t.Fatalf("expected workspace_task, got %q", decision.Mode)
	}
}
