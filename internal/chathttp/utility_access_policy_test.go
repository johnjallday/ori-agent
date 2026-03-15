package chathttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestIsUtilityToolAllowedForAgent_DefaultAllowsWebTools(t *testing.T) {
	ag := &agent.Agent{
		Settings: types.Settings{
			Model:       "gpt-4o-mini",
			Temperature: 1.0,
		},
	}

	if !isUtilityToolAllowedForAgent(ag, "web_search") {
		t.Fatalf("expected web_search to be allowed by default")
	}
	if !isUtilityToolAllowedForAgent(ag, "web_fetch") {
		t.Fatalf("expected web_fetch to be allowed by default")
	}
	if !isUtilityToolAllowedForAgent(ag, "browser") {
		t.Fatalf("expected browser to be allowed by default")
	}
	if !isUtilityToolAllowedForAgent(ag, "time") {
		t.Fatalf("expected non-web utility to be allowed")
	}
}

func TestIsUtilityToolAllowedForAgent_DisabledWebTools(t *testing.T) {
	allowWebSearch := false
	ag := &agent.Agent{
		Settings: types.Settings{
			Model:          "gpt-4o-mini",
			Temperature:    1.0,
			AllowWebSearch: &allowWebSearch,
		},
	}

	if isUtilityToolAllowedForAgent(ag, "web_search") {
		t.Fatalf("expected web_search to be blocked when allow_web_search=false")
	}
	if isUtilityToolAllowedForAgent(ag, "web_fetch") {
		t.Fatalf("expected web_fetch to be blocked when allow_web_search=false")
	}
	if isUtilityToolAllowedForAgent(ag, "browser") {
		t.Fatalf("expected browser to be blocked when allow_web_search=false")
	}
	if !isUtilityToolAllowedForAgent(ag, "time") {
		t.Fatalf("expected non-web utility to remain allowed")
	}
}

func TestExecuteDirectTool_BlocksWebUtilityWhenDisabled(t *testing.T) {
	allowWebSearch := false
	ag := &agent.Agent{
		Settings: types.Settings{
			Model:          "gpt-4o-mini",
			Temperature:    1.0,
			AllowWebSearch: &allowWebSearch,
		},
		Plugins: map[string]types.LoadedPlugin{},
	}
	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
	}

	result := h.executeDirectTool(context.Background(), runtimeTestAgent(ag), "test-agent", &DirectToolCommand{
		ToolName: "web_search",
		Args:     `{"query":"ai"}`,
	})
	if result.Success {
		t.Fatalf("expected execution to fail when web tools are disabled")
	}
	if !strings.Contains(strings.ToLower(result.Result), "disabled") {
		t.Fatalf("expected disabled message, got %q", result.Result)
	}
}

func TestGetAvailableToolNames_FiltersDisabledWebUtilities(t *testing.T) {
	allowWebSearch := false
	ag := &agent.Agent{
		Settings: types.Settings{
			Model:          "gpt-4o-mini",
			Temperature:    1.0,
			AllowWebSearch: &allowWebSearch,
		},
		Plugins: map[string]types.LoadedPlugin{},
	}
	h := &Handler{
		utilityRegistry: NewDefaultUtilityToolRegistry(),
	}

	tools := h.getAvailableToolNames(runtimeTestAgent(ag))
	joined := strings.Join(tools, ",")
	if strings.Contains(joined, "web_search") || strings.Contains(joined, "web_fetch") || strings.Contains(joined, "browser") {
		t.Fatalf("expected web utilities to be filtered, got %v", tools)
	}
	if !strings.Contains(joined, "time") {
		t.Fatalf("expected non-web utility to remain available, got %v", tools)
	}
}
